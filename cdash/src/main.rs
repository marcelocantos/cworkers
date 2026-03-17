// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! cdash — TUI dashboard for cworkers.
//! Sidebar of worker IDs, main panel shows selected worker's transcript.

use crossterm::{
    event::{self, Event, KeyCode, KeyEventKind},
    terminal::{disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen},
    ExecutableCommand,
};
use notify::{recommended_watcher, EventKind, RecursiveMode, Watcher};
use ratatui::{
    layout::{Constraint, Layout},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, List, ListItem, ListState, Paragraph, Wrap},
    DefaultTerminal, Frame,
};
use serde::Deserialize;
use std::{
    collections::BTreeMap,
    fs::File,
    io::{self, BufRead, BufReader, Seek, SeekFrom},
    path::PathBuf,
    sync::mpsc,
    time::{Duration, Instant},
};

// --- Data model ---

#[derive(Debug, Clone, Deserialize)]
struct ActivityEvent {
    ts: String,
    id: String,
    event: String,
    #[serde(default)]
    model: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
struct DetailEvent {
    #[allow(dead_code)]
    ts: String,
    event: String,
    #[serde(default)]
    data: Option<String>,
}

#[derive(Debug, Clone)]
struct Worker {
    id: String,
    model: String,
    status: Status,
    last_seen: Instant,
}

#[derive(Debug, Clone, PartialEq)]
enum Status {
    Active,
    Done,
    Error,
    Stale,
}

impl Status {
    fn color(&self) -> Color {
        match self {
            Status::Active => Color::Green,
            Status::Done => Color::DarkGray,
            Status::Error => Color::Red,
            Status::Stale => Color::Yellow,
        }
    }

    fn is_alive(&self) -> bool {
        matches!(self, Status::Active | Status::Stale)
    }
}

// --- Log reading ---

fn data_dir() -> PathBuf {
    let home = std::env::var("HOME").unwrap_or_default();
    PathBuf::from(home).join(".local/share/cworkers")
}

fn read_activity(path: &PathBuf, offset: &mut u64, workers: &mut BTreeMap<String, Worker>) {
    let Ok(mut file) = File::open(path) else { return };
    file.seek(SeekFrom::Start(*offset)).ok();
    let reader = BufReader::new(&file);

    for line in reader.lines() {
        let Ok(line) = line else { break };
        if line.is_empty() { continue; }
        let Ok(ev) = serde_json::from_str::<ActivityEvent>(&line) else { continue; };

        match ev.event.as_str() {
            "start" => {
                workers.insert(ev.id.clone(), Worker {
                    id: ev.id,
                    model: ev.model.unwrap_or_else(|| "sonnet".into()),
                    status: Status::Active,
                    last_seen: Instant::now(),
                });
            }
            "done" => {
                if let Some(w) = workers.get_mut(&ev.id) {
                    w.status = Status::Done;
                    w.last_seen = Instant::now();
                }
            }
            "error" => {
                if let Some(w) = workers.get_mut(&ev.id) {
                    w.status = Status::Error;
                    w.last_seen = Instant::now();
                }
            }
            "heartbeat" => {
                if let Some(w) = workers.get_mut(&ev.id) {
                    w.last_seen = Instant::now();
                }
            }
            _ => {}
        }
    }

    *offset = file.metadata().map(|m| m.len()).unwrap_or(*offset);
}

fn mark_stale(workers: &mut BTreeMap<String, Worker>) {
    let cutoff = Instant::now() - Duration::from_secs(30);
    for w in workers.values_mut() {
        if w.status == Status::Active && w.last_seen < cutoff {
            w.status = Status::Stale;
        }
    }
}

fn read_worker_log(id: &str) -> Vec<DetailEvent> {
    let path = data_dir().join("workers").join(format!("{}.jsonl", id));
    let Ok(file) = File::open(path) else { return vec![] };
    BufReader::new(file)
        .lines()
        .filter_map(|l| l.ok())
        .filter(|l| !l.is_empty())
        .filter_map(|l| serde_json::from_str(&l).ok())
        .collect()
}

// --- App state ---

struct App {
    workers: BTreeMap<String, Worker>,
    list_state: ListState,
    offset: u64,
    path: PathBuf,
    show_all: bool,
    transcript_scroll: u16,
}

impl App {
    fn new() -> Self {
        let path = data_dir().join("activity.jsonl");
        let mut workers = BTreeMap::new();
        let mut offset = 0u64;
        read_activity(&path, &mut offset, &mut workers);
        let mut app = Self {
            workers,
            list_state: ListState::default(),
            offset,
            path,
            show_all: false,
            transcript_scroll: 0,
        };
        if !app.visible_ids().is_empty() {
            app.list_state.select(Some(0));
        }
        app
    }

