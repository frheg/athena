// Package cluster is two MCP servers for a Kubernetes cluster: a read-only one (pods, deployments, events, logs,
// nodes, GPU state, metrics) and one for a short list of actions named in a file. Each runs with its own service
// account, so what it may do is set by the cluster's RBAC first; the code adds a namespace allow-list on top.
package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Kube is a minimal Kubernetes API client (no client-go: the few calls needed are plain REST).
type Kube struct {
	URL   string
	Token string
	HTTP  *http.Client
}

const saDir = "/var/run/secrets/kubernetes.io/serviceaccount/"

// NewKube connects in-cluster (service account), or to KUBE_API_URL with KUBE_TOKEN when given.
func NewKube(apiURL, token string) (*Kube, error) {
	if apiURL != "" {
		return &Kube{URL: strings.TrimRight(apiURL, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}, nil
	}
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" {
		return nil, errors.New("not in a cluster and KUBE_API_URL is not set")
	}
	tok, err := os.ReadFile(saDir + "token")
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(saDir + "ca.crt")
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return &Kube{URL: "https://" + host + ":" + port, Token: strings.TrimSpace(string(tok)), HTTP: &http.Client{Timeout: 30 * time.Second, Transport: tr}}, nil
}

// APIError is a non-2xx answer from the API server.
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("kubernetes: %d %s", e.Code, e.Message) }

func (k *Kube) do(ctx context.Context, method, path string, q url.Values, out any) error {
	u := k.URL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	if k.Token != "" {
		req.Header.Set("Authorization", "Bearer "+k.Token)
	}
	req.Header.Set("Accept", "application/json")
	res, err := k.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		var st struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &st)
		if st.Message == "" {
			st.Message = strings.TrimSpace(string(b))
		}
		return &APIError{res.StatusCode, st.Message}
	}
	if s, ok := out.(*string); ok {
		*s = string(b)
		return nil
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Get reads a JSON object.
func (k *Kube) Get(ctx context.Context, path string, q url.Values, out any) error {
	return k.do(ctx, http.MethodGet, path, q, out)
}

// Delete removes an object.
func (k *Kube) Delete(ctx context.Context, path string) error {
	return k.do(ctx, http.MethodDelete, path, nil, nil)
}

// Namespaces is an allow-list; "*" allows every namespace (the service account's RBAC still applies).
type Namespaces []string

// Allows reports whether ns may be used.
func (n Namespaces) Allows(ns string) bool {
	for _, x := range n {
		if x == "*" || x == ns {
			return true
		}
	}
	return false
}

func (n Namespaces) check(ns string) error {
	if ns == "" {
		return errors.New("namespace is required")
	}
	if !n.Allows(ns) {
		return fmt.Errorf("namespace %q is not one this server may see (allowed: %s)", ns, strings.Join(n, ", "))
	}
	return nil
}

// ParseNamespaces reads "a,b,c".
func ParseNamespaces(s string) Namespaces {
	var out Namespaces
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// HTTPJSON does a JSON request to a plain HTTP service (gpuctl, VictoriaMetrics).
func HTTPJSON(ctx context.Context, c *http.Client, method, u string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("%s: HTTP %d: %s", u, res.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
