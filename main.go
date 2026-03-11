// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed help-agent.md
var agentGuide string

var version = "dev"

func defaultSockPath() string {
	return fmt.Sprintf("/tmp/cworkers-%d.sock", os.Getuid())
}

// maxPayload is the maximum size of a dispatch task payload (10 MB).
const maxPayload = 10 << 20

func main() {
	args := os.Args[1:]
	sock := defaultSockPath()
	var timeout time.Duration
	var model string
	var session string
	var transcript string
	var contextLines int
	var dispatchWait time.Duration

	// Parse flags.
	for i := 0; i < len(args); {
		switch {
		case args[i] == "--version":
			fmt.Println(version)
			return
		case args[i] == "--help" || args[i] == "-h":
			printUsage(os.Stdout)
			return
		case args[i] == "--help-agent":
			printUsage(os.Stdout)
			fmt.Println()
			fmt.Print(agentGuide)
			return
		case args[i] == "--sock" && i+1 < len(args):
			sock = args[i+1]
			args = append(args[:i], args[i+2:]...)
		case args[i] == "--timeout" && i+1 < len(args):
			var err error
			timeout, err = time.ParseDuration(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "bad --timeout: %v\n", err)
				os.Exit(1)
			}
			args = append(args[:i], args[i+2:]...)
		case args[i] == "--model" && i+1 < len(args):
			model = args[i+1]
			args = append(args[:i], args[i+2:]...)
		case args[i] == "--session" && i+1 < len(args):
			session = args[i+1]
			args = append(args[:i], args[i+2:]...)
		case args[i] == "--transcript" && i+1 < len(args):
			transcript = args[i+1]
			args = append(args[:i], args[i+2:]...)
		case args[i] == "--context" && i+1 < len(args):
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "bad --context: %v\n", err)
				os.Exit(1)
			}
			contextLines = n
			args = append(args[:i], args[i+2:]...)
		case args[i] == "--wait" && i+1 < len(args):
			var err error
			dispatchWait, err = time.ParseDuration(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "bad --wait: %v\n", err)
				os.Exit(1)
			}
			args = append(args[:i], args[i+2:]...)
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(os.Stderr, "unknown flag: %s\n", args[i])
			os.Exit(1)
		default:
			i++
		}
	}

	if len(args) == 0 {
		printUsage(os.Stderr)
		os.Exit(1)
	}

	switch args[0] {
	case "serve":
		serve(sock, dispatchWait)
	case "worker":
		if session == "" {
			fmt.Fprintln(os.Stderr, "worker: --session is required")
			os.Exit(1)
		}
		worker(sock, timeout, model, session)
	case "dispatch":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: cworkers dispatch <task>")
			os.Exit(1)
		}
		dispatch(sock, strings.Join(args[1:], " "), model, session)
	case "shadow":
		shadowCmd(sock, session, transcript, contextLines)
	case "unshadow":
		unshadowCmd(sock, session)
	case "status":
		status(sock, session)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printUsage(os.Stderr)
		os.Exit(1)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: cworkers <command> [flags]

commands:
  serve      Start the broker
               --wait <dur>         Max time to wait for a worker on dispatch (default: 30s)
  worker     Block until a task arrives, print it
               --session <id>       Session to bind to (required)
               --timeout <dur>      Total lifetime before exit (e.g. 590s)
               --model <name>       Register as a specific model (e.g. opus, sonnet)
  dispatch   Send a task to an available worker
               --model <name>       Route to a specific model worker
               --session <id>       Use shadow context from this session
  shadow     Register a session transcript for context injection
               (no flags)           Auto-discover transcript from cwd; prints session ID
               --session <id>       Session identifier (explicit override)
               --transcript <path>  JSONL transcript to shadow (explicit override)
               --context <N>        Number of recent messages to keep (default: 50)
  unshadow   Remove a session's shadow registration
               --session <id>       Session identifier (required)
  status     Show pool size by model and shadow count
               --session <id>       Show status for a specific session