    fn refresh(&mut self) {
        read_activity(&self.path, &mut self.offset, &mut self.workers);
        mark_stale(&mut self.workers);
    }

    fn visible_ids(&self) -> Vec<String> {
        let mut ws: Vec<&Worker> = if self.show_all {
            self.workers.values().collect()
        } else {
            self.workers.values().filter(|w| w.status.is_alive()).collect()
        };
        // Active/stale first, then by ID descending.
        ws.sort_by(|a, b| {
            let ord = if a.status.is_alive() == b.status.is_alive() {
                std::cmp::Ordering::Equal
            } else if a.status.is_alive() {
                std::cmp::Ordering::Less
            } else {
                std::cmp::Ordering::Greater
            };
            ord.then_with(|| b.id.cmp(&a.id))
        });
        ws.into_iter().map(|w| w.id.clone()).collect()
    }

    fn selected_id(&self) -> Option<String> {
        let ids = self.visible_ids();
        self.list_state.selected().and_then(|i| ids.get(i).cloned())
    }
}

// --- TUI ---

fn draw(frame: &mut Frame, app: &mut App) {
    let area = frame.area();

    // Top bar + main area + bottom bar.
    let outer = Layout::vertical([
        Constraint::Length(1),
        Constraint::Min(0),
        Constraint::Length(1),
    ]).split(area);

    // Header.
    let active = app.workers.values().filter(|w| w.status == Status::Active).count();
    let total = app.workers.len();
    let mode = if app.show_all { "all" } else { "active" };
    let header = Line::from(vec![
        Span::styled(" cdash ", Style::default().fg(Color::Cyan).add_modifier(Modifier::BOLD)),
        Span::raw(format!("| {} active / {} total [{}]", active, total, mode)),
    ]);
    frame.render_widget(Paragraph::new(header), outer[0]);

    // Sidebar (12 cols) + transcript.
    let main = Layout::horizontal([
        Constraint::Length(8),
        Constraint::Min(0),
    ]).split(outer[1]);

    // Sidebar: worker ID list.
    let ids = app.visible_ids();
    let items: Vec<ListItem> = ids.iter().map(|id| {
        let style = if let Some(w) = app.workers.get(id) {
            Style::default().fg(w.status.color())
        } else {
            Style::default()
        };
        ListItem::new(Span::styled(id.as_str(), style))
    }).collect();

    let list = List::new(items)
        .block(Block::default().borders(Borders::RIGHT))
        .highlight_style(Style::default().add_modifier(Modifier::REVERSED));

    frame.render_stateful_widget(list, main[0], &mut app.list_state);

    // Transcript panel.
    if let Some(selected) = app.selected_id() {
        let events = read_worker_log(&selected);
        let mut lines: Vec<Line> = Vec::new();

        for ev in &events {
            let (label, color) = match ev.event.as_str() {
                "task" => ("TASK", Color::Cyan),
                "progress" => ("    ", Color::Yellow),
                "tool_use" => ("TOOL", Color::Blue),
                "result" => ("DONE", Color::Green),
                "error" => ("ERR ", Color::Red),
                _ => ("    ", Color::DarkGray),
            };
            if let Some(data) = &ev.data {
                // For result/error, render as markdown.
                if ev.event == "result" || ev.event == "error" {
                    lines.push(Line::from(Span::styled(
                        label,
                        Style::default().fg(color).add_modifier(Modifier::BOLD),
                    )));
                    let md = tui_markdown::from_str(data);
                    lines.extend(md.lines.into_iter());
                    lines.push(Line::from(""));
                } else {
                    lines.push(Line::from(vec![
                        Span::styled(label, Style::default().fg(color).add_modifier(Modifier::BOLD)),
                        Span::raw(" "),
                        Span::raw(data.clone()),
                    ]));
                }
            } else {
                lines.push(Line::from(Span::styled(label, Style::default().fg(color))));
            }
        }

        if lines.is_empty() {
            lines.push(Line::from(Span::styled("(no events)", Style::default().fg(Color::DarkGray))));
        }

        let para = Paragraph::new(lines)
            .wrap(Wrap { trim: false })
            .scroll((app.transcript_scroll, 0));
        frame.render_widget(para, main[1]);
    } else {
        let empty = Paragraph::new(Span::styled(
            " select a worker",
            Style::default().fg(Color::DarkGray),
        ));
        frame.render_widget(empty, main[1]);
    }

    // Footer.
    let footer = Line::from(vec![
        Span::styled(" q", Style::default().fg(Color::Yellow)),
        Span::raw(" quit  "),
        Span::styled("↑↓", Style::default().fg(Color::Yellow)),
        Span::raw(" select  "),
        Span::styled("tab", Style::default().fg(Color::Yellow)),
        Span::raw(" active/all  "),
        Span::styled("pgup/pgdn", Style::default().fg(Color::Yellow)),
        Span::raw(" scroll"),
    ]);
    frame.render_widget(Paragraph::new(footer), outer[2]);
}

fn run(terminal: &mut DefaultTerminal, app: &mut App, rx: mpsc::Receiver<()>) -> io::Result<()> {
    loop {
        terminal.draw(|frame| draw(frame, app))?;

        while rx.try_recv().is_ok() {
            app.refresh();
        }

        if event::poll(Duration::from_secs(1))? {
            if let Event::Key(key) = event::read()? {
                if key.kind != KeyEventKind::Press { continue; }
                match key.code {
                    KeyCode::Char('q') | KeyCode::Esc => return Ok(()),
                    KeyCode::Up | KeyCode::Char('k') => {
                        app.list_state.select_previous();
                        app.transcript_scroll = 0;
                    }
                    KeyCode::Down | KeyCode::Char('j') => {
                        app.list_state.select_next();
                        app.transcript_scroll = 0;
                    }
                    KeyCode::Tab => {
                        app.show_all = !app.show_all;
                        let ids = app.visible_ids();
                        if ids.is_empty() {
                            app.list_state.select(None);
                        } else {
                            app.list_state.select(Some(0));
                        }
                        app.transcript_scroll = 0;
                    }
                    KeyCode::PageDown => {
                        app.transcript_scroll = app.transcript_scroll.saturating_add(10);
                    }
                    KeyCode::PageUp => {
                        app.transcript_scroll = app.transcript_scroll.saturating_sub(10);
                    }
                    _ => {}
                }
            }
        } else {
            mark_stale(&mut app.workers);
        }
    }
}

fn main() -> io::Result<()> {
    let (tx, rx) = mpsc::channel();
    let dir = data_dir();

    let mut watcher = recommended_watcher(move |res: Result<notify::Event, notify::Error>| {
        if let Ok(ev) = res {
            if matches!(ev.kind, EventKind::Modify(_) | EventKind::Create(_)) {
                let _ = tx.send(());
            }
        }
    }).expect("file watcher");

    let _ = watcher.watch(&dir, RecursiveMode::NonRecursive);

    enable_raw_mode()?;
    io::stdout().execute(EnterAlternateScreen)?;
    let mut terminal = ratatui::init();

    let mut app = App::new();
    let result = run(&mut terminal, &mut app, rx);

    ratatui::restore();
    io::stdout().execute(LeaveAlternateScreen)?;
    disable_raw_mode()?;

    result
}
