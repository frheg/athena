package files

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client is the REST client core uses: uploads from the UI, and the text and previews it puts in front of the model.
// Files never change, so text and previews are cached (up to CacheBytes).
type Client struct {
	URL        string // the service, without /v1
	Token      string
	HTTP       *http.Client
	CacheBytes int // default 64 MB

	mu    sync.Mutex
	cache map[string][]byte
	order []string
	size  int
}

func (c *Client) hc() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

// Request builds a request to the REST API for subject (path is under /v1, like /files/<id>).
func (c *Client) Request(ctx context.Context, method, path string, query url.Values, body io.Reader, subject string) (*http.Request, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("subject", subject)
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+"/v1"+path+"?"+query.Encode(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Athena-Subject", subject)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

// Do sends a request (see Request); the caller closes the body.
func (c *Client) Do(req *http.Request) (*http.Response, error) { return c.hc().Do(req) }

func (c *Client) get(ctx context.Context, path string, query url.Values, subject string) ([]byte, error) {
	req, err := c.Request(ctx, http.MethodGet, path, query, nil, subject)
	if err != nil {
		return nil, err
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if res.StatusCode == http.StatusNotFound {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("files: %s", strings.TrimSpace(e.Error+" "+http.StatusText(res.StatusCode)))
	}
	return b, nil
}

func (c *Client) cached(key string, load func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	if b, ok := c.cache[key]; ok {
		c.mu.Unlock()
		return b, nil
	}
	c.mu.Unlock()
	b, err := load()
	if err != nil {
		return nil, err
	}
	limit := c.CacheBytes
	if limit <= 0 {
		limit = 64 << 20
	}
	if len(b) > limit/4 {
		return b, nil // too big to keep
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		c.cache = map[string][]byte{}
	}
	if _, ok := c.cache[key]; !ok {
		c.cache[key] = b
		c.order = append(c.order, key)
		c.size += len(b)
		for c.size > limit && len(c.order) > 0 {
			old := c.order[0]
			c.order = c.order[1:]
			c.size -= len(c.cache[old])
			delete(c.cache, old)
		}
	}
	return b, nil
}

// Get returns what is known about a file.
func (c *Client) Get(ctx context.Context, subject, id string) (File, error) {
	var f File
	b, err := c.get(ctx, "/files/"+url.PathEscape(id), nil, subject)
	if err == nil {
		err = json.Unmarshal(b, &f)
	}
	return f, err
}

// Text returns a file's readable text.
func (c *Client) Text(ctx context.Context, subject, id string) (string, error) {
	b, err := c.cached("text:"+subject+":"+id, func() ([]byte, error) {
		b, err := c.get(ctx, "/files/"+url.PathEscape(id)+"/text", nil, subject)
		if err != nil {
			return nil, err
		}
		var out struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, err
		}
		return []byte(out.Text), nil
	})
	return string(b), err
}

// Preview returns an image's JPEG preview.
func (c *Client) Preview(ctx context.Context, subject, id string) ([]byte, error) {
	return c.cached("preview:"+subject+":"+id, func() ([]byte, error) {
		return c.get(ctx, "/files/"+url.PathEscape(id)+"/content", url.Values{"preview": {"1"}}, subject)
	})
}

// Upload stores a file (used by tests and other services; core streams uploads through Request).
func (c *Client) Upload(ctx context.Context, subject, name string, data []byte) (File, error) {
	req, err := c.Request(ctx, http.MethodPost, "/files", url.Values{"name": {name}}, bytes.NewReader(data), subject)
	if err != nil {
		return File{}, err
	}
	res, err := c.Do(req)
	if err != nil {
		return File{}, err
	}
	defer res.Body.Close()
	var f File
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		return f, fmt.Errorf("files: %s", e.Error)
	}
	return f, json.Unmarshal(b, &f)
}