global flags:
  --sock <path>    Unix socket path (default: %s)
  --version        Print version and exit
  --help           Show this help
  --help-agent     Show help plus agent integration guide
`, defaultSockPath())
}

// --- Transcript shadow ---

type transcriptEntry struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message,omitempty"`
}

type messageEnvelope struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// shadow tails a JSONL transcript and maintains a rolling window of
// recent user/assistant messages as formatted text.
type shadow struct {
	mu       sync.RWMutex
	messages []string
	maxLines int
	done     chan struct{}
}

func newShadow(maxLines int) *shadow {
	if maxLines <= 0 {
		maxLines = 50
	}
	return &shadow{
		maxLines: maxLines,
		done:     make(chan struct{}),
	}
}

func (s *shadow) add(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
	if len(s.messages) > s.maxLines {
		s.messages = s.messages[len(s.messages)-s.maxLines:]
	}
}

func (s *shadow) snapshot() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.messages) == 0 {
		return ""
	}
	return strings.Join(s.messages, "\n\n")
}

func (s *shadow) stop() {
	select {
	case <-s.done:
		// Already closed.
	default:
		close(s.done)
	}
}

// --- Shadow Registry ---

type shadowRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*shadow // session-id → shadow
}

func newShadowRegistry() *shadowRegistry {
	return &shadowRegistry{
		sessions: make(map[string]*shadow),
	}
}

func (r *shadowRegistry) register(sessionID, transcriptPath string, contextLines int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// If there's an existing shadow for this session, stop it first.
	if old, ok := r.sessions[sessionID]; ok {
		old.stop()
	}

	s := newShadow(contextLines)
	r.sessions[sessionID] = s
	go tailTranscript(transcriptPath, s)
}

func (r *shadowRegistry) unregister(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s, ok := r.sessions[sessionID]; ok {
		s.stop()
		delete(r.sessions, sessionID)
	}
}

func (r *shadowRegistry) get(sessionID string) *shadow {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[sessionID]
}

func (r *shadowRegistry) count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

func (r *shadowRegistry) has(sessionID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.sessions[sessionID]
	return ok
}

func extractText(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return ""
}

// tailTranscript reads a JSONL file and tails it for new lines.
// Tracks byte offset manually to avoid the partial-read duplication
// bug identified in the architecture review.
func tailTranscript(path string, s *shadow) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "broker: shadow: open %s: %v\n", path, err)
		return
	}
	defer f.Close()

	fmt.Fprintf(os.Stderr, "broker: shadowing %s\n", path)

	scanner := bufio.NewScanner(f)
	// Allow lines up to 10MB (transcript entries can be large).
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	for {
		for scanner.Scan() {
			processLine(scanner.Bytes(), s)
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "broker: shadow: scan: %v\n", err)
			return
		}
		// EOF — wait for more data and reset the scanner on the same file handle.
		select {
		case <-s.done:
			return
		case <-time.After(500 * time.Millisecond):
		}
		scanner = bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	}
}

func processLine(line []byte, s *shadow) {
	var entry transcriptEntry
	if json.Unmarshal(line, &entry) != nil {
		return
	}
	if entry.Type != "user" && entry.Type != "assistant" {
		return
	}
	if entry.Message == nil {
		return
	}

	var env messageEnvelope
	if json.Unmarshal(entry.Message, &env) != nil {
		return
	}

	text := extractText(env.Content)
	if text == "" || env.Role == "" {
		return
	}

	const maxMsgLen = 2000
	if len(text) > maxMsgLen {
		text = text[:maxMsgLen] + "..."
	}

	role := strings.ToUpper(env.Role[:1]) + env.Role[1:]
	s.add(fmt.Sprintf("[%s]: %s", role, text))
}

// --- Pool ---

type taggedWorker struct {
	conn    net.Conn
	model   string
	session string
}

