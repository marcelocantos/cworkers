// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

//go:embed help-agent.md
var agentGuide string

var version = "dev"

var debug bool

const defaultPort = 4242

func main() {
	args := os.Args[1:]
	port := defaultPort

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
		case args[i] == "--debug":
			debug = true
			args = append(args[:i], args[i+1:]...)
		case args[i] == "--port" && i+1 < len(args):
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "bad --port: %v\n", err)
				os.Exit(1)
			}
			port = n
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
		serve(port)
	case "status":
		statusCmd(port)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printUsage(os.Stderr)
		os.Exit(1)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `usage: cworkers <command> [flags]

commands:
  serve      Start the MCP broker daemon
               --port <N>    HTTP port (default: %d)
  status     Show pool and shadow state

global flags:
  --port <N>     HTTP port (default: %d)
  --version      Print version and exit
  --help         Show this help
  --help-agent   Show help plus agent integration guide
`, defaultPort, defaultPort)
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
	cwd      string
	first    sync.Once
}

func newShadow(cwd string, maxLines int) *shadow {
	if maxLines <= 0 {
		maxLines = 50
	}
	return &shadow{
		maxLines: maxLines,
		done:     make(chan struct{}),
		cwd:      cwd,
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
	default:
		close(s.done)
	}
}

// --- Shadow Registry ---

type shadowRegistry struct {
	mu      sync.RWMutex
	shadows map[string]*shadow // cwd → shadow
}

func newShadowRegistry() *shadowRegistry {
	return &shadowRegistry{
		shadows: make(map[string]*shadow),
	}
}

// getOrCreate returns an existing shadow for the cwd, or discovers the
// transcript and registers a new one. Thread-safe.
func (r *shadowRegistry) getOrCreate(cwd string) (*shadow, error) {
	r.mu.RLock()
	s, ok := r.shadows[cwd]
	r.mu.RUnlock()
	if ok {
		return s, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock.
	if s, ok := r.shadows[cwd]; ok {
		return s, nil
	}

	transcript, err := discoverTranscript(cwd)
	if err != nil {
		return nil, err
	}

	s = newShadow(cwd, 50)
	r.shadows[cwd] = s
	go tailTranscript(transcript, s)
	log.Printf("shadow registered cwd=%s transcript=%s", cwd, filepath.Base(transcript))
	return s, nil
}

func (r *shadowRegistry) count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.shadows)
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
func tailTranscript(path string, s *shadow) {
	f, err := os.Open(path)
	if err != nil {
		log.Printf("shadow: open %s: %v", path, err)
		return
	}
	defer f.Close()

	log.Printf("shadowing %s", path)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	for {
		for scanner.Scan() {
			processLine(scanner.Bytes(), s)
		}
		if err := scanner.Err(); err != nil {
			log.Printf("shadow: scan: %v", err)
			return
		}
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

// --- Progress Throttle ---

// progressThrottle controls which worker output lines get forwarded as
// MCP progress notifications. Headings are tiered by depth:
//
//	tier 0 (#)    — always forwarded immediately
//	tier 1 (##)   — at most once per 10s
//	tier 2 (###+) — suppressed (never forwarded)
//
// Non-heading text is suppressed entirely.
type progressThrottle struct {
	lastSent  [2]time.Time // tiers 0 and 1 only; tier 2 is always suppressed
	intervals [2]time.Duration
}

func newProgressThrottle() *progressThrottle {
	return &progressThrottle{
		intervals: [2]time.Duration{0, 10 * time.Second},
	}
}

// headingTier returns the tier (0, 1, 2) for a heading line, or -1 if
// the line is not a heading.
func headingTier(line string) int {
	if !strings.HasPrefix(line, "#") {
		return -1
	}
	if strings.HasPrefix(line, "### ") {
		return 2
	}
	if strings.HasPrefix(line, "## ") {
		return 1
	}
	if strings.HasPrefix(line, "# ") {
		return 0
	}
	return -1
}

// shouldForward returns true if the heading at the given tier should be
// forwarded now, and updates the last-sent timestamp.
func (t *progressThrottle) shouldForward(tier int) bool {
	if tier < 0 || tier >= 2 {
		return false // non-heading or tier 2+ suppressed
	}
	now := time.Now()
	if now.Sub(t.lastSent[tier]) < t.intervals[tier] {
		return false
	}
	t.lastSent[tier] = now
	return true
}

// --- Worker Process ---

// workerProc represents a pre-spawned claude -p process waiting for
// a prompt on stdin.
type workerProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cwd    string
	model  string
	cancel context.CancelFunc
}

// spawnFunc is the function used to create workers. Replaced in tests.
var spawnFunc = spawnWorker

// spawnWorker starts a claude -p process ready to receive a prompt on stdin.
// The process initialises while stdin is held open; when a task arrives
// the broker writes the prompt and closes stdin to trigger execution.
func spawnWorker(cwd, model string) (*workerProc, error) {
	ctx, cancel := context.WithCancel(context.Background())

	args := []string{
		"-p",
		"--verbose",
		"--output-format", "stream-json",
		"--dangerously-skip-permissions",
	}
	if model != "" {
		args = append(args, "--model", model)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = cwd
	// Unset CLAUDECODE to avoid nested session detection.
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start claude: %w", err)
	}

	// Drain stderr in a goroutine to avoid blocking.
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 256*1024), 256*1024)
		for scanner.Scan() {
			log.Printf("claude stderr [%s]: %s", model, scanner.Text())
		}
	}()

	return &workerProc{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		cwd:    cwd,
		model:  model,
		cancel: cancel,
	}, nil
}

