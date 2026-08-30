/**
 * bd dashboard — app.js
 * Native ES modules, no build step required.
 */

// ── State ────────────────────────────────────────────────────────────────────
const state = {
  issues: new Map(),   // id → issue object
  stats: {},
  view: 'board',
  searchQuery: '',
  selectedId: null,
  sseReady: false,
};

// ── API helpers ───────────────────────────────────────────────────────────────
const api = {
  async get(path) {
    const r = await fetch('/api/v1' + path);
    if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
    return r.json();
  },
  async patch(path, body) {
    const r = await fetch('/api/v1' + path, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
    return r.json();
  },
  async post(path, body = {}) {
    const r = await fetch('/api/v1' + path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
    return r.json();
  },
};

// ── Rendering helpers ─────────────────────────────────────────────────────────
const typeIcons = {
  bug: '🐛', feature: '✦', task: '◻', epic: '◈', chore: '⚙', default: '◻',
};

function typeIcon(t) {
  return typeIcons[t] || typeIcons.default;
}

function priorityBadge(p) {
  const label = `P${p}`;
  return `<span class="priority-badge prio-${p}">${label}</span>`;
}

function statusIcon(s) {
  const icons = { open: '○', in_progress: '◐', closed: '✓', blocked: '❄', deferred: '◌' };
  return icons[s] || '?';
}

function reltime(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  const secs = Math.floor((Date.now() - d) / 1000);
  if (secs < 5) return 'just now';
  if (secs < 60) return `${secs}s ago`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  return d.toLocaleDateString();
}

// Copies text to the clipboard and flashes a checkmark on the triggering
// button so pasting an id into an agent prompt doesn't require guessing
// whether the click registered.
function copyToClipboard(text, btnEl) {
  const fallback = () => {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); } catch (e) { console.error('copy failed:', e); }
    document.body.removeChild(ta);
  };

  const onCopied = () => {
    if (!btnEl) return;
    const original = btnEl.textContent;
    btnEl.textContent = '✓';
    btnEl.classList.add('copied');
    setTimeout(() => {
      btnEl.textContent = original;
      btnEl.classList.remove('copied');
    }, 1200);
  };

  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).then(onCopied).catch(() => { fallback(); onCopied(); });
  } else {
    fallback();
    onCopied();
  }
}

function escapeHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// ── Issue card ────────────────────────────────────────────────────────────────
function renderCard(issue) {
  const card = document.createElement('div');
  card.className = `issue-card status-${issue.status}`;
  card.dataset.id = issue.id;
  card.innerHTML = `
    <div class="card-top">
      <span class="card-id">${escapeHtml(issue.id)}</span>
      <span class="card-type-icon" title="${escapeHtml(issue.issue_type || '')}">${typeIcon(issue.issue_type)}</span>
      ${priorityBadge(issue.priority ?? 2)}
    </div>
    <div class="card-title">${escapeHtml(issue.title)}</div>
    <div class="card-bottom">
      ${issue.assignee ? `<span class="assignee-chip">${escapeHtml(issue.assignee)}</span>` : ''}
      <span>${reltime(issue.updated_at)}</span>
    </div>
  `;
  card.addEventListener('click', () => openDetail(issue.id));
  return card;
}

// ── Kanban board ──────────────────────────────────────────────────────────────
const todayStart = new Date();
todayStart.setHours(0, 0, 0, 0);

function isToday(issue) {
  if (!issue.closed_at && !issue.updated_at) return false;
  const d = new Date(issue.closed_at || issue.updated_at);
  return d >= todayStart;
}

function columnIdFor(issue) {
  if (issue.status === 'closed' && !isToday(issue)) return null;
  const map = { open: 'col-open', in_progress: 'col-in_progress', blocked: 'col-blocked', closed: 'col-closed' };
  return map[issue.status] || null;
}

function renderBoard(issues) {
  const cols = { 'col-open': [], 'col-in_progress': [], 'col-blocked': [], 'col-closed': [] };
  const query = state.searchQuery.toLowerCase();

  for (const issue of issues) {
    if (query && !issue.title.toLowerCase().includes(query) && !issue.id.toLowerCase().includes(query)) continue;
    const col = columnIdFor(issue);
    if (col) cols[col].push(issue);
  }

  for (const [colId, items] of Object.entries(cols)) {
    const el = document.getElementById(colId);
    if (!el) continue;
    el.innerHTML = '';
    items.sort((a, b) => (a.priority ?? 2) - (b.priority ?? 2));
    for (const issue of items) el.appendChild(renderCard(issue));
  }
}

