// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Pool: add/take with model filtering ---

func TestPoolTakeAnyModel(t *testing.T) {
	p := &pool{}
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	p.add(c1, "opus")

	got := p.take("")
	if got != c1 {
		t.Fatal("take('') should match any model")
	}
	if p.count() != 0 {
		t.Fatalf("pool should be empty, got %d", p.count())
	}
}

func TestPoolTakeSpecificModel(t *testing.T) {
	p := &pool{}
	c1, c2 := net.Pipe()
	c3, c4 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	defer c3.Close()
	defer c4.Close()

	p.add(c1, "sonnet")
	p.add(c3, "opus")

	got := p.take("opus")
	if got != c3 {
		t.Fatal("take('opus') should skip sonnet and return opus worker")
	}
	if p.count() != 1 {
		t.Fatalf("pool should have 1 worker, got %d", p.count())
	}
}

func TestPoolTakeNoMatch(t *testing.T) {
	p := &pool{}
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	p.add(c1, "sonnet")

	got := p.take("opus")
	if got != nil {
		t.Fatal("take('opus') should return nil when only sonnet is available")
	}
	if p.count() != 1 {
		t.Fatalf("pool should still have 1 worker, got %d", p.count())
	}
}

func TestPoolCounts(t *testing.T) {
	p := &pool{}
	conns := make([]net.Conn, 6)
	for i := range conns {
		c1, c2 := net.Pipe()
		conns[i] = c1
		defer c1.Close()
		defer c2.Close()
	}

	p.add(conns[0], "opus")
	p.add(conns[1], "opus")
	p.add(conns[2], "sonnet")
	p.add(conns[3], "sonnet")
	p.add(conns[4], "sonnet")
	p.add(conns[5], "")

	counts := p.counts()
	if counts["opus"] != 2 {
		t.Errorf("opus count: want 2, got %d", counts["opus"])
	}
	if counts["sonnet"] != 3 {
		t.Errorf("sonnet count: want 3, got %d", counts["sonnet"])
	}
	if counts["any"] != 1 {
		t.Errorf("any count: want 1, got %d", counts["any"])
	}
}

// --- Dispatch queue waiters ---

