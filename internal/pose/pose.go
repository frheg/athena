// Package pose is a client for the pose service (pose/server.py): a camera picture in, the person's pose, hands and
// gestures out.
package pose

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the pose service.
type Client struct {
	URL   string // base URL, without /v1
	Token string
	HTTP  *http.Client
}

// Analyse sends one JPEG and returns the service's answer as it is (JSON: pose, poses, hands, ms).
func (c *Client) Analyse(ctx context.Context, jpeg []byte) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/v1/pose", bytes.NewReader(jpeg))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "image/jpeg")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
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
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(b))
		}
		return nil, fmt.Errorf("pose: %d %s", res.StatusCode, e.Error)
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("pose: the answer is not JSON")
	}
	return b, nil
}
