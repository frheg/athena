// Package api is core's HTTP API, used by the UI (and by anything else that wants to talk to Athena).
// JSON in and out; turn events stream as Server-Sent Events.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/frheg/athena/internal/engine"
	"github.com/frheg/athena/internal/gate"
	"github.com/frheg/athena/internal/memory"
	"github.com/frheg/athena/internal/store"
)

// API serves /api/*.
type API struct {
	E         *engine.Engine
	Scheduler *engine.Scheduler
	Memory    *memory.Client // nil: no memory service
	Owner     string         // the one user, for now
	Token     string         // API_TOKEN: when set, every request needs "Authorization: Bearer <token>"
	Version   string
	Info      map[string]any // shown on the status page (model names and such; no secrets)
	Ready     func(ctx context.Context) error
}

type ctxKey struct{}

func (a *API) owner(r *http.Request) string {
	if o, ok := r.Context().Value(ctxKey{}).(string); ok {
		return o
	}
	return a.Owner
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, memory.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, engine.ErrBusy):
		code = http.StatusConflict
	case errors.As(err, new(badRequest)):
		code = http.StatusBadRequest
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

type badRequest struct{ error }

func bad(format string, a ...any) error { return badRequest{fmt.Errorf(format, a...)} }

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return bad("bad JSON body: %v", err)
	}
	return nil
}

func intParam(r *http.Request, name string, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, max)
}

// Handler returns the routes.
func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/sessions", a.listSessions)
	m.HandleFunc("POST /api/sessions", a.createSession)
	m.HandleFunc("GET /api/sessions/{id}", a.getSession)
	m.HandleFunc("PATCH /api/sessions/{id}", a.patchSession)
	m.HandleFunc("POST /api/sessions/{id}/messages", a.postMessage)
	m.HandleFunc("GET /api/turns/{id}", a.getTurn)
	m.HandleFunc("GET /api/turns/{id}/events", a.turnEvents)
	m.HandleFunc("POST /api/turns/{id}/cancel", a.cancelTurn)
	m.HandleFunc("GET /api/events", a.ownerEvents)
	m.HandleFunc("GET /api/approvals", a.listApprovals)
	m.HandleFunc("POST /api/approvals/{id}", a.answerApproval)
	m.HandleFunc("GET /api/permissions", a.listPermissions)
	m.HandleFunc("DELETE /api/permissions/{id}", a.revokePermission)
	m.HandleFunc("GET /api/audit", a.audit)
	m.HandleFunc("GET /api/notifications", a.listNotifications)
	m.HandleFunc("POST /api/notifications/read", a.readNotifications)
	m.HandleFunc("GET /api/jobs", a.listJobs)
	m.HandleFunc("POST /api/jobs", a.saveJob)
	m.HandleFunc("DELETE /api/jobs/{id}", a.deleteJob)
	m.HandleFunc("POST /api/jobs/{id}/run", a.runJob)
	m.HandleFunc("GET /api/tools", a.tools)
	m.HandleFunc("GET /api/status", a.status)
	m.HandleFunc("/api/memory/", a.memoryProxy)
	m.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such endpoint"})
	})
	return a.auth(m)
}

func (a *API) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.Token != "" {
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(tok), []byte(a.Token)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or wrong API token"})
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, a.Owner)))
	})
}

// ---------- sessions ----------

func (a *API) listSessions(w http.ResponseWriter, r *http.Request) {
	ss, err := a.E.Store.Sessions(r.Context(), a.owner(r), store.Like(r.URL.Query().Get("q")), intParam(r, "limit", 100, 500))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"sessions": ss})
}

func (a *API) createSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
		Text  string `json:"text"` // optional first message
	}
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			fail(w, err)
			return
		}
	}
	s, err := a.E.Store.CreateSession(r.Context(), engine.NewID(), a.owner(r), "user", strings.TrimSpace(in.Title))
	if err != nil {
		fail(w, err)
		return
	}
	out := map[string]any{"session": s}
	if strings.TrimSpace(in.Text) != "" {
		turn, err := a.E.Start(r.Context(), a.owner(r), s.ID, "user", in.Text)
		if err != nil {
			fail(w, err)
			return
		}
		out["turn_id"] = turn
	}
	writeJSON(w, 201, out)
}