// dispatch sends a prompt to the worker and blocks until it returns a result.
// The onText callback is called with each new text fragment as it arrives from
// the worker's assistant output, enabling real-time progress reporting.
func (w *workerProc) dispatch(prompt string, onText func(string)) (string, error) {
	// Write prompt and close stdin to signal EOF.
	if _, err := io.WriteString(w.stdin, prompt); err != nil {
		w.cancel()
		return "", fmt.Errorf("write prompt: %w", err)
	}
	w.stdin.Close()

	// Parse NDJSON output from stdout.
	scanner := bufio.NewScanner(w.stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var resultText string
	var textParts []string
	var errMsg string
	var lineCount int

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineCount++

		var base struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &base) != nil {
			log.Printf("worker [%s]: unparseable line: %.100s", w.model, line)
			continue
		}

		if debug {
			log.Printf("ndjson [%s]: type=%s line=%.200s", w.model, base.Type, line)
		}

		switch base.Type {
		case "assistant":
			var msg struct {
				Message struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &msg) == nil {
				for _, b := range msg.Message.Content {
					if b.Type == "text" && b.Text != "" {
						if debug {
							log.Printf("onText [%s]: %.200s", w.model, b.Text)
						}
						textParts = append(textParts, b.Text)
						if onText != nil {
							onText(b.Text)
						}
					}
				}
			}
		case "result":
			var res struct {
				Subtype string `json:"subtype"`
				Result  string `json:"result"`
				Errors  []struct {
					Message string `json:"message"`
				} `json:"errors"`
			}
			if json.Unmarshal(line, &res) == nil {
				if res.Subtype == "success" {
					resultText = res.Result
				} else {
					var msgs []string
					for _, e := range res.Errors {
						msgs = append(msgs, e.Message)
					}
					errMsg = strings.Join(msgs, "; ")
				}
			}
		}
	}

	// Wait for process to exit.
	waitErr := w.cmd.Wait()

	log.Printf("worker [%s]: done, %d NDJSON lines, result=%d bytes, textParts=%d, err=%q, waitErr=%v",
		w.model, lineCount, len(resultText), len(textParts), errMsg, waitErr)

	if resultText != "" {
		return resultText, nil
	}
	if len(textParts) > 0 {
		return strings.Join(textParts, ""), nil
	}
	if errMsg != "" {
		return "", fmt.Errorf("claude error: %s", errMsg)
	}
	if waitErr != nil {
		return "", fmt.Errorf("claude exited: %w", waitErr)
	}
	return "Worker finished (no result text).", nil
}

func (w *workerProc) kill() {
	if w.cancel != nil {
		w.cancel()
	}
}

// --- Pool ---

type pool struct {
	mu   sync.Mutex
	idle map[string][]*workerProc // key: cwd + "\x00" + model
}

func newPool() *pool {
	return &pool{
		idle: make(map[string][]*workerProc),
	}
}

func poolKey(cwd, model string) string {
	return cwd + "\x00" + model
}

func (p *pool) take(cwd, model string) *workerProc {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := poolKey(cwd, model)
	workers := p.idle[key]
	if len(workers) == 0 {
		return nil
	}
	w := workers[len(workers)-1]
	p.idle[key] = workers[:len(workers)-1]
	if len(p.idle[key]) == 0 {
		delete(p.idle, key)
	}
	return w
}

func (p *pool) put(w *workerProc) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := poolKey(w.cwd, w.model)
	p.idle[key] = append(p.idle[key], w)
}

func (p *pool) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, ws := range p.idle {
		n += len(ws)
	}
	return n
}

