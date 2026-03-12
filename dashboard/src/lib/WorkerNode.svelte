<script>
  import { store } from './store.svelte.js';
  import { formatElapsed } from './util.js';
  import WorkerNode from './WorkerNode.svelte';

  let { id, childrenOf, depth = 1 } = $props();

  let worker = $derived(store.workers[id]);
  let children = $derived(childrenOf.get(id) || []);
  let isSelected = $derived(store.selectedID === id);

  // Re-derive elapsed every second for running workers.
  let tick = $state(0);
  $effect(() => {
    if (worker?.status === 'running') {
      const iv = setInterval(() => { tick++; }, 1000);
      return () => clearInterval(iv);
    }
  });
  // Reference tick to force re-evaluation.
  let liveElapsed = $derived((tick, worker ? formatElapsed(worker.started_at, worker.ended_at) : ''));

  let idSuffix = $derived(id.slice(-6));
  let copied = $state(false);

  function select(e) {
    e.stopPropagation();
    store.selectWorker(id);
  }

  function copyID(e) {
    e.stopPropagation();
    navigator.clipboard.writeText(id);
    copied = true;
    setTimeout(() => { copied = false; }, 1500);
  }
</script>

{#if worker && (store.filter !== 'running' || store.hasRunningWorker(id))}
  <button
    class="worker-node"
    class:selected={isSelected}
    style:padding-left="{depth * 16 + 12}px"
    onclick={select}
    type="button"
  >
    <div class="row">
      <span class="wid">
        {children.length > 0 ? '\u251c ' : '\u2500 '}{worker.display_name}
      </span>
      <span class="uuid-chip" title="Click to copy full UUID" role="button" tabindex="-1" onclick={copyID} onkeydown={(e) => e.key === 'Enter' && copyID(e)}>
        {copied ? 'copied' : idSuffix}
      </span>
      <span class="badge {worker.status}">{worker.status}</span>
    </div>
    <div class="row sub">
      <span class="model">{worker.model || 'default'}</span>
      <span class="model">{liveElapsed}</span>
    </div>
    <div class="task">{worker.task}</div>
  </button>

  {#each children as childID (childID)}
    <WorkerNode id={childID} {childrenOf} depth={depth + 1} />
  {/each}
{/if}

<style>
  .worker-node {
    width: 100%;
    padding: 5px 12px;
    background: none;
    border: none;
    border-bottom: 1px solid var(--border);
    cursor: pointer;
    transition: background 0.1s;
    font-family: var(--mono);
    font-size: 13px;
    color: var(--fg);
    text-align: left;
    display: block;
  }
  .worker-node:hover { background: var(--bg3); }
  .worker-node.selected {
    background: var(--bg3);
    border-left: 3px solid var(--accent);
  }
  .row {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }
  .row.sub { margin-top: 2px; }
  .wid {
    font-weight: 600;
    color: var(--accent);
    font-size: 12px;
  }
  .model {
    color: var(--fg-dim);
    font-size: 11px;
  }
  .task {
    color: var(--fg);
    font-size: 12px;
    margin-top: 2px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .uuid-chip {
    color: var(--fg-dim);
    font-size: 10px;
    cursor: pointer;
    padding: 1px 4px;
    border-radius: 3px;
    background: var(--bg2);
    border: 1px solid var(--border);
  }
  .uuid-chip:hover {
    color: var(--fg);
    border-color: var(--fg-dim);
  }
  .badge {
    padding: 1px 6px;
    border-radius: 3px;
    font-size: 10px;
    font-weight: 600;
  }
  :global(.badge.running) { background: rgba(122,162,247,0.2); color: var(--accent); }
  :global(.badge.done) { background: rgba(158,206,106,0.2); color: var(--green); }
  :global(.badge.error) { background: rgba(247,118,142,0.2); color: var(--red); }
</style>
