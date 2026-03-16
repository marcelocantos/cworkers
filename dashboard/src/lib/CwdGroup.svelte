<script>
  import { store } from './store.svelte.js';
  import { shortenPath } from './util.js';
  import WorkerNode from './WorkerNode.svelte';

  let { cwd, rootIDs, childrenOf } = $props();

  let collapsed = $derived(store.isCollapsed(cwd));
  let counts = $derived(store.countGroup(rootIDs));
  let displayPath = $derived(shortenPath(cwd));
  let countText = $derived(
    counts.running > 0 ? `${counts.running}/${counts.total}` : `${counts.total}`
  );

  function toggle() {
    store.toggleSession(cwd);
  }

  let reversedRoots = $derived([...rootIDs].reverse());
</script>

<button class="session-node" onclick={toggle} type="button">
  <span class="toggle">{collapsed ? '\u25b6' : '\u25bc'}</span>
  <span class="session-path" title={cwd}>{displayPath}</span>
  <span class="session-counts">{countText}</span>
</button>

{#if !collapsed}
  {#each reversedRoots as id (id)}
    <WorkerNode {id} {childrenOf} depth={1} />
  {/each}
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
  .session-path {
    color: var(--cyan);
    font-weight: 600;
    font-size: 12px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
  }
  .session-counts {
    color: var(--fg-dim);
    font-size: 11px;
    flex-shrink: 0;
  }
</style>
