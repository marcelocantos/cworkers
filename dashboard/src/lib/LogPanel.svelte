<script>
  import { store } from './store.svelte.js';
  import { formatTime } from './util.js';
  import { Marked } from 'marked';
  import { markedHighlight } from 'marked-highlight';
  import hljs from 'highlight.js/lib/core';
  import jsyaml from 'js-yaml';

  // Register common languages.
  import go from 'highlight.js/lib/languages/go';
  import javascript from 'highlight.js/lib/languages/javascript';
  import typescript from 'highlight.js/lib/languages/typescript';
  import python from 'highlight.js/lib/languages/python';
  import bashLang from 'highlight.js/lib/languages/bash';
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
  import objectivec from 'highlight.js/lib/languages/objectivec';

  hljs.registerLanguage('go', go);
  hljs.registerLanguage('javascript', javascript);
  hljs.registerLanguage('js', javascript);
  hljs.registerLanguage('typescript', typescript);
  hljs.registerLanguage('ts', typescript);
  hljs.registerLanguage('python', python);
  hljs.registerLanguage('py', python);
  hljs.registerLanguage('bash', bashLang);
  hljs.registerLanguage('shell', shellLang);
  hljs.registerLanguage('sh', bashLang);
  hljs.registerLanguage('zsh', bashLang);
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
  hljs.registerLanguage('objectivec', objectivec);

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

  // Detect home directory from common OS paths.
  let homeDir = $state('');
  fetch('/api/home').then(r => r.ok ? r.text() : '').then(h => homeDir = h.trim());

  const extToLang = {
    '.go': 'go', '.js': 'javascript', '.mjs': 'javascript', '.cjs': 'javascript',
    '.ts': 'typescript', '.mts': 'typescript', '.tsx': 'typescript', '.jsx': 'javascript',
    '.py': 'python', '.rs': 'rust', '.c': 'c', '.h': 'c', '.cpp': 'cpp', '.cc': 'cpp',
    '.cxx': 'cpp', '.hpp': 'cpp', '.hh': 'cpp', '.hxx': 'cpp', '.m': 'objectivec', '.mm': 'objectivec',
    '.json': 'json', '.yaml': 'yaml', '.yml': 'yaml', '.sql': 'sql',
    '.css': 'css', '.html': 'html', '.xml': 'xml', '.svg': 'xml',
    '.md': 'markdown', '.markdown': 'markdown',
    '.diff': 'diff', '.patch': 'diff',
    '.sh': 'bash', '.bash': 'bash', '.zsh': 'bash',
    '.swift': 'swift', '.kt': 'kotlin', '.kts': 'kotlin',
    '.rb': 'ruby', '.java': 'java', '.scala': 'scala',
    '.toml': 'yaml', '.ini': 'yaml', '.cfg': 'yaml',
    '.svelte': 'html', '.vue': 'html',
    '.glsl': 'c', '.vert': 'c', '.frag': 'c', '.shader': 'c', '.metal': 'c',
  };

  function langForPath(path) {
    if (!path) return null;
    const dot = path.lastIndexOf('.');
    if (dot === -1) return null;
    const lang = extToLang[path.substring(dot).toLowerCase()];
    return lang && hljs.getLanguage(lang) ? lang : null;
  }

  let showSystem = $state(false);
  let worker = $derived(store.selectedWorker);
  let events = $derived(store.selectedEvents);
  let allParsed = $derived(events.map(ev => parseEvent(ev)));
  // Map tool_use id → { name, input } for linking results back to their tool.
  let toolUseMap = $derived((() => {
    const m = new Map();
    for (const ev of allParsed) {
      if (ev.kind === 'assistant') {
        for (const b of ev.blocks) {
          if (b.kind === 'tool_use' && b.id) m.set(b.id, b);
        }
      }
    }
    return m;
  })());
  let parsed = $derived(showSystem ? allParsed : allParsed.filter(ev => ev.kind !== 'system'));
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
              content = jsyaml.dump(content, { lineWidth: -1, noRefs: true });
            }
            content = content.replace(/\s*<system-reminder>[\s\S]*?<\/system-reminder>\s*/g, '');
            results.push({
              toolUseId: block.tool_use_id,
              content: content,
              oneliner: formatFileOneliner(content),
              contentHtml: formatFileContentHtml(content),
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

  // Tools whose summary line already captures all meaningful input.
  const simpleTools = new Set(['Glob']);

  function isSimpleTool(name, input) {
    if (!simpleTools.has(name)) return false;
    if (!input || typeof input !== 'object') return true;
    const keys = Object.keys(input);
    if (name === 'Glob') return keys.every(k => k === 'pattern' || k === 'path');
    return false;
  }

  let tooltip = $state({ show: false, text: '', x: 0, y: 0 });
  let tipTimer = null;

  function showTip(e) {
    const el = e.currentTarget;
    if (el.scrollWidth <= el.clientWidth) return;
    const rect = el.getBoundingClientRect();
    tipTimer = setTimeout(() => {
      tooltip = { show: true, text: el.dataset.tip, x: rect.left, y: rect.bottom + 4 };
    }, 100);
  }

  function hideTip() {
    clearTimeout(tipTimer);
    tooltip = { ...tooltip, show: false };
  }

  const catNRe = /^\s*\d+→/;

  function stripCatN(text) {
    if (!text || typeof text !== 'string') return text;
    const lines = text.split('\n');
    if (!lines.some(l => catNRe.test(l))) return null;
    return lines.map(l => l.replace(catNRe, ''));
  }

  function formatFileOneliner(text) {
    const lines = stripCatN(text);
    if (!lines) return text;
    return lines.map(l => l.trim() === '' ? '\u2022' : l).join(' ');
  }

  function formatFileContentHtml(text) {
    const lines = stripCatN(text);
    if (!lines) return null;
    return lines.map(line => {
      const escaped = line.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
      return '  ' + escaped;
    }).join('\n');
  }

  function openFile(path) {
    fetch('/api/open', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path }),
    });
  }

  function highlightFileContent(contentHtml, lang) {
    // contentHtml has lines like "  escaped code" or empty-line-dot spans.
    // Extract plain text, highlight as a block, then re-insert indent.
    const lines = contentHtml.split('\n');
    const codeLines = [];
    const blankSet = new Set();
    for (let i = 0; i < lines.length; i++) {
      if (lines[i].includes('empty-line-dot')) {
        blankSet.add(i);
        codeLines.push('');
      } else {
        // Strip leading 2-space indent and unescape HTML entities for highlighting.
        const raw = lines[i].replace(/^ {2}/, '')
          .replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>');
        codeLines.push(raw);
      }
    }
    const highlighted = hljs.highlight(codeLines.join('\n'), { language: lang }).value;
    const hLines = highlighted.split('\n');
    return hLines.map((line, i) => {
      if (blankSet.has(i)) return '<span class="empty-line-dot">\u2022</span>';
      return '  ' + line;
    }).join('\n');
  }

  function highlightYaml(obj) {
    const text = jsyaml.dump(obj, { lineWidth: -1, noRefs: true });
    return hljs.highlight(text, { language: 'yaml' }).value;
  }
