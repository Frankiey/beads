# bd dashboard — Visual Architecture Plan

A live, web-based mission control for beads: track agent tasks in real time,
drill into issue details, visualize dependency graphs, and see the full picture
of your workflow at a glance.

---

## 1. Vision

`bd dashboard` opens a browser window showing everything `bd ready` knows —
and much more. Rather than scrolling through terminal output, you get an
interactive panel where you can:

- Watch agent activity as it happens (live updates, no refresh)
- Drill from board → issue → dependency chain → comments with one click
- See which issues are blocked, who owns what, and what's next
- Explore the dependency graph as an interactive force-directed diagram
- Filter, search, and sort without leaving the page

---

## 2. How It Works (One-Line Summary)

`bd dashboard` starts an embedded HTTP server inside the beads process, serves
a single-page app from `go:embed`-packaged assets, streams live updates over
Server-Sent Events, and opens your default browser. No Docker, no Node runtime,
no separate install.

---

## 3. Command

```
bd dashboard [flags]

Flags:
  --port int      Port to listen on (default: 7700, auto-increments if busy)
  --no-open       Do not open browser automatically
  --host string   Bind address (default: 127.0.0.1)
  --read-only     Disable mutations from the UI
```

The command is implemented as a standard Cobra subcommand in `cmd/bd/dashboard.go`.
It blocks until the user sends SIGINT/SIGTERM or closes the terminal, exactly
like `bd server`.

---

## 4. Architecture Overview

```
┌──────────────────────────────────────────────────────┐
│  bd dashboard process                                 │
│                                                       │
│  ┌─────────────┐    ┌──────────────────────────────┐ │
│  │  HTTP Server │    │  SSE Hub                     │ │
│  │  (net/http)  │───▶│  broadcasts to all browsers  │ │
│  └──────┬──────┘    └──────────────────────────────┘ │
│         │                          ▲                  │
│  ┌──────▼──────┐    ┌─────────────┴──────────────┐   │
│  │  REST API   │    │  Storage Watcher            │   │
│  │  /api/v1/   │    │  polls or hooks Dolt writes │   │
│  └──────┬──────┘    └────────────────────────────┘   │
│         │                                             │
│  ┌──────▼──────┐                                     │
│  │  Storage    │  (existing beads storage layer)      │
│  │  interface  │                                      │
│  └─────────────┘                                     │
│                                                       │
│  ┌─────────────────────────────────────────────────┐ │
│  │  Embedded frontend (go:embed static/)           │ │
│  │  index.html · app.js · style.css · vendor/      │ │
│  └─────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────┘
          ▲ HTTP + SSE
          │
   Browser (auto-opened)
```

### 4.1 Package Layout

```
beads/
├── cmd/bd/
│   └── dashboard.go           # Cobra command, wires everything
├── internal/dashboard/
│   ├── server.go              # HTTP server setup, router
│   ├── handlers.go            # REST API handlers
│   ├── sse.go                 # Server-Sent Events hub
│   ├── watcher.go             # Storage change detection → SSE
│   ├── openurl.go             # Cross-platform browser open
│   └── static/                # Embedded frontend (go:embed)
│       ├── index.html
│       ├── app.js
│       ├── style.css
│       └── vendor/
│           ├── d3.v7.min.js
│           └── preact.min.js  # optional: tiny component model
```

---

## 5. Backend

### 5.1 REST API

All endpoints under `/api/v1/`. Responses are JSON. Read paths proxy directly
to the beads storage interface; write paths respect `--read-only`.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/issues` | Paginated issue list; accepts `?status=&priority=&assignee=&q=` |
| GET | `/api/v1/issues/ready` | Same semantics as `bd ready` |
| GET | `/api/v1/issues/:id` | Single issue with deps, comments, events |
| GET | `/api/v1/issues/:id/deps` | Dependency graph rooted at this issue |
| GET | `/api/v1/graph` | Full dependency graph (all issues, all edges) |
| GET | `/api/v1/events` | Recent audit events, newest-first |
| GET | `/api/v1/stats` | Aggregate counts by status/priority/type |
| PATCH | `/api/v1/issues/:id` | Update status, assignee, priority |
| POST | `/api/v1/issues/:id/claim` | Claim an issue |
| POST | `/api/v1/issues/:id/close` | Close with reason |

### 5.2 Server-Sent Events

`GET /api/v1/stream` — returns `Content-Type: text/event-stream`.

Event types emitted by the watcher:

```
event: issue.created
data: {"id":"bd-123","title":"...","status":"open",...}

