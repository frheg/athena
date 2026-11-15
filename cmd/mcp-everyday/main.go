// mcp-everyday: weather, places, public transport, exchange rates, time, calculator, parcels. See internal/everyday.
//
//	ENTUR_CLIENT_NAME   identifies the app to Entur, "<company>-<app>" (default athena-assistant)
//	BRING_API_UID       Mybring login; with BRING_API_KEY enables parcel tracking (optional)
//	BRING_API_KEY
//	LISTEN              default :8080
//	MCP_TOKENS          caller=token pairs (empty: no check)
package main

import (
	"log/slog"
	"os"

	"github.com/frheg/athena/internal/env"
	"github.com/frheg/athena/internal/everyday"
	"github.com/frheg/athena/internal/mcpx"
	"github.com/frheg/athena/internal/probe"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	probe.IfAsked()
	t := everyday.New(everyday.Config{
		UserAgent:   "Athena/" + version + " (+https://github.com/frheg/athena)",
		EnturClient: env.Str("ENTUR_CLIENT_NAME", ""),
		BringUID:    env.Str("BRING_API_UID", ""),
		BringKey:    env.Str("BRING_API_KEY", ""),
	})
	if err := mcpx.Run(mcpx.Config{Name: "mcp-everyday", Version: version}, func(s *mcp.Server) { t.Register(s) }); err != nil {
		slog.Error("stopped", "err", err)
		os.Exit(1)
	}
}
