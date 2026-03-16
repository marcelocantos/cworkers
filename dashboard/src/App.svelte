<script>
  import { onMount, onDestroy } from 'svelte';
  import { store } from './lib/store.svelte.js';
  import SessionNode from './lib/SessionNode.svelte';
  import CwdGroup from './lib/CwdGroup.svelte';
  import LogPanel from './lib/LogPanel.svelte';

  let { sessionWorkers, cwdGroups, childrenOf } = $derived(store.tree);
  let { running, total, connectedSessions, totalSessions } = $derived(store.counts);

  // MCP sessions: newest first, filtered by active state.
  let visibleSessionIDs = $derived(
    [...sessionWorkers.keys()].reverse().filter(sid => {
      if (store.filter !== 'running') return true;
      return store.isSessionActive(sid);
    })
  );

  // Legacy CWD groups (workers without session_id): newest first, filtered.
  let visibleCwdKeys = $derived(
    [...cwdGroups.keys()].reverse().filter(cwd => {
      if (store.filter !== 'running') return true;
      const rootIDs = cwdGroups.get(cwd) || [];
      return store.groupHasRunning(rootIDs);
    })
  );

  onMount(async () => {
    await store.loadInitial();
    store.restoreFromURL();
    store.connect();
  });

  onDestroy(() => {
    store.destroy();
  });
</script>

<header>
  <h1>cworkers</h1>
  <span class="status" class:connected={store.connected} class:disconnected={!store.connected}>
    {store.connected ? 'connected' : 'disconnected'}
  </span>
  <span class="status counts">
    {connectedSessions} session{connectedSessions !== 1 ? 's' : ''}
    &middot;
    {running} running / {total} total
  </span>
</header>

<div class="container">
  <div class="tree-panel">
    <div class="panel-header">Sessions</div>
    <div class="toolbar">
      <button
        class:active={store.filter === 'running'}
        onclick={() => store.setFilter('running')}
      >active</button>
      <button
        class:active={store.filter === 'all'}
        onclick={() => store.setFilter('all')}
      >all</button>
    </div>
    <div class="items" role="tree">
      {#each visibleSessionIDs as sid (sid)}
        <SessionNode
          sessionID={sid}
          rootIDs={sessionWorkers.get(sid) || []}
          {childrenOf}
        />
      {/each}
      {#each visibleCwdKeys as cwd (cwd)}
        <CwdGroup
          {cwd}
          rootIDs={cwdGroups.get(cwd) || []}
          {childrenOf}
        />
      {/each}
    </div>
  </div>

  <LogPanel />
</div>

<style>
  header {
    padding: 8px 16px;
    background: var(--bg2);
    border-bottom: 1px solid var(--border);
    display: flex;
    align-items: center;
    gap: 16px;
    flex-shrink: 0;
  }
  h1 {
    font-size: 14px;
    font-weight: 600;
    color: var(--accent);
  }
  .status {
    font-size: 12px;
    color: var(--fg-dim);
  }
  .connected { color: var(--green); }
  .disconnected { color: var(--red); }
  .container {
    display: flex;
    flex: 1;
    overflow: hidden;
  }
  .tree-panel {
    width: 380px;
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .panel-header {
    padding: 6px 12px;
    background: var(--bg3);
    border-bottom: 1px solid var(--border);
    font-size: 11px;
    font-weight: 600;
    color: var(--fg-dim);
    text-transform: uppercase;
    letter-spacing: 0.5px;
    flex-shrink: 0;
  }
  .items {
    overflow-y: auto;
    flex: 1;
  }
  .toolbar {
    padding: 6px 12px;
    background: var(--bg3);
    border-bottom: 1px solid var(--border);
    display: flex;
    gap: 8px;
    flex-shrink: 0;
  }
  .toolbar button {
    background: var(--bg2);
    color: var(--fg-dim);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 3px 10px;
    font-family: var(--mono);
    font-size: 11px;
    cursor: pointer;
  }
  .toolbar button:hover { color: var(--fg); border-color: var(--fg-dim); }
  .toolbar button.active { color: var(--accent); border-color: var(--accent); }
</style>