func (p *pool) counts() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := make(map[string]int)
	for _, ws := range p.idle {
		for _, w := range ws {
			model := w.model
			if model == "" {
				model = "default"
			}
			m[model]++
		}
	}
	return m
}

func (p *pool) drain() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, ws := range p.idle {
		for _, w := range ws {
			w.kill()
		}
		delete(p.idle, key)
	}
}

// --- Transcript Discovery ---

// discoverTranscript finds the most recently modified .jsonl file in
// the Claude Code project directory for the given working directory.
func discoverTranscript(cwd string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}

	// Claude Code encodes the cwd by replacing "/" and "." with "-" and prepending "-".
	encoded := "-" + strings.NewReplacer("/", "-", ".", "-").Replace(cwd[1:])
	projectDir := filepath.Join(home, ".claude", "projects", encoded)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return "", fmt.Errorf("read project dir %s: %w", projectDir, err)
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
		return "", fmt.Errorf("no .jsonl transcripts found in %s", projectDir)
	}

	return filepath.Join(projectDir, newest), nil
}

// --- MCP Broker ---

type broker struct {
	pool *pool
	reg  *shadowRegistry
}

func (b *broker) handleCwork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	task, _ := args["task"].(string)
	cwd, _ := args["cwd"].(string)
	model, _ := args["model"].(string)

	if task == "" {
		return mcp.NewToolResultError("missing required parameter: task"), nil
	}
	if cwd == "" {
		return mcp.NewToolResultError("missing required parameter: cwd"), nil
	}
	if model == "" {
		model = "sonnet"
	}

	// Get or auto-register shadow for this cwd.
	s, err := b.reg.getOrCreate(cwd)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("shadow setup: %v", err)), nil
	}

	// Build prompt with shadow context.
	prompt := task
	if shadowCtx := s.snapshot(); shadowCtx != "" {
		prompt = fmt.Sprintf(
			"=== CONVERSATION CONTEXT (recent messages from root session) ===\n%s\n=== END CONTEXT ===\n\nTASK: %s",
			shadowCtx, task,
		)
	}

	// Try to get an idle worker.
	w := b.pool.take(s.cwd, model)
	if w == nil {
		log.Printf("no idle %s worker for %s, spawning cold", model, s.cwd)
		var err error
		w, err = spawnFunc(s.cwd, model)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("spawn worker: %v", err)), nil
		}
	} else {
		log.Printf("dispatching to idle %s worker for %s (pool: %d)", model, s.cwd, b.pool.count())
	}

	// Pre-warm a replacement in the background.
	go func() {
		rw, err := spawnFunc(s.cwd, model)
		if err != nil {
			log.Printf("pre-warm failed: %v", err)
			return
		}
		b.pool.put(rw)
		log.Printf("pre-warmed %s worker for %s (pool: %d)", model, s.cwd, b.pool.count())
	}()

	// Set up content-driven progress notifications.
	progressToken := req.Params.Meta.ProgressToken
	mcpSrv := server.ServerFromContext(ctx)
	throttle := newProgressThrottle()
	start := time.Now()

	sendProgress := func(msg string) {
		if mcpSrv == nil {
			log.Printf("sendProgress: no MCP server in context")
			return
		}
		if debug {
			log.Printf("sendProgress: msg=%q progressToken=%v", msg, progressToken)
		}
		elapsed := int(time.Since(start).Seconds())
		if progressToken != nil {
			err := mcpSrv.SendNotificationToClient(ctx, "notifications/progress", map[string]any{
				"progressToken": progressToken,
				"progress":      elapsed,
				"total":         0,
				"message":       msg,
			})
			if err != nil {
				log.Printf("sendProgress (progress): %v", err)
			} else if debug {
				log.Printf("sendProgress (progress): sent OK")
			}
		}
		err := mcpSrv.SendNotificationToClient(ctx, "notifications/message", map[string]any{
			"level": "info",
			"data":  fmt.Sprintf("cwork: %s", msg),
		})
		if err != nil {
			log.Printf("sendProgress (message): %v", err)
		} else if debug {
			log.Printf("sendProgress (message): sent OK")
		}
	}

	// Track last activity and most recent heading for heartbeat context.
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixMilli())
	var lastHeadingMu sync.Mutex
	lastHeading := "(no status yet)"

	// onText is called by dispatch for each assistant text chunk.
	// Each call is one turn's text — check for heading prefix directly.
	onText := func(text string) {
		lastActivity.Store(time.Now().UnixMilli())
		line := strings.TrimSpace(text)
		tier := headingTier(line)
		if tier < 0 {
			return
		}
		// Extract just the first line if the chunk contains more.
		if idx := strings.IndexByte(line, '\n'); idx >= 0 {
			line = line[:idx]
		}
		lastHeadingMu.Lock()
		lastHeading = line
		lastHeadingMu.Unlock()
		if throttle.shouldForward(tier) {
			elapsed := int(time.Since(start).Seconds())
			msg := fmt.Sprintf("[%ds] %s", elapsed, line)
			if debug {
				log.Printf("progress: %s", msg)
			}
			sendProgress(msg)
		}
	}

	// Dispatch in a goroutine so we can handle cancellation and heartbeats.
	type dispatchResult struct {
		text string
		err  error
	}
	done := make(chan dispatchResult, 1)
	go func() {
		result, err := w.dispatch(prompt, onText)
		done <- dispatchResult{result, err}
	}()

	// Fallback heartbeat: if no content-driven progress for 30s, send
	// an elapsed-time notification to keep the connection alive.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var result dispatchResult
	for {
		select {
		case result = <-done:
			goto finished
		case <-ticker.C:
			lastMs := lastActivity.Load()
			silent := time.Since(time.UnixMilli(lastMs))
			if debug {
				log.Printf("heartbeat tick: silent=%v", silent.Round(time.Second))
			}
			if silent >= 30*time.Second {
				elapsed := int(time.Since(start).Seconds())
				lastHeadingMu.Lock()
				h := lastHeading
				lastHeadingMu.Unlock()
				sendProgress(fmt.Sprintf("[%ds] %s", elapsed, h))
				lastActivity.Store(time.Now().UnixMilli())
			}
		case <-ctx.Done():
			w.kill()
			return mcp.NewToolResultError("request cancelled"), nil
		}
	}

