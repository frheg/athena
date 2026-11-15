// Package probe lets a binary check its own health: "<binary> health" asks the running server's /health and exits
// 0 or 1. Container images without a shell use it for their health check.
package probe

import (
	"net/http"
	"os"
	"strings"
	"time"
)

// IfAsked runs the probe and exits when the first argument is "health"; otherwise it returns.
func IfAsked() {
	if len(os.Args) < 2 || os.Args[1] != "health" {
		return
	}
	addr := os.Getenv("LISTEN")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	res, err := c.Get("http://" + addr + "/health")
	if err != nil || res.StatusCode != 200 {
		os.Exit(1)
	}
	os.Exit(0)
}