event: issue.updated
data: {"id":"bd-123","status":"in_progress",...}

event: issue.closed
data: {"id":"bd-123","close_reason":"Done"}

event: dep.added
data: {"from":"bd-123","to":"bd-456","dep_type":"blocks"}

event: heartbeat
data: {"ts":"2026-05-11T10:00:00Z"}
```

The watcher polls the Dolt commit log on a short interval (250 ms default,
configurable via `--poll-interval`). When a new Dolt commit appears, it diffs
changed rows and emits targeted events. This approach is storage-agnostic and
does not require Dolt-specific hooks.

### 5.3 Graph Endpoint

`/api/v1/graph` returns:

```json
{
  "nodes": [
    { "id": "bd-1", "title": "...", "status": "open", "priority": 1, "type": "feature", "assignee": "..." }
  ],
  "edges": [
    { "from": "bd-2", "to": "bd-1", "dep_type": "blocks" }
  ]
}
```

D3 consumes this directly for the force-directed layout.

---

## 6. Frontend

The frontend is a single HTML file plus two JS bundles (app logic + vendor).
No build step is required for development — the JS uses native ES modules with
import maps. For production embedding, a Makefile target runs `esbuild` to
bundle and minify into the `static/` directory before `go:embed` picks it up.

### 6.1 Visual Design Language

Mirrors the Ayu color theme used in the CLI (`internal/ui/styles.go`):

| Concept | Color | Use |
|---------|-------|-----|
| Open | `#39BAE6` (blue) | Status pills, graph nodes |
| In Progress | `#FFB454` (amber) | Active work, pulsing ring |
| Closed | `#7FD962` (green) | Done items |
| Blocked | `#F26D78` (red) | Blocked indicator |
| Deferred | `#A37ACC` (purple) | Dimmed, future work |
| P0 Critical | `#F26D78` bold | Priority badge |
| P1 High | `#FFB454` | Priority badge |
| P2 Medium | `#39BAE6` | Priority badge |
| P3/P4 | `#6C7680` muted | Priority badge |

Typography: `JetBrains Mono` for IDs and code; system sans-serif for prose.
Background: `#0D1017` (Ayu dark) with `#13191F` card surfaces.

### 6.2 Views

#### View 1 — Mission Control (default landing)

```
┌─────────────────────────────────────────────────────────────┐
│  bd dashboard          ● live    [search...]   [filters ▾]  │
├──────────────┬──────────────────────────────────────────────┤
│  STATS BAR   │                                              │
│  ○ 12 open   │                                              │
│  ◐ 4 active  │         KANBAN BOARD                         │
│  ✓ 89 closed │                                              │
│  ❄ 2 blocked │                                              │
├──────────────┤                                              │
│  ACTIVITY    │                                              │
│  FEED (live) │                                              │
│              │                                              │
│  bd-123      │                                              │
│  claimed     │                                              │
│  2s ago      │                                              │
│  ─────────   │                                              │
│  bd-456      │                                              │
│  created     │                                              │
│  1m ago      │                                              │
└──────────────┴──────────────────────────────────────────────┘
```

The Kanban board has four columns: `open` · `in_progress` · `blocked` ·
`closed (today)`. Cards show: ID, title, priority badge, type icon, assignee
avatar (initials). Dragging a card across columns fires a PATCH to update
status.

#### View 2 — Dependency Graph

Full-screen D3 force-directed graph. Nodes are issues; edges are dependencies.