func TestDispatchWaiterFulfilledByAdd(t *testing.T) {
	p := &pool{}

	// Register a waiter before any workers exist.
	ch := p.wait("opus", time.Now().Add(5*time.Second))

	// Add a matching worker — should fulfil the waiter.
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	p.add(c1, "opus")

	select {
	case got := <-ch:
		if got != c1 {
			t.Fatal("waiter should receive the added connection")
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not fulfilled within 1s")
	}

	// The worker should NOT be in the pool (it was given to the waiter).
	if p.count() != 0 {
		t.Fatalf("pool should be empty after waiter fulfilled, got %d", p.count())
	}
}

func TestDispatchWaiterModelMismatch(t *testing.T) {
	p := &pool{}

	ch := p.wait("opus", time.Now().Add(500*time.Millisecond))

	// Add a non-matching worker.
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	p.add(c1, "sonnet")

	select {
	case <-ch:
		t.Fatal("waiter for opus should not be fulfilled by sonnet worker")
	case <-time.After(100 * time.Millisecond):
		// Expected — waiter not fulfilled.
	}

	// sonnet worker should be in the pool.
	if p.count() != 1 {
		t.Fatalf("pool should have 1 worker, got %d", p.count())
	}
}

func TestDispatchWaiterExpired(t *testing.T) {
	p := &pool{}

	// Create a waiter that's already expired.
	ch := p.wait("opus", time.Now().Add(-1*time.Second))

	// Add a matching worker — should NOT fulfil the expired waiter.
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	p.add(c1, "opus")

	select {
	case <-ch:
		t.Fatal("expired waiter should not be fulfilled")
	case <-time.After(100 * time.Millisecond):
		// Expected.
	}

	// Worker should be in pool (waiter was skipped).
	if p.count() != 1 {
		t.Fatalf("pool should have 1 worker, got %d", p.count())
	}
}

func TestRemoveWaiter(t *testing.T) {
	p := &pool{}
	ch := p.wait("opus", time.Now().Add(5*time.Second))
	p.removeWaiter(ch)

	// Now add a worker — no waiter to receive it.
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	p.add(c1, "opus")

	if p.count() != 1 {
		t.Fatalf("pool should have 1 worker after waiter removed, got %d", p.count())
	}
}

func TestExpiredWaitersCompacted(t *testing.T) {
	p := &pool{}

	// Create several expired waiters.
	p.wait("opus", time.Now().Add(-1*time.Second))
	p.wait("opus", time.Now().Add(-1*time.Second))
	p.wait("sonnet", time.Now().Add(-1*time.Second))

	// Add a worker — expired waiters should be cleaned up.
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	p.add(c1, "opus")

	p.mu.Lock()
	nWaiters := len(p.waiters)
	p.mu.Unlock()

	if nWaiters != 0 {
		t.Fatalf("expired waiters should be compacted, got %d remaining", nWaiters)
	}
	if p.count() != 1 {
		t.Fatalf("worker should be in pool, got %d", p.count())
	}
}

func TestDispatchWaiterWildcard(t *testing.T) {
	p := &pool{}

	// Waiter with no model preference.
	ch := p.wait("", time.Now().Add(5*time.Second))

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	p.add(c1, "haiku")

	select {
	case got := <-ch:
		if got != c1 {
			t.Fatal("wildcard waiter should receive any model")
		}
	case <-time.After(time.Second):
		t.Fatal("wildcard waiter was not fulfilled")
	}
}

// --- Shadow snapshot rolling window ---

func TestShadowRollingWindow(t *testing.T) {
	s := newShadow(3)

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
	s := newShadow(10)
	if snap := s.snapshot(); snap != "" {
		t.Errorf("empty shadow should return empty string, got %q", snap)
	}
}

func TestShadowDefaultMaxLines(t *testing.T) {
	s := newShadow(0)
	if s.maxLines != 50 {
		t.Errorf("default maxLines should be 50, got %d", s.maxLines)
	}
}

func TestShadowConcurrency(t *testing.T) {
	s := newShadow(100)
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
	s := newShadow(10)
	line := `{"type":"user","message":{"role":"user","content":"hello broker"}}`
	processLine([]byte(line), s)

	snap := s.snapshot()
	if snap != "[User]: hello broker" {
		t.Errorf("processLine user: got %q", snap)
	}
}

func TestProcessLineAssistantMessage(t *testing.T) {
	s := newShadow(10)
	line := `{"type":"assistant","message":{"role":"assistant","content":"I can help"}}`
	processLine([]byte(line), s)

	snap := s.snapshot()
	if snap != "[Assistant]: I can help" {
		t.Errorf("processLine assistant: got %q", snap)
	}
}

func TestProcessLineSkipsNonUserAssistant(t *testing.T) {
	s := newShadow(10)
	line := `{"type":"system","message":{"role":"system","content":"system prompt"}}`
	processLine([]byte(line), s)

	if snap := s.snapshot(); snap != "" {
		t.Errorf("processLine should skip system type, got %q", snap)
	}
}

func TestProcessLineTruncatesLongMessages(t *testing.T) {
	s := newShadow(10)
	longText := strings.Repeat("x", 3000)
	line := fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"%s"}}`, longText)
	processLine([]byte(line), s)

	snap := s.snapshot()
	// Should be [User]: + 2000 chars + "..."
	if !strings.HasSuffix(snap, "...") {
		t.Error("long message should be truncated with ...")
	}
	// Role prefix "[User]: " = 8 chars, 2000 content, 3 "..." = 2011
	if len(snap) != 8+2000+3 {
		t.Errorf("truncated message length: want %d, got %d", 2011, len(snap))
	}
}

// --- End-to-end over Unix socket ---

func tempSock(t *testing.T) string {
	t.Helper()
	// Unix socket paths are limited to ~104 bytes on macOS.
	// t.TempDir() paths can exceed that, so use /tmp directly.
	f, err := os.CreateTemp("/tmp", "broker-test-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	os.Remove(path)
	t.Cleanup(func() { os.Remove(path) })
	return path
}

func startTestBroker(t *testing.T) (string, *shadowRegistry, func()) {
	t.Helper()
	sock := tempSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	p := &pool{}
	reg := newShadowRegistry()
	dispatchWait := 5 * time.Second

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleConn(conn, p, reg, dispatchWait)
		}
	}()

	return sock, reg, func() { ln.Close() }
}

func TestE2EWorkerReceivesTask(t *testing.T) {
	sock, _, cleanup := startTestBroker(t)
	defer cleanup()

	// Register a worker.
	wConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer wConn.Close()
	fmt.Fprintf(wConn, "WORKER opus\n")

	time.Sleep(50 * time.Millisecond)

	// Dispatch a task.
	dConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer dConn.Close()
	fmt.Fprintf(dConn, "DISPATCH opus\ndo something cool")
	if uc, ok := dConn.(*net.UnixConn); ok {
		uc.CloseWrite()
	}

	// Read dispatch response.
	resp, _ := io.ReadAll(dConn)
	if strings.TrimSpace(string(resp)) != "OK" {
		t.Fatalf("dispatch response: got %q, want OK", resp)
	}

	// Read the task on the worker side.
	task, _ := io.ReadAll(wConn)
	if string(task) != "do something cool" {
		t.Fatalf("worker received: %q, want 'do something cool'", task)
	}
}

func TestE2EDispatchQueueWaitsForWorker(t *testing.T) {
	sock, _, cleanup := startTestBroker(t)
	defer cleanup()

	// Dispatch first (no workers yet).
	dConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer dConn.Close()
	fmt.Fprintf(dConn, "DISPATCH opus\nqueued task")
	if uc, ok := dConn.(*net.UnixConn); ok {
		uc.CloseWrite()
	}

	time.Sleep(100 * time.Millisecond)

	// Now register a worker — should fulfil the queued dispatch.
	wConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer wConn.Close()
	fmt.Fprintf(wConn, "WORKER opus\n")

	// Worker should receive the task.
	wConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	task, err := io.ReadAll(wConn)
	if err != nil {
		t.Fatalf("worker read: %v", err)
	}
	if string(task) != "queued task" {
		t.Fatalf("worker received: %q, want 'queued task'", task)
	}

	// Dispatch should get OK.
	resp, _ := io.ReadAll(dConn)
	if strings.TrimSpace(string(resp)) != "OK" {
		t.Fatalf("dispatch response: got %q, want OK", resp)
	}
}

func TestE2EShadowRegisterAndDispatch(t *testing.T) {
	// Create a transcript file.
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")
	txContent := `{"type":"user","message":{"role":"user","content":"what is 2+2?"}}
{"type":"assistant","message":{"role":"assistant","content":"4"}}
`
	os.WriteFile(txPath, []byte(txContent), 0644)

	sock, _, cleanup := startTestBroker(t)
	defer cleanup()

	// Register shadow via protocol.
	sConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(sConn, "SHADOW sess1 %s 10\n", txPath)
	resp, _ := io.ReadAll(sConn)
	sConn.Close()
	if strings.TrimSpace(string(resp)) != "OK" {
		t.Fatalf("SHADOW response: got %q, want OK", resp)
	}

	// Give the tailer a moment to read.
	time.Sleep(200 * time.Millisecond)

	// Register a worker.
	wConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer wConn.Close()
	fmt.Fprintf(wConn, "WORKER\n")
	time.Sleep(50 * time.Millisecond)

	// Dispatch with session ID.
	dConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer dConn.Close()
	fmt.Fprintf(dConn, "DISPATCH  sess1\nsolve it")
	if uc, ok := dConn.(*net.UnixConn); ok {
		uc.CloseWrite()
	}

	// Worker should receive context + task.
	task, _ := io.ReadAll(wConn)
	taskStr := string(task)
	if !strings.Contains(taskStr, "CONVERSATION CONTEXT") {
		t.Error("task should include conversation context header")
	}
	if !strings.Contains(taskStr, "[User]: what is 2+2?") {
		t.Error("task should include user message from transcript")
	}
	if !strings.Contains(taskStr, "[Assistant]: 4") {
		t.Error("task should include assistant message from transcript")
	}
	if !strings.Contains(taskStr, "TASK: solve it") {
		t.Error("task should include the actual task text")
	}
}

func TestE2EShadowContextIncluded(t *testing.T) {
	// Create a transcript file.
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")
	txContent := `{"type":"user","message":{"role":"user","content":"what is 2+2?"}}
{"type":"assistant","message":{"role":"assistant","content":"4"}}
`
	os.WriteFile(txPath, []byte(txContent), 0644)

	sock, _, cleanup := startTestBroker(t)
	defer cleanup()

	// Register shadow via protocol.
	sConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(sConn, "SHADOW sess1 %s 10\n", txPath)
	resp, _ := io.ReadAll(sConn)
	sConn.Close()
	if strings.TrimSpace(string(resp)) != "OK" {
		t.Fatalf("SHADOW response: got %q, want OK", resp)
	}

	// Give the tailer a moment to read.
	time.Sleep(200 * time.Millisecond)

	// Register a worker.
	wConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer wConn.Close()
	fmt.Fprintf(wConn, "WORKER\n")
	time.Sleep(50 * time.Millisecond)

	// Dispatch with session.
	dConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer dConn.Close()
	fmt.Fprintf(dConn, "DISPATCH  sess1\nsolve it")
	if uc, ok := dConn.(*net.UnixConn); ok {
		uc.CloseWrite()
	}

	// Worker should receive context + task.
	task, _ := io.ReadAll(wConn)
	taskStr := string(task)
	if !strings.Contains(taskStr, "CONVERSATION CONTEXT") {
		t.Error("task should include conversation context header")
	}
	if !strings.Contains(taskStr, "[User]: what is 2+2?") {
		t.Error("task should include user message from transcript")
	}
	if !strings.Contains(taskStr, "[Assistant]: 4") {
		t.Error("task should include assistant message from transcript")
	}
	if !strings.Contains(taskStr, "TASK: solve it") {
		t.Error("task should include the actual task text")
	}
}

func TestE2EUnshadow(t *testing.T) {
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")
	txContent := `{"type":"user","message":{"role":"user","content":"hello"}}
