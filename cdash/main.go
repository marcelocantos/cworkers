// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// cdash — TUI dashboard for cworkers.
// Sidebar of worker IDs, main panel shows selected worker's transcript
// rendered as markdown via glamour.

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"
)

// --- Data model ---

type activityEvent struct {
	TS    string `json:"ts"`
	ID    string `json:"id"`
	Event string `json:"event"`
	Model string `json:"model,omitempty"`
}

type detailEvent struct {
	TS    string `json:"ts"`
	Event string `json:"event"`
	Data  string `json:"data,omitempty"`
}

type worker struct {
	ID       string
	Model    string
	Status   string // "active", "done", "error", "stale"
	LastSeen time.Time
}

func dataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "cworkers")
}

// --- Log reading ---

func readActivity(path string, offset *int64, workers map[string]*worker) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	f.Seek(*offset, 0)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var ev activityEvent
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Event {
		case "start":
			model := ev.Model
			if model == "" {
				model = "sonnet"
			}
			workers[ev.ID] = &worker{
				ID: ev.ID, Model: model, Status: "active", LastSeen: time.Now(),
			}
		case "done":
			if w, ok := workers[ev.ID]; ok {
				w.Status = "done"
				w.LastSeen = time.Now()
			}
		case "error":
			if w, ok := workers[ev.ID]; ok {
				w.Status = "error"
				w.LastSeen = time.Now()
			}
		case "heartbeat":
			if w, ok := workers[ev.ID]; ok {
				w.LastSeen = time.Now()
			}
		}
	}

	pos, _ := f.Seek(0, 1)
	*offset = pos
}

func markStale(workers map[string]*worker) {
	cutoff := time.Now().Add(-30 * time.Second)
	for _, w := range workers {
		if w.Status == "active" && w.LastSeen.Before(cutoff) {
			w.Status = "stale"
		}
	}
}

func readWorkerLog(id string) []detailEvent {
	path := filepath.Join(dataDir(), "workers", id+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var events []detailEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		var ev detailEvent
		if json.Unmarshal(scanner.Bytes(), &ev) == nil {
			events = append(events, ev)
		}
	}
	return events
}

// --- Styles ---

var (
	sidebarStyle = lipgloss.NewStyle().
			Width(8).
			BorderRight(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("240"))

	activeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))  // green
	doneStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // gray
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))  // red
	staleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))  // yellow
	selectedStyle = lipgloss.NewStyle().Reverse(true)

	headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true) // cyan
	hlStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))            // yellow
	dimStyle    = lipgloss.NewStyle()
)

func statusStyle(status string) lipgloss.Style {
	switch status {
	case "active":
		return activeStyle
	case "error":
		return errorStyle
	case "stale":
		return staleStyle
	default:
		return doneStyle
	}
}

// --- Bubbletea model ---

type fileChangeMsg struct{}

type model struct {
	workers    map[string]*worker
	offset     int64
	path       string
	showAll    bool
	selected   int
	viewport   viewport.Model
	width      int
	height     int
	watcher    *fsnotify.Watcher
	renderer   *glamour.TermRenderer
}

func newModel() model {
	dir := dataDir()
	path := filepath.Join(dir, "activity.jsonl")

	workers := make(map[string]*worker)
	var offset int64
	readActivity(path, &offset, workers)

	w, _ := fsnotify.NewWatcher()
	if w != nil {
		w.Add(path)
	}

	r, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)

	return model{
		workers:  workers,
		offset:   offset,
		path:     path,
		showAll:  false,
		selected: 0,
		watcher:  w,
		renderer: r,
	}
}