finished:
	if result.err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("worker error: %v", result.err)), nil
	}

	// On first use for this cwd, append a setup suggestion.
	var setupHint string
	s.first.Do(func() {
		setupHint = `

---
SETUP NOTE: For best results, ensure the project's CLAUDE.md contains a
cworkers directive near the top. If it doesn't have one, suggest adding
this to the user:

` + "```" + `
## cworkers

**MANDATORY**: Never run builds (make, go build, npm run, etc.), tests,
file reads, or searches directly in the root session. Always delegate
via the ` + "`cwork`" + ` MCP tool. The only exceptions are trivial git commands
(git status, git diff) whose output you already know will be small.
When in doubt, delegate.
` + "```"
	})

	return mcp.NewToolResultText(result.text + setupHint), nil
}

// --- HTTP Status ---

type statusResponse struct {
	Workers int            `json:"workers"`
	Models  map[string]int `json:"models"`
	Shadows int            `json:"shadows"`
}

func (b *broker) handleStatus(w http.ResponseWriter, _ *http.Request) {
	resp := statusResponse{
		Workers: b.pool.count(),
		Models:  b.pool.counts(),
		Shadows: b.reg.count(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// --- Serve ---

func serve(port int) {
	b := &broker{
		pool: newPool(),
		reg:  newShadowRegistry(),
	}

	mcpSrv := server.NewMCPServer("cworkers", version,
		server.WithInstructions(agentGuide),
	)

	mcpSrv.AddTool(
		mcp.NewTool("cwork",
			mcp.WithDescription("Dispatch a task to a worker agent. Returns the worker's result."),
			mcp.WithString("task", mcp.Required(), mcp.Description("The task prompt for the worker")),
			mcp.WithString("cwd", mcp.Required(), mcp.Description("Working directory of the calling session")),
			mcp.WithString("model", mcp.Description("Model to use (default: sonnet). Options: sonnet, opus")),
		),
		b.handleCwork,
	)

	transport := server.NewStreamableHTTPServer(mcpSrv)

	mux := http.NewServeMux()
	mux.Handle("/mcp", transport)
	mux.HandleFunc("/status", b.handleStatus)

	httpSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("shutting down")
		httpSrv.Close()
	}()

	log.Printf("cworkers MCP server listening on :%d", port)
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}

	b.pool.drain()
}

// --- Status CLI ---

func statusCmd(port int) {
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/status", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: connect: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var s statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		fmt.Fprintf(os.Stderr, "status: decode: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("WORKERS: %d", s.Workers)
	if len(s.Models) > 0 {
		fmt.Printf(" (")
		first := true
		for m, c := range s.Models {
			if !first {
				fmt.Printf(", ")
			}
			fmt.Printf("%s: %d", m, c)
			first = false
		}
		fmt.Printf(")")
	}
	fmt.Printf(", shadows: %d\n", s.Shadows)
}

// --- Utility ---

func filterEnv(env []string, exclude string) []string {
	prefix := exclude + "="
	var result []string
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			result = append(result, e)
		}
	}
	return result
}