`
	os.WriteFile(txPath, []byte(txContent), 0644)

	sock, _, cleanup := startTestBroker(t)
	defer cleanup()

	// Register shadow.
	sConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(sConn, "SHADOW sess1 %s\n", txPath)
	resp, _ := io.ReadAll(sConn)
	sConn.Close()
	if strings.TrimSpace(string(resp)) != "OK" {
		t.Fatalf("SHADOW response: got %q, want OK", resp)
	}

	time.Sleep(200 * time.Millisecond)

	// Unshadow.
	uConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(uConn, "UNSHADOW sess1\n")
	resp, _ = io.ReadAll(uConn)
	uConn.Close()
	if strings.TrimSpace(string(resp)) != "OK" {
		t.Fatalf("UNSHADOW response: got %q, want OK", resp)
	}

	// Register a worker.
	wConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer wConn.Close()
	fmt.Fprintf(wConn, "WORKER\n")
	time.Sleep(50 * time.Millisecond)

	// Dispatch with session — should have no context since unshadowed.
	dConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer dConn.Close()
	fmt.Fprintf(dConn, "DISPATCH  sess1\ndo work")
	if uc, ok := dConn.(*net.UnixConn); ok {
		uc.CloseWrite()
	}

	task, _ := io.ReadAll(wConn)
	taskStr := string(task)
	if strings.Contains(taskStr, "CONVERSATION CONTEXT") {
		t.Error("task should NOT include conversation context after unshadow")
	}
	if taskStr != "do work" {
		t.Errorf("worker should receive raw task, got %q", taskStr)
	}
}

func TestE2EMultipleSessionsShadow(t *testing.T) {
	dir := t.TempDir()

	// Session A transcript.
	txPathA := filepath.Join(dir, "transcriptA.jsonl")
	os.WriteFile(txPathA, []byte(`{"type":"user","message":{"role":"user","content":"session A context"}}
