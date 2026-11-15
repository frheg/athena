package cluster

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// Actions is the file of what the act server may do. Nothing outside it exists.
//
//	restart:              # delete the pods so their controller makes new ones (safe with GitOps: nothing in the
//	  - name: makeup      # manifests changes, so Flux or Argo have nothing to revert)
//	    namespace: makeup
//	    selector: app=makeup
//	gpu:
//	  consumers: [llm]    # GPU consumers that may be acquired and released through gpuctl
//	  max_ttl: 2h
type Actions struct {
	Restart []RestartTarget `yaml:"restart"`
	GPU     struct {
		Consumers []string `yaml:"consumers"`
		MaxTTL    string   `yaml:"max_ttl"`
	} `yaml:"gpu"`
}

// RestartTarget is one thing that may be restarted.
type RestartTarget struct {
	Name      string `yaml:"name" json:"name"`
	Namespace string `yaml:"namespace" json:"namespace"`
	Selector  string `yaml:"selector" json:"selector"`
	Note      string `yaml:"note" json:"note,omitempty"`
}

var selectorRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+=[A-Za-z0-9._-]+(,[A-Za-z0-9._/-]+=[A-Za-z0-9._-]+)*$`)

// LoadActions reads and checks the actions file.
func LoadActions(file string) (*Actions, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var a Actions
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("actions: %w", err)
	}
	seen := map[string]bool{}
	for _, t := range a.Restart {
		if t.Name == "" || t.Namespace == "" || !selectorRe.MatchString(t.Selector) {
			return nil, fmt.Errorf("actions: restart %q needs a name, a namespace and a selector like app=x", t.Name)
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("actions: restart %q is listed twice", t.Name)
		}
		seen[t.Name] = true
	}
	if a.GPU.MaxTTL == "" {
		a.GPU.MaxTTL = "2h"
	}
	if _, err := time.ParseDuration(a.GPU.MaxTTL); err != nil {
		return nil, fmt.Errorf("actions: gpu.max_ttl: %w", err)
	}
	return &a, nil
}

// Actor is the act server.
type Actor struct {
	Kube    *Kube
	Actions *Actions
	GPUURL  string
	HTTP    *http.Client

	mu   sync.Mutex
	last map[string]time.Time
}

const restartGap = 2 * time.Minute

// RestartIn names a restart target.
type RestartIn struct {
	Name string `json:"name" jsonschema:"one of the names from the actions tool"`
}

// Restarted is the restart result.
type Restarted struct {
	Name    string   `json:"name"`
	Deleted []string `json:"deleted_pods"`
	Note    string   `json:"note"`
}

// Restart deletes the pods of a listed target.
func (a *Actor) Restart(ctx context.Context, name string) (Restarted, error) {
	var t *RestartTarget
	for i := range a.Actions.Restart {
		if a.Actions.Restart[i].Name == name {
			t = &a.Actions.Restart[i]
		}
	}
	if t == nil {
		return Restarted{}, fmt.Errorf("%q is not a listed restart target", name)
	}
	a.mu.Lock()
	if a.last == nil {
		a.last = map[string]time.Time{}
	}
	if since := time.Since(a.last[name]); since < restartGap {
		a.mu.Unlock()
		return Restarted{}, fmt.Errorf("%s was restarted %s ago; wait %s between restarts", name, since.Round(time.Second), restartGap)
	}
	a.last[name] = time.Now()
	a.mu.Unlock()
	var list struct {
		Items []struct {
			Metadata meta `json:"metadata"`
		} `json:"items"`
	}
	base := "/api/v1/namespaces/" + url.PathEscape(t.Namespace) + "/pods"
	if err := a.Kube.Get(ctx, base, url.Values{"labelSelector": {t.Selector}}, &list); err != nil {
		return Restarted{}, err
	}
	if len(list.Items) == 0 {
		return Restarted{}, fmt.Errorf("no pods match %s in %s", t.Selector, t.Namespace)
	}
	out := Restarted{Name: name, Note: "The pods were deleted; their controller starts new ones. Check with the cluster pods tool in a minute."}
	for _, p := range list.Items {
		if err := a.Kube.Delete(ctx, base+"/"+url.PathEscape(p.Metadata.Name)); err != nil {
			return out, fmt.Errorf("deleting %s: %w", p.Metadata.Name, err)
		}
		out.Deleted = append(out.Deleted, p.Metadata.Name)
	}
	return out, nil
}

// GPUIn names a GPU consumer.
type GPUIn struct {
	Consumer string `json:"consumer" jsonschema:"a GPU consumer from the actions tool"`
	TTL      string `json:"ttl,omitempty" jsonschema:"how long to hold it, for example 30m (acquire only)"`
}

func (a *Actor) gpu(ctx context.Context, op string, in GPUIn) (map[string]any, error) {
	if a.GPUURL == "" {
		return nil, fmt.Errorf("no GPU arbiter is configured (GPUCTL_URL)")
	}
	ok := false
	for _, c := range a.Actions.GPU.Consumers {
		ok = ok || c == in.Consumer
	}
	if !ok {
		return nil, fmt.Errorf("%q is not a listed GPU consumer (listed: %s)", in.Consumer, strings.Join(a.Actions.GPU.Consumers, ", "))
	}
	body := map[string]string{"name": in.Consumer}
	if op == "acquire" {
		ttl := in.TTL
		if ttl == "" {
			ttl = "30m"
		}
		d, err := time.ParseDuration(ttl)
		maxTTL, _ := time.ParseDuration(a.Actions.GPU.MaxTTL)
		if err != nil || d <= 0 || d > maxTTL {
			return nil, fmt.Errorf("ttl must be a duration up to %s", a.Actions.GPU.MaxTTL)
		}
		body["ttl"] = ttl
	}
	var out map[string]any
	// an acquire waits until the new holder is ready; that can take minutes
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	err := HTTPJSON(ctx, a.HTTP, http.MethodPost, strings.TrimRight(a.GPUURL, "/")+"/"+op, body, &out)
	return out, err
}

// Register adds the act tools.
func (a *Actor) Register(s *mcp.Server) {
	type list struct {
		Restart      []RestartTarget `json:"restart"`
		GPUConsumers []string        `json:"gpu_consumers"`
		GPUMaxTTL    string          `json:"gpu_max_ttl"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "actions", Description: "List what this server can do: the restart targets and the GPU consumers. Nothing else is possible."},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, list, error) {
			return nil, list{Restart: a.Actions.Restart, GPUConsumers: a.Actions.GPU.Consumers, GPUMaxTTL: a.Actions.GPU.MaxTTL}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "restart", Description: "Restart one listed target by deleting its pods (their controller makes new ones)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in RestartIn) (*mcp.CallToolResult, Restarted, error) {
			r, err := a.Restart(ctx, in.Name)
			return nil, r, err
		})
	if a.GPUURL != "" && len(a.Actions.GPU.Consumers) > 0 {
		mcp.AddTool(s, &mcp.Tool{Name: "gpu_acquire", Description: "Give the GPU to a listed consumer for a while (it waits until that consumer is ready)."},
			func(ctx context.Context, _ *mcp.CallToolRequest, in GPUIn) (*mcp.CallToolResult, map[string]any, error) {
				r, err := a.gpu(ctx, "acquire", in)
				return nil, r, err
			})
		mcp.AddTool(s, &mcp.Tool{Name: "gpu_release", Description: "End a consumer's GPU lease early, so the GPU returns to the default."},
			func(ctx context.Context, _ *mcp.CallToolRequest, in GPUIn) (*mcp.CallToolResult, map[string]any, error) {
				r, err := a.gpu(ctx, "release", in)
				return nil, r, err
			})
	}
}
