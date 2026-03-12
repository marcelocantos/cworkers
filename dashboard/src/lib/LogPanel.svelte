<script>
  import { store } from './store.svelte.js';
  import { formatTime } from './util.js';
  import { Marked } from 'marked';
  import { markedHighlight } from 'marked-highlight';
  import hljs from 'highlight.js/lib/core';

  // Register common languages.
  import go from 'highlight.js/lib/languages/go';
  import javascript from 'highlight.js/lib/languages/javascript';
  import typescript from 'highlight.js/lib/languages/typescript';
  import python from 'highlight.js/lib/languages/python';
  import shellLang from 'highlight.js/lib/languages/shell';
  import json_lang from 'highlight.js/lib/languages/json';
  import yaml from 'highlight.js/lib/languages/yaml';
  import sql from 'highlight.js/lib/languages/sql';
  import css from 'highlight.js/lib/languages/css';
  import xml from 'highlight.js/lib/languages/xml';
  import markdown from 'highlight.js/lib/languages/markdown';
  import diff from 'highlight.js/lib/languages/diff';
  import rust from 'highlight.js/lib/languages/rust';
  import cpp from 'highlight.js/lib/languages/cpp';
  import c from 'highlight.js/lib/languages/c';

  hljs.registerLanguage('go', go);
  hljs.registerLanguage('javascript', javascript);
  hljs.registerLanguage('js', javascript);
  hljs.registerLanguage('typescript', typescript);
  hljs.registerLanguage('ts', typescript);
  hljs.registerLanguage('python', python);
  hljs.registerLanguage('py', python);
  hljs.registerLanguage('bash', shellLang);
  hljs.registerLanguage('shell', shellLang);
  hljs.registerLanguage('sh', shellLang);
  hljs.registerLanguage('zsh', shellLang);
  hljs.registerLanguage('json', json_lang);
  hljs.registerLanguage('yaml', yaml);
  hljs.registerLanguage('yml', yaml);
  hljs.registerLanguage('sql', sql);
  hljs.registerLanguage('css', css);
  hljs.registerLanguage('html', xml);
  hljs.registerLanguage('xml', xml);
  hljs.registerLanguage('markdown', markdown);
  hljs.registerLanguage('md', markdown);
  hljs.registerLanguage('diff', diff);
  hljs.registerLanguage('rust', rust);
  hljs.registerLanguage('rs', rust);
  hljs.registerLanguage('cpp', cpp);
  hljs.registerLanguage('c', c);

  const marked = new Marked(
    markedHighlight({
      langPrefix: 'hljs language-',
      highlight(code, lang) {
        if (lang && hljs.getLanguage(lang)) {
          return hljs.highlight(code, { language: lang }).value;
        }
        return hljs.highlightAuto(code).value;
      },
    }),
  );
  marked.setOptions({ gfm: true, breaks: true });

  function renderMarkdown(text) {
    return marked.parse(text);
  }

  let worker = $derived(store.selectedWorker);
  let events = $derived(store.selectedEvents);
  let parsed = $derived(events.map(ev => parseEvent(ev)));
  let headerText = $derived(
    worker
      ? `${worker.display_name} \u2014 ${worker.model || 'default'} \u2014 ${worker.status}`
      : 'Select a worker'
  );

  let scrollRef = $state(null);
  let autoFollow = $state(true);
  let expandedTools = $state(new Set());

  $effect(() => {
    if (autoFollow && events.length > 0 && scrollRef) {
      scrollRef.scrollTop = scrollRef.scrollHeight;
    }
  });

  function onScroll() {
    if (!scrollRef) return;
    const { scrollTop, scrollHeight, clientHeight } = scrollRef;
    autoFollow = scrollHeight - scrollTop - clientHeight < 40;
  }

  function toggleTool(index) {
    if (expandedTools.has(index)) {
      expandedTools.delete(index);
    } else {
      expandedTools.add(index);
    }
    expandedTools = new Set(expandedTools);
  }

  // Parse a raw event into a renderable shape.
  function parseEvent(ev) {
    let data;
    try {
      data = JSON.parse(ev.data);
    } catch {
      return { kind: 'unknown', raw: ev.data, time: ev.created_at };
    }

    const time = ev.created_at;

    switch (ev.type) {
      case 'assistant': {
        const blocks = [];
        for (const block of (data.message?.content ?? [])) {
          if (block.type === 'text' && block.text) {
            blocks.push({ kind: 'text', html: renderMarkdown(block.text) });
          } else if (block.type === 'tool_use') {
            blocks.push({
              kind: 'tool_use',
              name: block.name,
              input: block.input,
              id: block.id,
            });
          } else if (block.type === 'thinking') {
            // Skip thinking blocks.
          }
        }
        return { kind: 'assistant', blocks, time };
      }

      case 'user': {
        // Tool results.
        const results = [];
        for (const block of (data.message?.content ?? [])) {
          if (block.type === 'tool_result') {
            let content = block.content;
            if (typeof content !== 'string') {
              content = JSON.stringify(content, null, 2);
            }
            results.push({
              toolUseId: block.tool_use_id,
              content: content,
              isError: block.is_error ?? false,
            });
          }
        }
        return { kind: 'tool_result', results, time };
      }

      case 'result': {
        return {
          kind: 'result',
          subtype: data.subtype,
          result: data.result,
          durationMs: data.duration_ms,
          numTurns: data.num_turns,
          time,
        };
      }

      case 'system': {
        return { kind: 'system', subtype: data.subtype, time };
      }

      default:
        return { kind: 'other', type: ev.type, time };
    }
  }

  function truncate(s, max) {
    if (!s || s.length <= max) return s;
    return s.substring(0, max) + '\u2026';
  }

  function formatInput(input) {
    if (!input) return '';
    if (typeof input === 'string') return input;
    // Show key fields for common tools.
    if (input.command) return input.command;
    if (input.file_path) return input.file_path;
    if (input.pattern) return input.pattern + (input.path ? ` in ${input.path}` : '');
    if (input.query) return input.query;
    if (input.prompt) return truncate(input.prompt, 120);
    return JSON.stringify(input, null, 2);
  }
