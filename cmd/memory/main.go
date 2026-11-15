// memory: a general memory service for assistants. MCP tools at /mcp (search, remember, forget) and a REST API at
// /v1 (recall, items). See internal/memory.
//
//	DATABASE_URL        Postgres (required)
//	QDRANT_URL          Qdrant; empty = text search only
//	QDRANT_KEY          its API key, if any
//	QDRANT_COLLECTION   default memory
//	EMBED_URL           OpenAI-compatible base URL ending in /v1; empty = text search only
//	EMBED_MODEL         model name sent to the embedder
//	EMBED_KEY           its API key, if any
//	RECALL_MIN_SCORE    vector search threshold, default 0.45
//	LISTEN              default :8080
//	MCP_TOKENS          caller=token pairs; the caller is the tenant
package main

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/frheg/athena/internal/env"
	"github.com/frheg/athena/internal/mcpx"
	"github.com/frheg/athena/internal/memory"
	"github.com/frheg/athena/internal/pg"
	"github.com/frheg/athena/internal/probe"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	probe.IfAsked()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "memory")
	if err := run(log); err != nil {
		log.Error("stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	dsn, err := env.Must("DATABASE_URL")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	db, err := pg.Open(ctx, dsn, 8, func(m string, a ...any) { log.Warn(m, a...) })
	cancel()
	if err != nil {
		return err
	}
	sub, _ := fs.Sub(memory.Migrations, "migrations")
	if err := pg.Migrate(context.Background(), db, sub, "memory_migrations"); err != nil {
		return err
	}
	min, _ := strconv.ParseFloat(env.Str("RECALL_MIN_SCORE", "0"), 64)
	s := &memory.Service{DB: db, Log: log, MinScore: min}
	if u := env.Str("EMBED_URL", ""); u != "" {
		s.Embed = &memory.Embedder{URL: u, Model: env.Str("EMBED_MODEL", ""), Key: env.Str("EMBED_KEY", ""), HTTP: &http.Client{Timeout: 3 * time.Minute}}
	}
	if u := env.Str("QDRANT_URL", ""); u != "" {
		s.Vectors = &memory.Qdrant{URL: u, Key: env.Str("QDRANT_KEY", ""), Collection: env.Str("QDRANT_COLLECTION", "memory"), HTTP: &http.Client{Timeout: 30 * time.Second}}
	}
	if s.Embed == nil || s.Vectors == nil {
		log.Info("vectors are off (needs both EMBED_URL and QDRANT_URL): text search only")
		s.Embed, s.Vectors = nil, nil
	}
	go func() { // vectors that could not be made at write time (embedder asleep, Qdrant down) are made later
		for {
			if n, err := s.Backfill(context.Background()); err != nil {
				log.Warn("backfill", "err", err)
			} else if n > 0 {
				log.Info("backfill", "embedded", n)
			}
			time.Sleep(time.Minute)
		}
	}()
	return mcpx.Run(mcpx.Config{Name: "memory", Version: version, Ready: s.Ready, Routes: s.Routes}, func(srv *mcp.Server) { s.Register(srv) })
}