- **Node size** encodes priority (P0 = largest)
- **Node color** encodes status (see color table above)
- **Edge style**: solid = `blocks`, dashed = `related`, dotted = `discovered-from`
- **Hover**: tooltip with title, status, assignee
- **Click**: opens the issue detail panel
- **Zoom/pan**: standard D3 zoom
- **Filter sidebar**: toggle which dep types and statuses to show
- **Cluster mode**: group nodes by assignee or by epic/parent

The graph is updated live when SSE events arrive — new nodes animate in, status
changes trigger a color transition.

#### View 3 — Issue Detail Panel

Slides in from the right (60% width overlay). Contains:

```
┌────────────────────────────────────────────────┐
│  bd-123  ◐ in_progress  P1  [feature]  ✕       │
│  ─────────────────────────────────────────────  │
│  Title here                                     │
│                                                 │
│  Description ──────────────────────────────     │
│  ...                                            │
│                                                 │
│  Acceptance Criteria ──────────────────────     │
│  ...                                            │
│                                                 │
│  Dependencies ─────────────────────────────     │
│  ● blocks   bd-124  open                        │
│  ○ blocked-by  bd-100  ✓ closed                 │
│                                                 │
│  Activity ─────────────────────────────────     │
│  f.noorloos  claimed    2026-05-11 10:00        │
│  f.noorloos  created    2026-05-11 09:45        │
│                                                 │
│  [Claim]  [Close]  [View in Graph]              │
└────────────────────────────────────────────────┘
```

#### View 4 — Agent Activity Feed (full page)

A reverse-chronological stream of all events. Each row:

```
  10:02:34  bd-789  f.noorloos    claimed        ◐ in_progress
  10:01:12  bd-123  claude-3      created   P1   ○ open
  09:58:45  bd-456  claude-3      closed    ✓    "Completed"
```

Click any row to open that issue in the detail panel.
Filters: by actor, by event type, by time range.

#### View 5 — Ready Work Queue

Mirrors `bd ready` exactly, but rendered as a sortable table with live
highlighting. The top item pulses gently to indicate "this is claimable now."
One-click claim button on each row.

#### View 6 — Priority Matrix

2×2 quadrant (Eisenhower-style):

```
              HIGH PRIORITY          LOW PRIORITY
  ┌──────────────────────┬──────────────────────┐
  │  P0 / P1             │  P2                  │
  │  Do Now              │  Schedule            │
  │                      │                      │
  ├──────────────────────┼──────────────────────┤
  │  P3                  │  P4                  │
  │  Delegate            │  Backlog             │
  │                      │                      │
  └──────────────────────┴──────────────────────┘
```

Issue cards are positioned within quadrants, sortable by age within each cell.

---

## 7. Live Update Strategy

```
Storage write (bd update / bd create / etc.)
    │
    ▼
Dolt auto-commit (existing behaviour)
    │
    ▼
Watcher detects new Dolt HEAD (polls commit hash, 250 ms)
    │
    ▼
Watcher diffs changed rows (SQL: SELECT * WHERE updated_at > last_seen)
    │
    ▼
SSE Hub broadcasts typed event to all connected browsers
    │
    ▼
Frontend receives event → minimal DOM/state patch (no full reload)
```

The poll interval of 250 ms is intentionally short — Dolt reads are
in-process (embedded mode) and extremely fast. For server mode, the watcher
calls the same storage interface and is still local-network at worst.

---

## 8. Implementation Plan

### Phase 1 — Minimal Viable Dashboard

Goal: `bd dashboard` opens a browser with a live Kanban board.

1. `internal/dashboard/server.go` — HTTP server, static file serving, graceful shutdown
2. `internal/dashboard/sse.go` — SSE hub (fan-out to N clients)
3. `internal/dashboard/watcher.go` — Dolt commit poller → SSE events
4. `internal/dashboard/handlers.go` — `/api/v1/issues` and `/api/v1/stats`
5. `internal/dashboard/static/index.html` — layout shell
6. `internal/dashboard/static/style.css` — Ayu dark theme, CSS variables
7. `internal/dashboard/static/app.js` — Kanban board, SSE connection, basic routing
8. `cmd/bd/dashboard.go` — Cobra command, browser open, server lifecycle
9. `internal/dashboard/openurl.go` — `open`/`xdg-open`/`start` cross-platform

