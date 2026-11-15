// mcp-cluster-read: read-only view of a Kubernetes cluster for any MCP client. See internal/cluster.
// Give it a service account that can only get and list (pods, pods/log, deployments, events, nodes).
//
//	NAMESPACES     comma-separated namespaces it may look at ("*" = all that RBAC allows) (required)
//	KUBE_API_URL, KUBE_TOKEN   outside a cluster; inside, the service account is used
//	GPUCTL_URL     the GPU arbiter (optional)
//	METRICS_URL    a Prometheus-compatible query API, for example VictoriaMetrics (optional)
//	LISTEN, MCP_TOKENS
package main

import (
	"context"
	"errors"
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
	ns := cluster.ParseNamespaces(env.Str("NAMESPACES", ""))
	if len(ns) == 0 {
		return errors.New("NAMESPACES is not set")
	}
	k, err := cluster.NewKube(env.Str("KUBE_API_URL", ""), env.Str("KUBE_TOKEN", ""))
	if err != nil {
		return err
	}
	r := &cluster.Reader{Kube: k, Namespaces: ns, GPUURL: env.Str("GPUCTL_URL", ""), MetricsURL: env.Str("METRICS_URL", ""),
		HTTP: &http.Client{Timeout: 20 * time.Second}}
	ready := func(ctx context.Context) error { return k.Get(ctx, "/version", nil, nil) }
	return mcpx.Run(mcpx.Config{Name: "mcp-cluster-read", Version: version, Ready: ready}, func(s *mcp.Server) { r.Register(s) })
}
