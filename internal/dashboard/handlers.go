package dashboard

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// Handlers holds the storage reference for REST API handlers.
type Handlers struct {
	store    storage.Storage
	readOnly bool
}

// NewHandlers creates a Handlers instance.
func NewHandlers(store storage.Storage, readOnly bool) *Handlers {
	return &Handlers{store: store, readOnly: readOnly}
}

// writeJSON encodes v as JSON and writes it with status code.
func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError encodes a JSON error body.
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ListIssues handles GET /api/v1/issues
// Query params: status, priority, assignee, q, limit
func (h *Handlers) ListIssues(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := types.IssueFilter{}

	if s := q.Get("status"); s != "" {
		st := types.Status(s)
		filter.Status = &st
	}
	if p := q.Get("priority"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			filter.Priority = &n
		}
	}
	if a := q.Get("assignee"); a != "" {
		filter.Assignee = &a
	}
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			filter.Limit = n
		}
	}

	search := q.Get("q")
	issues, err := h.store.SearchIssues(r.Context(), search, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, issues)
}

// ReadyIssues handles GET /api/v1/issues/ready
func (h *Handlers) ReadyIssues(w http.ResponseWriter, r *http.Request) {
	issues, err := h.store.GetReadyWork(r.Context(), types.WorkFilter{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, issues)
}

// GetIssue handles GET /api/v1/issues/{id}
func (h *Handlers) GetIssue(w http.ResponseWriter, r *http.Request, id string) {
	issue, err := h.store.GetIssue(r.Context(), id)
	if err != nil {
		if err == storage.ErrNotFound {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Attach deps and comments for detail view.
	deps, _ := h.store.GetDependenciesWithMetadata(r.Context(), id)
	issue.Dependencies = flattenDeps(deps)
	issue.Comments, _ = h.store.GetIssueComments(r.Context(), id)

	writeJSON(w, http.StatusOK, issue)
}

// GetGraph handles GET /api/v1/graph
func (h *Handlers) GetGraph(w http.ResponseWriter, r *http.Request) {
	filter := types.IssueFilter{}
	issues, err := h.store.SearchIssues(r.Context(), "", filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type Node struct {
		ID       string      `json:"id"`
		Title    string      `json:"title"`
		Status   types.Status `json:"status"`
		Priority int         `json:"priority"`
		Type     string      `json:"type"`
		Assignee string      `json:"assignee,omitempty"`
	}
	type Edge struct {
		From    string `json:"from"`
		To      string `json:"to"`
		DepType string `json:"dep_type"`
	}
	type Graph struct {
		Nodes []Node `json:"nodes"`
		Edges []Edge `json:"edges"`
	}

	g := Graph{}
	for _, iss := range issues {
		g.Nodes = append(g.Nodes, Node{
			ID:       iss.ID,
			Title:    iss.Title,
			Status:   iss.Status,
			Priority: iss.Priority,
			Type:     string(iss.IssueType),
			Assignee: iss.Assignee,
		})
		deps, err := h.store.GetDependenciesWithMetadata(r.Context(), iss.ID)
		if err != nil {
			continue
		}
		for _, d := range deps {
			g.Edges = append(g.Edges, Edge{
				From:    iss.ID,
				To:      d.ID,
				DepType: string(d.DependencyType),
			})
		}
	}
	writeJSON(w, http.StatusOK, g)
}

// GetStats handles GET /api/v1/stats
func (h *Handlers) GetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.store.GetStatistics(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// GetEvents handles GET /api/v1/events
// Query params: hours (lookback window, default 168 = 7d), limit (default 100, max 500)
func (h *Handlers) GetEvents(w http.ResponseWriter, r *http.Request) {
	hours := 168
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			hours = n
		}
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	events, err := h.store.GetAllEventsSince(r.Context(), since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sort.Slice(events, func(i, j int) bool { return events[i].CreatedAt.After(events[j].CreatedAt) })
	if len(events) > limit {
		events = events[:limit]
	}

	type feedEvent struct {
		ID        string    `json:"id"`
		IssueID   string    `json:"issue_id"`
		EventType string    `json:"event_type"`
		Actor     string    `json:"actor"`
		Status    string    `json:"status,omitempty"`
		CreatedAt time.Time `json:"created_at"`
	}
	out := make([]feedEvent, 0, len(events))
	for _, ev := range events {
		out = append(out, feedEvent{
			ID:        ev.ID,
			IssueID:   ev.IssueID,
			EventType: string(ev.EventType),
			Actor:     ev.Actor,
			Status:    eventDerivedStatus(ev),
			CreatedAt: ev.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// eventDerivedStatus infers the issue's resulting status from an event
// without an extra store round-trip, since only a handful of event types
// carry status information at all. EventStatusChanged's NewValue is a JSON
// object (json.Marshal of the update's field map, e.g. {"status":"..."}),
// not a bare status string — RecordFullEventInTable/updateIssueInTx encode it
// that way, so it must be parsed rather than used verbatim.
func eventDerivedStatus(ev *types.Event) string {
	switch ev.EventType {
	case types.EventClosed:
		return "closed"
	case types.EventReopened:
		return "open"
	case types.EventClaimed:
		return "in_progress"
	case types.EventStatusChanged:
		if ev.NewValue == nil {
			return ""
		}
		var fields struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(*ev.NewValue), &fields); err == nil {
			return fields.Status
		}
	}
	return ""
}

// PatchIssue handles PATCH /api/v1/issues/{id}
func (h *Handlers) PatchIssue(w http.ResponseWriter, r *http.Request, id string) {
	if h.readOnly {
		writeError(w, http.StatusForbidden, "dashboard is in read-only mode")
		return
	}
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := h.store.UpdateIssue(r.Context(), id, updates, "dashboard"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.GetIssue(w, r, id)
}

// ClaimIssue handles POST /api/v1/issues/{id}/claim
func (h *Handlers) ClaimIssue(w http.ResponseWriter, r *http.Request, id string) {
	if h.readOnly {
		writeError(w, http.StatusForbidden, "dashboard is in read-only mode")
		return
	}
	updates := map[string]interface{}{
		"status":   "in_progress",
		"assignee": "dashboard",
	}
	if err := h.store.UpdateIssue(r.Context(), id, updates, "dashboard"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.GetIssue(w, r, id)
}

// CloseIssue handles POST /api/v1/issues/{id}/close
func (h *Handlers) CloseIssue(w http.ResponseWriter, r *http.Request, id string) {
	if h.readOnly {
		writeError(w, http.StatusForbidden, "dashboard is in read-only mode")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	reason := body.Reason
	if reason == "" {
		reason = "closed via dashboard"
	}
	if err := h.store.CloseIssue(r.Context(), id, reason, "dashboard", ""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.GetIssue(w, r, id)
}

// flattenDeps converts IssueWithDependencyMetadata to Dependency slice.
func flattenDeps(deps []*types.IssueWithDependencyMetadata) []*types.Dependency {
	if len(deps) == 0 {
		return nil
	}
	result := make([]*types.Dependency, 0, len(deps))
	for _, d := range deps {
		result = append(result, &types.Dependency{
			DependsOnID: d.ID,
			Type:        d.DependencyType,
		})
	}
	return result
}
