package dashboard

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// fakeEventStore is a minimal storage.Storage + storage.EventQueryStore
// double for watcher tests. It embeds a nil storage.Storage so it satisfies
// the full (100+ method) interface; only the methods the watcher actually
// calls are implemented below.
type fakeEventStore struct {
	storage.Storage
	events []*types.Event
	issues map[string]*types.Issue
}

func (f *fakeEventStore) EventsSince(_ context.Context, cursor storage.EventCursor, issueID string, limit int) ([]*types.Event, error) {
	var out []*types.Event
	for _, ev := range f.events {
		if issueID != "" && ev.IssueID != issueID {
			continue
		}
		if ev.CreatedAt.After(cursor.CreatedAt) || (ev.CreatedAt.Equal(cursor.CreatedAt) && ev.ID > cursor.ID) {
			out = append(out, ev)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeEventStore) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	iss, ok := f.issues[id]
	if !ok {
		return nil, fmt.Errorf("issue %s not found", id)
	}
	return iss, nil
}

func (f *fakeEventStore) GetAllEventsSince(_ context.Context, since time.Time) ([]*types.Event, error) {
	var out []*types.Event
	for _, ev := range f.events {
		if ev.CreatedAt.After(since) {
			out = append(out, ev)
		}
	}
	return out, nil
}

func drain(hub *Hub, ch chan SSEEvent) []SSEEvent {
	var got []SSEEvent
	for {
		select {
		case ev := <-ch:
			got = append(got, ev)
		default:
			return got
		}
	}
}

func newTestWatcher(store *fakeEventStore) (*Watcher, chan SSEEvent) {
	hub := NewHub()
	ch := hub.subscribe()
	eq, _ := storage.Storage(store).(storage.EventQueryStore)
	w := &Watcher{
		store:   store,
		eventsQ: eq,
		hub:     hub,
		cursor:  storage.EventCursor{CreatedAt: time.Now().Add(-eventLagSlack)},
		seen:    make(map[string]time.Time),
	}
	return w, ch
}

// A recently-committed event (inside the still-unconfirmed slack window)
// must not be broadcast twice just because a later poll re-fetches it —
// this is the overlap-and-dedup behavior storage.EventQueryStore's
// COMMIT-VISIBILITY-LAG doc calls for.
func TestWatcherPoll_DedupsUnconfirmedEventAcrossPolls(t *testing.T) {
	now := time.Now()
	store := &fakeEventStore{
		events: []*types.Event{
			{ID: "e1", IssueID: "bd-1", EventType: types.EventCreated, CreatedAt: now.Add(-1 * time.Second)},
		},
		issues: map[string]*types.Issue{"bd-1": {ID: "bd-1"}},
	}
	w, ch := newTestWatcher(store)
	ctx := context.Background()

	w.poll(ctx)
	got := drain(w.hub, ch)
	if len(got) != 1 || got[0].Type != "issue.created" {
		t.Fatalf("poll 1: want one issue.created event, got %+v", got)
	}

	// Event e1 is still within the slack window, so the cursor has not
	// advanced past it — a real EventsSince would return it again.
	w.poll(ctx)
	got = drain(w.hub, ch)
	if len(got) != 0 {
		t.Fatalf("poll 2: expected no re-broadcast of unconfirmed event, got %+v", got)
	}
}

// Once an event ages past the slack boundary, the cursor advances beyond it
// and its id is pruned from the in-memory dedup set.
func TestWatcherPoll_AdvancesCursorPastConfirmedEvent(t *testing.T) {
	now := time.Now()
	store := &fakeEventStore{
		events: []*types.Event{
			{ID: "e1", IssueID: "bd-1", EventType: types.EventCreated, CreatedAt: now.Add(-eventLagSlack - time.Second)},
		},
		issues: map[string]*types.Issue{"bd-1": {ID: "bd-1"}},
	}
	w, ch := newTestWatcher(store)
	w.cursor = storage.EventCursor{CreatedAt: now.Add(-2 * eventLagSlack)}
	ctx := context.Background()

	w.poll(ctx)
	drain(w.hub, ch)

	if w.cursor.ID != "e1" {
		t.Fatalf("expected cursor to advance to confirmed event e1, got cursor=%+v", w.cursor)
	}
	if _, stillSeen := w.seen["e1"]; stillSeen {
		t.Fatalf("expected e1 to be pruned from the unconfirmed-dedup set once confirmed")
	}
}

// Multiple events for the same issue within one poll collapse to a single
// broadcast carrying the latest event's type, matching the pre-existing
// "keep the latest event per issue" behavior.
func TestWatcherPoll_CollapsesMultipleEventsPerIssue(t *testing.T) {
	now := time.Now()
	store := &fakeEventStore{
		events: []*types.Event{
			{ID: "e1", IssueID: "bd-1", EventType: types.EventUpdated, CreatedAt: now.Add(-2 * time.Second)},
			{ID: "e2", IssueID: "bd-1", EventType: types.EventClosed, CreatedAt: now.Add(-1 * time.Second)},
		},
		issues: map[string]*types.Issue{"bd-1": {ID: "bd-1"}},
	}
	w, ch := newTestWatcher(store)
	ctx := context.Background()

	w.poll(ctx)
	got := drain(w.hub, ch)
	if len(got) != 1 || got[0].Type != "issue.closed" {
		t.Fatalf("want single collapsed issue.closed event, got %+v", got)
	}
}

// The legacy fallback (used when a store doesn't implement EventQueryStore)
// still emits events.
func TestWatcherPollLegacy_EmitsEvents(t *testing.T) {
	now := time.Now()
	store := &fakeEventStore{
		events: []*types.Event{
			{ID: "e1", IssueID: "bd-1", EventType: types.EventCreated, CreatedAt: now},
		},
		issues: map[string]*types.Issue{"bd-1": {ID: "bd-1"}},
	}
	hub := NewHub()
	ch := hub.subscribe()
	w := &Watcher{store: store, eventsQ: nil, hub: hub, lastSeen: now.Add(-time.Minute)}

	w.pollLegacy(context.Background())
	got := drain(hub, ch)
	if len(got) != 1 || got[0].Type != "issue.created" {
		t.Fatalf("want one issue.created event, got %+v", got)
	}
}