// ── Stats ─────────────────────────────────────────────────────────────────────
// Field names match types.Statistics' JSON tags (internal/types/types.go),
// not a nested count_by_status map. blocked_issues is a *int, nil when the
// backend skipped the blocked-set traversal (bd stats --no-blocked path).
function renderStats(stats) {
  document.getElementById('stat-open').textContent    = stats.open_issues ?? 0;
  document.getElementById('stat-active').textContent  = stats.in_progress_issues ?? 0;
  document.getElementById('stat-closed').textContent  = stats.closed_issues ?? 0;
  document.getElementById('stat-blocked').textContent = stats.blocked_issues ?? 0;
}

// ── Activity feed (sidebar) ───────────────────────────────────────────────────
const activityFeed = [];
const MAX_FEED = 20;

function pushActivity(event, data) {
  activityFeed.unshift({ event, data, ts: Date.now() });
  if (activityFeed.length > MAX_FEED) activityFeed.length = MAX_FEED;
  renderActivityFeed();
  prependFeedRow(event, data);
}

function renderActivityFeed() {
  const ul = document.getElementById('activity-feed');
  ul.innerHTML = '';
  for (const item of activityFeed) {
    const li = document.createElement('li');
    li.className = 'feed-item';
    const verb = item.event.replace('issue.', '').replace('dep.', 'dep ');
    const id = item.data?.id || item.data?.from || '';
    li.innerHTML = `
      <span class="feed-id">${escapeHtml(id)}</span>
      <span class="feed-verb">${escapeHtml(verb)}</span>
      <span class="feed-time">${reltime(item.ts)}</span>
    `;
    if (id) li.addEventListener('click', () => openDetail(id));
    ul.appendChild(li);
  }
}

// ── Ready table ───────────────────────────────────────────────────────────────
async function renderReady() {
  let issues;
  try {
    issues = await api.get('/issues/ready');
  } catch (e) {
    console.error('ready:', e);
    return;
  }
  const tbody = document.getElementById('ready-rows');
  tbody.innerHTML = '';
  for (const iss of (issues || [])) {
    const tr = document.createElement('tr');
    tr.dataset.id = iss.id;
    tr.innerHTML = `
      <td class="id-cell">${escapeHtml(iss.id)}</td>
      <td>${escapeHtml(iss.title)}</td>
      <td>${priorityBadge(iss.priority ?? 2)}</td>
      <td><span class="card-type-icon">${typeIcon(iss.issue_type)}</span></td>
      <td>${iss.assignee ? escapeHtml(iss.assignee) : '<span class="muted">–</span>'}</td>
      <td><button class="btn btn-primary claim-btn" data-id="${escapeHtml(iss.id)}">Claim</button></td>
    `;
    tr.querySelector('.claim-btn').addEventListener('click', async (e) => {
      e.stopPropagation();
      try {
        const updated = await api.post(`/issues/${iss.id}/claim`);
        state.issues.set(updated.id, updated);
        renderBoard([...state.issues.values()]);
        renderReady();
      } catch (err) { console.error(err); }
    });
    tr.addEventListener('click', () => openDetail(iss.id));
    tbody.appendChild(tr);
  }
}

// ── Activity feed (full page) ─────────────────────────────────────────────────
const feedVerbLabels = {
  created: 'created', updated: 'updated', claimed: 'claimed',
  status_changed: 'status changed', commented: 'commented', closed: 'closed',
  reopened: 'reopened', dependency_added: 'dep added', dependency_removed: 'dep removed',
  label_added: 'label added', label_removed: 'label removed', compacted: 'compacted',
  lease_reclaimed: 'lease reclaimed',
};

function feedStatusBadge(status) {
  if (!status) return '<span class="muted">–</span>';
  return `<span class="badge status-${escapeHtml(status)}">${statusIcon(status)} ${escapeHtml(status)}</span>`;
}

