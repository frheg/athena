package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/frheg/athena/internal/mcpx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SendIn is a message to send.
type SendIn struct {
	To      string `json:"to" jsonschema:"recipient name (see the recipients tool), for example owner"`
	Subject string `json:"subject" jsonschema:"one line"`
	Body    string `json:"body" jsonschema:"plain text"`
}

// Routes is the REST API under /v1/.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/send", func(w http.ResponseWriter, r *http.Request) {
		var in SendIn
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		m, err := s.Send(r.Context(), in.To, in.Subject, in.Body)
		if err != nil {
			code := 400
			if errors.Is(err, errLimit) {
				code = 429
			} else if strings.HasPrefix(err.Error(), "smtp") {
				code = 502
			}
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "dry_run": m.DryRun})
	})
	mux.HandleFunc("GET /v1/recipients", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"recipients": s.Names(), "dry_run": s.SMTP.Host == ""})
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Sent is the send tool's result.
type Sent struct {
	OK     bool `json:"ok"`
	DryRun bool `json:"dry_run" jsonschema:"true when the service only logs messages (no mail server configured)"`
}

// Register adds the MCP tools.
func (s *Service) Register(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{Name: "send", Description: "Send a message (mail) to a known recipient by name. It only reaches the configured recipients."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in SendIn) (*mcp.CallToolResult, Sent, error) {
			m, err := s.Send(ctx, in.To, in.Subject, in.Body)
			if err != nil {
				return nil, Sent{}, err
			}
			if m.DryRun {
				// said in words: a model reads "ok": true as sent, whatever dry_run says
				return mcpx.Text("NOT SENT: no mail server is set up (dry run), so the message was only logged. Tell the user it " +
					"did not reach them, and that mail needs SMTP settings first."), Sent{OK: false, DryRun: true}, nil
			}
			return nil, Sent{OK: true}, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "recipients", Description: "List the names messages can be sent to."},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string][]string, error) {
			return nil, map[string][]string{"recipients": s.Names()}, nil
		})
}

// Client sends through a notify service; core uses it for job results.
type Client struct {
	URL   string // without /v1
	Token string
	To    string // recipient name for the owner, default owner
	HTTP  *http.Client
}

// Send implements engine.Notifier.
func (c *Client) Send(ctx context.Context, _ string, subject, body string) error {
	to := c.To
	if to == "" {
		to = "owner"
	}
	b, _ := json.Marshal(SendIn{To: to, Subject: subject, Body: body})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/v1/send", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 90 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("notify: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