**Deliverable:** Kanban board with live card updates. No D3 yet.

### Phase 2 — Dependency Graph

1. `/api/v1/graph` endpoint
2. `vendor/d3.v7.min.js` embedded
3. Graph view: force-directed layout, node color/size encoding
4. Live graph updates from SSE (node state changes, new edges)
5. Issue detail panel (slide-in)

**Deliverable:** Full interactive dependency graph with drill-down.

### Phase 3 — Full Feature Set

1. Activity feed view
2. Ready work queue with claim button
3. Priority matrix view
4. Inline mutations (PATCH status, claim, close)
5. Keyboard navigation (j/k to move, Enter to open, g to go to graph)
6. URL routing (`#/issues/bd-123`, `#/graph`, `#/feed`)
7. Dark/light mode toggle (auto-detected, matches CLI)
8. `--read-only` enforcement in API handlers

### Phase 4 — Polish

1. Smooth animations (node transitions, card slides)
2. Search with real-time filtering
3. Saved filter presets (persisted to `~/.config/beads/dashboard.json`)
4. Assignee avatars (initials + color derived from email hash)
5. Export: PNG of graph view
6. `bd dashboard --port 0` for auto-port (useful in scripts)

---

## 9. Key Technical Decisions

### Why embedded HTTP, not a separate process?

The dashboard shares the same storage connection as the running `bd` process.
No IPC, no auth, no port-coordination dance. Kill the terminal tab, server
stops. Simple.

### Why SSE instead of WebSocket?

SSE is unidirectional and simpler to implement server-side. All mutations go
through normal REST calls. WebSocket would add complexity for no gain here —
the UI never needs a long-lived bidirectional channel.

### Why go:embed instead of a build step?

Zero external toolchain dependency. `go build` produces a fully self-contained
binary. For development, the server can be started with `--dev` to serve from
disk instead of the embed — fast iteration without rebuilds.

### Why D3 instead of a React graph library?

D3 v7 is the standard for custom force-directed graphs. It gives full control
over layout, transitions, and edge rendering. Preact/React graph libs trade
that control for ease, and none of them match the visual quality needed here.
D3 is a single file, embedded at 280 KB minified.

### Why Preact (optional) instead of React?

3 KB vs 40 KB. The dashboard UI is a small SPA. Preact's API is identical to
React; if the codebase ever needs to grow, swapping in React is a one-line
change in the import map.

---

## 10. File Reference Summary

| Path | Purpose |
|------|---------|
| `cmd/bd/dashboard.go` | Cobra command entry point |
| `internal/dashboard/server.go` | HTTP server, router, embed mount |
| `internal/dashboard/handlers.go` | REST handlers, query param parsing |
| `internal/dashboard/sse.go` | SSE hub: register, deregister, broadcast |
| `internal/dashboard/watcher.go` | Dolt commit poller → event emission |
| `internal/dashboard/openurl.go` | Open browser (darwin/linux/windows) |
| `internal/dashboard/static/index.html` | SPA shell, import map |
| `internal/dashboard/static/style.css` | Ayu dark theme, CSS custom properties |
| `internal/dashboard/static/app.js` | Router, views, SSE client, D3 wiring |
| `internal/dashboard/static/vendor/d3.v7.min.js` | D3 library |
| `internal/dashboard/static/vendor/preact.min.js` | Preact (optional) |

---

## 11. Out of Scope (for now)

- Authentication / multi-user access (dashboard binds to 127.0.0.1 only)
- Remote dashboard (bd serving over a network for a team)
- Mobile layout
- Dolt history timeline / time-travel view
- Inline markdown editing of issue descriptions
- Webhook-triggered refresh from external CI/CD

These are natural follow-on phases once the core is stable.