function feedRow(ev) {
  const tr = document.createElement('tr');
  if (ev.issue_id) tr.dataset.id = ev.issue_id;
  tr.innerHTML = `
    <td class="mono muted">${escapeHtml(new Date(ev.created_at).toLocaleTimeString())}</td>
    <td class="id-cell">${escapeHtml(ev.issue_id)}</td>
    <td>${escapeHtml(ev.actor || '–')}</td>
    <td>${escapeHtml(feedVerbLabels[ev.event_type] || ev.event_type)}</td>
    <td>${feedStatusBadge(ev.status)}</td>
  `;
  if (ev.issue_id) tr.addEventListener('click', () => openDetail(ev.issue_id));
  return tr;
}

async function renderFeed() {
  let events;
  try {
    events = await api.get('/events?limit=200');
  } catch (e) {
    console.error('feed:', e);
    return;
  }
  const tbody = document.getElementById('feed-rows');
  tbody.innerHTML = '';
  for (const ev of (events || [])) {
    tbody.appendChild(feedRow(ev));
  }
  if (!events || events.length === 0) {
    const tr = document.createElement('tr');
    tr.innerHTML = `<td colspan="5" style="color:var(--text-muted);text-align:center;padding:24px">No recent activity</td>`;
    tbody.appendChild(tr);
  }
}

// SSE event names ("issue.created") back to the backend's event_type
// vocabulary ("created") so live rows use the same feedVerbLabels lookup as
// the initial /events fetch.
const sseToEventType = {
  'issue.created': 'created', 'issue.updated': 'updated', 'issue.closed': 'closed',
  'dep.added': 'dependency_added', 'dep.removed': 'dependency_removed',
};

// Live-prepend a synthetic feed row from an SSE event without a full refetch,
// so the feed view updates instantly while it's the active tab.
function prependFeedRow(sseType, data) {
  if (state.view !== 'feed' || !data) return;
  const tbody = document.getElementById('feed-rows');
  if (!tbody) return;
  tbody.querySelector('td[colspan]')?.closest('tr')?.remove();
  const ev = {
    created_at: new Date().toISOString(),
    issue_id: data.id || '',
    actor: data.actor || '',
    event_type: sseToEventType[sseType] || sseType,
    status: data.status || '',
  };
  tbody.insertBefore(feedRow(ev), tbody.firstChild);
}

