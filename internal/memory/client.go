package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is the REST client other services use (core: recall before a turn, and the UI's memory view).
type Client struct {
	URL   string // the service, without /v1
	Token string
	HTTP  *http.Client
}

// Do sends a request to the REST API for subject and decodes the answer into out. It returns the HTTP status.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, subject string, out any) (int, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("subject", subject)
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+"/v1"+path+"?"+query.Encode(), r)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Athena-Subject", subject)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(b))
		}
		return res.StatusCode, fmt.Errorf("memory: %s", e.Error)
	}
	if out != nil {
		return res.StatusCode, json.Unmarshal(b, out)
	}
	return res.StatusCode, nil
}

// Recall finds the k items closest to query.
func (c *Client) Recall(ctx context.Context, subject, query string, k int) ([]Item, error) {
	var out struct {
		Items []Item `json:"items"`
	}
	_, err := c.Do(ctx, http.MethodPost, "/recall", nil, map[string]any{"subject": subject, "query": query, "k": k}, subject, &out)
	return out.Items, err
}
