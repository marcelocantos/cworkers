// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

//go:embed help-agent.md
var agentGuide string

//go:embed dashboard/dist/index.html
var dashboardHTML string

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
	depth  int
	cancel context.CancelFunc
}

const maxDepth = 3

// claudePath is the resolved path to the claude binary. Set at startup.
var claudePath = "claude"

// spawnFunc is the function used to create workers. Replaced in tests.
var spawnFunc = spawnWorker

// spawnWorker starts a claude -p process ready to receive a prompt on stdin.
// The process initialises while stdin is held open; when a task arrives
// the broker writes the prompt and closes stdin to trigger execution.
// wid is the worker ID that children of this worker will use as their parent.
func spawnWorker(cwd, model string, port, depth int, wid string) (*workerProc, error) {
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
	// Give workers access to cwork at the next depth level, unless at max.
	if depth < maxDepth {
		mcpCfg := fmt.Sprintf(
			`{"mcpServers":{"cworkers":{"type":"http","url":"http://localhost:%d/mcp?depth=%d&wid=%s"}}}`,
			port, depth+1, wid,
		)
		args = append(args, "--mcp-config", mcpCfg)
	}

	cmd := exec.CommandContext(ctx, claudePath, args...)
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
			log.Printf("[%s] stderr: %s", wid, scanner.Text())
		}
	}()

	return &workerProc{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		cwd:    cwd,
		model:  model,
		depth:  depth,
		cancel: cancel,
	}, nil
}

// dispatch sends a prompt to the worker and blocks until it returns a result.
// The onText callback is called with each new text fragment as it arrives from
// the worker's assistant output, enabling real-time progress reporting.
func (w *workerProc) dispatch(prompt string, wid string, onText func(string), onToolUse func(name string), onLine func(lineType string, raw []byte)) (string, error) {
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
			log.Printf("[%s] unparseable line: %.100s", wid, line)
			continue
		}

		if debug {
			log.Printf("[%s] ndjson: type=%s line=%.200s", wid, base.Type, line)
		}

		if onLine != nil {
			// Store a copy — scanner reuses the buffer.
			onLine(base.Type, append([]byte(nil), line...))
		}

		switch base.Type {
		case "assistant":
			var msg struct {
				Message struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
						Name string `json:"name"`
					} `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &msg) == nil {
				for _, b := range msg.Message.Content {
					switch {
					case b.Type == "text" && b.Text != "":
						if debug {
							log.Printf("[%s] onText: %.200s", wid, b.Text)
						}
						textParts = append(textParts, b.Text)
						if onText != nil {
							onText(b.Text)
						}
					case b.Type == "tool_use" && b.Name != "":
						if debug {
							log.Printf("[%s] onToolUse: %s", wid, b.Name)
						}
						if onToolUse != nil {
							onToolUse(b.Name)
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

	log.Printf("[%s] done: %d NDJSON lines, result=%d bytes, textParts=%d, err=%q, waitErr=%v",
		wid, lineCount, len(resultText), len(textParts), errMsg, waitErr)

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
	mu         sync.Mutex
	idle       map[string][]*workerProc // key: cwd + "\x00" + model
	maxIdlePer int
}

func newPool(maxIdlePerKey int) *pool {
	if maxIdlePerKey <= 0 {
		maxIdlePerKey = 1
	}
	return &pool{
		idle:       make(map[string][]*workerProc),
		maxIdlePer: maxIdlePerKey,
	}
}

func poolKey(cwd, model string, depth int) string {
	return fmt.Sprintf("%s\x00%s\x00%d", cwd, model, depth)
}

func (p *pool) take(cwd, model string, depth int) *workerProc {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := poolKey(cwd, model, depth)
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
	key := poolKey(w.cwd, w.model, w.depth)
	if len(p.idle[key]) >= p.maxIdlePer {
		w.kill()
		log.Printf("pool: discarded excess %s worker for %s (cap %d)", w.model, w.cwd, p.maxIdlePer)
		return
	}
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

	// Claude Code encodes the cwd by replacing "/", ".", and "_" with "-" and prepending "-".
	encoded := "-" + strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(cwd[1:])
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

// --- Depth context ---

type contextKey string

const depthKey contextKey = "depth"
const parentIDKey contextKey = "parentID"

func depthFromContext(ctx context.Context) int {
	if v, ok := ctx.Value(depthKey).(int); ok {
		return v
	}
	return 0
}

func parentIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(parentIDKey).(string); ok {
		return v
	}
	return ""
}

// delegationGuidance returns prompt text that progressively discourages
// further cwork delegation as depth increases.
func delegationGuidance(depth int) string {
	switch {
	case depth <= 0:
		return ""
	case depth == 1:
		return `
=== DELEGATION POLICY ===
You have access to cwork for sub-delegation, but prefer doing the work
yourself. Only delegate if the task clearly decomposes into independent
subtasks that would benefit from parallelism. For sequential work, just
do it directly.
=== END POLICY ===
`
	case depth == 2:
		return `
=== DELEGATION POLICY ===
You have access to cwork but you SHOULD NOT use it unless the task is
very large with obviously independent parts. Do the work yourself.
=== END POLICY ===
`
	default:
		return `
=== DELEGATION POLICY ===
You are at maximum delegation depth. cwork calls WILL FAIL. Do all work
directly — do not attempt to delegate.
=== END POLICY ===
`
	}
}

// --- SQLite Store ---

const dbSchema = `
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	client_name TEXT NOT NULL DEFAULT '',
	client_version TEXT NOT NULL DEFAULT '',
	cwd TEXT NOT NULL DEFAULT '',
	transcript TEXT NOT NULL DEFAULT '',
	depth INTEGER NOT NULL DEFAULT 0,
	connected_at TEXT NOT NULL,
	disconnected_at TEXT
);
CREATE TABLE IF NOT EXISTS workers (
	id TEXT PRIMARY KEY,
	parent_id TEXT,
	display_name TEXT NOT NULL,
	cwd TEXT NOT NULL,
	model TEXT NOT NULL,
	task TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'running',
	started_at TEXT NOT NULL,
	ended_at TEXT
);
CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	worker_id TEXT NOT NULL REFERENCES workers(id),
	type TEXT NOT NULL,
	data TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_worker ON events(worker_id);