// ── Issue detail panel ────────────────────────────────────────────────────────
async function openDetail(id) {
  state.selectedId = id;
  const panel = document.getElementById('detail-panel');
  const content = document.getElementById('detail-content');

  // Show loading state immediately.
  panel.classList.remove('hidden');
  panel.classList.add('open');
  content.innerHTML = `<p style="color:var(--text-muted)">Loading ${escapeHtml(id)}…</p>`;

  let issue;
  try {
    issue = await api.get(`/issues/${id}`);
  } catch (e) {
    content.innerHTML = `<p style="color:var(--blocked)">Error: ${escapeHtml(e.message)}</p>`;
    return;
  }

  const deps = issue.dependencies || [];
  const comments = issue.comments || [];

  const depsHtml = deps.length ? `
    <ul class="dep-list">
      ${deps.map(d => `
        <li class="dep-item" data-id="${escapeHtml(d.depends_on_id || d.issue_id)}">
          <span class="dep-type">${escapeHtml(d.type || '')}</span>
          <span class="dep-id">${escapeHtml(d.depends_on_id || d.issue_id)}</span>
        </li>`).join('')}
    </ul>` : '<p style="color:var(--text-muted);font-size:12px">None</p>';

  const commentsHtml = comments.length ? comments.map(c => `
    <div style="font-size:12px;margin-bottom:8px">
      <span style="color:var(--text-muted);font-family:var(--font-mono)">${escapeHtml(c.author || '')} · ${reltime(c.created_at)}</span>
      <p style="margin-top:4px;white-space:pre-wrap">${escapeHtml(c.text || '')}</p>
    </div>`).join('') : '<p style="color:var(--text-muted);font-size:12px">None</p>';

  content.innerHTML = `
    <div class="detail-meta">
      <span class="badge mono id-badge">
        ${escapeHtml(issue.id)}
        <button class="copy-id-btn" id="btn-copy-id" type="button" title="Copy issue ID">⧉</button>
      </span>
      <span class="badge status-${issue.status}">${statusIcon(issue.status)} ${escapeHtml(issue.status)}</span>
      <span class="badge">${priorityBadge(issue.priority ?? 2)}</span>
      ${issue.issue_type ? `<span class="badge">${escapeHtml(issue.issue_type)}</span>` : ''}
      ${issue.assignee ? `<span class="badge mono">assignee: ${escapeHtml(issue.assignee)}</span>` : ''}
    </div>
    <h1>${escapeHtml(issue.title)}</h1>

    <div class="detail-owner-line" style="font-size:12px;color:var(--text-muted);margin:-8px 0 16px">
      Owner: ${issue.owner ? `<span style="color:var(--text)">${escapeHtml(issue.owner)}</span>` : 'unknown'}
      &nbsp;·&nbsp;
      Last updated: <span style="color:var(--text)" title="${escapeHtml(issue.updated_at || '')}">${issue.updated_at ? reltime(issue.updated_at) : 'unknown'}</span>
    </div>

    ${issue.description ? `
    <div class="detail-section">
      <h3>Description</h3>
      <p>${escapeHtml(issue.description)}</p>
    </div>` : ''}

    ${issue.acceptance_criteria ? `
    <div class="detail-section">
      <h3>Acceptance Criteria</h3>
      <p>${escapeHtml(issue.acceptance_criteria)}</p>
    </div>` : ''}

    <div class="detail-section">
      <h3>Dependencies</h3>
      ${depsHtml}
    </div>

    <div class="detail-section">
      <h3>Activity</h3>
      ${commentsHtml}
      <form id="comment-form" class="comment-form">
        <textarea id="comment-text" placeholder="Add a comment to steer this issue…" rows="3"></textarea>
        <button type="submit" class="btn btn-primary">Comment</button>
      </form>
    </div>

    <div class="detail-actions">
      ${issue.status !== 'in_progress' && issue.status !== 'closed' ? `<button class="btn btn-primary" id="btn-claim">Claim</button>` : ''}
      ${issue.status !== 'closed' ? `<button class="btn btn-danger" id="btn-close">Close</button>` : ''}
      <button class="btn" id="btn-graph">View in Graph</button>
    </div>
  `;

  // Wire action buttons.
  content.querySelector('#btn-copy-id')?.addEventListener('click', (e) => {
    copyToClipboard(issue.id, e.currentTarget);
  });

  content.querySelector('#btn-claim')?.addEventListener('click', async () => {
    try {
      const updated = await api.post(`/issues/${id}/claim`);
      state.issues.set(updated.id, updated);
      renderBoard([...state.issues.values()]);
      openDetail(id); // refresh panel
    } catch (e) { console.error(e); }
  });

  content.querySelector('#btn-close')?.addEventListener('click', async () => {
    const reason = prompt('Close reason:');
    if (reason === null) return;
    try {
      const updated = await api.post(`/issues/${id}/close`, { reason: reason || 'closed via dashboard' });
      state.issues.set(updated.id, updated);
      renderBoard([...state.issues.values()]);
      openDetail(id);
    } catch (e) { console.error(e); }
  });

  content.querySelector('#comment-form')?.addEventListener('submit', async (e) => {
    e.preventDefault();
    const textEl = content.querySelector('#comment-text');
    const text = textEl.value.trim();
    if (!text) return;
    try {
      const updated = await api.post(`/issues/${id}/comments`, { text });
      state.issues.set(updated.id, updated);
      openDetail(id); // refresh panel with the new comment
    } catch (e) { console.error(e); }
  });

  content.querySelector('#btn-graph')?.addEventListener('click', () => {
    switchView('graph');
    // Graph will highlight selected node if loaded.
  });

  // Wire dep links.
  content.querySelectorAll('.dep-item[data-id]').forEach(el => {
    el.addEventListener('click', () => openDetail(el.dataset.id));
  });
}

function closeDetail() {
  const panel = document.getElementById('detail-panel');
  panel.classList.remove('open');
  panel.classList.add('hidden');
  state.selectedId = null;
}

