package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Model is one model an endpoint serves.
type Model struct {
	ID          string `json:"id"`
	Endpoint    string `json:"endpoint"` // the name of the endpoint that serves it: primary, fallback
	Description string `json:"description,omitempty"`
	Loaded      bool   `json:"loaded"` // known to be in memory now (llama-swap reports it); others may take a while to start
	Default     bool   `json:"default"`
	Vision      bool   `json:"vision,omitempty"` // it can see images (set by core from VISION_MODELS)
}

// NamedEndpoint is an endpoint the catalog asks for its models.
type NamedEndpoint struct {
	Name string
	Endpoint
}

// Catalog lists the models the configured endpoints serve (GET /v1/models), so a conversation can pick one.
// Lists are cached for a short while; an endpoint that does not answer keeps its last list.
type Catalog struct {
	Endpoints []NamedEndpoint
	HTTP      *http.Client
	TTL       time.Duration // default 30 s

	mu     sync.Mutex
	lists  map[string][]Model
	errs   map[string]string
	loaded time.Time
}

func (c *Catalog) fetch(ctx context.Context, ne NamedEndpoint) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(ne.URL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if ne.Key != "" {
		req.Header.Set("Authorization", "Bearer "+ne.Key)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
			Status      *struct {
				Value string `json:"value"`
			} `json:"status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&out); err != nil {
		return nil, err
	}
	models := []Model{}
	for _, d := range out.Data {
		m := Model{ID: d.ID, Endpoint: ne.Name, Description: d.Description}
		if d.Status != nil {
			m.Loaded = d.Status.Value == "ready" || d.Status.Value == "loaded"
		}
		models = append(models, m)
	}
	return models, nil
}

// Models returns every endpoint's models, primary first, and the errors of endpoints that did not answer.
func (c *Catalog) Models(ctx context.Context) ([]Model, map[string]string) {
	c.mu.Lock()
	ttl := c.TTL
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	fresh := c.lists != nil && time.Since(c.loaded) < ttl
	c.mu.Unlock()
	if !fresh {
		lists, errs := map[string][]Model{}, map[string]string{}
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, ne := range c.Endpoints {
			if ne.URL == "" {
				continue
			}
			wg.Add(1)
			go func(ne NamedEndpoint) {
				defer wg.Done()
				fctx, cancel := context.WithTimeout(ctx, 8*time.Second)
				defer cancel()
				m, err := c.fetch(fctx, ne)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs[ne.Name] = err.Error()
					return
				}
				lists[ne.Name] = m
			}(ne)
		}
		wg.Wait()
		c.mu.Lock()
		if c.lists == nil {
			c.lists = map[string][]Model{}
		}
		for k, v := range lists {
			c.lists[k] = v
		}
		c.errs = errs
		c.loaded = time.Now()
		c.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []Model{}
	seen := map[string]bool{}
	for _, ne := range c.Endpoints {
		list := append([]Model(nil), c.lists[ne.Name]...)
		sort.SliceStable(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		for _, m := range list {
			if !seen[m.ID] { // a model served by both is used through the first
				seen[m.ID] = true
				m.Default = ne.Name == c.Endpoints[0].Name && m.ID == ne.Model // the first endpoint's model is the default
				out = append(out, m)
			}
		}
	}
	errs := map[string]string{}
	for k, v := range c.errs {
		errs[k] = v
	}
	return out, errs
}

// Resolve returns the endpoint that serves model id, with the model set.
func (c *Catalog) Resolve(ctx context.Context, id string) (Endpoint, bool) {
	models, _ := c.Models(ctx)
	for _, m := range models {
		if m.ID != id {
			continue
		}
		for _, ne := range c.Endpoints {
			if ne.Name == m.Endpoint {
				ep := ne.Endpoint
				ep.Model = id
				return ep, true
			}
		}
	}
	return Endpoint{}, false
}

// With returns a copy of the client that calls ep first (the fallback stays as it was, unless it is the same
// server, where it would only repeat the failure).
func (c *Client) With(ep Endpoint) *Client {
	cl := *c
	cl.Primary = ep
	if strings.TrimRight(cl.Fallback.URL, "/") == strings.TrimRight(ep.URL, "/") && cl.Fallback.Model == ep.Model {
		cl.Fallback = Endpoint{}
	}
	return &cl
}
