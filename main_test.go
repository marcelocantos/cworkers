// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Pool tests ---

// --- Status HTTP endpoint ---

func TestStatusEndpoint(t *testing.T) {
	b := &broker{
		active:   make(map[*workerProc]struct{}),
		eventHub: newEventHub(),
	}

	// Track two active workers.
	w1 := &workerProc{cwd: "/proj", model: "sonnet"}
	w2 := &workerProc{cwd: "/proj", model: "opus"}
	b.trackWorker(w1)
	b.trackWorker(w2)

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	b.handleStatus(w, req)

	var resp statusResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.ActiveWorkers != 2 {
		t.Errorf("want 2 active workers, got %d", resp.ActiveWorkers)
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