// getSession returns the conversation as the UI shows it: messages, turns and tool calls.
func (a *API) getSession(w http.ResponseWriter, r *http.Request) {
	ctx, owner, id := r.Context(), a.owner(r), r.PathValue("id")
	s, err := a.E.Store.Session(ctx, owner, id)
	if err != nil {
		fail(w, err)
		return
	}
	msgs, err := a.E.Store.Messages(ctx, id, 0)
	if err != nil {
		fail(w, err)
		return
	}
	turns, _ := a.E.Store.Turns(ctx, id)
	calls, _ := a.E.Store.ToolCalls(ctx, id, "")
	for i := range calls {
		if r := []rune(calls[i].Result); len(r) > 4000 {
			calls[i].Result = string(r[:4000]) + "…"
		}
	}
	approvals, _ := a.E.Store.Approvals(ctx, owner, "waiting", "", 100)
	waiting := []store.Approval{}
	for _, ap := range approvals {
		if ap.SessionID == id {
			waiting = append(waiting, ap)
		}
	}
	writeJSON(w, 200, map[string]any{"session": s, "messages": msgs, "turns": turns, "tool_calls": calls, "approvals": waiting})
}

func (a *API) patchSession(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title    *string `json:"title"`
		Archived bool    `json:"archived"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := a.E.Store.UpdateSession(r.Context(), a.owner(r), r.PathValue("id"), in.Title, in.Archived); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) postMessage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if len(in.Text) > 100_000 {
		fail(w, bad("the message is longer than 100000 characters"))
		return
	}
	turn, err := a.E.Start(r.Context(), a.owner(r), r.PathValue("id"), "user", in.Text)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, engine.ErrBusy) {
			fail(w, err)
		} else {
			fail(w, bad("%v", err))
		}
		return
	}
	writeJSON(w, 202, map[string]string{"turn_id": turn})
}

// ---------- turns and events ----------

func (a *API) getTurn(w http.ResponseWriter, r *http.Request) {
	t, err := a.E.Store.Turn(r.Context(), a.owner(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	calls, _ := a.E.Store.ToolCalls(r.Context(), t.SessionID, t.ID)
	writeJSON(w, 200, map[string]any{"turn": t, "tool_calls": calls})
}

func (a *API) cancelTurn(w http.ResponseWriter, r *http.Request) {
	if err := a.E.Cancel(r.Context(), a.owner(r), r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func lastID(r *http.Request) int {
	s := r.Header.Get("Last-Event-ID")
	if s == "" {
		s = r.URL.Query().Get("after")
	}
	n, _ := strconv.Atoi(s)
	return n
}

// sse streams the hub's events of key. It ends after a closing event when closes is true.
func (a *API) sse(w http.ResponseWriter, r *http.Request, key string, closes bool, final func() *engine.Event) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, errors.New("streaming is not supported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: do not buffer
	w.WriteHeader(200)
	send := func(e engine.Event) {
		b, _ := json.Marshal(e.Data)
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, b)
	}
	past, ch, closed := a.E.Hub.Subscribe(key, lastID(r))
	defer a.E.Hub.Unsubscribe(key, ch)
	for _, e := range past {
		send(e)
	}
	fl.Flush()
	if closes && closed {
		return
	}
	// the hub forgets after a restart: a turn that finished before then is closed from the database
	if closes && len(past) == 0 && final != nil {
		if e := final(); e != nil {
			send(*e)
			fl.Flush()
			return
		}
	}
	beat := time.NewTicker(20 * time.Second)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			fl.Flush()
		case e := <-ch:
			send(e)
			fl.Flush()
			if closes && (e.Type == "done" || e.Type == "error") {
				return
			}
		}
	}
}

func (a *API) turnEvents(w http.ResponseWriter, r *http.Request) {
	t, err := a.E.Store.Turn(r.Context(), a.owner(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	a.sse(w, r, t.ID, true, func() *engine.Event {
		switch t.Status {
		case "done":
			return &engine.Event{ID: 1, Type: "done", Data: map[string]string{"status": "done"}}
		case "failed", "cancelled":
			return &engine.Event{ID: 1, Type: "error", Data: map[string]string{"error": t.Error}}
		case "waiting":
			return &engine.Event{ID: 1, Type: "status", Data: map[string]string{"status": "waiting"}}
		}
		return nil
	})
}

func (a *API) ownerEvents(w http.ResponseWriter, r *http.Request) {
	a.sse(w, r, engine.OwnerStream(a.owner(r)), false, nil)
}

// ---------- approvals and permissions ----------

func (a *API) listApprovals(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "all" {
		status = ""
	} else if status == "" {
		status = "waiting"
	}
	list, err := a.E.Store.Approvals(r.Context(), a.owner(r), status, "", intParam(r, "limit", 100, 500))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"approvals": list})
}

func (a *API) answerApproval(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Answer        string `json:"answer"` // once, session, always, deny
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	var exp *time.Time
	if in.ExpiresInDays > 0 {
		t := time.Now().AddDate(0, 0, in.ExpiresInDays)
		exp = &t
	}
	ap, err := a.E.Answer(r.Context(), a.owner(r), r.PathValue("id"), in.Answer, exp)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, fmt.Errorf("%w: the approval does not exist or was already answered", err))
		} else {
			fail(w, bad("%v", err))
		}
		return
	}
	writeJSON(w, 200, map[string]any{"approval": ap})
}

func (a *API) listPermissions(w http.ResponseWriter, r *http.Request) {
	list, err := a.E.Store.Permissions(r.Context(), a.owner(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"permissions": list})
}

func (a *API) revokePermission(w http.ResponseWriter, r *http.Request) {
	ctx, owner, id := r.Context(), a.owner(r), r.PathValue("id")
	if err := a.E.Store.RevokePermission(ctx, owner, id); err != nil {
		fail(w, err)
		return
	}
	_ = a.E.Store.Audit(ctx, owner, "user", nil, nil, "permission.revoke", "", map[string]string{"permission": id})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	list, err := a.E.Store.AuditLog(r.Context(), a.owner(r), store.AuditQuery{SessionID: q.Get("session"), Tool: q.Get("tool"),
		Event: q.Get("event"), Text: store.Like(q.Get("q")), Before: before, Limit: intParam(r, "limit", 100, 500)})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"audit": list})
}

// ---------- notifications and jobs ----------

func (a *API) listNotifications(w http.ResponseWriter, r *http.Request) {
	list, err := a.E.Store.Notifications(r.Context(), a.owner(r), r.URL.Query().Get("unread") == "1", intParam(r, "limit", 100, 500))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"notifications": list})
}

func (a *API) readNotifications(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"` // empty: all
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.IDs == nil {
		in.IDs = []int64{}
	}
	if err := a.E.Store.MarkRead(r.Context(), a.owner(r), in.IDs); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type jobOut struct {
	store.Job
	Next *time.Time `json:"next_run,omitempty"`
}

func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := a.E.Store.Jobs(r.Context(), a.owner(r))
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]jobOut, 0, len(jobs))
	for _, j := range jobs {
		o := jobOut{Job: j}
		if c, err := engine.ParseCron(j.Schedule); err == nil && j.Enabled {
			if n, ok := c.Next(time.Now().In(a.E.Zone)); ok {
				o.Next = &n
			}
		}
		out = append(out, o)
	}
	writeJSON(w, 200, map[string]any{"jobs": out, "zone": a.E.Zone.String()})
}