type pool struct {
	mu      sync.Mutex
	workers []taggedWorker
	// waiters holds dispatch requests waiting for a worker.
	waiters []dispatchWaiter
}

type dispatchWaiter struct {
	model   string
	session string
	ch      chan net.Conn
	expires time.Time
}

func (p *pool) add(conn net.Conn, model, session string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Compact expired waiters and find a match in one pass.
	now := time.Now()
	n := 0
	matched := -1
	for i, w := range p.waiters {
		if now.After(w.expires) {
			continue // drop expired
		}
		if matched < 0 && w.session == session && (w.model == "" || w.model == model) {
			matched = n
		}
		p.waiters[n] = p.waiters[i]
		n++
	}
	p.waiters = p.waiters[:n]

	if matched >= 0 {
		w := p.waiters[matched]
		p.waiters = append(p.waiters[:matched], p.waiters[matched+1:]...)
		w.ch <- conn
		return
	}

	p.workers = append(p.workers, taggedWorker{conn: conn, model: model, session: session})
}

// take returns the first worker matching the requested model and session.
// If model is "", any model matches. Session must always match exactly.
func (p *pool) take(model, session string) net.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, w := range p.workers {
		if w.session == session && (model == "" || w.model == model) {
			p.workers = append(p.workers[:i], p.workers[i+1:]...)
			return w.conn
		}
	}
	return nil
}

// wait registers a dispatch waiter and returns a channel that will
// receive a worker connection when one becomes available.
func (p *pool) wait(model, session string, deadline time.Time) chan net.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan net.Conn, 1)
	p.waiters = append(p.waiters, dispatchWaiter{
		model:   model,
		session: session,
		ch:      ch,
		expires: deadline,
	})
	return ch
}

// removeWaiter removes a waiter's channel (on timeout).
func (p *pool) removeWaiter(ch chan net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, w := range p.waiters {
		if w.ch == ch {
			p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
			return
		}
	}
}

func (p *pool) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.workers)
}

// countForSession returns the number of workers for a given session.
func (p *pool) countForSession(session string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, w := range p.workers {
		if w.session == session {
			n++
		}
	}
	return n
}

// countsForSession returns per-model worker counts for a given session.
func (p *pool) countsForSession(session string) map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := make(map[string]int)
	for _, w := range p.workers {
		if w.session == session {
			model := w.model
			if model == "" {
				model = "any"
			}
			m[model]++
		}
	}
	return m
}

func (p *pool) counts() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := make(map[string]int)
	for _, w := range p.workers {
		model := w.model
		if model == "" {
			model = "any"
		}
		m[model]++
	}
	return m
}

// drain closes all pooled worker connections and cancels all pending waiters.
func (p *pool) drain() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.workers {
		w.conn.Close()
	}
	p.workers = nil
	for _, w := range p.waiters {
		close(w.ch)
	}
	p.waiters = nil
}

// --- Serve ---

func serve(sock string, dispatchWait time.Duration) {
	os.Remove(sock)

	if dispatchWait <= 0 {
		dispatchWait = 30 * time.Second
	}

	ln, err := net.Listen("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}

	if err := os.Chmod(sock, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "broker: chmod socket: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "broker: listening on %s (dispatch wait: %s)\n", sock, dispatchWait)

	p := &pool{}
	reg := newShadowRegistry()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			fmt.Fprintf(os.Stderr, "broker: accept: %v\n", err)
			continue
		}
		go handleConn(conn, p, reg, dispatchWait)
	}

	fmt.Fprintf(os.Stderr, "broker: shutting down\n")
	p.drain()
	os.Remove(sock)
}

