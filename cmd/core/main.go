// core: Athena's turn engine, permission gate, scheduler and HTTP API. Everything is configured by environment:
//
//	DATABASE_URL        Postgres (required)
//	LLM_URL, LLM_MODEL, LLM_KEY                       the model server (OpenAI-compatible, base URL ending in /v1)
//	LLM_FALLBACK_URL, LLM_FALLBACK_MODEL, LLM_FALLBACK_KEY   used when the first does not answer (optional)
//	LLM_TEMPERATURE, LLM_MAX_TOKENS, LLM_WAKE_DEADLINE  (default 0.3, 0 = server default, 3m)
//	MODEL_SWITCH        let a conversation pick its model (default true)
//	MODEL_SWITCH_ENDPOINTS  which endpoints the picker offers: primary, fallback or both (default primary)
//	TOOL_SERVERS        name=url pairs of MCP servers, for example web=http://mcp-web:8080/mcp
//	TOOL_TOKENS         name=token pairs: the token core sends to each server
//	POLICY_FILE         the permission policy (default /etc/athena/policy.yaml); missing = every tool denied
//	MEMORY_URL, MEMORY_TOKEN   the memory service (optional)
//	FILES_URL, FILES_TOKEN     the files service, for attachments (optional; without it the UI cannot attach)
//	TTS_URL, TTS_TOKEN  a speech server for reading answers aloud, and writing speech down for live mode in the browser
//	                    (OpenAI-compatible, base URL ending in /v1; optional)
//	POSE_URL, POSE_TOKEN  the pose service, for the camera in live mode (optional; base URL without /v1)
//	VISION_MODELS       models that can see images, comma-separated (* = all); others get a note instead
//	NOTIFY_URL, NOTIFY_TOKEN, NOTIFY_TO   the notify service for job results (optional; NOTIFY_TO default owner)
//	REMINDERS_URL, REMINDERS_TOKEN        mcp-notes, whose due reminders core delivers every minute (optional)
//	OWNER               the user's name inside Athena, default owner
//	API_TOKEN           when set, every /api request needs "Authorization: Bearer <token>" (the gate adds it)
//	ASSISTANT_NAME      default Athena
//	TIME_ZONE           IANA zone for dates and job schedules, default the container's (TZ) or UTC
//	HISTORY_MESSAGES    messages of history sent to the model, default 40
//	LISTEN              default :8080
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/frheg/athena/internal/api"
	"github.com/frheg/athena/internal/engine"
	"github.com/frheg/athena/internal/env"
	"github.com/frheg/athena/internal/files"
	"github.com/frheg/athena/internal/gate"
	"github.com/frheg/athena/internal/llm"
	"github.com/frheg/athena/internal/memory"
	"github.com/frheg/athena/internal/notes"
	"github.com/frheg/athena/internal/notify"
	"github.com/frheg/athena/internal/pose"
	"github.com/frheg/athena/internal/probe"
	"github.com/frheg/athena/internal/speech"
	"github.com/frheg/athena/internal/store"
	"github.com/frheg/athena/internal/toolbox"
)

var version = "dev"

func main() {
	probe.IfAsked()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "core")
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("stopped", "err", err)
		os.Exit(1)
	}
}

// memoryRecall adapts the memory client to the engine.
type memoryRecall struct{ c *memory.Client }

func (m memoryRecall) Recall(ctx context.Context, owner, query string, k int) ([]engine.Recalled, error) {
	items, err := m.c.Recall(ctx, owner, query, k)
	out := make([]engine.Recalled, 0, len(items))
	for _, it := range items {
		out = append(out, engine.Recalled{ID: it.ID, Text: it.Text, Kind: it.Kind, Score: it.Score})
	}
	return out, err
}

// policyHolder reloads the policy file when it changes; a broken file keeps the last good policy.
type policyHolder struct {
	file string
	cur  atomic.Pointer[gate.Policy]
	mod  time.Time
	log  *slog.Logger
}