// ── View switching ────────────────────────────────────────────────────────────
function switchView(name) {
  state.view = name;
  document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
  document.querySelectorAll('.tab').forEach(t => {
    t.classList.toggle('active', t.dataset.view === name);
  });
  const viewEl = document.getElementById(`view-${name}`);
  if (viewEl) viewEl.classList.add('active');

  if (name === 'ready') renderReady();
  if (name === 'graph') renderGraphView();
  if (name === 'feed') renderFeed();

  navIndex = -1;
  clearNavHighlight();
}

// ── D3 force-directed graph view ────────────────────────────────────────────
// Node size encodes priority (P0 largest); node color encodes status; edge
// style encodes dep_type (solid = blocks, dashed = related, dotted =
// discovered-from). Checkbox filters in #graph-filters only ever hide those
// three known dep types — anything else always renders, so the UI never
// silently drops an edge type it has no control for.
const graphDepDasharray = {
  related: '5,4',
  'discovered-from': '2,4',
};
const graphFilterableDepTypes = new Set(['blocks', 'related', 'discovered-from']);
const graphStatusColors = {
  open: '#39BAE6', in_progress: '#FFB454', closed: '#7FD962',
  blocked: '#F26D78', deferred: '#A37ACC',
};

function graphNodeRadius(priority) {
  return 7 + Math.max(0, 4 - (priority ?? 2)) * 2.5; // P0 largest
}

let graphState = null; // lazily-created persistent D3 simulation state

function initGraph() {
  const svgEl = document.getElementById('graph-svg');
  const svg = d3.select(svgEl);
  svg.selectAll('*').remove();

  const root = svg.append('g').attr('class', 'graph-root');
  const linkLayer = root.append('g').attr('class', 'graph-links');
  const nodeLayer = root.append('g').attr('class', 'graph-nodes');
  const emptyText = svg.append('text').attr('class', 'graph-empty').style('display', 'none');

  svg.call(d3.zoom().scaleExtent([0.2, 4]).on('zoom', (event) => {
    root.attr('transform', event.transform);
  }));

  const tooltip = d3.select('#graph-container').append('div')
    .attr('class', 'graph-tooltip')
    .style('opacity', 0);

  const simulation = d3.forceSimulation()
    .force('link', d3.forceLink().id(d => d.id).distance(70).strength(0.4))
    .force('charge', d3.forceManyBody().strength(-220))
    .force('collide', d3.forceCollide().radius(d => graphNodeRadius(d.priority) + 6))
    .force('center', d3.forceCenter());

  graphState = {
    svg, root, linkLayer, nodeLayer, emptyText, simulation, tooltip,
    linkSel: linkLayer.selectAll('line'),
    nodeSel: nodeLayer.selectAll('g.graph-node'),
    activeDepFilters: new Set(['blocks', 'related', 'discovered-from']),
  };

  simulation.on('tick', () => {
    graphState.linkSel
      .attr('x1', d => d.source.x).attr('y1', d => d.source.y)
      .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
    graphState.nodeSel.attr('transform', d => `translate(${d.x},${d.y})`);
  });

  if (typeof ResizeObserver !== 'undefined') {
    new ResizeObserver(() => resizeGraphCenter()).observe(document.getElementById('graph-container'));
  }
  resizeGraphCenter();

  document.querySelectorAll('.dep-filter').forEach(cb => {
    cb.addEventListener('change', () => {
      const active = new Set();
      document.querySelectorAll('.dep-filter:checked').forEach(c => active.add(c.value));
      graphState.activeDepFilters = active;
      applyGraphFilter();
    });
  });

  return graphState;
}

function resizeGraphCenter() {
  if (!graphState) return;
  const container = document.getElementById('graph-container');
  const w = container.clientWidth || 800;
  const h = container.clientHeight || 600;
  graphState.svg.attr('viewBox', [0, 0, w, h]);
  graphState.emptyText.attr('x', w / 2).attr('y', h / 2);
  graphState.simulation.force('center', d3.forceCenter(w / 2, h / 2));
  graphState.simulation.alpha(0.3).restart();
}

function applyGraphFilter() {
  if (!graphState) return;
  graphState.linkSel.style('display', d =>
    (!graphFilterableDepTypes.has(d.dep_type) || graphState.activeDepFilters.has(d.dep_type)) ? null : 'none'
  );
}