CREATE INDEX IF NOT EXISTS idx_workers_cwd ON workers(cwd);
CREATE INDEX IF NOT EXISTS idx_workers_status ON workers(status);
CREATE INDEX IF NOT EXISTS idx_sessions_connected ON sessions(connected_at);
`

func initDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec(dbSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	// Migrations: add columns/indexes that may not exist in older DBs.
	db.Exec(`ALTER TABLE workers ADD COLUMN session_id TEXT REFERENCES sessions(id)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_workers_session ON workers(session_id)`)
	db.Exec(`ALTER TABLE sessions ADD COLUMN cwd TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE sessions ADD COLUMN transcript TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE sessions ADD COLUMN depth INTEGER NOT NULL DEFAULT 0`)

	// Purge sessions from previous server runs. They're ephemeral —
	// active clients will reconnect and get fresh registrations.
	db.Exec(`DELETE FROM sessions`)

	return db, nil
}

func newWorkerID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// sessionRow is the JSON shape returned by the sessions API.
type sessionRow struct {
	ID             string  `json:"id"`
	ClientName     string  `json:"client_name"`
	ClientVersion  string  `json:"client_version"`
	CWD            string  `json:"cwd"`
	Transcript     string  `json:"transcript"`
	Depth          int     `json:"depth"`
	ConnectedAt    string  `json:"connected_at"`
	DisconnectedAt *string `json:"disconnected_at,omitempty"`
}

// workerRow is the JSON shape returned by the workers API (no events).
type workerRow struct {
	ID          string  `json:"id"`
	SessionID   *string `json:"session_id,omitempty"`
	ParentID    *string `json:"parent_id"`
	DisplayName string  `json:"display_name"`
	CWD         string  `json:"cwd"`
	Model       string  `json:"model"`
	Task        string  `json:"task"`
	Status      string  `json:"status"`
	StartedAt   string  `json:"started_at"`
	EndedAt     *string `json:"ended_at,omitempty"`
}

// eventRow is the JSON shape returned by the events API.
type eventRow struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Data      string `json:"data"`
	CreatedAt string `json:"created_at"`
}

// --- SSE Messages ---

type sseMsg struct {
	Event string `json:"event"`
	// Only one of the following is set, depending on Event.
	Worker  *workerRow  `json:"worker,omitempty"`
	Session *sessionRow `json:"session,omitempty"`
	ID      string      `json:"id,omitempty"`
	Entry   *eventRow   `json:"entry,omitempty"`
	Status  string      `json:"status,omitempty"`
}

func (m sseMsg) encode() []byte {
	data, _ := json.Marshal(m)
	return data
}

// writeSSE writes an SSE data frame and flushes.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, msg sseMsg) {
	fmt.Fprintf(w, "data: %s\n\n", msg.encode())
	flusher.Flush()
}

// --- SSE Event Hub ---

type sseClient struct {
	ch       chan []byte
	workerID string // empty = lifecycle only; set = also receive events for this worker
}

type eventHub struct {
	mu      sync.RWMutex
	clients map[*sseClient]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{clients: make(map[*sseClient]struct{})}
}

func (h *eventHub) subscribe(workerID string) *sseClient {
	c := &sseClient{
		ch:       make(chan []byte, 64),
		workerID: workerID,
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *eventHub) unsubscribe(c *sseClient) {
	h.mu.Lock()
	delete(h.clients, c)
	close(c.ch)
	h.mu.Unlock()
}

// broadcastLifecycle sends a lifecycle event (worker_start, worker_done)
// to all connected clients.
func (h *eventHub) broadcastLifecycle(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		select {
		case c.ch <- data:
		default:
		}
	}
}

// sendWorkerEvent sends a worker_event only to clients subscribed to
// the given worker ID.
func (h *eventHub) sendWorkerEvent(workerID string, data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.workerID == workerID {
			select {
			case c.ch <- data:
			default:
			}
		}
	}
}

// --- MCP Broker ---

type broker struct {
	pool         *pool
	reg          *shadowRegistry
	port         int
	db           *sql.DB
	mu           sync.Mutex
	active       map[*workerProc]struct{}
	nextID       atomic.Int64
	eventHub *eventHub
}

// nextDisplayID generates a sequential integer for building hierarchical
// display names (e.g. "w3", which a child extends to "w3.1").
func (b *broker) nextDisplayID() int64 {
	return b.nextID.Add(1)
}

func (b *broker) registerSession(sessionID, clientName, clientVersion string, depth int) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := b.db.Exec(
		`INSERT OR REPLACE INTO sessions (id, client_name, client_version, cwd, depth, connected_at) VALUES (?, ?, ?, '', ?, ?)`,
		sessionID, clientName, clientVersion, depth, now,
	)
	if err != nil {
		log.Printf("db: insert session: %v", err)
		return
	}
	row := sessionRow{
		ID: sessionID, ClientName: clientName, ClientVersion: clientVersion, Depth: depth, ConnectedAt: now,
	}
	b.eventHub.broadcastLifecycle(sseMsg{Event: "session_start", Session: &row}.encode())
}

func (b *broker) updateSessionCWD(sessionID, cwd string) {
	// Best-effort transcript discovery from CWD.
	var transcript string
	if tp, err := discoverTranscript(cwd); err == nil {
		// Store just the base filename (which is the Claude Code session ID).
		transcript = strings.TrimSuffix(filepath.Base(tp), ".jsonl")
		log.Printf("session %s: transcript=%s", sessionID, transcript)
	}

	_, err := b.db.Exec(`UPDATE sessions SET cwd = ?, transcript = ? WHERE id = ?`, cwd, transcript, sessionID)
	if err != nil {
		log.Printf("db: update session cwd: %v", err)
		return
	}
	row := sessionRow{ID: sessionID, CWD: cwd, Transcript: transcript}
	b.eventHub.broadcastLifecycle(sseMsg{Event: "session_update", Session: &row}.encode())
}

func (b *broker) disconnectSession(sessionID string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := b.db.Exec(
		`UPDATE sessions SET disconnected_at = ? WHERE id = ? AND disconnected_at IS NULL`,
		now, sessionID,
	)
	if err != nil {
		log.Printf("db: disconnect session: %v", err)
		return
	}
	b.eventHub.broadcastLifecycle(sseMsg{Event: "session_end", ID: sessionID}.encode())
}

func (b *broker) registerWorker(id, sessionID, parentID, displayName, cwd, model, task string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var parentPtr, sessionPtr *string
	if parentID != "" {
		parentPtr = &parentID
	}
	if sessionID != "" {
		sessionPtr = &sessionID
	}

	_, err := b.db.Exec(
		`INSERT INTO workers (id, session_id, parent_id, display_name, cwd, model, task, status, started_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'running', ?)`,
		id, sessionPtr, parentPtr, displayName, cwd, model, task, now,
	)
	if err != nil {
		log.Printf("db: insert worker: %v", err)
		return
	}

	row := workerRow{
		ID: id, SessionID: sessionPtr, ParentID: parentPtr, DisplayName: displayName,
		CWD: cwd, Model: model, Task: task, Status: "running", StartedAt: now,
	}
	b.eventHub.broadcastLifecycle(sseMsg{Event: "worker_start", Worker: &row}.encode())
}

func (b *broker) appendEvent(workerID, evType, evData string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := b.db.Exec(
		`INSERT INTO events (worker_id, type, data, created_at) VALUES (?, ?, ?, ?)`,
		workerID, evType, evData, now,
	)
	if err != nil {
		log.Printf("db: insert event: %v", err)
		return
	}

	entry := eventRow{Type: evType, Data: evData, CreatedAt: now}
	b.eventHub.sendWorkerEvent(workerID, sseMsg{Event: "worker_event", ID: workerID, Entry: &entry}.encode())
}

func (b *broker) finishWorker(id, status string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := b.db.Exec(
		`UPDATE workers SET status = ?, ended_at = ? WHERE id = ?`,
		status, now, id,
	)
	if err != nil {
		log.Printf("db: finish worker: %v", err)
		return
	}

	b.eventHub.broadcastLifecycle(sseMsg{Event: "worker_done", ID: id, Status: status}.encode())
}

func (b *broker) trackWorker(w *workerProc) {
	b.mu.Lock()
	b.active[w] = struct{}{}
	b.mu.Unlock()
}

func (b *broker) untrackWorker(w *workerProc) {
	b.mu.Lock()
	delete(b.active, w)
	b.mu.Unlock()
}

func (b *broker) shutdown() {
	b.pool.drain()
	b.mu.Lock()
	for w := range b.active {
		w.kill()
	}
	b.mu.Unlock()
}

func (b *broker) handleCwork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	task, _ := args["task"].(string)
	cwd, _ := args["cwd"].(string)
	model, _ := args["model"].(string)
	depth := depthFromContext(ctx)
	parentWID := parentIDFromContext(ctx)

	// Build hierarchical display name: w1, w1.1, w1.1.2, etc.
	seq := b.nextDisplayID()
	var displayName string
	if parentWID != "" {
		displayName = fmt.Sprintf("%s.%d", parentWID, seq)
	} else {
		displayName = fmt.Sprintf("w%d", seq)
	}

	// UUIDv7 for DB/API identity.
	id := newWorkerID()

	if depth >= maxDepth {
		log.Printf("[%s] rejected: max depth %d reached", displayName, maxDepth)
		return mcp.NewToolResultError(fmt.Sprintf("cwork rejected: maximum delegation depth (%d) reached — do the work directly", maxDepth)), nil
	}

	if task == "" {
		return mcp.NewToolResultError("missing required parameter: task"), nil
	}
	if cwd == "" {
		return mcp.NewToolResultError("missing required parameter: cwd"), nil
	}
	if model == "" {
		model = "sonnet"
	}

	// Extract MCP session ID for linking workers to sessions.
	var sessionID string
	if cs := server.ClientSessionFromContext(ctx); cs != nil {
		sessionID = cs.SessionID()
	}

	log.Printf("[%s] cwork request: depth=%d model=%s cwd=%s session=%s task=%.100s", displayName, depth, model, cwd, sessionID, task)

	// Register worker for dashboard tracking.
	taskSummary := task
	if len(taskSummary) > 200 {
		taskSummary = taskSummary[:200] + "..."
	}
	b.registerWorker(id, sessionID, parentWID, displayName, cwd, model, taskSummary)

	// Get or auto-register shadow for this cwd.
	s, err := b.reg.getOrCreate(cwd)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("shadow setup: %v", err)), nil
	}

	// Build prompt with shadow context and delegation guidance.
	var promptParts []string
	if guidance := delegationGuidance(depth); guidance != "" {
		promptParts = append(promptParts, guidance)
	}
	if shadowCtx := s.snapshot(); shadowCtx != "" {
		promptParts = append(promptParts,
			fmt.Sprintf("=== CONVERSATION CONTEXT (recent messages from root session) ===\n%s\n=== END CONTEXT ===", shadowCtx),
		)
	}
	promptParts = append(promptParts, fmt.Sprintf("TASK: %s", task))
	prompt := strings.Join(promptParts, "\n\n")

	// Try to get an idle worker.
	w := b.pool.take(s.cwd, model, depth)
	if w == nil {
		log.Printf("[%s] no idle %s worker, spawning cold", displayName, model)
		var err error
		w, err = spawnFunc(s.cwd, model, b.port, depth, displayName)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("spawn worker: %v", err)), nil
		}
	} else {
		log.Printf("[%s] dispatching to idle %s worker (pool: %d)", displayName, model, b.pool.count())
	}

	// Pre-warm a replacement in the background.
	go func() {
		rw, err := spawnFunc(s.cwd, model, b.port, depth, displayName)
		if err != nil {
			log.Printf("[%s] pre-warm failed: %v", displayName, err)
			return
		}
		b.pool.put(rw)
		log.Printf("[%s] pre-warmed %s worker (pool: %d)", displayName, model, b.pool.count())
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
	hadHeading := false

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
		hadHeading = true
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

	// onToolUse is called by dispatch for each tool_use content block.
	// When no heading has been seen yet, forward the tool name as a
	// low-priority progress indicator so the caller isn't left blind.
	onToolUse := func(name string) {
		lastActivity.Store(time.Now().UnixMilli())
		lastHeadingMu.Lock()
		haveHeading := hadHeading
		if !haveHeading {
			lastHeading = fmt.Sprintf("using %s", name)
		}
		lastHeadingMu.Unlock()
		if !haveHeading && throttle.shouldForward(1) {
			elapsed := int(time.Since(start).Seconds())
			sendProgress(fmt.Sprintf("[%ds] using %s", elapsed, name))
		}
	}

	// onLine stores every raw NDJSON line in the event log.
	onLine := func(lineType string, raw []byte) {
		b.appendEvent(id, lineType, string(raw))
	}

	// Dispatch in a goroutine so we can handle cancellation and heartbeats.
	b.trackWorker(w)
	type dispatchResult struct {
		text string
		err  error
	}
	done := make(chan dispatchResult, 1)
	go func() {
		result, err := w.dispatch(prompt, displayName, onText, onToolUse, onLine)
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
			b.untrackWorker(w)
			b.finishWorker(id, "error")
			w.kill()
			return mcp.NewToolResultError("request cancelled"), nil
		}
	}

finished:
	b.untrackWorker(w)
	if result.err != nil {
		b.finishWorker(id, "error")
		return mcp.NewToolResultError(fmt.Sprintf("worker error: %v", result.err)), nil
	}
	b.finishWorker(id, "done")

	// On first use for this cwd, append a setup suggestion.
	var setupHint string
	s.first.Do(func() {
		setupHint = `