`), 0644)

	// Session B transcript.
	txPathB := filepath.Join(dir, "transcriptB.jsonl")
	os.WriteFile(txPathB, []byte(`{"type":"user","message":{"role":"user","content":"session B context"}}
`), 0644)

	sock, _, cleanup := startTestBroker(t)
	defer cleanup()

	// Register both shadows.
	for _, tc := range []struct {
		sess, path string
	}{
		{"sessA", txPathA},
		{"sessB", txPathB},
	} {
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(c, "SHADOW %s %s\n", tc.sess, tc.path)
		resp, _ := io.ReadAll(c)
		c.Close()
		if strings.TrimSpace(string(resp)) != "OK" {
			t.Fatalf("SHADOW %s response: got %q", tc.sess, resp)
		}
	}

	time.Sleep(200 * time.Millisecond)

	// Dispatch to session A.
	wA, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer wA.Close()
	fmt.Fprintf(wA, "WORKER\n")
	time.Sleep(50 * time.Millisecond)

	dA, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer dA.Close()
	fmt.Fprintf(dA, "DISPATCH  sessA\ntask A")
	if uc, ok := dA.(*net.UnixConn); ok {
		uc.CloseWrite()
	}

	taskA, _ := io.ReadAll(wA)
	taskAStr := string(taskA)
	if !strings.Contains(taskAStr, "session A context") {
		t.Error("task A should include session A context")
	}
	if strings.Contains(taskAStr, "session B context") {
		t.Error("task A should NOT include session B context")
	}

	// Dispatch to session B.
	wB, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer wB.Close()
	fmt.Fprintf(wB, "WORKER\n")
	time.Sleep(50 * time.Millisecond)

	dB, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer dB.Close()
	fmt.Fprintf(dB, "DISPATCH  sessB\ntask B")
	if uc, ok := dB.(*net.UnixConn); ok {
		uc.CloseWrite()
	}

	taskB, _ := io.ReadAll(wB)
	taskBStr := string(taskB)
	if !strings.Contains(taskBStr, "session B context") {
		t.Error("task B should include session B context")
	}
	if strings.Contains(taskBStr, "session A context") {
		t.Error("task B should NOT include session A context")
	}
}

func TestShadowRegistryConcurrency(t *testing.T) {
	reg := newShadowRegistry()
	dir := t.TempDir()

	var wg sync.WaitGroup

	// Concurrent register/unregister/get.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sessID := fmt.Sprintf("sess-%d", n%5)
			txPath := filepath.Join(dir, fmt.Sprintf("tx-%d.jsonl", n))
			os.WriteFile(txPath, []byte(`{"type":"user","message":{"role":"user","content":"hi"}}