func (p *policyHolder) load() error {
	st, err := os.Stat(p.file)
	if err != nil {
		return err
	}
	if !st.ModTime().After(p.mod) && p.cur.Load() != nil {
		return nil
	}
	pol, err := gate.Load(p.file)
	if err != nil {
		return err
	}
	p.cur.Store(pol)
	p.mod = st.ModTime()
	p.log.Info("policy loaded", "file", p.file, "tools", len(pol.Tools), "rules", len(pol.Rules))
	return nil
}

func run(log *slog.Logger) error {
	dsn, err := env.Must("DATABASE_URL")
	if err != nil {
		return err
	}
	llmURL, err := env.Must("LLM_URL")
	if err != nil {
		return err
	}
	zone := time.Local
	if z := env.Str("TIME_ZONE", ""); z != "" {
		if zone, err = time.LoadLocation(z); err != nil {
			return fmt.Errorf("TIME_ZONE: %w", err)
		}
	}

	pol := &policyHolder{file: env.Str("POLICY_FILE", "/etc/athena/policy.yaml"), log: log}
	if err := pol.load(); err != nil {
		log.Warn("no usable policy: every tool is denied until it is fixed", "file", pol.file, "err", err)
		empty, _ := gate.Parse([]byte("{}"))
		pol.cur.Store(empty)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	octx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	st, err := store.Open(octx, dsn, func(m string, a ...any) { log.Warn(m, a...) })
	cancel()
	if err != nil {
		return err
	}
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	temp, _ := strconv.ParseFloat(env.Str("LLM_TEMPERATURE", "0.3"), 64)
	model := &llm.Client{
		Primary:      llm.Endpoint{URL: llmURL, Model: env.Str("LLM_MODEL", ""), Key: env.Str("LLM_KEY", "")},
		Fallback:     llm.Endpoint{URL: env.Str("LLM_FALLBACK_URL", ""), Model: env.Str("LLM_FALLBACK_MODEL", ""), Key: env.Str("LLM_FALLBACK_KEY", "")},
		HTTP:         &http.Client{Timeout: 15 * time.Minute},
		WakeDeadline: env.Dur("LLM_WAKE_DEADLINE", 3*time.Minute),
		Temperature:  temp,
		MaxTokens:    env.Int("LLM_MAX_TOKENS", 0),
	}

	tokens := env.Pairs("TOOL_TOKENS")
	var servers []toolbox.Server
	for name, url := range env.Pairs("TOOL_SERVERS") {
		servers = append(servers, toolbox.Server{Name: name, URL: url, Token: tokens[name]})
	}
	box := toolbox.New(servers, log)
	go box.Run(ctx, time.Minute)

	e := &engine.Engine{Store: st, LLM: model, Tools: box, Policy: pol.cur.Load, Hub: engine.NewHub(30 * time.Minute), Log: log,
		Zone: zone, Name: env.Str("ASSISTANT_NAME", "Athena"), History: env.Int("HISTORY_MESSAGES", 40)}
	if env.Bool("MODEL_SWITCH", true) {
		// Only the listed endpoints are offered for switching. The default leaves the fallback out: on a host where it
		// runs in system RAM next to a partly offloaded primary, using both at once starves the primary of memory.
		named := map[string]llm.Endpoint{"primary": model.Primary, "fallback": model.Fallback}
		cat := &llm.Catalog{HTTP: &http.Client{Timeout: 10 * time.Second}}
		for _, n := range strings.Split(env.Str("MODEL_SWITCH_ENDPOINTS", "primary"), ",") {
			if ep, ok := named[strings.TrimSpace(n)]; ok && ep.URL != "" {
				cat.Endpoints = append(cat.Endpoints, llm.NamedEndpoint{Name: strings.TrimSpace(n), Endpoint: ep})
			}
		}
		if len(cat.Endpoints) > 0 {
			e.Models = cat
		}
	}
	var mem *memory.Client
	if u := env.Str("MEMORY_URL", ""); u != "" {
		mem = &memory.Client{URL: u, Token: env.Str("MEMORY_TOKEN", "")}
		e.Memory = memoryRecall{mem}
	}
	var fc *files.Client
	if u := env.Str("FILES_URL", ""); u != "" {
		// uploads stream from the browser through core, so a slow phone connection needs a long timeout
		fc = &files.Client{URL: u, Token: env.Str("FILES_TOKEN", ""), HTTP: &http.Client{Timeout: 10 * time.Minute}}
		e.Files = fc
	}
	if v := strings.TrimSpace(env.Str("VISION_MODELS", "")); v != "" {
		seeing := map[string]bool{}
		for _, m := range strings.Split(v, ",") {
			seeing[strings.TrimSpace(m)] = true
		}
		e.Vision = func(model string) bool { return seeing["*"] || seeing[model] }
	}
	var tts *speech.Client
	if u := env.Str("TTS_URL", ""); u != "" {
		tts = &speech.Client{URL: u, Token: env.Str("TTS_TOKEN", "")}
	}
	var camera *pose.Client
	if u := env.Str("POSE_URL", ""); u != "" {
		camera = &pose.Client{URL: u, Token: env.Str("POSE_TOKEN", "")}
	}
	var notifier engine.Notifier
	if u := env.Str("NOTIFY_URL", ""); u != "" {
		notifier = &notify.Client{URL: u, Token: env.Str("NOTIFY_TOKEN", ""), To: env.Str("NOTIFY_TO", "owner")}
	}
	sched := engine.NewScheduler(e, notifier)
	if u := env.Str("REMINDERS_URL", ""); u != "" {
		nc := &notes.Client{URL: u, Token: env.Str("REMINDERS_TOKEN", "")}
		sched.Reminders = func(ctx context.Context) ([]engine.DueReminder, error) {
			due, err := nc.Due(ctx)
			out := make([]engine.DueReminder, 0, len(due))
			for _, r := range due {
				out = append(out, engine.DueReminder{Owner: r.Subject, Text: r.Text, DueAt: r.DueAt})
			}
			return out, err
		}
	}
	go sched.Run(ctx)
	go func() { // pick up policy edits without a restart
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				if err := pol.load(); err != nil {
					log.Error("policy reload failed: keeping the last good policy", "err", err)
				}
			}
		}
	}()

	box.Refresh(ctx) // so a resumed turn sees its tools
	e.Recover(ctx)

	ready := func(ctx context.Context) error { return st.DB.Ping(ctx) }
	a := &api.API{E: e, Scheduler: sched, Memory: mem, Files: fc, Speech: tts, Pose: camera, Owner: env.Str("OWNER", "owner"), Token: env.Str("API_TOKEN", ""), Version: version, Ready: ready,
		Info: map[string]any{"model": model.Primary.Model, "fallback_model": model.Fallback.Model, "policy_file": pol.file,
			"memory": mem != nil, "notify": notifier != nil, "files": fc != nil, "speech": tts != nil, "camera": camera != nil, "vision_models": env.Str("VISION_MODELS", "")}}

	mux := http.NewServeMux()
	mux.Handle("/api/", a.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		c, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := ready(c); err != nil {
			http.Error(w, "database: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "# TYPE athena_running_turns gauge\nathena_running_turns %d\n", e.Running())
		for _, s := range box.Statuses() {
			up := 0
			if s.Up {
				up = 1
			}
			fmt.Fprintf(w, "athena_tool_server_up{server=%q} %d\nathena_tool_server_tools{server=%q} %d\n", s.Name, up, s.Name, s.Tools)
		}
		var waiting int
		_ = st.DB.QueryRow(r.Context(), `SELECT count(*) FROM approvals WHERE status = 'waiting'`).Scan(&waiting)
		fmt.Fprintf(w, "# TYPE athena_waiting_approvals gauge\nathena_waiting_approvals %d\n", waiting)
	})
	srv := &http.Server{Addr: env.Str("LISTEN", ":8080"), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("listening", "addr", srv.Addr, "servers", len(servers), "memory", mem != nil, "notify", notifier != nil, "zone", zone.String())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