</script>

<div class="log-panel">
  <div class="panel-header">
    {headerText}
    <button
      class="system-toggle"
      class:active={showSystem}
      type="button"
      onclick={() => showSystem = !showSystem}
    >SYS</button>
  </div>

  {#if !store.selectedID}
    <div class="empty-state">Select a worker to view its output</div>
  {:else if allParsed.length > 0 && parsed.length === 0}
    <div class="empty-state">No visible events (enable SYS to show system events)</div>
  {:else if allParsed.length === 0}
    <div class="empty-state">No events yet</div>
  {:else}
    <div
      bind:this={scrollRef}
      class="log-scroll"
      onscroll={onScroll}
      onclick={(e) => {
        const link = e.target.closest('.file-link');
        if (link) { e.preventDefault(); e.stopPropagation(); openFile(link.dataset.path); }
      }}
    >
      {#each parsed as ev, i (i)}
        {#if ev.kind === 'assistant'}
          {#each ev.blocks as block, bi}
            {#if block.kind === 'text'}
              <div class="ev-text">
                {#if bi === 0}
                  <span class="ev-time text-time">{formatTime(ev.time)}</span>
                {/if}
                <div class="md-body">
                  {@html block.html}
                </div>
              </div>
            {:else if block.kind === 'tool_use' && block.name === 'Bash'}
              <div class="ev-tool-bash">
                <button
                  class="ev-tool"
                  type="button"
                  onclick={() => toggleTool(`${i}-${bi}`)}
                >
                  <span class="tool-chevron">{expandedTools.has(`${i}-${bi}`) ? '\u25bc' : '\u25b8'}</span>
                  <span class="tool-name">Bash</span> <span class="bash-desc">{block.input?.description || 'command'}</span>
                  <span class="ev-time">{formatTime(ev.time)}</span>
                </button>
                {#if expandedTools.has(`${i}-${bi}`)}
                  <pre class="bash-command"><code>{@html hljs.highlight(block.input?.command || '', { language: 'bash' }).value}</code></pre>
                {/if}
              </div>
            {:else if block.kind === 'tool_use' && block.name === 'Agent'}
              <div class="ev-tool-agent">
                <button
                  class="ev-tool"
                  type="button"
                  onclick={() => toggleTool(`${i}-${bi}`)}
                >
                  <span class="tool-chevron">{expandedTools.has(`${i}-${bi}`) ? '\u25bc' : '\u25b8'}</span>
                  <span class="tool-name">Agent</span><span class="agent-type">[{block.input?.subagent_type || 'general'}]</span>
                  <span class="bash-desc">{block.input?.description || 'task'}</span>
                  <span class="ev-time">{formatTime(ev.time)}</span>
                </button>
                {#if expandedTools.has(`${i}-${bi}`)}
                  <pre class="bash-command"><code>{@html hljs.highlight(block.input?.prompt || '', { language: 'markdown' }).value}</code></pre>
                {/if}
              </div>
            {:else if block.kind === 'tool_use' && block.name === 'Read'}
              {@const locText = displayPath + (block.input?.offset != null || block.input?.limit != null ? '@' + (block.input?.offset ?? '') + (block.input?.limit != null ? ':' + block.input.limit : '') : '')}
              {@const filePath = block.input?.file_path || ''}
              {@const displayPath = filePath.startsWith(homeDir) ? '~' + filePath.slice(homeDir.length) : filePath}
              {@const locHtml = '<span class="file-link" data-path="' + filePath.replace(/"/g, '&quot;') + '">' + displayPath + '</span>' + (block.input?.offset != null || block.input?.limit != null ? '<span class="read-punct">@</span>' + (block.input?.offset ?? '') + (block.input?.limit != null ? '<span class="read-punct">:</span>' + block.input.limit : '') : '')}
              {@const extra = Object.fromEntries(Object.entries(block.input || {}).filter(([k]) => !['file_path','offset','limit'].includes(k)))}
              {#if Object.keys(extra).length === 0}
                <div class="ev-tool">
                  <span class="tool-name">Read</span>
                  <span class="tool-summary ellip-tip" data-tip={locText} onmouseenter={showTip} onmouseleave={hideTip}>{@html locHtml}</span>
                  <span class="ev-time">{formatTime(ev.time)}</span>
                </div>
              {:else}
                <button
                  class="ev-tool"
                  type="button"
                  onclick={() => toggleTool(`${i}-${bi}`)}
                >
                  <span class="tool-chevron">{expandedTools.has(`${i}-${bi}`) ? '\u25bc' : '\u25b8'}</span>
                  <span class="tool-name">Read</span>
                  {#if !expandedTools.has(`${i}-${bi}`)}
                    <span class="tool-summary ellip-tip" data-tip={locText} onmouseenter={showTip} onmouseleave={hideTip}>{@html locHtml}</span>
                  {/if}
                  <span class="ev-time">{formatTime(ev.time)}</span>
                </button>
                {#if expandedTools.has(`${i}-${bi}`)}
                  <pre class="tool-input"><code>{@html highlightYaml(extra)}</code></pre>
                {/if}
              {/if}
            {:else if block.kind === 'tool_use' && isSimpleTool(block.name, block.input)}
              <div class="ev-tool">
                <span class="tool-name">{block.name}</span>
                <span class="tool-summary ellip-tip" data-tip={formatInput(block.input)} onmouseenter={showTip} onmouseleave={hideTip}>{formatInput(block.input)}</span>
                <span class="ev-time">{formatTime(ev.time)}</span>
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
                  <span class="tool-summary ellip-tip" data-tip={formatInput(block.input)} onmouseenter={showTip} onmouseleave={hideTip}>{formatInput(block.input)}</span>
                {/if}
                <span class="ev-time">{formatTime(ev.time)}</span>
              </button>
              {#if expandedTools.has(`${i}-${bi}`)}
                <pre class="tool-input"><code>{@html highlightYaml(block.input)}</code></pre>
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
              <span class="result-label">{res.isError ? 'error' : '└─'}</span>
              {#if !expandedTools.has(`res-${i}`)}
                <span class="result-summary ellip-tip" data-tip={res.oneliner} onmouseenter={showTip} onmouseleave={hideTip}>{@html (res.oneliner || res.content).replace(/\u2022/g, '<span class="empty-line-dot">\u2022</span>')}</span>
              {/if}
              <span class="ev-time">{formatTime(ev.time)}</span>
            </button>
            {#if expandedTools.has(`res-${i}`)}
              {@const srcTool = toolUseMap.get(res.toolUseId)}
              {@const lang = srcTool?.name === 'Read' ? langForPath(srcTool.input?.file_path) : null}
              {#if lang && res.contentHtml}
                <pre class="tool-output" class:is-error={res.isError}><code>{@html highlightFileContent(res.contentHtml, lang)}</code></pre>
              {:else if res.contentHtml}
                <pre class="tool-output" class:is-error={res.isError}>{@html res.contentHtml}</pre>
              {:else}
                <pre class="tool-output" class:is-error={res.isError}>{res.content}</pre>
              {/if}
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

{#if tooltip.show}
  <div class="global-tooltip" style="left:{tooltip.x}px;top:{tooltip.y}px">
    {tooltip.text}
  </div>
{/if}

<style>
  .log-panel {
    flex: 1;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .panel-header {
    display: flex;
    align-items: center;
    gap: 8px;
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
  .system-toggle {
    margin-left: auto;
    padding: 1px 6px;
    font-size: 10px;
    font-family: var(--mono);
    background: none;
    border: 1px solid var(--border);
    border-radius: 3px;
    color: var(--fg-dim);
    cursor: pointer;
  }
  .system-toggle:hover { color: var(--fg-dim); border-color: var(--fg-dim); }
  .system-toggle.active {
    color: var(--accent);
    border-color: var(--accent);
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
    position: relative;
    padding: 6px 16px;
    border-left: 2px solid var(--accent);
    margin: 4px 12px;
    border-radius: 2px;
  }
  .text-time {
    position: absolute;
    top: 6px;
    right: 0;
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
    flex: 1;
    min-width: 0;
  }
  .agent-type {
    color: var(--accent);
    font-weight: 600;
    margin-left: 1px;
  }
  .bash-desc {
    color: var(--fg-dim);
    font-weight: 400;
    margin-left: 2px;
  }
  .bash-command {
    margin: 0 16px 4px 32px;
    padding: 6px 10px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 4px;
    font-size: 11px;
    line-height: 1.5;
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .bash-command code {
    font-family: var(--mono);
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
    flex: 1;
    min-width: 0;
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
  :global(.file-link) {
    cursor: pointer;
    text-decoration: underline;
    text-decoration-color: var(--border);
    text-underline-offset: 2px;
  }
  :global(.file-link:hover) {
    text-decoration-color: var(--accent);
    color: var(--accent);
  }
  :global(.read-punct) {
    color: var(--yellow);
  }
  :global(.empty-line-dot) {
    color: var(--yellow);
    opacity: 0.5;
    user-select: none;
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

  .global-tooltip {
    position: fixed;
    z-index: 9999;
    max-width: 600px;
    padding: 6px 10px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--fg);
    font-size: 11px;
    font-family: var(--mono);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.4;
    pointer-events: none;
    box-shadow: 0 4px 12px rgba(0,0,0,0.4);
  }
</style>