func handleConn(conn net.Conn, p *pool, reg *shadowRegistry, dispatchWait time.Duration) {
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}

	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		conn.Close()
		return
	}

	// Split the command line preserving empty fields between spaces.
	// The protocol uses positional fields: "DISPATCH <model> <session>"
	// where either field can be empty. strings.Fields would collapse
	// consecutive spaces and lose the empty model field.
	cmdParts := strings.SplitN(trimmed, " ", 2)
	cmd := cmdParts[0]

	// For commands other than DISPATCH, Fields-style parsing is fine
	// since they don't have optional positional empty fields.
	parts := strings.Fields(trimmed)

	switch cmd {
	case "WORKER":
		// Parse "WORKER <model> <session>" with positional fields.
		// Both model and session can be empty. Use SplitN to preserve empty fields.
		workerArgs := ""
		if len(cmdParts) > 1 {
			workerArgs = cmdParts[1]
		}
		wFields := strings.SplitN(workerArgs, " ", 2)
		model := ""
		session := ""
		if len(wFields) > 0 {
			model = wFields[0]
		}
		if len(wFields) > 1 {
			session = wFields[1]
		}
		p.add(conn, model, session)
		label := "any"
		if model != "" {
			label = model
		}
		if session != "" {
			fmt.Fprintf(os.Stderr, "broker: worker registered [%s] session=%s (pool: %d)\n", label, session, p.count())
		} else {
			fmt.Fprintf(os.Stderr, "broker: worker registered [%s] (pool: %d)\n", label, p.count())
		}

	case "DISPATCH":
		// Parse "DISPATCH <model> <session>" with positional fields.
		// Split into exactly 4 parts (cmd, model, session, rest) to
		// preserve empty fields. "DISPATCH  sess1" => ["DISPATCH", "", "sess1"].
		dispatchArgs := ""
		if len(cmdParts) > 1 {
			dispatchArgs = cmdParts[1]
		}
		argFields := strings.SplitN(dispatchArgs, " ", 2)
		model := ""
		session := ""
		if len(argFields) > 0 {
			model = argFields[0]
		}
		if len(argFields) > 1 {
			session = argFields[1]
		}

		// Deadline prevents a stuck caller from holding a goroutine forever.
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		task, err := io.ReadAll(io.LimitReader(reader, maxPayload))
		conn.SetReadDeadline(time.Time{}) // clear for response write
		if err != nil {
			fmt.Fprintf(conn, "ERROR: %v\n", err)
			conn.Close()
			return
		}

		// Build payload with shadow context.
		var payload []byte
		var s *shadow
		if session != "" {
			s = reg.get(session)
		}
		if s != nil {
			ctx := s.snapshot()
			if ctx != "" {
				payload = fmt.Appendf(nil,
					"=== CONVERSATION CONTEXT (recent messages from root session) ===\n%s\n=== END CONTEXT ===\n\nTASK: %s",
					ctx, task,
				)
			} else {
				payload = task
			}
		} else {
			payload = task
		}

		target := "any"
		if model != "" {
			target = model
		}

		// Try to find a live worker immediately.
		w := p.take(model, session)
		if w == nil {
			// No worker available — wait for one to register.
			fmt.Fprintf(os.Stderr, "broker: no %s worker available, waiting up to %s\n", target, dispatchWait)
			deadline := time.Now().Add(dispatchWait)
			ch := p.wait(model, session, deadline)

			timer := time.NewTimer(dispatchWait)
			defer timer.Stop()

			select {
			case w = <-ch:
				// Got a worker.
			case <-timer.C:
				p.removeWaiter(ch)
				fmt.Fprintf(os.Stderr, "broker: dispatch timed out — no %s workers\n", target)
				fmt.Fprintf(conn, "NO_WORKERS\n")
				conn.Close()
				return
			}
		}

		// Try the worker (and any subsequent ones if stale).
		for {
			_, werr := w.Write(payload)
			w.Close()
			if werr == nil {
				fmt.Fprintf(os.Stderr, "broker: dispatched to %s (%d bytes payload, %d bytes context, pool: %d)\n",
					target, len(task), len(payload)-len(task), p.count())
				fmt.Fprintf(conn, "OK\n")
				conn.Close()
				return
			}
			fmt.Fprintf(os.Stderr, "broker: stale worker removed\n")
			w = p.take(model, session)
			if w == nil {
				fmt.Fprintf(os.Stderr, "broker: dispatch failed — no %s workers\n", target)
				fmt.Fprintf(conn, "NO_WORKERS\n")
				conn.Close()
				return
			}
		}

	case "SHADOW":
		if len(parts) < 3 {
			fmt.Fprintf(conn, "ERROR: usage: SHADOW <session-id> <transcript-path> [context-lines]\n")
			conn.Close()
			return
		}
		sessionID := parts[1]
		txPath := parts[2]
		ctxLines := 50
		if len(parts) > 3 {
			n, err := strconv.Atoi(parts[3])
			if err != nil {
				fmt.Fprintf(conn, "ERROR: bad context-lines: %v\n", err)
				conn.Close()
				return
			}
			ctxLines = n
		}
		reg.register(sessionID, txPath, ctxLines)
		fmt.Fprintf(os.Stderr, "broker: shadow registered [%s] -> %s (context: %d)\n", sessionID, txPath, ctxLines)
		fmt.Fprintf(conn, "OK\n")
		conn.Close()

	case "UNSHADOW":
		if len(parts) < 2 {
			fmt.Fprintf(conn, "ERROR: usage: UNSHADOW <session-id>\n")
			conn.Close()
			return
		}
		sessionID := parts[1]
		reg.unregister(sessionID)
		fmt.Fprintf(os.Stderr, "broker: shadow unregistered [%s]\n", sessionID)
		fmt.Fprintf(conn, "OK\n")
		conn.Close()

	case "STATUS":
		sessionID := ""
		if len(parts) > 1 {
			sessionID = parts[1]
		}

		var sb strings.Builder
		if sessionID != "" {
			// Session-scoped status.
			shadowed := reg.has(sessionID)
			matching := p.countForSession(sessionID)
			fmt.Fprintf(&sb, "SESSION: %s, shadow: %t, available_workers: %d", sessionID, shadowed, matching)
			counts := p.countsForSession(sessionID)
			if len(counts) > 0 {
				fmt.Fprintf(&sb, " (")
				first := true
				for m, c := range counts {
					if !first {
						fmt.Fprintf(&sb, ", ")
					}
					fmt.Fprintf(&sb, "%s: %d", m, c)
					first = false
				}
				fmt.Fprintf(&sb, ")")
			}
		} else {
			// Global status.
			counts := p.counts()
			fmt.Fprintf(&sb, "WORKERS: %d", p.count())
			if len(counts) > 0 {
				fmt.Fprintf(&sb, " (")
				first := true
				for m, c := range counts {
					if !first {
						fmt.Fprintf(&sb, ", ")
					}
					fmt.Fprintf(&sb, "%s: %d", m, c)
					first = false
				}
				fmt.Fprintf(&sb, ")")
			}
			fmt.Fprintf(&sb, ", shadows: %d", reg.count())
		}
		fmt.Fprintf(&sb, "\n")
		fmt.Fprint(conn, sb.String())
		conn.Close()

	default:
		fmt.Fprintf(conn, "ERROR: unknown command: %s\n", cmd)
		conn.Close()
	}
}