`), 0644)

			reg.register(sessID, txPath, 10)
			_ = reg.get(sessID)
			_ = reg.count()
			reg.unregister(sessID)
			_ = reg.get(sessID)
		}(i)
	}

	wg.Wait()

	// All sessions should be unregistered.
	if c := reg.count(); c != 0 {
		t.Errorf("registry should be empty after all unregisters, got %d", c)
	}
}

func TestE2EStatus(t *testing.T) {
	sock, _, cleanup := startTestBroker(t)
	defer cleanup()

	// Add two workers.
	for _, model := range []string{"opus", "sonnet"} {
		c, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fmt.Fprintf(c, "WORKER %s\n", model)
	}

	time.Sleep(50 * time.Millisecond)

	// Query status.
	sConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer sConn.Close()
	fmt.Fprintf(sConn, "STATUS\n")

	resp, _ := io.ReadAll(sConn)
	respStr := string(resp)
	if !strings.Contains(respStr, "WORKERS: 2") {
		t.Errorf("status should show 2 workers, got %q", respStr)
	}
	if !strings.Contains(respStr, "opus: 1") {
		t.Errorf("status should show opus: 1, got %q", respStr)
	}
	if !strings.Contains(respStr, "sonnet: 1") {
		t.Errorf("status should show sonnet: 1, got %q", respStr)
	}
	if !strings.Contains(respStr, "shadows: 0") {
		t.Errorf("status should show shadows: 0, got %q", respStr)
	}
}

func TestE2EStatusWithSession(t *testing.T) {
	dir := t.TempDir()
	txPath := filepath.Join(dir, "transcript.jsonl")
	os.WriteFile(txPath, []byte(`{"type":"user","message":{"role":"user","content":"hello"}}
