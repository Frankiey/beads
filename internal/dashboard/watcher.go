package dashboard

import (
	"context"
	"encoding/json"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// eventLagSlack keeps the watcher's confirmed cursor this far behind
// wall-clock time. On a version-controlled backend a committed row can
// become visible to readers slightly after its logical created_at (see
// storage.EventQueryStore's COMMIT-VISIBILITY-LAG doc), so the cursor is
// never advanced past this boundary: events newer than it stay
// "unconfirmed" and are re-scanned (and deduped by id) on every poll until
// they age past the boundary. This is the overlap-and-dedup strategy the
// storage layer recommends.
const eventLagSlack = 45 * time.Second

const eventPollLimit = 500

// Watcher polls the durable event log for new events and broadcasts them to
// the SSE hub.
type Watcher struct {
	store    storage.Storage
	eventsQ  storage.EventQueryStore // nil if store doesn't support keyset paging
	hub      *Hub
	interval time.Duration

	// Cursor-based state (used when eventsQ != nil).
	cursor storage.EventCursor
	seen   map[string]time.Time // unconfirmed event id -> created_at

	// Legacy fallback state (used when eventsQ == nil).
	lastSeen time.Time
}

// NewWatcher creates a watcher that polls at the given interval.
func NewWatcher(store storage.Storage, hub *Hub, interval time.Duration) *Watcher {
	eq, _ := store.(storage.EventQueryStore)
	return &Watcher{
		store:    store,
		eventsQ:  eq,
		hub:      hub,
		interval: interval,
		// UTC: created_at is stored in UTC and the embedded Dolt engine
		// compares this cursor literally, so a local-zone time.Now() shifts
		// the cursor forward by the host's UTC offset and it can then never
		// be exceeded by a real event's created_at — see the same note on
		// GetEvents's `since` in handlers.go.
		cursor:   storage.EventCursor{CreatedAt: time.Now().UTC().Add(-eventLagSlack)},
		seen:     make(map[string]time.Time),
		lastSeen: time.Now().UTC(),
	}
}

// Run polls for events until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// heartbeat every 30 s keeps the SSE connection alive through proxies.
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			w.hub.Broadcast(SSEEvent{Type: "heartbeat", Data: `{"ts":"` + time.Now().UTC().Format(time.RFC3339) + `"}`})
		case <-ticker.C:
			if w.eventsQ != nil {
				w.poll(ctx)
			} else {
				w.pollLegacy(ctx)
			}
		}
	}
}

// poll uses the keyset-cursor EventsSince API, staying eventLagSlack behind
// wall-clock time so a late-committing row can never be permanently skipped.
func (w *Watcher) poll(ctx context.Context) {
	events, err := w.eventsQ.EventsSince(ctx, w.cursor, "", eventPollLimit)
	if err != nil || len(events) == 0 {
		return
	}

	latestPerIssue := make(map[string]*types.Event)
	for _, ev := range events {
		if _, dup := w.seen[ev.ID]; dup {
			continue
		}
		w.seen[ev.ID] = ev.CreatedAt
		if prev, ok := latestPerIssue[ev.IssueID]; !ok || ev.CreatedAt.After(prev.CreatedAt) {
			latestPerIssue[ev.IssueID] = ev
		}
	}
	for issueID, ev := range latestPerIssue {
		w.emitIssueEvent(ctx, issueID, ev.EventType)
	}

	// Advance the cursor only up to the safe boundary. Events newer than
	// the boundary remain unconfirmed and are re-fetched (and deduped via
	// w.seen) on the next poll.
	safeBoundary := time.Now().UTC().Add(-eventLagSlack)
	for i := len(events) - 1; i >= 0; i-- {
		if !events[i].CreatedAt.After(safeBoundary) {
			w.cursor = storage.EventCursor{CreatedAt: events[i].CreatedAt, ID: events[i].ID}
			break
		}
	}

	// Entries at or behind the new cursor can never be re-fetched (the
	// store's keyset query is a strict (created_at, id) > cursor
	// comparison); drop them so w.seen doesn't grow unbounded.
	for id, ts := range w.seen {
		if ts.Before(w.cursor.CreatedAt) || (ts.Equal(w.cursor.CreatedAt) && id <= w.cursor.ID) {
			delete(w.seen, id)
		}
	}
}

// pollLegacy is used when the store doesn't implement storage.EventQueryStore.
// It has the same commit-visibility-lag exposure the original implementation
// had; kept only as a compatibility fallback.
func (w *Watcher) pollLegacy(ctx context.Context) {
	cutoff := w.lastSeen
	w.lastSeen = time.Now().UTC()

	events, err := w.store.GetAllEventsSince(ctx, cutoff)
	if err != nil || len(events) == 0 {
		return
	}

	latestPerIssue := make(map[string]*types.Event)
	for _, ev := range events {
		if prev, ok := latestPerIssue[ev.IssueID]; !ok || ev.CreatedAt.After(prev.CreatedAt) {
			latestPerIssue[ev.IssueID] = ev
		}
	}
	for issueID, ev := range latestPerIssue {
		w.emitIssueEvent(ctx, issueID, ev.EventType)
	}
}

func (w *Watcher) emitIssueEvent(ctx context.Context, issueID string, evType types.EventType) {
	issue, err := w.store.GetIssue(ctx, issueID)
	if err != nil {
		return
	}
	payload, err := json.Marshal(issue)
	if err != nil {
		return
	}
	w.hub.Broadcast(SSEEvent{Type: eventSSEType(evType), Data: string(payload)})
}

func eventSSEType(t types.EventType) string {
	switch t {
	case types.EventCreated:
		return "issue.created"
	case types.EventClosed:
		return "issue.closed"
	case types.EventDependencyAdded:
		return "dep.added"
	case types.EventDependencyRemoved:
		return "dep.removed"
	default:
		return "issue.updated"
	}
}