// --- Worker ---

// worker loops internally, reconnecting to the broker every 60 seconds.
// The agent sees a single blocking bash call that either prints a task
// or exits cleanly on overall timeout. No agent-level looping needed.
func worker(sock string, timeout time.Duration, model, session string) {
	if timeout <= 0 {
		timeout = 590 * time.Second
	}
	deadline := time.Now().Add(timeout)

	const reconnectInterval = 60 * time.Second

	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		wait := min(reconnectInterval, remaining)

		task := workerTryOnce(sock, model, session, wait)
		if task != nil {
			fmt.Print(string(task))
			return
		}
	}

	// Overall timeout — exit cleanly with no output.
}

// workerTryOnce connects to the broker, registers, and waits up to
// the given duration for a task. Returns nil if no task arrived.
func workerTryOnce(sock, model, session string, wait time.Duration) []byte {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		// Broker might be restarting — sleep briefly and let caller retry.
		time.Sleep(time.Second)
		return nil
	}
	defer conn.Close()

	fmt.Fprintf(conn, "WORKER %s %s\n", model, session)

	conn.SetReadDeadline(time.Now().Add(wait))

	task, err := io.ReadAll(io.LimitReader(conn, maxPayload))
	if err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return nil
		}
		return nil
	}
	if len(task) == 0 {
		return nil
	}
	return task
}