`), 0644)

	sock, _, cleanup := startTestBroker(t)
	defer cleanup()

	// Register a shadow for sess1.
	sConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(sConn, "SHADOW sess1 %s\n", txPath)
	resp, _ := io.ReadAll(sConn)
	sConn.Close()
	if strings.TrimSpace(string(resp)) != "OK" {
		t.Fatalf("SHADOW response: got %q", resp)
	}

	// Add an opus worker.
	wConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer wConn.Close()
	fmt.Fprintf(wConn, "WORKER opus\n")
	time.Sleep(50 * time.Millisecond)

	// Query session-scoped status for sess1.
	stConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer stConn.Close()
	fmt.Fprintf(stConn, "STATUS sess1\n")

	resp, _ = io.ReadAll(stConn)
	respStr := string(resp)
	if !strings.Contains(respStr, "SESSION: sess1") {
		t.Errorf("should contain session ID, got %q", respStr)
	}
	if !strings.Contains(respStr, "shadow: true") {
		t.Errorf("should show shadow: true, got %q", respStr)
	}
	if !strings.Contains(respStr, "available_workers: 1") {
		t.Errorf("should show available_workers: 1, got %q", respStr)
	}
	if !strings.Contains(respStr, "opus: 1") {
		t.Errorf("should show opus: 1, got %q", respStr)
	}
}

func TestE2EStatusWithSessionNoShadow(t *testing.T) {
	sock, _, cleanup := startTestBroker(t)
	defer cleanup()

	// Query session-scoped status for a session that doesn't exist.
	stConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer stConn.Close()
	fmt.Fprintf(stConn, "STATUS nosuch\n")

	resp, _ := io.ReadAll(stConn)
	respStr := string(resp)
	if !strings.Contains(respStr, "SESSION: nosuch") {
		t.Errorf("should contain session ID, got %q", respStr)
	}
	if !strings.Contains(respStr, "shadow: false") {
		t.Errorf("should show shadow: false, got %q", respStr)
	}
	if !strings.Contains(respStr, "available_workers: 0") {
		t.Errorf("should show available_workers: 0, got %q", respStr)
	}
}

func TestE2ENoWorkersTimeout(t *testing.T) {
	// Use a very short dispatch wait to test timeout.
	sock := tempSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	p := &pool{}
	reg := newShadowRegistry()
	dispatchWait := 200 * time.Millisecond

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleConn(conn, p, reg, dispatchWait)
		}
	}()

	dConn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer dConn.Close()
	fmt.Fprintf(dConn, "DISPATCH opus\ntask that will fail")
	if uc, ok := dConn.(*net.UnixConn); ok {
		uc.CloseWrite()
	}

	resp, _ := io.ReadAll(dConn)
	if strings.TrimSpace(string(resp)) != "NO_WORKERS" {
		t.Fatalf("expected NO_WORKERS, got %q", resp)
	}
}

func TestE2EUnknownCommand(t *testing.T) {
	sock, _, cleanup := startTestBroker(t)
	defer cleanup()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "BOGUS\n")

	reader := bufio.NewReader(conn)
	line, _ := reader.ReadString('\n')
	if !strings.Contains(line, "ERROR") {
		t.Errorf("unknown command should return ERROR, got %q", line)
	}
}

// --- processLine: empty role guard ---

func TestProcessLineEmptyRole(t *testing.T) {
	s := newShadow(10)
	line := `{"type":"user","message":{"role":"","content":"should be skipped"}}`
	processLine([]byte(line), s)

	if snap := s.snapshot(); snap != "" {
		t.Errorf("processLine should skip empty role, got %q", snap)
	}
}

// --- workerTryOnce ---

func TestWorkerTryOnceNoServer(t *testing.T) {
	result := workerTryOnce("/tmp/cworkers-nonexistent-test.sock", "opus", 100*time.Millisecond)
	if result != nil {
		t.Errorf("workerTryOnce with no server should return nil, got %q", result)
	}
}

func TestWorkerTryOnceReceivesTask(t *testing.T) {
	sock := tempSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		reader := bufio.NewReader(conn)
		reader.ReadString('\n')
		conn.Write([]byte("test task payload"))
		conn.Close()
	}()

	result := workerTryOnce(sock, "opus", 3*time.Second)
	if string(result) != "test task payload" {
		t.Errorf("workerTryOnce: got %q, want 'test task payload'", result)
	}
}

func TestWorkerTryOnceTimeout(t *testing.T) {
	sock := tempSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			defer conn.Close()
			time.Sleep(2 * time.Second)
		}
	}()

	start := time.Now()
	result := workerTryOnce(sock, "", 200*time.Millisecond)
	elapsed := time.Since(start)

	if result != nil {
		t.Errorf("workerTryOnce should return nil on timeout, got %q", result)
	}
	if elapsed > time.Second {
		t.Errorf("workerTryOnce should time out quickly, took %s", elapsed)
	}
}

// --- Pool drain ---

func TestPoolDrain(t *testing.T) {
	p := &pool{}
	c1, c2 := net.Pipe()
	c3, c4 := net.Pipe()
	defer c2.Close()
	defer c4.Close()

	p.add(c1, "opus")
	p.add(c3, "sonnet")

	ch := p.wait("haiku", time.Now().Add(5*time.Second))

	p.drain()

	if p.count() != 0 {
		t.Fatalf("pool should be empty after drain, got %d", p.count())
	}

	// Waiter channel should be closed.
	_, ok := <-ch
	if ok {
		t.Fatal("waiter channel should be closed after drain")
	}

	// Worker connections should be closed.
	_, err := c1.Write([]byte("test"))
	if err == nil {
		t.Fatal("c1 should be closed after drain")
	}
}
