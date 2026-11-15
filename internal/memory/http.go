package memory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/frheg/athena/internal/mcpx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// who returns tenant and subject of a request: the tenant is the authenticated caller, the subject comes from the
// X-Athena-Subject header (or ?subject= on the REST API).
func who(ctx context.Context, subject string) (string, string) {
	tenant := mcpx.Caller(ctx)
	if tenant == "" {
		tenant = "default"
	}
	if subject == "" {
		subject = mcpx.Subject(ctx)
	}
	if subject == "" {
		subject = "default"
	}
	return tenant, subject
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, ErrNotFound) {
		code = http.StatusNotFound
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

type itemIn struct {
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Source  string `json:"source"`
}

// Routes is the REST API (for UIs and for recall), mounted under /v1/.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/recall", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Subject  string  `json:"subject"`
			Query    string  `json:"query"`
			K        int     `json:"k"`
			MinScore float64 `json:"min_score"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, err)
			return
		}
		t, sub := who(r.Context(), in.Subject)
		items, err := s.Search(r.Context(), t, sub, in.Query, in.K, in.MinScore)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
	})
	mux.HandleFunc("GET /v1/items", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		t, sub := who(r.Context(), q.Get("subject"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		items, err := s.List(r.Context(), t, sub, q.Get("kind"), q.Get("q"), limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items, "kinds": Kinds})
	})
	mux.HandleFunc("POST /v1/items", func(w http.ResponseWriter, r *http.Request) {
		var in itemIn
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, err)
			return
		}
		t, sub := who(r.Context(), in.Subject)
		it, replaced, err := s.Remember(r.Context(), t, sub, in.Kind, in.Text, in.Source)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"item": it, "replaced": replaced})
	})
	mux.HandleFunc("PATCH /v1/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in itemIn
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, err)
			return
		}
		t, sub := who(r.Context(), in.Subject)
		it, err := s.Update(r.Context(), t, sub, r.PathValue("id"), in.Kind, in.Text)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"item": it})
	})
	mux.HandleFunc("DELETE /v1/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		t, sub := who(r.Context(), r.URL.Query().Get("subject"))
		if err := s.Forget(r.Context(), t, sub, r.PathValue("id")); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /v1/reindex", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Reindex(r.Context()); err != nil {
			writeErr(w, err)
			return
		}
		go func() {
			if _, err := s.Backfill(context.Background()); err != nil {
				s.Log.Warn("reindex", "err", err)
			}
		}()
		writeJSON(w, 202, map[string]bool{"started": true})
	})
}

// SearchIn are the arguments of the search tool.
type SearchIn struct {
	Query string `json:"query" jsonschema:"what to look for, in plain words"`
	K     int    `json:"k,omitempty" jsonschema:"how many items, default 5"`
}

// RememberIn are the arguments of the remember tool.
type RememberIn struct {
	Text string `json:"text" jsonschema:"one short, self-contained fact about the user, written so it makes sense on its own later"`
	Kind string `json:"kind,omitempty" jsonschema:"fact, preference, person, place, event or other (default fact)"`
}

// ForgetIn are the arguments of the forget tool.
type ForgetIn struct {
	ID string `json:"id" jsonschema:"the item's id, from search"`
}

// Items is a list result.
type Items struct {
	Items []Item `json:"items"`
}

// Saved is the remember result.
type Saved struct {
	Item     Item `json:"item"`
	Replaced bool `json:"replaced" jsonschema:"true when a very similar item was updated instead of adding a new one"`
}

// Register adds the MCP tools.
func (s *Service) Register(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{Name: "search", Description: "Search what is remembered about the user, by meaning. Use it when the answer may depend on the user's own facts, preferences, people or places."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in SearchIn) (*mcp.CallToolResult, Items, error) {
			t, sub := who(ctx, "")
			items, err := s.Search(ctx, t, sub, in.Query, in.K, 0)
			return nil, Items{Items: items}, err
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "remember", Description: "Store one lasting fact about the user (a preference, a person, a place, a plan). Not for passing details of this conversation. The user can see and edit everything stored."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in RememberIn) (*mcp.CallToolResult, Saved, error) {
			t, sub := who(ctx, "")
			it, replaced, err := s.Remember(ctx, t, sub, in.Kind, in.Text, "assistant")
			return nil, Saved{Item: it, Replaced: replaced}, err
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "forget", Description: "Delete one remembered item, when the user asks to forget it or it is wrong."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ForgetIn) (*mcp.CallToolResult, map[string]bool, error) {
			t, sub := who(ctx, "")
			if err := s.Forget(ctx, t, sub, in.ID); err != nil {
				return nil, nil, err
			}
			return nil, map[string]bool{"ok": true}, nil
		})
}
