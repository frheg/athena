// files: a general file store for assistants. MCP tools at /mcp (list, read, find) and a REST API at /v1 (upload,
// list, content, text, delete). See internal/files.
//
//	DATABASE_URL     Postgres (required)
//	MAX_FILE_MB      largest file, default 20
//	QUOTA_MB         space per user, default 2048
//	LISTEN, MCP_TOKENS
package main

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/frheg/athena/internal/env"
	"github.com/frheg/athena/internal/files"
	"github.com/frheg/athena/internal/mcpx"
	"github.com/frheg/athena/internal/pg"
	"github.com/frheg/athena/internal/probe"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	probe.IfAsked()
	if err := run(); err != nil {
		slog.Error("stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	dsn, err := env.Must("DATABASE_URL")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := pg.Open(ctx, dsn, 4, func(m string, a ...any) { slog.Warn(m, a...) })
	if err != nil {
		return err
	}
	sub, _ := fs.Sub(files.Migrations, "migrations")
	if err := pg.Migrate(ctx, db, sub, "files_migrations"); err != nil {
		return err
	}
	s := &files.Service{DB: db, MaxBytes: int64(env.Int("MAX_FILE_MB", 20)) << 20, Quota: int64(env.Int("QUOTA_MB", 2048)) << 20}
	return mcpx.Run(mcpx.Config{Name: "files", Version: version, Ready: func(ctx context.Context) error { return db.Ping(ctx) }, Routes: s.Routes},
		func(srv *mcp.Server) { s.Register(srv) })
}
