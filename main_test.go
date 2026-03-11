// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Pool tests ---

func TestPoolTakeAndPut(t *testing.T) {
	p := newPool()
	w := &workerProc{cwd: "/proj", model: "sonnet"}
	p.put(w)

	got := p.take("/proj", "sonnet")
	if got != w {
		t.Fatal("take should return the put worker")
	}
	if p.count() != 0 {
		t.Fatalf("pool should be empty, got %d", p.count())
	}
}

func TestPoolTakeNoMatch(t *testing.T) {
	p := newPool()
	w := &workerProc{cwd: "/proj", model: "sonnet"}
	p.put(w)

	got := p.take("/proj", "opus")
	if got != nil {
		t.Fatal("take('opus') should return nil when only sonnet is available")
	}
	if p.count() != 1 {
		t.Fatalf("pool should still have 1 worker, got %d", p.count())
	}
}

func TestPoolTakeCwdIsolation(t *testing.T) {
	p := newPool()
	wA := &workerProc{cwd: "/projA", model: "sonnet"}
	wB := &workerProc{cwd: "/projB", model: "sonnet"}
	p.put(wA)
	p.put(wB)

	got := p.take("/projA", "sonnet")
	if got != wA {
		t.Fatal("take should return projA's worker")
	}
	if p.count() != 1 {
		t.Fatalf("pool should have 1 worker, got %d", p.count())
	}

	got = p.take("/projA", "sonnet")
	if got != nil {
		t.Fatal("take from projA should return nil when only projB remains")
	}
}

func TestPoolCounts(t *testing.T) {
	p := newPool()
	p.put(&workerProc{cwd: "/proj", model: "opus"})
	p.put(&workerProc{cwd: "/proj", model: "opus"})
	p.put(&workerProc{cwd: "/proj", model: "sonnet"})
	p.put(&workerProc{cwd: "/other", model: "sonnet"})

	if p.count() != 4 {
		t.Fatalf("total count: want 4, got %d", p.count())
	}

	counts := p.counts()
	if counts["opus"] != 2 {
		t.Errorf("opus count: want 2, got %d", counts["opus"])
	}
	if counts["sonnet"] != 2 {
		t.Errorf("sonnet count: want 2, got %d", counts["sonnet"])
	}
}

func TestPoolDrain(t *testing.T) {
	p := newPool()
	p.put(&workerProc{cwd: "/proj", model: "sonnet"})
	p.put(&workerProc{cwd: "/proj", model: "opus"})
	p.drain()

	if p.count() != 0 {
		t.Fatalf("pool should be empty after drain, got %d", p.count())
	}
}

// --- Shadow tests ---

func TestShadowRollingWindow(t *testing.T) {
	s := newShadow("", 3)

	s.add("msg1")
	s.add("msg2")
	s.add("msg3")

	snap := s.snapshot()
	if snap != "msg1\n\nmsg2\n\nmsg3" {
		t.Errorf("snapshot mismatch: got %q", snap)
	}

	// Adding a 4th message should evict the oldest.
	s.add("msg4")
	snap = s.snapshot()
	if snap != "msg2\n\nmsg3\n\nmsg4" {
		t.Errorf("after overflow, snapshot mismatch: got %q", snap)
	}
}

func TestShadowEmpty(t *testing.T) {
	s := newShadow("", 10)
	if snap := s.snapshot(); snap != "" {
		t.Errorf("empty shadow should return empty string, got %q", snap)
	}
}

func TestShadowDefaultMaxLines(t *testing.T) {
	s := newShadow("", 0)
	if s.maxLines != 50 {
		t.Errorf("default maxLines should be 50, got %d", s.maxLines)
	}
}

func TestShadowConcurrency(t *testing.T) {
	s := newShadow("", 100)
	var wg sync.WaitGroup

	// Concurrent writers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.add(fmt.Sprintf("writer-%d-msg-%d", n, j))
			}
		}(i)
	}

	// Concurrent readers.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = s.snapshot()
			}
		}()
	}

	wg.Wait()

	// After 1000 adds with maxLines=100, should have exactly 100.
	snap := s.snapshot()
	parts := strings.Split(snap, "\n\n")
	if len(parts) != 100 {
		t.Errorf("after concurrent adds, want 100 messages, got %d", len(parts))
	}
}

