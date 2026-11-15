package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Embedder turns text into vectors through an OpenAI-compatible /v1/embeddings endpoint (llama.cpp, vLLM, TEI, ...).
type Embedder struct {
	URL   string // base URL ending in /v1
	Model string
	Key   string
	HTTP  *http.Client
}

// Embed returns one vector per text.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": e.Model, "input": texts})
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := post(ctx, e.HTTP, strings.TrimRight(e.URL, "/")+"/embeddings", body, map[string]string{"Authorization": bearer(e.Key)}, &out); err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embed: asked for %d vectors, got %d", len(texts), len(out.Data))
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, fmt.Errorf("embed: bad index %d", d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	return vecs, nil
}

func bearer(k string) string {
	if k == "" {
		return ""
	}
	return "Bearer " + k
}

// Qdrant is a minimal client for one collection.
type Qdrant struct {
	URL        string
	Key        string
	Collection string
	HTTP       *http.Client

	mu    sync.Mutex
	ready bool
}

func (q *Qdrant) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(q.URL, "/")+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if q.Key != "" {
		req.Header.Set("api-key", q.Key)
	}
	res, err := q.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		return &httpErr{res.StatusCode, strings.TrimSpace(string(b))}
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

type httpErr struct {
	code int
	body string
}

func (e *httpErr) Error() string {
	if len(e.body) > 300 {
		e.body = e.body[:300]
	}
	return fmt.Sprintf("HTTP %d: %s", e.code, e.body)
}

// ensure creates the collection with this vector size when it does not exist.
func (q *Qdrant) ensure(ctx context.Context, size int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ready {
		return nil
	}
	c := "/collections/" + q.Collection
	var info struct {
		Result struct {
			Config struct {
				Params struct {
					Vectors struct {
						Size int `json:"size"`
					} `json:"vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	err := q.do(ctx, http.MethodGet, c, nil, &info)
	if he, ok := err.(*httpErr); ok && he.code == http.StatusNotFound {
		if err := q.do(ctx, http.MethodPut, c, map[string]any{"vectors": map[string]any{"size": size, "distance": "Cosine"}}, nil); err != nil {
			return fmt.Errorf("qdrant: create collection: %w", err)
		}
		for _, f := range []string{"tenant", "subject"} {
			_ = q.do(ctx, http.MethodPut, c+"/index?wait=true", map[string]any{"field_name": f, "field_schema": "keyword"}, nil)
		}
	} else if err != nil {
		return fmt.Errorf("qdrant: %w", err)
	} else if s := info.Result.Config.Params.Vectors.Size; s != 0 && s != size {
		return fmt.Errorf("qdrant: collection %s has vectors of size %d, the embedder gives %d (another model?): use a new collection and reindex", q.Collection, s, size)
	}
	q.ready = true
	return nil
}

func (q *Qdrant) upsert(ctx context.Context, id string, vec []float32, payload map[string]string) error {
	if err := q.ensure(ctx, len(vec)); err != nil {
		return err
	}
	return q.do(ctx, http.MethodPut, "/collections/"+q.Collection+"/points?wait=true",
		map[string]any{"points": []map[string]any{{"id": id, "vector": vec, "payload": payload}}}, nil)
}

func (q *Qdrant) remove(ctx context.Context, id string) error {
	err := q.do(ctx, http.MethodPost, "/collections/"+q.Collection+"/points/delete?wait=true", map[string]any{"points": []string{id}}, nil)
	if he, ok := err.(*httpErr); ok && he.code == http.StatusNotFound {
		return nil
	}
	return err
}

type hit struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

func (q *Qdrant) search(ctx context.Context, vec []float32, tenant, subject string, k int, min float64) ([]hit, error) {
	if err := q.ensure(ctx, len(vec)); err != nil {
		return nil, err
	}
	var out struct {
		Result []hit `json:"result"`
	}
	err := q.do(ctx, http.MethodPost, "/collections/"+q.Collection+"/points/search", map[string]any{
		"vector": vec, "limit": k, "score_threshold": min, "with_payload": false,
		"filter": map[string]any{"must": []map[string]any{
			{"key": "tenant", "match": map[string]string{"value": tenant}},
			{"key": "subject", "match": map[string]string{"value": subject}},
		}},
	}, &out)
	return out.Result, err
}

func (q *Qdrant) ping(ctx context.Context) error {
	return q.do(ctx, http.MethodGet, "/collections", nil, nil)
}

func post(ctx context.Context, c *http.Client, url string, body []byte, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second}
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if res.StatusCode >= 300 {
		return &httpErr{res.StatusCode, strings.TrimSpace(string(b))}
	}
	return json.Unmarshal(b, out)
}
