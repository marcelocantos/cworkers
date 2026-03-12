// Reactive state for the cworkers dashboard.
// Uses Svelte 5 runes in a .svelte.js module for shared reactive state.

class WorkerState {
  /** @type {Record<string, import('./types').WorkerInfo>} */
  workers = $state({});
  /** @type {string[]} */
  workerOrder = $state([]);
  /** @type {Record<string, {id: string, client_name: string, client_version: string, connected_at: string, disconnected_at: string|null}>} */
  sessions = $state({});
  /** @type {string[]} */
  sessionOrder = $state([]);
  /** @type {string|null} */
  selectedID = $state(null);
  /** @type {'all'|'running'} */
  filter = $state('running');
  /** @type {boolean} */
  connected = $state(false);
  /** @type {Set<string>} */
  collapsedSessions = $state(new Set());
  /** @type {Array} */
  selectedEvents = $state([]);

  /** @type {EventSource|null} */
  #evtSource = null;
  #reconnectTimer = null;

  // --- Derived state ---

  get selectedWorker() {
    if (!this.selectedID) return null;
    return this.workers[this.selectedID] ?? null;
  }

  get counts() {
    let running = 0, total = 0;
    for (const id of this.workerOrder) {
      const w = this.workers[id];
      if (!w) continue;
      total++;
      if (w.status === 'running') running++;
    }
    const connectedSessions = this.sessionOrder.filter(
      sid => this.sessions[sid] && !this.sessions[sid].disconnected_at
    ).length;
    return { running, total, connectedSessions, totalSessions: this.sessionOrder.length };
  }

  /**
   * Build tree: MCP sessions -> root workers -> child workers.
   * Workers without a session_id (legacy) are grouped by cwd.
   */
  get tree() {
    const sessionWorkers = new Map(); // sessionID -> rootWorkerIDs[]
    const cwdGroups = new Map();      // cwd -> rootWorkerIDs[] (legacy/orphan)
    const childrenOf = new Map();

    for (const id of this.workerOrder) {
      const w = this.workers[id];
      if (!w) continue;

      if (w.parent_id) {
        if (!childrenOf.has(w.parent_id)) childrenOf.set(w.parent_id, []);
        childrenOf.get(w.parent_id).push(id);
      } else {
        const sid = w.session_id;
        if (sid) {
          if (!sessionWorkers.has(sid)) sessionWorkers.set(sid, []);
          sessionWorkers.get(sid).push(id);
        } else {
          const cwd = w.cwd || '(unknown)';
          if (!cwdGroups.has(cwd)) cwdGroups.set(cwd, []);
          cwdGroups.get(cwd).push(id);
        }
      }
    }

    // Include root sessions (depth 0), even those without workers.
    for (const sid of this.sessionOrder) {
      const s = this.sessions[sid];
      if (s && s.depth === 0 && !sessionWorkers.has(sid)) {
        sessionWorkers.set(sid, []);
      }
    }

    return { sessionWorkers, cwdGroups, childrenOf };
  }

  // --- Actions ---

  selectWorker(id) {
    this.selectedID = id;
    this.selectedEvents = [];

    this.#updateURL();
    if (id) {
      this.#loadWorkerEvents(id);
    }

    // Reconnect SSE with worker filter to stream new events.
    this.#connectSSE(id);
  }

  setFilter(f) {
    this.filter = f;
    this.#updateURL();
  }

