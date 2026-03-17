<script>
  import { store } from './store.svelte.js';
  import { shortenPath } from './util.js';
  import WorkerNode from './WorkerNode.svelte';

  let { sessionID, rootIDs, childrenOf } = $props();

  let session = $derived(store.sessions[sessionID]);
  let collapsed = $derived(store.isCollapsed(sessionID));
  let counts = $derived(store.countGroup(rootIDs));
  let isConnected = $derived(session && !session.disconnected_at);
  let cwd = $derived(store.sessionCWD(sessionID));
  let displayPath = $derived(cwd ? shortenPath(cwd) : null);
  let countText = $derived(
    counts.total === 0
      ? ''
      : counts.running > 0
        ? `${counts.running}/${counts.total}`
        : `${counts.total}`
  );

  function toggle() {
    store.toggleSession(sessionID);
  }

  // Reverse root workers so newest appears first.
  let reversedRoots = $derived([...rootIDs].reverse());
</script>

<button class="session-node" onclick={toggle} type="button">
  <span class="toggle">{collapsed ? '\u25b6' : '\u25bc'}</span>
  <span class="conn-dot" class:connected={isConnected} class:disconnected={!isConnected}
        title={isConnected ? 'connected' : 'disconnected'}></span>
  {#if displayPath}
    <span class="session-path" title={cwd}>{displayPath}</span>
  {:else}
    <span class="session-idle">idle</span>
  {/if}
  {#if countText}
    <span class="session-counts">{countText}</span>
  {/if}
</button>

{#if !collapsed}
  {#if rootIDs.length === 0}
    <div class="no-workers">no workers yet</div>
  {:else}
    {#each reversedRoots as id (id)}
      <WorkerNode {id} {childrenOf} depth={1} />
    {/each}
  {/if}
{/if}

<style>
  .session-node {
    width: 100%;
    padding: 6px 12px;
    background: none;
    border: none;
    border-bottom: 1px solid var(--border);
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 6px;
    font-family: var(--mono);
    font-size: 13px;
    color: var(--fg);
    text-align: left;
  }
  .session-node:hover { background: var(--bg3); }
  .toggle {
    color: var(--fg-dim);
    font-size: 10px;
    width: 12px;
    text-align: center;
    flex-shrink: 0;
  }
  .conn-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .conn-dot.connected { background: var(--green); }
  .conn-dot.disconnected { background: var(--fg-dim); opacity: 0.4; }
  .session-path {
    color: var(--cyan);
    font-weight: 600;
    font-size: 12px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
  }
  .session-idle {
    color: var(--fg-dim);
    font-style: italic;
    font-size: 12px;
    flex: 1;
  }
  .session-counts {
    color: var(--fg-dim);
    font-size: 11px;
    flex-shrink: 0;
  }
  .no-workers {
    padding: 4px 12px 4px 40px;
    color: var(--fg-dim);
    font-size: 11px;
    font-style: italic;
    border-bottom: 1px solid var(--border);
  }
</style>