// --- extractText ---

func TestExtractTextString(t *testing.T) {
	raw := json.RawMessage(`"hello world"`)
	got := extractText(raw)
	if got != "hello world" {
		t.Errorf("extractText string: got %q", got)
	}
}

func TestExtractTextBlocks(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"part1"},{"type":"image","text":""},{"type":"text","text":"part2"}]`)
	got := extractText(raw)
	if got != "part1\npart2" {
		t.Errorf("extractText blocks: got %q", got)
	}
}

func TestExtractTextInvalid(t *testing.T) {
	raw := json.RawMessage(`12345`)
	got := extractText(raw)
	if got != "" {
		t.Errorf("extractText invalid: should return empty, got %q", got)
	}
}

// --- processLine ---

func TestProcessLineUserMessage(t *testing.T) {
	s := newShadow("", 10)
	line := `{"type":"user","message":{"role":"user","content":"hello broker"}}`
	processLine([]byte(line), s)

	snap := s.snapshot()
	if snap != "[User]: hello broker" {
		t.Errorf("processLine user: got %q", snap)
	}
}

func TestProcessLineAssistantMessage(t *testing.T) {
	s := newShadow("", 10)
	line := `{"type":"assistant","message":{"role":"assistant","content":"I can help"}}`
	processLine([]byte(line), s)

	snap := s.snapshot()
	if snap != "[Assistant]: I can help" {
		t.Errorf("processLine assistant: got %q", snap)
	}
}

func TestProcessLineSkipsNonUserAssistant(t *testing.T) {
	s := newShadow("", 10)
	line := `{"type":"system","message":{"role":"system","content":"system prompt"}}`
	processLine([]byte(line), s)

	if snap := s.snapshot(); snap != "" {
		t.Errorf("processLine should skip system type, got %q", snap)
	}
}

func TestProcessLineTruncatesLongMessages(t *testing.T) {
	s := newShadow("", 10)
	longText := strings.Repeat("x", 3000)
	line := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"%s"}}`, longText)
	processLine([]byte(line), s)

	snap := s.snapshot()
	if !strings.HasSuffix(snap, "...") {
		t.Error("long message should be truncated with ...")
	}
	// Role prefix "[User]: " = 8 chars, 2000 content, 3 "..." = 2011
	if len(snap) != 8+2000+3 {
		t.Errorf("truncated message length: want %d, got %d", 2011, len(snap))
	}
}

func TestProcessLineEmptyRole(t *testing.T) {
	s := newShadow("", 10)
	line := `{"type":"user","message":{"role":"","content":"should be skipped"}}`
	processLine([]byte(line), s)

	if snap := s.snapshot(); snap != "" {
		t.Errorf("processLine should skip empty role, got %q", snap)
	}
}

// --- Shadow Registry ---

func TestShadowRegistryGetOrCreate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := "/test/project"
	encoded := "-test-project"
	projectDir := filepath.Join(home, ".claude", "projects", encoded)
	os.MkdirAll(projectDir, 0755)
	os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(`{"type":"user","message":{"role":"user","content":"hi"}}
`), 0644)

	reg := newShadowRegistry()

	// First call creates the shadow.
	s1, err := reg.getOrCreate(cwd)
	if err != nil {
		t.Fatalf("getOrCreate: %v", err)
	}
	if s1 == nil {
		t.Fatal("getOrCreate returned nil shadow")
	}
	if reg.count() != 1 {
		t.Fatalf("want 1 shadow, got %d", reg.count())
	}

	// Second call returns the same shadow.
	s2, err := reg.getOrCreate(cwd)
	if err != nil {
		t.Fatalf("getOrCreate (2nd): %v", err)
	}
	if s1 != s2 {
		t.Fatal("second getOrCreate should return the same shadow")
	}
	if reg.count() != 1 {
		t.Fatalf("still want 1 shadow, got %d", reg.count())
	}
}

