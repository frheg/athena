// notify: sends messages out (mail now). MCP tools at /mcp (send, recipients), REST at /v1 (send, recipients).
// See internal/notify.
//
//	RECIPIENTS     name=address pairs, the only places messages go, for example owner=me@example.org (required)
//	SMTP_HOST      mail server; empty = dry run (messages are logged, not sent)
//	SMTP_PORT      default 587 (STARTTLS); 465 = TLS
//	SMTP_USER, SMTP_PASS, SMTP_FROM
//	NOTIFY_PER_HOUR  most messages per hour, default 20
//	NOTIFY_FOOTER  a line added under every message
//	LISTEN         default :8080
//	MCP_TOKENS     caller=token pairs
package main

import (
	"errors"
	"log/slog"
	"os"

	"github.com/frheg/athena/internal/env"
	"github.com/frheg/athena/internal/mcpx"
	"github.com/frheg/athena/internal/notify"
	"github.com/frheg/athena/internal/probe"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	probe.IfAsked()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "notify")
	s := &notify.Service{
		SMTP: notify.SMTP{Host: env.Str("SMTP_HOST", ""), Port: env.Int("SMTP_PORT", 587), User: env.Str("SMTP_USER", ""),
			Pass: env.Str("SMTP_PASS", ""), From: env.Str("SMTP_FROM", "")},
		Recipients: env.Pairs("RECIPIENTS"),
		PerHour:    env.Int("NOTIFY_PER_HOUR", 20),
		Footer:     env.Str("NOTIFY_FOOTER", "Sent by Athena"),
		Log:        log,
	}
	err := func() error {
		if len(s.Recipients) == 0 {
			return errors.New("RECIPIENTS is not set: name=address pairs")
		}
		if s.SMTP.Host != "" && s.SMTP.From == "" {
			return errors.New("SMTP_FROM is not set")
		}
		if s.SMTP.Host == "" {
			log.Warn("SMTP_HOST is not set: dry run, messages are only logged")
		}
		return mcpx.Run(mcpx.Config{Name: "notify", Version: version, Routes: s.Routes}, func(srv *mcp.Server) { s.Register(srv) })
	}()
	if err != nil {
		log.Error("stopped", "err", err)
		os.Exit(1)
	}
}
