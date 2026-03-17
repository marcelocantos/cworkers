// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! cdash — TUI dashboard for cworkers.
//! Watches activity.jsonl and displays live worker status.

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
    widgets::{Block, Borders, Cell, Paragraph, Row, Table, TableState},
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

#[derive(Debug, Clone)]
struct Worker {
    id: String,
    model: String,
    status: Status,
    started_at: String,
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
    fn label(&self) -> &str {
        match self {
            Status::Active => "active",
            Status::Done => "done",
            Status::Error => "error",
            Status::Stale => "stale",
        }
    }

    fn color(&self) -> Color {
        match self {
            Status::Active => Color::Green,
            Status::Done => Color::DarkGray,
            Status::Error => Color::Red,
            Status::Stale => Color::Yellow,
        }
    }
}

// --- Log reading ---

fn activity_path() -> PathBuf {
    let home = std::env::var("HOME").unwrap_or_default();
    PathBuf::from(home).join(".local/share/cworkers/activity.jsonl")
}

fn read_activity(path: &PathBuf, offset: &mut u64, workers: &mut BTreeMap<String, Worker>) {
    let Ok(mut file) = File::open(path) else { return };
    file.seek(SeekFrom::Start(*offset)).ok();
    let reader = BufReader::new(&file);

    for line in reader.lines() {
        let Ok(line) = line else { break };
        if line.is_empty() {
            continue;
        }
        let Ok(ev) = serde_json::from_str::<ActivityEvent>(&line) else {
            continue;
        };

        match ev.event.as_str() {
            "start" => {
                workers.insert(
                    ev.id.clone(),
                    Worker {
                        id: ev.id,
                        model: ev.model.unwrap_or_else(|| "sonnet".into()),
                        status: Status::Active,
                        started_at: ev.ts,
                        last_seen: Instant::now(),
                    },
                );
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

// --- App state ---

struct App {
    workers: BTreeMap<String, Worker>,
    table_state: TableState,
    offset: u64,
    path: PathBuf,
}

impl App {
    fn new() -> Self {
        let path = activity_path();
        let mut workers = BTreeMap::new();
        let mut offset = 0u64;
        read_activity(&path, &mut offset, &mut workers);
        Self {
            workers,
            table_state: TableState::default(),
            offset,
            path,
        }
    }

    fn refresh(&mut self) {
        read_activity(&self.path, &mut self.offset, &mut self.workers);
        mark_stale(&mut self.workers);
    }

    fn sorted_workers(&self) -> Vec<Worker> {
        let mut ws: Vec<Worker> = self.workers.values().cloned().collect();
        ws.sort_by(|a, b| {
            let ord = match (&a.status, &b.status) {
                (Status::Active | Status::Stale, Status::Done | Status::Error) => {
                    std::cmp::Ordering::Less
                }
                (Status::Done | Status::Error, Status::Active | Status::Stale) => {
                    std::cmp::Ordering::Greater
                }
                _ => std::cmp::Ordering::Equal,
            };
            ord.then_with(|| b.id.cmp(&a.id))
        });
        ws
    }
}

// --- TUI ---

fn draw(frame: &mut Frame, app: &mut App) {
    let area = frame.area();
    let chunks =
        Layout::vertical([Constraint::Length(1), Constraint::Min(0), Constraint::Length(1)])
            .split(area);

    let active = app
        .workers
        .values()
        .filter(|w| w.status == Status::Active)
        .count();
    let total = app.workers.len();
    let header = Line::from(vec![
        Span::styled(
            " cdash ",
            Style::default()
                .fg(Color::Cyan)
                .add_modifier(Modifier::BOLD),
        ),
        Span::raw(format!("| {} active / {} total", active, total)),
    ]);
    frame.render_widget(Paragraph::new(header), chunks[0]);

    let workers = app.sorted_workers();
    let header_row = Row::new(vec!["ID", "Model", "Status", "Started"])
        .style(Style::default().add_modifier(Modifier::BOLD).fg(Color::DarkGray));

    let rows: Vec<Row> = workers
        .iter()
        .map(|w| {
            let status_style = Style::default().fg(w.status.color());
            Row::new(vec![
                Cell::from(w.id.clone()),
                Cell::from(w.model.clone()),
                Cell::from(w.status.label().to_string()).style(status_style),
                Cell::from(w.started_at.clone()),
            ])
        })
        .collect();

    let widths = [
        Constraint::Length(10),
        Constraint::Length(8),
        Constraint::Length(8),
        Constraint::Min(24),
    ];

    let table = Table::new(rows, widths)
        .header(header_row)
        .block(Block::default().borders(Borders::NONE))
        .row_highlight_style(Style::default().add_modifier(Modifier::REVERSED));

    frame.render_stateful_widget(table, chunks[1], &mut app.table_state);

    let footer = Line::from(vec![
        Span::styled(" q", Style::default().fg(Color::Yellow)),
        Span::raw(" quit  "),
        Span::styled("↑↓", Style::default().fg(Color::Yellow)),
        Span::raw(" navigate"),
    ]);
    frame.render_widget(Paragraph::new(footer), chunks[2]);
}

fn run(terminal: &mut DefaultTerminal, app: &mut App, rx: mpsc::Receiver<()>) -> io::Result<()> {
    loop {
        terminal.draw(|frame| draw(frame, app))?;

        while rx.try_recv().is_ok() {
            app.refresh();
        }

        if event::poll(Duration::from_secs(1))? {
            if let Event::Key(key) = event::read()? {
                if key.kind != KeyEventKind::Press {
                    continue;
                }
                match key.code {
                    KeyCode::Char('q') | KeyCode::Esc => return Ok(()),
                    KeyCode::Up | KeyCode::Char('k') => {
                        app.table_state.select_previous();
                    }
                    KeyCode::Down | KeyCode::Char('j') => {
                        app.table_state.select_next();
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
    let path = activity_path();

    let mut watcher = recommended_watcher(move |res: Result<notify::Event, notify::Error>| {
        if let Ok(ev) = res {
            if matches!(ev.kind, EventKind::Modify(_) | EventKind::Create(_)) {
                let _ = tx.send(());
            }
        }
    })
    .expect("file watcher");

    if let Some(parent) = path.parent() {
        let _ = watcher.watch(parent, RecursiveMode::NonRecursive);
    }

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
