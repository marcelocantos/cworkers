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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed agents-guide.md
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
	var transcript string
	var timeout time.Duration
	var contextLines int
	var model string
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
		case args[i] == "--model" && i+1 < len(args):
			model = args[i+1]
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
		serve(sock, transcript, contextLines, dispatchWait)
	case "worker":
		worker(sock, timeout, model)
	case "dispatch":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: cworkers dispatch <task>")
			os.Exit(1)
		}
		dispatch(sock, strings.Join(args[1:], " "), model)
	case "status":
		status(sock)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printUsage(os.Stderr)
		os.Exit(1)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: cworkers <command> [flags]

commands:
  serve     Start the broker
              --transcript <path>  JSONL transcript to shadow
              --context <N>        Number of recent messages to keep (default: 50)
              --wait <dur>         Max time to wait for a worker on dispatch (default: 30s)
  worker    Block until a task arrives, print it
              --timeout <dur>      Total lifetime before exit (e.g. 590s)
              --model <name>       Register as a specific model (e.g. opus, sonnet)
  dispatch  Send a task to an available worker
              --model <name>       Route to a specific model worker
  status    Show pool size by model

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
}

func newShadow(maxLines int) *shadow {
	if maxLines <= 0 {
		maxLines = 50
	}
	return &shadow{maxLines: maxLines}
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
		time.Sleep(500 * time.Millisecond)
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
	conn  net.Conn
	model string
}

type pool struct {
	mu      sync.Mutex
	workers []taggedWorker
	// waiters holds dispatch requests waiting for a worker.
	waiters []dispatchWaiter
}

type dispatchWaiter struct {
	model   string
	ch      chan net.Conn
	expires time.Time
}

func (p *pool) add(conn net.Conn, model string) {
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
		if matched < 0 && (w.model == "" || w.model == model) {
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

	p.workers = append(p.workers, taggedWorker{conn: conn, model: model})
}

// take returns the first worker matching the requested model.
// If model is "", any worker matches.
func (p *pool) take(model string) net.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, w := range p.workers {
		if model == "" || w.model == model {
			p.workers = append(p.workers[:i], p.workers[i+1:]...)
			return w.conn
		}
	}
	return nil
}

// wait registers a dispatch waiter and returns a channel that will
// receive a worker connection when one becomes available.
func (p *pool) wait(model string, deadline time.Time) chan net.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan net.Conn, 1)
	p.waiters = append(p.waiters, dispatchWaiter{
		model:   model,
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

func serve(sock, transcript string, contextLines int, dispatchWait time.Duration) {
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

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		ln.Close()
	}()

	var s *shadow

	if transcript != "" {
		s = newShadow(contextLines)
		go tailTranscript(transcript, s)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			fmt.Fprintf(os.Stderr, "broker: accept: %v\n", err)
			continue
		}
		go handleConn(conn, p, s, dispatchWait)
	}

	fmt.Fprintf(os.Stderr, "broker: shutting down\n")
	p.drain()
	os.Remove(sock)
}

func handleConn(conn net.Conn, p *pool, s *shadow, dispatchWait time.Duration) {
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return
	}

	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) == 0 {
		conn.Close()
		return
	}
	cmd := parts[0]
	model := ""
	if len(parts) > 1 {
		model = parts[1]
	}

	switch cmd {
	case "WORKER":
		p.add(conn, model)
		if model != "" {
			fmt.Fprintf(os.Stderr, "broker: worker registered [%s] (pool: %d)\n", model, p.count())
		} else {
			fmt.Fprintf(os.Stderr, "broker: worker registered (pool: %d)\n", p.count())
		}

	case "DISPATCH":
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
		w := p.take(model)
		if w == nil {
			// No worker available — wait for one to register.
			fmt.Fprintf(os.Stderr, "broker: no %s worker available, waiting up to %s\n", target, dispatchWait)
			deadline := time.Now().Add(dispatchWait)
			ch := p.wait(model, deadline)

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
			w = p.take(model)
			if w == nil {
				fmt.Fprintf(os.Stderr, "broker: dispatch failed — no %s workers\n", target)
				fmt.Fprintf(conn, "NO_WORKERS\n")
				conn.Close()
				return
			}
		}

	case "STATUS":
		var sb strings.Builder
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
		if s != nil {
			fmt.Fprintf(&sb, ", shadow: %d bytes", len(s.snapshot()))
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
func worker(sock string, timeout time.Duration, model string) {
	if timeout <= 0 {
		timeout = 590 * time.Second
	}
	deadline := time.Now().Add(timeout)

	const reconnectInterval = 60 * time.Second

	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		wait := min(reconnectInterval, remaining)

		task := workerTryOnce(sock, model, wait)
		if task != nil {
			fmt.Print(string(task))
			return
		}
	}

	// Overall timeout — exit cleanly with no output.
}

// workerTryOnce connects to the broker, registers, and waits up to
// the given duration for a task. Returns nil if no task arrived.
func workerTryOnce(sock, model string, wait time.Duration) []byte {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		// Broker might be restarting — sleep briefly and let caller retry.
		time.Sleep(time.Second)
		return nil
	}
	defer conn.Close()

	if model != "" {
		fmt.Fprintf(conn, "WORKER %s\n", model)
	} else {
		fmt.Fprintf(conn, "WORKER\n")
	}

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

func dispatch(sock, task, model string) {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dispatch: connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if model != "" {
		fmt.Fprintf(conn, "DISPATCH %s\n%s", model, task)
	} else {
		fmt.Fprintf(conn, "DISPATCH\n%s", task)
	}

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

// --- Status ---

func status(sock string) {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "STATUS\n")

	resp, err := io.ReadAll(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: read: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(string(resp))
}