---
SETUP NOTE: For best results, ensure CLAUDE.md (global or project)
contains a cworkers directive near the top. If it doesn't have one,
suggest adding this to the user:

` + "```" + `
## cworkers

**MANDATORY**: Never run builds (make, go build, npm run, etc.), tests,
file reads, or searches directly in the root session. Always delegate
via the ` + "`cwork`" + ` MCP tool. The only exceptions are trivial git commands
(git status, git diff) whose output you already know will be small.
When in doubt, delegate.

Workers start instantly (pre-warmed pool). Parallelise aggressively —
if tasks are independent, fire them as concurrent cwork calls rather
than sequencing. Don't batch unrelated work into one worker when
separate parallel workers would be faster.
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

// --- Dashboard HTTP handlers ---

func (b *broker) handleDashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, dashboardHTML)
}

func (b *broker) handleAPISessions(w http.ResponseWriter, _ *http.Request) {
	rows, err := b.db.Query(`SELECT id, client_name, client_version, cwd, transcript, depth, connected_at, disconnected_at FROM sessions ORDER BY connected_at`)
	if err != nil {
		http.Error(w, fmt.Sprintf("db query: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := make([]sessionRow, 0)
	for rows.Next() {
		var r sessionRow
		if err := rows.Scan(&r.ID, &r.ClientName, &r.ClientVersion, &r.CWD, &r.Transcript, &r.Depth, &r.ConnectedAt, &r.DisconnectedAt); err != nil {
			http.Error(w, fmt.Sprintf("db scan: %v", err), http.StatusInternalServerError)
			return
		}
		list = append(list, r)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (b *broker) handleAPIWorkers(w http.ResponseWriter, _ *http.Request) {
	rows, err := b.db.Query(`SELECT id, session_id, parent_id, display_name, cwd, model, task, status, started_at, ended_at FROM workers ORDER BY started_at`)
	if err != nil {
		http.Error(w, fmt.Sprintf("db query: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := make([]workerRow, 0)
	for rows.Next() {
		var r workerRow
		if err := rows.Scan(&r.ID, &r.SessionID, &r.ParentID, &r.DisplayName, &r.CWD, &r.Model, &r.Task, &r.Status, &r.StartedAt, &r.EndedAt); err != nil {
			http.Error(w, fmt.Sprintf("db scan: %v", err), http.StatusInternalServerError)
			return
		}
		list = append(list, r)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (b *broker) handleWorkerEvents(w http.ResponseWriter, r *http.Request) {
	workerID := r.PathValue("id")
	if workerID == "" {
		http.Error(w, "missing worker id", http.StatusBadRequest)
		return
	}

	rows, err := b.db.Query(`SELECT id, type, data, created_at FROM events WHERE worker_id = ? ORDER BY id`, workerID)
	if err != nil {
		http.Error(w, fmt.Sprintf("db query: %v", err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	list := make([]eventRow, 0)
	for rows.Next() {
		var r eventRow
		if err := rows.Scan(&r.ID, &r.Type, &r.Data, &r.CreatedAt); err != nil {
			http.Error(w, fmt.Sprintf("db scan: %v", err), http.StatusInternalServerError)
			return
		}
		list = append(list, r)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func handleHome(w http.ResponseWriter, _ *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	io.WriteString(w, home)
}

func handleOpenFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Line int    `json:"line,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	// Validate path is absolute and exists.
	if !filepath.IsAbs(req.Path) {
		http.Error(w, "path must be absolute", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(req.Path); err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if err := exec.Command("open", req.Path).Run(); err != nil {
		http.Error(w, "open failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (b *broker) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	// Send a hello event so the client knows the connection is live.
	writeSSE(w, flusher, sseMsg{Event: "hello"})

	workerID := r.URL.Query().Get("worker_id")
	c := b.eventHub.subscribe(workerID)
	defer b.eventHub.unsubscribe(c)

	for {
		select {
		case data := <-c.ch:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// --- Serve ---

// loadConfig reads ~/.config/cworkers/config.json and applies settings.
func loadConfig(homeDir string) {
	data, err := os.ReadFile(filepath.Join(homeDir, ".config", "cworkers", "config.json"))
	if err != nil {
		return // no config file is fine
	}
	var cfg struct {
		ClaudePath string `json:"claude_path"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("warning: invalid config.json: %v", err)
		return
	}
	if cfg.ClaudePath != "" {
		claudePath = cfg.ClaudePath
		log.Printf("claude path: %s (from config)", claudePath)
	}
}

func serve(port int) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("home dir: %v", err)
	}

	loadConfig(homeDir)

	dbPath := filepath.Join(homeDir, ".local", "share", "cworkers", "cworkers.db")
	db, err := initDB(dbPath)
	if err != nil {
		log.Fatalf("init db: %v", err)
	}
	defer db.Close()

	b := &broker{
		pool:     newPool(1),
		reg:      newShadowRegistry(),
		port:     port,
		db:       db,
		active:   make(map[*workerProc]struct{}),
		eventHub: newEventHub(),
	}

	hooks := &server.Hooks{}

	mcpSrv := server.NewMCPServer("cworkers", version,
		server.WithInstructions(agentGuide),
		server.WithHooks(hooks),
	)

	hooks.AddOnRegisterSession(func(ctx context.Context, session server.ClientSession) {
		sid := session.SessionID()
		depth := depthFromContext(ctx)
		var clientName, clientVersion string
		if ci, ok := session.(server.SessionWithClientInfo); ok {
			info := ci.GetClientInfo()
			clientName = info.Name
			clientVersion = info.Version
		}
		log.Printf("session connected: id=%s depth=%d client=%s/%s", sid, depth, clientName, clientVersion)
		b.registerSession(sid, clientName, clientVersion, depth)

		// Request roots from root sessions to discover CWD.
		if depth > 0 {
			return
		}
		go func() {
			// Brief delay to ensure the session is fully initialized.
			time.Sleep(500 * time.Millisecond)
			rootsCtx := mcpSrv.WithContext(context.Background(), session)
			result, err := mcpSrv.RequestRoots(rootsCtx, mcp.ListRootsRequest{})
			if err != nil {
				log.Printf("session %s: RequestRoots: %v", sid, err)
				return
			}
			if len(result.Roots) > 0 {
				uri := result.Roots[0].URI
				// Convert file:// URI to path.
				if parsed, err := url.Parse(uri); err == nil && parsed.Scheme == "file" {
					cwd := parsed.Path
					log.Printf("session %s: roots → cwd=%s", sid, cwd)
					b.updateSessionCWD(sid, cwd)
				} else {
					log.Printf("session %s: roots URI not file://: %s", sid, uri)
				}
			}
		}()
	})
	hooks.AddOnUnregisterSession(func(ctx context.Context, session server.ClientSession) {
		sid := session.SessionID()
		log.Printf("session disconnected: id=%s", sid)
		b.disconnectSession(sid)
	})

	mcpSrv.AddTool(
		mcp.NewTool("cwork",
			mcp.WithDescription("Dispatch a task to a worker agent. Returns the worker's result."),
			mcp.WithString("task", mcp.Required(), mcp.Description("The task prompt for the worker")),
			mcp.WithString("cwd", mcp.Required(), mcp.Description("Working directory of the calling session")),
			mcp.WithString("model", mcp.Description("Model to use (default: sonnet). Options: sonnet, opus, haiku")),
		),
		b.handleCwork,
	)

	// Inject depth from URL query string into context for each request.
	transport := server.NewStreamableHTTPServer(mcpSrv,
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			q := r.URL.Query()
			if d := q.Get("depth"); d != "" {
				if n, err := strconv.Atoi(d); err == nil {
					ctx = context.WithValue(ctx, depthKey, n)
				}
			}
			if wid := q.Get("wid"); wid != "" {
				ctx = context.WithValue(ctx, parentIDKey, wid)
			}
			return ctx
		}),
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", transport)
	mux.HandleFunc("/status", b.handleStatus)
	mux.HandleFunc("/dashboard", b.handleDashboard)
	mux.HandleFunc("/api/sessions", b.handleAPISessions)
	mux.HandleFunc("/api/workers", b.handleAPIWorkers)
	mux.HandleFunc("/api/workers/{id}/events", b.handleWorkerEvents)
	mux.HandleFunc("/api/events", b.handleSSE)
	mux.HandleFunc("POST /api/open", handleOpenFile)
	mux.HandleFunc("GET /api/home", handleHome)

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

	b.shutdown()
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