func TestShadowRegistryConcurrency(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Set up 5 distinct "projects".
	for i := 0; i < 5; i++ {
		encoded := fmt.Sprintf("-proj-%d", i)
		projectDir := filepath.Join(home, ".claude", "projects", encoded)
		os.MkdirAll(projectDir, 0755)
		os.WriteFile(filepath.Join(projectDir, "session.jsonl"), []byte(`{"type":"user","message":{"role":"user","content":"hi"}}
`), 0644)
	}

	reg := newShadowRegistry()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			cwd := fmt.Sprintf("/proj/%d", n%5)
			_, _ = reg.getOrCreate(cwd)
			_ = reg.count()
		}(i)
	}

	wg.Wait()

	if reg.count() != 5 {
		t.Errorf("want 5 shadows, got %d", reg.count())
	}
}

// --- discoverTranscript ---

func TestDiscoverTranscriptFindsNewest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := "/foo/bar"
	encoded := "-foo-bar"
	projectDir := filepath.Join(home, ".claude", "projects", encoded)
	os.MkdirAll(projectDir, 0755)

	oldFile := filepath.Join(projectDir, "old-session.jsonl")
	newFile := filepath.Join(projectDir, "new-session.jsonl")

	os.WriteFile(oldFile, []byte(`{"type":"user"}`), 0644)
	oldTime := time.Now().Add(-1 * time.Hour)
	os.Chtimes(oldFile, oldTime, oldTime)

	os.WriteFile(newFile, []byte(`{"type":"user"}`), 0644)

	transcript, err := discoverTranscript(cwd)
	if err != nil {
		t.Fatalf("discoverTranscript: %v", err)
	}

	if transcript != newFile {
		t.Errorf("should find newest file, got %s, want %s", transcript, newFile)
	}
}

func TestDiscoverTranscriptDotsInPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := "/Users/me/work/github.com/org/project"
	encoded := "-Users-me-work-github-com-org-project"
	projectDir := filepath.Join(home, ".claude", "projects", encoded)
	os.MkdirAll(projectDir, 0755)
	txFile := filepath.Join(projectDir, "session.jsonl")
	os.WriteFile(txFile, []byte(`{"type":"user"}`), 0644)

	transcript, err := discoverTranscript(cwd)
	if err != nil {
		t.Fatalf("discoverTranscript: %v", err)
	}
	if transcript != txFile {
		t.Errorf("got %s, want %s", transcript, txFile)
	}
}

func TestDiscoverTranscriptNoFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := "/foo/bar"
	encoded := "-foo-bar"
	projectDir := filepath.Join(home, ".claude", "projects", encoded)
	os.MkdirAll(projectDir, 0755)

	_, err := discoverTranscript(cwd)
	if err == nil {
		t.Fatal("should error when no transcripts found")
	}
}

func TestDiscoverTranscriptNoProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := discoverTranscript("/nonexistent/project")
	if err == nil {
		t.Fatal("should error when project dir doesn't exist")
	}
}

// --- Status HTTP endpoint ---

func TestStatusEndpoint(t *testing.T) {
	b := &broker{
		pool: newPool(),
		reg:  newShadowRegistry(),
	}

	b.pool.put(&workerProc{cwd: "/proj", model: "sonnet"})
	b.pool.put(&workerProc{cwd: "/proj", model: "opus"})

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	b.handleStatus(w, req)

	var resp statusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Workers != 2 {
		t.Errorf("want 2 workers, got %d", resp.Workers)
	}
	if resp.Models["sonnet"] != 1 {
		t.Errorf("want 1 sonnet, got %d", resp.Models["sonnet"])
	}
	if resp.Models["opus"] != 1 {
		t.Errorf("want 1 opus, got %d", resp.Models["opus"])
	}
	if resp.Shadows != 0 {
		t.Errorf("want 0 shadows, got %d", resp.Shadows)
	}
}

// --- filterEnv ---

func TestFilterEnv(t *testing.T) {
	env := []string{"PATH=/usr/bin", "CLAUDECODE=abc123", "HOME=/home/user"}
	filtered := filterEnv(env, "CLAUDECODE")

	for _, e := range filtered {
		if strings.HasPrefix(e, "CLAUDECODE=") {
			t.Error("CLAUDECODE should be filtered out")
		}
	}
	if len(filtered) != 2 {
		t.Errorf("want 2 env vars, got %d", len(filtered))
	}
}
