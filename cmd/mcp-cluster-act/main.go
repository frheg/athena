// mcp-cluster-act: the listed cluster actions, nothing else. See internal/cluster.
// Give it its own service account that can only list and delete pods in the namespaces of its restart targets.
//
//	ACTIONS_FILE   the actions (required), see policy/cluster-actions.example.yaml
//	KUBE_API_URL, KUBE_TOKEN   outside a cluster; inside, the service account is used
//	GPUCTL_URL     the GPU arbiter (optional; needed for gpu_acquire and gpu_release)
//	LISTEN, MCP_TOKENS
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/frheg/athena/internal/cluster"
	"github.com/frheg/athena/internal/env"
	"github.com/frheg/athena/internal/mcpx"
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
	file, err := env.Must("ACTIONS_FILE")
	if err != nil {
		return err
	}
	acts, err := cluster.LoadActions(file)
	if err != nil {
		return err
	}
	k, err := cluster.NewKube(env.Str("KUBE_API_URL", ""), env.Str("KUBE_TOKEN", ""))
	if err != nil {
		return err
	}
	a := &cluster.Actor{Kube: k, Actions: acts, GPUURL: env.Str("GPUCTL_URL", ""), HTTP: &http.Client{Timeout: 10 * time.Minute}}
	ready := func(ctx context.Context) error { return k.Get(ctx, "/version", nil, nil) }
	return mcpx.Run(mcpx.Config{Name: "mcp-cluster-act", Version: version, Ready: ready}, func(s *mcp.Server) { a.Register(s) })
}