func (a *API) saveJob(w http.ResponseWriter, r *http.Request) {
	var in store.Job
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	in.Name, in.Prompt, in.Schedule = strings.TrimSpace(in.Name), strings.TrimSpace(in.Prompt), strings.TrimSpace(in.Schedule)
	if in.Name == "" || in.Prompt == "" {
		fail(w, bad("a job needs a name and a prompt"))
		return
	}
	if len(in.Name) > 60 || strings.ContainsAny(in.Name, ":\n") {
		fail(w, bad("the name must be at most 60 characters, without ':' or line breaks"))
		return
	}
	if _, err := engine.ParseCron(in.Schedule); err != nil {
		fail(w, bad("%v", err))
		return
	}
	in.ID, in.Owner = engine.NewID(), a.owner(r)
	j, err := a.E.Store.UpsertJob(r.Context(), in)
	if err != nil {
		fail(w, err)
		return
	}
	_ = a.E.Store.Audit(r.Context(), in.Owner, "user", nil, nil, "job.save", "", map[string]any{"job": j.Name, "schedule": j.Schedule, "enabled": j.Enabled})
	writeJSON(w, 200, map[string]any{"job": j})
}

func (a *API) deleteJob(w http.ResponseWriter, r *http.Request) {
	ctx, owner := r.Context(), a.owner(r)
	j, err := a.E.Store.Job(ctx, owner, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if err := a.E.Store.DeleteJob(ctx, owner, j.ID); err != nil {
		fail(w, err)
		return
	}
	_ = a.E.Store.Audit(ctx, owner, "user", nil, nil, "job.delete", "", map[string]string{"job": j.Name})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) runJob(w http.ResponseWriter, r *http.Request) {
	j, err := a.E.Store.Job(r.Context(), a.owner(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	sid, err := a.Scheduler.RunJob(r.Context(), j)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 202, map[string]string{"session_id": sid})
}

// ---------- tools, status ----------

type toolOut struct {
	Name        string `json:"name"`
	Server      string `json:"server"`
	Description string `json:"description"`
	Class       string `json:"class"`
	Untrusted   bool   `json:"untrusted"`
	Default     string `json:"default"`
}

func (a *API) tools(w http.ResponseWriter, _ *http.Request) {
	pol := a.E.Policy()
	out := []toolOut{}
	for _, t := range a.E.Tools.Tools() {
		info := pol.Classify(t.Name)
		o := toolOut{Name: t.Name, Server: t.Server, Description: t.Description, Class: info.Class, Untrusted: info.Untrusted}
		if !info.Known {
			o.Default = gate.Deny + " (not in the policy)"
		} else {
			o.Default = pol.Defaults[info.Class]
		}
		out = append(out, o)
	}
	writeJSON(w, 200, map[string]any{"tools": out, "servers": a.E.Tools.Statuses(), "jobs_may": pol.Jobs.Allow, "limits": pol.Limits, "rules": pol.Rules})
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"version": a.Version, "running_turns": a.E.Running(), "servers": a.E.Tools.Statuses(), "info": a.Info, "zone": a.E.Zone.String()}
	if a.Memory != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		_, err := a.Memory.Do(ctx, http.MethodGet, "/items", url.Values{"limit": {"1"}}, nil, a.owner(r), nil)
		cancel()
		out["memory"] = map[string]any{"up": err == nil, "error": errString(err)}
	} else {
		out["memory"] = map[string]any{"up": false, "error": "not configured"}
	}
	if a.Ready != nil {
		out["database"] = map[string]any{"up": a.Ready(r.Context()) == nil}
	}
	writeJSON(w, 200, out)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// memoryProxy passes the UI's memory view through to the memory service, for the signed-in user.
// /api/memory/items[...] maps to /v1/items[...].
func (a *API) memoryProxy(w http.ResponseWriter, r *http.Request) {
	if a.Memory == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the memory service is not configured"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/memory")
	if rest != "/items" && !strings.HasPrefix(rest, "/items/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such endpoint"})
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete:
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body any
	if r.Method == http.MethodPost || r.Method == http.MethodPatch {
		var in map[string]any
		if err := decode(r, &in); err != nil {
			fail(w, err)
			return
		}
		in["source"] = "user"
		body = in
	}
	q := url.Values{}
	for _, k := range []string{"q", "kind", "limit"} {
		if v := r.URL.Query().Get(k); v != "" {
			q.Set(k, v)
		}
	}
	var out json.RawMessage
	code, err := a.Memory.Do(r.Context(), r.Method, rest, q, body, a.owner(r), &out)
	if err != nil {
		if code == 0 {
			code = http.StatusBadGateway
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	if r.Method != http.MethodGet {
		_ = a.E.Store.Audit(r.Context(), a.owner(r), "user", nil, nil, "memory."+strings.ToLower(r.Method), "", map[string]string{"path": rest})
	}
	writeJSON(w, code, out)
}