func (m model) sortedIDs() []string {
	var ids []string
	for _, w := range m.workers {
		if m.showAll || w.Status == "active" || w.Status == "stale" {
			ids = append(ids, w.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		wi, wj := m.workers[ids[i]], m.workers[ids[j]]
		ai := wi.Status == "active" || wi.Status == "stale"
		aj := wj.Status == "active" || wj.Status == "stale"
		if ai != aj {
			return ai
		}
		return ids[i] > ids[j]
	})
	return ids
}

func (m model) selectedID() string {
	ids := m.sortedIDs()
	if m.selected >= 0 && m.selected < len(ids) {
		return ids[m.selected]
	}
	return ""
}

func (m *model) refresh() {
	readActivity(m.path, &m.offset, m.workers)
	markStale(m.workers)
}

func (m *model) renderTranscript() string {
	id := m.selectedID()
	if id == "" {
		return "  (select a worker)"
	}

	events := readWorkerLog(id)
	if len(events) == 0 {
		return "  (no events)"
	}

	var sb strings.Builder
	for _, ev := range events {
		switch ev.Event {
		case "task":
			sb.WriteString("**TASK** ")
			sb.WriteString(ev.Data)
			sb.WriteString("\n\n---\n\n")
		case "progress":
			sb.WriteString(ev.Data)
			sb.WriteString("\n\n")
		case "tool_use":
			sb.WriteString("*using ")
			sb.WriteString(ev.Data)
			sb.WriteString("*\n\n")
		case "result":
			sb.WriteString(ev.Data)
			sb.WriteString("\n")
		case "error":
			sb.WriteString("**ERROR** ")
			sb.WriteString(ev.Data)
			sb.WriteString("\n")
		}
	}

	if m.renderer != nil {
		rendered, err := m.renderer.Render(sb.String())
		if err == nil {
			return rendered
		}
	}
	return sb.String()
}

// --- Tea interface ---

func (m model) Init() tea.Cmd {
	if m.watcher != nil {
		return watchFile(m.watcher)
	}
	return nil
}

func watchFile(w *fsnotify.Watcher) tea.Cmd {
	return func() tea.Msg {
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return nil
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					return fileChangeMsg{}
				}
			case _, ok := <-w.Errors:
				if !ok {
					return nil
				}
			}
		}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		contentWidth := m.width - 10 // sidebar + border
		if contentWidth < 20 {
			contentWidth = 20
		}
		m.viewport = viewport.New(contentWidth, m.height-2)
		if m.renderer != nil {
			m.renderer, _ = glamour.NewTermRenderer(
				glamour.WithAutoStyle(),
				glamour.WithWordWrap(contentWidth-4),
			)
		}
		m.viewport.SetContent(m.renderTranscript())
		return m, nil

	case fileChangeMsg:
		m.refresh()
		m.viewport.SetContent(m.renderTranscript())
		return m, watchFile(m.watcher)

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.selected > 0 {
				m.selected--
				m.viewport.SetContent(m.renderTranscript())
				m.viewport.GotoTop()
			}
		case "down", "j":
			ids := m.sortedIDs()
			if m.selected < len(ids)-1 {
				m.selected++
				m.viewport.SetContent(m.renderTranscript())
				m.viewport.GotoTop()
			}
		case "a":
			m.showAll = !m.showAll
			ids := m.sortedIDs()
			if m.selected >= len(ids) {
				m.selected = max(0, len(ids)-1)
			}
			m.viewport.SetContent(m.renderTranscript())
			m.viewport.GotoTop()
		case "pgdown", " ":
			m.viewport.HalfViewDown()
		case "pgup":
			m.viewport.HalfViewUp()
		}
		return m, nil
	}

	return m, nil
}

func (m model) View() string {
	if m.width == 0 {
		return ""
	}

	// Header.
	active := 0
	for _, w := range m.workers {
		if w.Status == "active" {
			active++
		}
	}
	header := headerStyle.Render(" cdash ") +
		fmt.Sprintf("| %d active / %d total", active, len(m.workers))

	// Sidebar.
	ids := m.sortedIDs()
	var sideLines []string
	for i, id := range ids {
		w := m.workers[id]
		line := statusStyle(w.Status).Render(id)
		if i == m.selected {
			line = selectedStyle.Render(id)
		}
		sideLines = append(sideLines, line)
	}
	// Pad sidebar to fill height.
	for len(sideLines) < m.height-2 {
		sideLines = append(sideLines, "")
	}
	sidebar := sidebarStyle.Render(strings.Join(sideLines, "\n"))

	// Content.
	content := m.viewport.View()

	// Footer.
	hl := hlStyle.Render
	var activeLabel, allLabel string
	if m.showAll {
		activeLabel = "active"
		allLabel = hl("all")
	} else {
		activeLabel = hl("active")
		allLabel = "all"
	}
	footer := fmt.Sprintf(" %s quit  %s select  %s %s/%s  %s scroll",
		hl("q"), hl("↑↓"), hl("a"), activeLabel, allLabel, hl("pgup/pgdn"))

	// Compose.
	main := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, content)
	return lipgloss.JoinVertical(lipgloss.Left, header, main, footer)
}

func main() {
	m := newModel()
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "cdash: %v\n", err)
		os.Exit(1)
	}
	if m.watcher != nil {
		m.watcher.Close()
	}
}