// --- Dispatch ---

const exitNoWorkers = 2

func dispatch(sock, task, model, session string) {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dispatch: connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "DISPATCH %s %s\n%s", model, session, task)

	if uc, ok := conn.(*net.UnixConn); ok {
		uc.CloseWrite()
	}

	resp, err := io.ReadAll(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dispatch: read: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(string(resp))

	if strings.TrimSpace(string(resp)) == "NO_WORKERS" {
		os.Exit(exitNoWorkers)
	}
}

// --- Shadow / Unshadow CLI ---

// discoverTranscript finds the most recently modified .jsonl file in
// the Claude Code project directory for the current working directory.
// Returns the transcript path and a session ID derived from its filename.
func discoverTranscript() (transcript, sessionID string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("home dir: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("getwd: %w", err)
	}

	// Claude Code encodes the cwd by replacing "/" with "-" and prepending "-".
	encoded := "-" + strings.ReplaceAll(cwd[1:], "/", "-")
	projectDir := filepath.Join(home, ".claude", "projects", encoded)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "", "", fmt.Errorf("read project dir %s: %w", projectDir, err)
	}

	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = e.Name()
		}
	}

	if newest == "" {
		return "", "", fmt.Errorf("no .jsonl transcripts found in %s", projectDir)
	}

	transcript = filepath.Join(projectDir, newest)
	sessionID = strings.TrimSuffix(newest, ".jsonl")
	return transcript, sessionID, nil
}

func shadowCmd(sock, session, transcript string, contextLines int) {
	// Auto-discover transcript and session if not provided.
	if session == "" && transcript == "" {
		var err error
		transcript, session, err = discoverTranscript()
		if err != nil {
			fmt.Fprintf(os.Stderr, "shadow: auto-discover: %v\n", err)
			os.Exit(1)
		}
	} else if session == "" {
		fmt.Fprintln(os.Stderr, "shadow: --session is required (or omit both --session and --transcript for auto-discovery)")
		os.Exit(1)
	} else if transcript == "" {
		fmt.Fprintln(os.Stderr, "shadow: --transcript is required (or omit both --session and --transcript for auto-discovery)")
		os.Exit(1)
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shadow: connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if contextLines > 0 {
		fmt.Fprintf(conn, "SHADOW %s %s %d\n", session, transcript, contextLines)
	} else {
		fmt.Fprintf(conn, "SHADOW %s %s\n", session, transcript)
	}

	resp, err := io.ReadAll(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shadow: read: %v\n", err)
		os.Exit(1)
	}

	trimmed := strings.TrimSpace(string(resp))
	if strings.HasPrefix(trimmed, "OK") {
		// Print session ID so the agent can capture it.
		fmt.Println(session)
	} else {
		fmt.Print(string(resp))
	}
}

func unshadowCmd(sock, session string) {
	if session == "" {
		fmt.Fprintln(os.Stderr, "unshadow: --session is required")
		os.Exit(1)
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unshadow: connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "UNSHADOW %s\n", session)

	resp, err := io.ReadAll(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unshadow: read: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(resp))
}

// --- Status ---

func status(sock, session string) {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if session != "" {
		fmt.Fprintf(conn, "STATUS %s\n", session)
	} else {
		fmt.Fprintf(conn, "STATUS\n")
	}

	resp, err := io.ReadAll(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: read: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(string(resp))
}