  #updateURL() {
    const params = new URLSearchParams();
    if (this.filter !== 'running') params.set('filter', this.filter);
    const qs = params.toString();
    const hash = this.selectedID ? `#${this.selectedID}` : '';
    history.replaceState(null, '', `${location.pathname}${qs ? '?' + qs : ''}${hash}`);
  }

  // Restore selection and filter from URL.
  restoreFromURL() {
    const params = new URLSearchParams(location.search);
    const filter = params.get('filter');
    if (filter === 'all' || filter === 'running') {
      this.filter = filter;
    }

    const hash = location.hash.slice(1);
    if (hash) {
      this.selectedID = hash;
      this.#loadWorkerEvents(hash);
      this.#connectSSE(hash);
    }
  }

  toggleSession(key) {
    if (this.collapsedSessions.has(key)) {
      this.collapsedSessions.delete(key);
    } else {
      this.collapsedSessions.add(key);
    }
    // Trigger reactivity by reassigning.
    this.collapsedSessions = new Set(this.collapsedSessions);
  }

  isCollapsed(key) {
    return this.collapsedSessions.has(key);
  }

  hasRunningWorker(id) {
    const w = this.workers[id];
    if (!w) return false;
    if (w.status === 'running') return true;
    const children = this.tree.childrenOf.get(id) || [];
    return children.some(c => this.hasRunningWorker(c));
  }

  groupHasRunning(rootIDs) {
    return rootIDs.some(id => this.hasRunningWorker(id));
  }

  /** Check if a session is "active" (connected or has running workers). */
  isSessionActive(sessionID) {
    const s = this.sessions[sessionID];
    if (s && !s.disconnected_at) return true;
    const rootIDs = this.tree.sessionWorkers.get(sessionID) || [];
    return this.groupHasRunning(rootIDs);
  }

  countGroup(rootIDs) {
    let running = 0, total = 0;
    const walk = (id) => {
      const w = this.workers[id];
      if (!w) return;
      total++;
      if (w.status === 'running') running++;
      for (const c of (this.tree.childrenOf.get(id) || [])) walk(c);
    };
    rootIDs.forEach(walk);
    return { running, total };
  }

  /** Get CWD for a session: from roots discovery, or fallback to first worker's CWD. */
  sessionCWD(sessionID) {
    const s = this.sessions[sessionID];
    if (s && s.cwd) return s.cwd;
    const rootIDs = this.tree.sessionWorkers.get(sessionID) || [];
    if (rootIDs.length === 0) return null;
    const w = this.workers[rootIDs[0]];
    return w ? w.cwd : null;
  }

  // --- SSE connection ---

  connect() {
    this.#connectSSE(null);
  }

  #connectSSE(workerID) {
    this.#evtSource?.close();
    if (this.#reconnectTimer) {
      clearTimeout(this.#reconnectTimer);
      this.#reconnectTimer = null;
    }

    const url = workerID
      ? `/api/events?worker_id=${encodeURIComponent(workerID)}`
      : '/api/events';

    this.#evtSource = new EventSource(url);
    this.#evtSource.onopen = () => { this.connected = true; };
    this.#evtSource.onerror = () => {
      this.connected = false;
      this.#evtSource?.close();
      this.#reconnectTimer = setTimeout(() => this.#connectSSE(this.selectedID), 3000);
    };
    this.#evtSource.onmessage = (e) => this.#handleEvent(JSON.parse(e.data));
  }

  async loadInitial() {
    try {
      const [workersResp, sessionsResp] = await Promise.all([
        fetch('/api/workers'),
        fetch('/api/sessions'),
      ]);
      const workers = await workersResp.json();
      const sessions = await sessionsResp.json();

      if (workers) {
        for (const w of workers) {
          this.workers[w.id] = w;
          if (!this.workerOrder.includes(w.id)) {
            this.workerOrder = [...this.workerOrder, w.id];
          }
        }
      }

      if (sessions) {
        for (const s of sessions) {
          this.sessions[s.id] = s;
          if (!this.sessionOrder.includes(s.id)) {
            this.sessionOrder = [...this.sessionOrder, s.id];
          }
        }
      }
    } catch (e) {
      console.error('loadInitial:', e);
    }
  }

  async #loadWorkerEvents(id) {
    try {
      const resp = await fetch(`/api/workers/${encodeURIComponent(id)}/events`);
      const events = await resp.json();
      // Only apply if still the selected worker.
      if (this.selectedID === id) {
        this.selectedEvents = events || [];
      }
    } catch (e) {
      console.error('loadWorkerEvents:', e);
    }
  }

  #handleEvent(msg) {
    switch (msg.event) {
      case 'session_start': {
        const s = msg.session;
        this.sessions[s.id] = s;
        if (!this.sessionOrder.includes(s.id)) {
          this.sessionOrder = [...this.sessionOrder, s.id];
        }
        break;
      }

      case 'session_update': {
        const s = this.sessions[msg.session.id];
        if (s) {
          if (msg.session.cwd) s.cwd = msg.session.cwd;
          if (msg.session.transcript) s.transcript = msg.session.transcript;
        }
        break;
      }

      case 'session_end': {
        const s = this.sessions[msg.id];
        if (s) {
          s.disconnected_at = new Date().toISOString();
        }
        break;
      }

      case 'worker_start': {
        const w = msg.worker;
        this.workers[w.id] = w;
        if (!this.workerOrder.includes(w.id)) {
          this.workerOrder = [...this.workerOrder, w.id];
        }
        break;
      }

      case 'worker_event': {
        // Append to selectedEvents if this event is for the selected worker.
        if (this.selectedID === msg.id) {
          this.selectedEvents = [...this.selectedEvents, msg.entry];
        }
        break;
      }

      case 'worker_done': {
        const w = this.workers[msg.id];
        if (w) {
          w.status = msg.status;
          w.ended_at = new Date().toISOString();
        }
        break;
      }
    }
  }

  destroy() {
    this.#evtSource?.close();
    if (this.#reconnectTimer) clearTimeout(this.#reconnectTimer);
  }
}

export const store = new WorkerState();
