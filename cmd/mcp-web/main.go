// mcp-web: web search and page reading for any MCP client. See internal/web.
//
//	SEARXNG_URL   SearXNG with format json enabled (required)
//	LISTEN        default :8080
//	MCP_TOKENS    caller=token pairs (empty: no check)
//	WEB_CACHE     cache time for fetched pages, default 15m
package main

import (
	"log/slog"
	"os"

	"github.com/frheg/athena/internal/env"
	"github.com/frheg/athena/internal/mcpx"
	"github.com/frheg/athena/internal/netguard"
	"github.com/frheg/athena/internal/probe"
	"github.com/frheg/athena/internal/web"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	probe.IfAsked()
	t := web.New(web.Config{
		SearxURL: env.Str("SEARXNG_URL", ""),
		CacheFor: env.Dur("WEB_CACHE", 0),
		Guard:    netguard.Options{UserAgent: "Athena/" + version + " (+https://github.com/frheg/athena)"},
	})
	err := mcpx.Run(mcpx.Config{Name: "mcp-web", Version: version, Ready: t.Ready}, func(s *mcp.Server) { t.Register(s) })
	if err != nil {
		slog.Error("stopped", "err", err)
		os.Exit(1)
	}
}
