// Package toolbox is core's MCP client side: it connects to the configured tool servers, keeps their tool lists up
// to date, and calls tools. Tool names offered to the model are "<server>__<tool>", so two servers cannot shadow each
// other and the names fit the model APIs (letters, digits, _ and -).
package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/frheg/athena/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is one configured MCP server.
type Server struct {
	Name  string
	URL   string
	Token string
}

// Entry is a tool as core knows it.
type Entry struct {
	Name        string `json:"name"` // full name: server__tool
	Server      string `json:"server"`
	Tool        string `json:"tool"` // the server's own name
	Description string `json:"description"`
	Schema      any    `json:"-"`
}

// Status of a server, for the UI.
type Status struct {
	Name      string    `json:"name"`
	Up        bool      `json:"up"`
	Tools     int       `json:"tools"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Box holds the servers, their sessions and tools.
type Box struct {
	servers []Server
	log     *slog.Logger
	mu      sync.RWMutex
	conns   map[string]*mcp.ClientSession
	tools   map[string]Entry
	status  map[string]Status
	MaxText int // longest tool result passed on, default 30000 characters
	Timeout time.Duration
}

var nameRe = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// New returns a box for the servers. Call Refresh (or Run) to connect.
func New(servers []Server, log *slog.Logger) *Box {
	b := &Box{servers: servers, log: log, conns: map[string]*mcp.ClientSession{}, tools: map[string]Entry{}, status: map[string]Status{},
		MaxText: 30000, Timeout: 90 * time.Second}
	for _, s := range servers {
		b.status[s.Name] = Status{Name: s.Name}
	}
	return b
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.token != "" {
		r.Header.Set("Authorization", "Bearer "+b.token)
	}
	if s := Subject(r.Context()); s != "" {
		r.Header.Set(SubjectHeader, s)
	}
	return b.next.RoundTrip(r)
}

// SubjectHeader names the user a tool call is made for. Servers that keep per-user data (memory) use it; they trust
// it only from an authenticated caller.
const SubjectHeader = "X-Athena-Subject"

type subjectKey struct{}

// WithSubject marks calls made with ctx as being for this user.
func WithSubject(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, subjectKey{}, owner)
}

// Subject returns the user set by WithSubject.
func Subject(ctx context.Context) string {
	s, _ := ctx.Value(subjectKey{}).(string)
	return s
}

func (b *Box) connect(ctx context.Context, s Server) (*mcp.ClientSession, error) {
	b.mu.RLock()
	cs := b.conns[s.Name]
	b.mu.RUnlock()
	if cs != nil {
		return cs, nil
	}
	hc := &http.Client{Timeout: b.Timeout, Transport: bearer{s.Token, http.DefaultTransport}}
	c := mcp.NewClient(&mcp.Implementation{Name: "athena-core", Version: "1"}, nil)
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: s.URL, HTTPClient: hc, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.conns[s.Name] = cs
	b.mu.Unlock()
	return cs, nil
}

func (b *Box) drop(name string) {
	b.mu.Lock()
	if cs := b.conns[name]; cs != nil {
		_ = cs.Close()
	}
	delete(b.conns, name)
	b.mu.Unlock()
}

// Refresh connects to every server and reloads its tools. A server that does not answer keeps no tools.
func (b *Box) Refresh(ctx context.Context) {
	var wg sync.WaitGroup
	for _, s := range b.servers {
		wg.Add(1)
		go func(s Server) {
			defer wg.Done()
			b.refreshOne(ctx, s)
		}(s)
	}
	wg.Wait()
}

func (b *Box) refreshOne(ctx context.Context, s Server) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	list, err := b.list(ctx, s)
	if err != nil {
		b.drop(s.Name) // reconnect next time; the server may have restarted
		if list, err = b.list(ctx, s); err != nil {
			b.mu.Lock()
			for k, e := range b.tools {
				if e.Server == s.Name {
					delete(b.tools, k)
				}
			}
			b.status[s.Name] = Status{Name: s.Name, Error: err.Error(), CheckedAt: time.Now()}
			b.mu.Unlock()
			return
		}
	}
	b.mu.Lock()
	for k, e := range b.tools {
		if e.Server == s.Name {
			delete(b.tools, k)
		}
	}
	for _, t := range list {
		full := s.Name + "__" + nameRe.ReplaceAllString(t.Name, "_")
		b.tools[full] = Entry{Name: full, Server: s.Name, Tool: t.Name, Description: t.Description, Schema: t.InputSchema}
	}
	b.status[s.Name] = Status{Name: s.Name, Up: true, Tools: len(list), CheckedAt: time.Now()}
	b.mu.Unlock()
}

func (b *Box) list(ctx context.Context, s Server) ([]*mcp.Tool, error) {
	cs, err := b.connect(ctx, s)
	if err != nil {
		return nil, err
	}
	var out []*mcp.Tool
	params := &mcp.ListToolsParams{}
	for {
		res, err := cs.ListTools(ctx, params)
		if err != nil {
			return nil, err
		}
		out = append(out, res.Tools...)
		if res.NextCursor == "" {
			return out, nil
		}
		params.Cursor = res.NextCursor
	}
}

// Run refreshes now and then every interval until ctx ends.
func (b *Box) Run(ctx context.Context, every time.Duration) {
	b.Refresh(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.Refresh(ctx)
		}
	}
}

// Tools returns the known tools, sorted by name.
func (b *Box) Tools() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Entry, 0, len(b.tools))
	for _, e := range b.tools {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns one tool.
func (b *Box) Lookup(name string) (Entry, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.tools[name]
	return e, ok
}

// Statuses reports every server.
func (b *Box) Statuses() []Status {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := []Status{}
	for _, s := range b.servers {
		out = append(out, b.status[s.Name])
	}
	return out
}

// ForModel turns entries into model tool definitions.
func ForModel(entries []Entry) []llm.Tool {
	out := make([]llm.Tool, 0, len(entries))
	for _, e := range entries {
		schema := e.Schema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, llm.Tool{Type: "function", Function: llm.ToolFunction{Name: e.Name, Description: e.Description, Parameters: schema}})
	}
	return out
}

// Call runs a tool. It returns the result as text and whether the tool reported an error.
func (b *Box) Call(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	e, ok := b.Lookup(name)
	if !ok {
		return "", true, fmt.Errorf("no tool %q (its server may be down)", name)
	}
	var server Server
	for _, s := range b.servers {
		if s.Name == e.Server {
			server = s
		}
	}
	var argv map[string]any
	if len(args) > 0 && string(args) != "null" {
		if err := json.Unmarshal(args, &argv); err != nil {
			return "", true, fmt.Errorf("the arguments are not a JSON object: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()
	call := func() (*mcp.CallToolResult, error) {
		cs, err := b.connect(ctx, server)
		if err != nil {
			return nil, err
		}
		return cs.CallTool(ctx, &mcp.CallToolParams{Name: e.Tool, Arguments: argv})
	}
	res, err := call()
	if err != nil && ctx.Err() == nil {
		b.drop(server.Name) // one retry on a fresh connection
		res, err = call()
	}
	if err != nil {
		return "", true, err
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(t.Text)
		}
	}
	text := sb.String()
	if text == "" && res.StructuredContent != nil {
		j, _ := json.Marshal(res.StructuredContent)
		text = string(j)
	}
	if r := []rune(text); len(r) > b.MaxText {
		text = string(r[:b.MaxText]) + fmt.Sprintf("\n[cut here: %d of %d characters shown]", b.MaxText, len(r))
	}
	return text, res.IsError, nil
}
