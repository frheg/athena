// mcp-notes: notes and reminders for any MCP client. MCP tools at /mcp, due reminders at POST /v1/reminders/due.
// See internal/notes.
//
//	DATABASE_URL   Postgres (required)
//	TIME_ZONE      how local times like "2026-11-16 08:00" are read, default UTC
//	LISTEN, MCP_TOKENS
package main

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"time"
	_ "time/tzdata"

	"github.com/frheg/athena/internal/env"
	"github.com/frheg/athena/internal/mcpx"
	"github.com/frheg/athena/internal/notes"
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
	zone, err := time.LoadLocation(env.Str("TIME_ZONE", "UTC"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := pg.Open(ctx, dsn, 4, func(m string, a ...any) { slog.Warn(m, a...) })
	if err != nil {
		return err
	}
	sub, _ := fs.Sub(notes.Migrations, "migrations")
	if err := pg.Migrate(ctx, db, sub, "notes_migrations"); err != nil {
		return err
	}
	s := &notes.Service{DB: db, Zone: zone}
	return mcpx.Run(mcpx.Config{Name: "mcp-notes", Version: version, Ready: func(ctx context.Context) error { return db.Ping(ctx) }, Routes: s.Routes},
		func(srv *mcp.Server) { s.Register(srv) })
}