</script>

<div class="log-panel">
  <div class="panel-header">{headerText}</div>

  {#if !worker}
    <div class="empty-state">Select a worker to view its output</div>
  {:else if parsed.length === 0}
    <div class="empty-state">No events yet</div>
  {:else}
    <div
      bind:this={scrollRef}
      class="log-scroll"
      onscroll={onScroll}
    >
      {#each parsed as ev, i (i)}
        {#if ev.kind === 'assistant'}
          {#each ev.blocks as block, bi}
            {#if block.kind === 'text'}
              <div class="ev-text">
                {#if bi === 0}
                  <span class="ev-time">{formatTime(ev.time)}</span>
                {/if}
                <div class="md-body">
                  {@html block.html}
                </div>
              </div>
            {:else if block.kind === 'tool_use'}
              <button
                class="ev-tool"
                type="button"
                onclick={() => toggleTool(`${i}-${bi}`)}
              >
                <span class="tool-chevron">{expandedTools.has(`${i}-${bi}`) ? '\u25bc' : '\u25b8'}</span>
                <span class="tool-name">{block.name}</span>
                {#if !expandedTools.has(`${i}-${bi}`)}
                  <span class="tool-summary">{truncate(formatInput(block.input), 80)}</span>
                {/if}
                <span class="ev-time">{formatTime(ev.time)}</span>
              </button>
              {#if expandedTools.has(`${i}-${bi}`)}
                <pre class="tool-input">{JSON.stringify(block.input, null, 2)}</pre>
              {/if}
            {/if}
          {/each}

        {:else if ev.kind === 'tool_result'}
          {#each ev.results as res}
            <button
              class="ev-tool-result"
              class:is-error={res.isError}
              type="button"
              onclick={() => toggleTool(`res-${i}`)}
            >
              <span class="tool-chevron">{expandedTools.has(`res-${i}`) ? '\u25bc' : '\u25b8'}</span>
              <span class="result-label">{res.isError ? 'error' : 'result'}</span>
              {#if !expandedTools.has(`res-${i}`)}
                <span class="result-summary">{truncate(res.content, 100)}</span>
              {/if}
              <span class="ev-time">{formatTime(ev.time)}</span>
            </button>
            {#if expandedTools.has(`res-${i}`)}
              <pre class="tool-output" class:is-error={res.isError}>{res.content}</pre>
            {/if}
          {/each}

        {:else if ev.kind === 'result'}
          <div class="ev-result" class:success={ev.subtype === 'success'} class:error={ev.subtype !== 'success'}>
            <span class="result-badge">{ev.subtype}</span>
            {#if ev.durationMs}
              <span class="result-meta">{Math.round(ev.durationMs / 1000)}s, {ev.numTurns} turns</span>
            {/if}
            <span class="ev-time">{formatTime(ev.time)}</span>
          </div>

        {:else if ev.kind === 'system'}
          <div class="ev-system">
            <span class="system-label">system</span>
            <span class="system-subtype">{ev.subtype}</span>
            <span class="ev-time">{formatTime(ev.time)}</span>
          </div>

        {:else if ev.kind === 'other'}
          <div class="ev-system">
            <span class="system-label">{ev.type}</span>
            <span class="ev-time">{formatTime(ev.time)}</span>
          </div>
        {/if}
      {/each}
    </div>
  {/if}
</div>

<style>
  .log-panel {
    flex: 1;
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
  .log-scroll {
    flex: 1;
    overflow-y: auto;
    padding: 8px 0;
  }
  .empty-state {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
    color: var(--fg-dim);
    font-size: 14px;
  }

  /* Timestamp */
  .ev-time {
    color: var(--fg-dim);
    font-size: 10px;
    flex-shrink: 0;
    margin-left: auto;
  }

  /* Assistant text */
  .ev-text {
    padding: 6px 16px;
    border-left: 2px solid var(--accent);
    margin: 4px 12px;
    border-radius: 2px;
  }

  /* Tool use — clickable to expand */
  .ev-tool {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 3px 16px;
    color: var(--yellow);
    font-size: 12px;
    font-family: var(--mono);
    background: none;
    border: none;
    width: 100%;
    text-align: left;
    cursor: pointer;
  }
  .ev-tool:hover { background: var(--bg3); }
  .tool-chevron { font-size: 10px; width: 10px; flex-shrink: 0; }
  .tool-name { font-weight: 600; flex-shrink: 0; }
  .tool-summary {
    color: var(--fg-dim);
    font-size: 11px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .tool-input {
    margin: 0 16px 4px 32px;
    padding: 6px 10px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 4px;
    font-size: 11px;
    line-height: 1.5;
    color: var(--fg);
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }

  /* Tool result — clickable to expand */
  .ev-tool-result {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 3px 16px;
    color: var(--fg-dim);
    font-size: 12px;
    font-family: var(--mono);
    background: none;
    border: none;
    width: 100%;
    text-align: left;
    cursor: pointer;
  }
  .ev-tool-result:hover { background: var(--bg3); }
  .ev-tool-result.is-error { color: var(--red); }
  .result-label {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    color: var(--green);
  }
  .ev-tool-result.is-error .result-label { color: var(--red); }
  .result-summary {
    color: var(--fg-dim);
    font-size: 11px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .tool-output {
    margin: 0 16px 4px 32px;
    padding: 6px 10px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 4px;
    font-size: 11px;
    line-height: 1.5;
    color: var(--fg);
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 400px;
    overflow-y: auto;
  }
  .tool-output.is-error {
    border-color: var(--red);
    color: var(--red);
  }

  /* Final result */
  .ev-result {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 16px;
    margin: 4px 12px;
    border-radius: 4px;
    font-size: 12px;
  }
  .ev-result.success { background: rgba(158,206,106,0.1); border: 1px solid var(--green); }
  .ev-result.error { background: rgba(247,118,142,0.1); border: 1px solid var(--red); }
  .result-badge {
    font-weight: 700;
    text-transform: uppercase;
    font-size: 11px;
  }
  .ev-result.success .result-badge { color: var(--green); }
  .ev-result.error .result-badge { color: var(--red); }
  .result-meta { color: var(--fg-dim); font-size: 11px; }

  /* System events */
  .ev-system {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 2px 16px;
    font-size: 11px;
    color: var(--fg-dim);
  }
  .system-label {
    font-weight: 600;
    color: var(--fg-dim);
    text-transform: uppercase;
    font-size: 10px;
  }
  .system-subtype { font-size: 11px; }

  /* Markdown body */
  .md-body {
    font-family: system-ui, sans-serif;
    font-size: 13px;
    line-height: 1.6;
    color: var(--fg);
  }
  .md-body :global(h1) {
    font-size: 16px;
    font-weight: 700;
    color: var(--accent);
    margin: 8px 0 4px;
    border-bottom: 1px solid var(--border);
    padding-bottom: 4px;
  }
  .md-body :global(h2) {
    font-size: 14px;
    font-weight: 700;
    color: var(--accent);
    margin: 6px 0 2px;
  }
  .md-body :global(h3) {
    font-size: 12px;
    font-weight: 700;
    color: var(--cyan);
    margin: 4px 0 2px;
  }
  .md-body :global(p) {
    margin: 4px 0;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .md-body :global(ul),
  .md-body :global(ol) {
    margin: 4px 0;
    padding-left: 20px;
  }
  .md-body :global(li) { margin: 2px 0; }
  .md-body :global(strong) { color: var(--fg); font-weight: 700; }
  .md-body :global(em) { color: var(--fg); font-style: italic; }
  .md-body :global(code) {
    background: var(--bg);
    padding: 1px 4px;
    border-radius: 3px;
    font-size: 11px;
    font-family: var(--mono);
    color: var(--orange);
  }
  .md-body :global(pre) {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 8px 10px;
    margin: 6px 0;
    overflow-x: auto;
    line-height: 1.5;
  }
  .md-body :global(pre code) {
    background: none;
    padding: 0;
    color: var(--fg);
    font-size: 11px;
    font-family: var(--mono);
  }
  .md-body :global(blockquote) {
    border-left: 3px solid var(--fg-dim);
    padding-left: 10px;
    margin: 4px 0;
    color: var(--fg-dim);
  }
  .md-body :global(table) {
    border-collapse: collapse;
    margin: 6px 0;
    font-size: 11px;
  }
  .md-body :global(th),
  .md-body :global(td) {
    border: 1px solid var(--border);
    padding: 3px 8px;
  }
  .md-body :global(th) { background: var(--bg3); font-weight: 600; }
  .md-body :global(hr) {
    border: none;
    border-top: 1px solid var(--border);
    margin: 8px 0;
  }
  .md-body :global(a) { color: var(--accent); text-decoration: none; }
  .md-body :global(a:hover) { text-decoration: underline; }
  .md-body :global(input[type="checkbox"]) { margin-right: 4px; }
</style>
