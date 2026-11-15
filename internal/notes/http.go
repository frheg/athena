package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/frheg/athena/internal/mcpx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func who(ctx context.Context) (string, string) {
	t, s := mcpx.Caller(ctx), mcpx.Subject(ctx)
	if t == "" {
		t = "default"
	}
	if s == "" {
		s = "default"
	}
	return t, s
}

// Routes is the REST API: due reminders, for the service that delivers them.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/reminders/due", func(w http.ResponseWriter, r *http.Request) {
		tenant, _ := who(r.Context())
		due, err := s.Due(r.Context(), tenant)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"reminders": due})
	})
}

// AddIn adds a note.
type AddIn struct {
	Title string   `json:"title"`
	Body  string   `json:"body,omitempty" jsonschema:"markdown"`
	Tags  []string `json:"tags,omitempty"`
}

// UpdateIn changes a note; empty fields stay as they are.
type UpdateIn struct {
	ID    string   `json:"id"`
	Title string   `json:"title,omitempty"`
	Body  string   `json:"body,omitempty"`
	Tags  []string `json:"tags,omitempty"`
}

// SearchIn finds notes.
type SearchIn struct {
	Query string `json:"query,omitempty" jsonschema:"words to look for; empty lists the newest"`
	Tag   string `json:"tag,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// IDIn names one item.
type IDIn struct {
	ID string `json:"id"`
}

// RemindIn adds a reminder.
type RemindIn struct {
	Text string `json:"text" jsonschema:"what to remind about, written for the moment it fires"`
	When string `json:"when" jsonschema:"local time like 2026-11-16 08:00, RFC 3339, or a delay like 2h"`
}

// ListIn lists reminders.
type ListIn struct {
	Past bool `json:"past,omitempty" jsonschema:"include reminders that already fired"`
}

// Register adds the MCP tools.
func (s *Service) Register(srv *mcp.Server) {
	type notes struct {
		Notes []Note `json:"notes"`
	}
	type reminders struct {
		Reminders []Reminder `json:"reminders"`
		Now       string     `json:"now"`
	}
	ok := map[string]bool{"ok": true}
	mcp.AddTool(srv, &mcp.Tool{Name: "add", Description: "Save a note for the user."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in AddIn) (*mcp.CallToolResult, Note, error) {
			t, sub := who(ctx)
			n, err := s.Add(ctx, t, sub, in.Title, in.Body, in.Tags)
			return nil, n, err
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "search", Description: "Find the user's notes by words or tag, or list the newest."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in SearchIn) (*mcp.CallToolResult, notes, error) {
			t, sub := who(ctx)
			n, err := s.Search(ctx, t, sub, in.Query, in.Tag, in.Limit)
			return nil, notes{n}, err
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "get", Description: "Read one note in full."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in IDIn) (*mcp.CallToolResult, Note, error) {
			t, sub := who(ctx)
			n, err := s.Get(ctx, t, sub, in.ID)
			return nil, n, err
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "update", Description: "Change a note's title, body or tags."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in UpdateIn) (*mcp.CallToolResult, Note, error) {
			t, sub := who(ctx)
			n, err := s.Update(ctx, t, sub, in.ID, in.Title, in.Body, in.Tags)
			return nil, n, err
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "delete", Description: "Delete a note."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in IDIn) (*mcp.CallToolResult, map[string]bool, error) {
			t, sub := who(ctx)
			return nil, ok, s.Delete(ctx, t, sub, in.ID)
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "remind", Description: "Set a reminder. At that time the user gets a notification (and a mail when set up)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in RemindIn) (*mcp.CallToolResult, Reminder, error) {
			t, sub := who(ctx)
			r, err := s.Remind(ctx, t, sub, in.Text, in.When)
			return nil, r, err
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "reminders", Description: "List the user's coming reminders."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in ListIn) (*mcp.CallToolResult, reminders, error) {
			t, sub := who(ctx)
			r, err := s.Reminders(ctx, t, sub, in.Past)
			return nil, reminders{r, s.now().In(s.zone()).Format(time.RFC3339)}, err
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "cancel_reminder", Description: "Cancel a coming reminder."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in IDIn) (*mcp.CallToolResult, map[string]bool, error) {
			t, sub := who(ctx)
			return nil, ok, s.Cancel(ctx, t, sub, in.ID)
		})
}

// Client fetches due reminders; core calls it every minute.
type Client struct {
	URL   string // without /v1
	Token string
	HTTP  *http.Client
}

// Due claims the reminders that are due now.
func (c *Client) Due(ctx context.Context) ([]Reminder, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/v1/reminders/due", nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var out struct {
		Reminders []Reminder `json:"reminders"`
		Error     string     `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("reminders: HTTP %d", res.StatusCode)
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("reminders: %s", out.Error)
	}
	return out.Reminders, nil
}
