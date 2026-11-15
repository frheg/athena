// mcp-sources: knowledge sources through their own APIs (Wikipedia, arXiv, GitHub). See internal/sources.
//
//	GITHUB_TOKEN  optional read-only token for public data (raises the limit from 60 to 5000 requests an hour)
//	LISTEN        default :8080
//	MCP_TOKENS    caller=token pairs (empty: no check)
package main

import (
	"log/slog"
	"os"

	"github.com/frheg/athena/internal/env"
	"github.com/frheg/athena/internal/mcpx"
	"github.com/frheg/athena/internal/probe"
	"github.com/frheg/athena/internal/sources"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	probe.IfAsked()
	t := sources.New(sources.Config{GitHubToken: env.Str("GITHUB_TOKEN", ""), UserAgent: "Athena/" + version + " (+https://github.com/frheg/athena)"})
	if err := mcpx.Run(mcpx.Config{Name: "mcp-sources", Version: version}, func(s *mcp.Server) { t.Register(s) }); err != nil {
		slog.Error("stopped", "err", err)
		os.Exit(1)
	}
}
