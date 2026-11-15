// Package mcpx is the shared scaffold for Athena's MCP tool servers: an HTTP server with the MCP endpoint at /mcp,
// per-caller tokens, /health, /ready and /metrics, and small helpers for tool results.
package mcpx

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/frheg/athena/internal/env"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type callerKey struct{}

// Caller returns the name of the caller that made the request ("" when tokens are off).
func Caller(ctx context.Context) string {
	s, _ := ctx.Value(callerKey{}).(string)
	return s
}

type subjectKey struct{}

// Subject returns the user the caller says the call is for (header X-Athena-Subject), "" when none.
// Only meaningful when tokens are on, since then the caller is known.
func Subject(ctx context.Context) string {
	s, _ := ctx.Value(subjectKey{}).(string)
	return s
}

// Config of a tool server. Everything comes from the environment:
//
//	LISTEN       address, default :8080
//	MCP_TOKENS   caller=token pairs; a request must carry "Authorization: Bearer <token>". Empty = no check.
type Config struct {
	Name, Version string
	// Ready reports whether the server can do its job (its dependencies answer). nil = always ready.
	Ready func(ctx context.Context) error
	// Routes adds plain HTTP routes next to /mcp (for a service that also has a REST API). They get the same
	// token check, and Caller and Subject work in them.
	Routes func(mux *http.ServeMux)
}

// Run builds the MCP server with register, serves it until SIGTERM, and returns.
func Run(cfg Config, register func(*mcp.Server)) error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", cfg.Name)
	slog.SetDefault(log)
	s := mcp.NewServer(&mcp.Implementation{Name: cfg.Name, Version: cfg.Version}, nil)
	register(s)
	h := Handler(s, cfg, env.Pairs("MCP_TOKENS"), log)
	srv := &http.Server{Addr: env.Str("LISTEN", ":8080"), Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("listening", "addr", srv.Addr, "auth", len(env.Pairs("MCP_TOKENS")) > 0)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Handler is the HTTP handler of a tool server (exported for tests).
func Handler(s *mcp.Server, cfg Config, tokens map[string]string, log *slog.Logger) http.Handler {
	var calls, denied atomic.Int64
	mcpH := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := ""
			if len(tokens) > 0 {
				tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				for name, want := range tokens {
					if subtle.ConstantTimeCompare([]byte(tok), []byte(want)) == 1 {
						caller = name
					}
				}
				if caller == "" {
					denied.Add(1)
					http.Error(w, "unknown caller", http.StatusUnauthorized)
					return
				}
			}
			calls.Add(1)
			ctx := context.WithValue(r.Context(), callerKey{}, caller)
			ctx = context.WithValue(ctx, subjectKey{}, r.Header.Get("X-Athena-Subject"))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", auth(mcpH))
	if cfg.Routes != nil {
		api := http.NewServeMux()
		cfg.Routes(api)
		mux.Handle("/v1/", auth(api))
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if cfg.Ready != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			if err := cfg.Ready(ctx); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "# TYPE mcp_requests_total counter\nmcp_requests_total{server=%q} %d\n", cfg.Name, calls.Load())
		fmt.Fprintf(w, "# TYPE mcp_requests_denied_total counter\nmcp_requests_denied_total{server=%q} %d\n", cfg.Name, denied.Load())
	})
	return mux
}

// Untrusted wraps text from outside sources so a model reads it as quoted data, not as instructions to follow.
func Untrusted(source, text string) string {
	return fmt.Sprintf("<untrusted source=%q>\n%s\n</untrusted>", source, text)
}

// JSON renders v as indented JSON text (for small structured results).
func JSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// Text is a plain text tool result.
func Text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// Clip cuts s to max runes and says so.
func Clip(s string, max int) (string, bool) {
	r := []rune(s)
	if max <= 0 || len(r) <= max {
		return s, false
	}
	return string(r[:max]) + fmt.Sprintf("\n\n[cut here: %d of %d characters shown]", max, len(r)), true
}