async function renderGraphView() {
  let graph;
  try {
    graph = await api.get('/graph');
  } catch (e) {
    console.error('graph:', e);
    return;
  }
  updateGraph(graph);
}

// updateGraph re-binds fresh data onto the persistent simulation (D3 join by
// id/edge key) so a live SSE-triggered refresh updates node color/size in
// place instead of resetting the whole layout.
function updateGraph(graph) {
  const gs = graphState || initGraph();

  const nodes = graph.nodes || [];
  const nodesById = new Map(nodes.map(n => [n.id, n]));

  const links = (graph.edges || [])
    .filter(e => nodesById.has(e.from) && nodesById.has(e.to))
    .map(e => ({ source: e.from, target: e.to, dep_type: e.dep_type }));

  gs.emptyText.style('display', nodes.length === 0 ? null : 'none').text('No issues to display');

  gs.linkSel = gs.linkLayer.selectAll('line')
    .data(links, d => `${d.source}-${d.target}-${d.dep_type}`)
    .join(
      enter => enter.append('line')
        .attr('class', 'graph-edge')
        .attr('stroke', '#1E2938')
        .attr('stroke-width', 1.5)
        .attr('stroke-dasharray', d => graphDepDasharray[d.dep_type] || null),
      update => update.attr('stroke-dasharray', d => graphDepDasharray[d.dep_type] || null),
      exit => exit.remove(),
    );
  applyGraphFilter();

  const drag = d3.drag()
    .on('start', (event, d) => {
      if (!event.active) gs.simulation.alphaTarget(0.3).restart();
      d.fx = d.x; d.fy = d.y;
    })
    .on('drag', (event, d) => { d.fx = event.x; d.fy = event.y; })
    .on('end', (event, d) => {
      if (!event.active) gs.simulation.alphaTarget(0);
      d.fx = null; d.fy = null;
    });

  gs.nodeSel = gs.nodeLayer.selectAll('g.graph-node')
    .data(nodes, d => d.id)
    .join(
      enter => {
        const sel = enter.append('g').attr('class', 'graph-node').style('cursor', 'pointer');
        sel.append('circle')
          .attr('r', d => graphNodeRadius(d.priority))
          .attr('fill', d => graphStatusColors[d.status] || '#6C7680')
          .attr('stroke', '#0D1017')
          .attr('stroke-width', 2);
        sel.append('text')
          .attr('x', d => graphNodeRadius(d.priority) + 4)
          .attr('y', 4)
          .attr('fill', '#BFBDB6')
          .attr('font-size', 11)
          .attr('font-family', 'var(--font-mono)')
          .text(d => d.id);
        sel.call(drag);
        sel.on('click', (event, d) => openDetail(d.id));
        sel.on('mouseenter', (event, d) => {
          d3.select(event.currentTarget).select('circle').attr('stroke', '#E6E1CF');
          gs.tooltip.style('opacity', 1).html(
            `<strong>${escapeHtml(d.id)}</strong> ${escapeHtml(d.title || '')}<br/>` +
            `${statusIcon(d.status)} ${escapeHtml(d.status || '')} · P${d.priority ?? 2}` +
            (d.assignee ? ` · ${escapeHtml(d.assignee)}` : '')
          );
        });
        sel.on('mousemove', (event) => {
          const [x, y] = d3.pointer(event, document.getElementById('graph-container'));
          gs.tooltip.style('left', `${x + 14}px`).style('top', `${y + 14}px`);
        });
        sel.on('mouseleave', (event) => {
          d3.select(event.currentTarget).select('circle').attr('stroke', '#0D1017');
          gs.tooltip.style('opacity', 0);
        });
        return sel;
      },
      update => {
        update.select('circle')
          .attr('r', d => graphNodeRadius(d.priority))
          .attr('fill', d => graphStatusColors[d.status] || '#6C7680');
        return update;
      },
      exit => exit.remove(),
    );

  gs.simulation.nodes(nodes);
  gs.simulation.force('link').links(links);
  gs.simulation.alpha(0.6).restart();
}

// ── SSE connection ────────────────────────────────────────────────────────────
function connectSSE() {
  const indicator = document.getElementById('live-indicator');
  const es = new EventSource('/api/v1/stream');

  es.addEventListener('open', () => {
    indicator.className = 'live-dot connected';
    indicator.title = 'Connected';
  });

  es.addEventListener('error', () => {
    indicator.className = 'live-dot error';
    indicator.title = 'Reconnecting…';
  });

  const handleIssueEvent = (data) => {
    if (!data) return;
    state.issues.set(data.id, data);
    if (state.view === 'board') renderBoard([...state.issues.values()]);
    if (state.view === 'graph') renderGraphView();
    api.get('/stats').then(renderStats).catch((e) => console.error('stats:', e));
  };

  es.addEventListener('issue.created', (e) => {
    const data = JSON.parse(e.data);
    handleIssueEvent(data);
    pushActivity('issue.created', data);
  });

  es.addEventListener('issue.updated', (e) => {
    const data = JSON.parse(e.data);
    handleIssueEvent(data);
    pushActivity('issue.updated', data);
  });

  es.addEventListener('issue.closed', (e) => {
    const data = JSON.parse(e.data);
    handleIssueEvent(data);
    pushActivity('issue.closed', data);
  });

  es.addEventListener('dep.added', (e) => {
    const data = JSON.parse(e.data);
    pushActivity('dep.added', data);
    if (state.view === 'graph') renderGraphView();
  });

  es.addEventListener('dep.removed', (e) => {
    const data = JSON.parse(e.data);
    pushActivity('dep.removed', data);
    if (state.view === 'graph') renderGraphView();
  });

  // Heartbeat keeps the connection alive through proxies.
  es.addEventListener('heartbeat', () => {
    // Nothing needed — just receiving it keeps the connection alive.
  });
}

// ── Keyboard shortcuts (j/k move, Enter open, g graph view, / search) ─────────
let navIndex = -1;

function navItemsForView() {
  if (state.view === 'board') return [...document.querySelectorAll('.issue-card')];
  if (state.view === 'ready') return [...document.querySelectorAll('#ready-rows tr[data-id]')];
  if (state.view === 'feed') return [...document.querySelectorAll('#feed-rows tr[data-id]')];
  return [];
}

function clearNavHighlight() {
  document.querySelectorAll('.kbd-selected').forEach(el => el.classList.remove('kbd-selected'));
}

function navMove(delta) {
  const items = navItemsForView();
  if (items.length === 0) return;
  navIndex = Math.min(Math.max(navIndex + delta, 0), items.length - 1);
  clearNavHighlight();
  const el = items[navIndex];
  el.classList.add('kbd-selected');
  el.scrollIntoView({ block: 'nearest' });
}

function navOpenSelected() {
  const el = navItemsForView()[navIndex];
  if (el?.dataset.id) openDetail(el.dataset.id);
}

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') { closeDetail(); return; }

  const typing = ['INPUT', 'TEXTAREA'].includes(document.activeElement?.tagName);
  if (e.key === '/' && !typing) {
    e.preventDefault();
    document.getElementById('search').focus();
    return;
  }
  if (typing) return;

  if (e.key === 'j') { e.preventDefault(); navMove(1); }
  else if (e.key === 'k') { e.preventDefault(); navMove(-1); }
  else if (e.key === 'Enter') { navOpenSelected(); }
  else if (e.key === 'g') { switchView('graph'); }
});

// ── Search ────────────────────────────────────────────────────────────────────
document.getElementById('search').addEventListener('input', (e) => {
  state.searchQuery = e.target.value;
  if (state.view === 'board') renderBoard([...state.issues.values()]);
});

// ── Wire up tabs ──────────────────────────────────────────────────────────────
document.querySelectorAll('.tab').forEach(btn => {
  btn.addEventListener('click', () => switchView(btn.dataset.view));
});

// ── Wire detail panel close ───────────────────────────────────────────────────
document.getElementById('detail-close').addEventListener('click', closeDetail);

// ── Boot ──────────────────────────────────────────────────────────────────────
async function boot() {
  try {
    const [issues, stats] = await Promise.all([
      api.get('/issues?limit=200'),
      api.get('/stats'),
    ]);

    for (const iss of (issues || [])) {
      state.issues.set(iss.id, iss);
    }
    renderBoard([...state.issues.values()]);
    renderStats(stats || {});
  } catch (e) {
    console.error('boot failed:', e);
  }

  connectSSE();
}

boot();
