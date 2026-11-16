// Package client is for programs that use Athena from another machine (athena-speaker, athena-live): its address
// and sign-in from the environment, JSON calls, and event streams that reconnect by themselves.
//
//	ATHENA_URL                      the address of Athena's page, like https://athena.example or http://host/athena
//	ATHENA_USER, ATHENA_PASSWORD    basic sign-in at the gate in front of Athena, or
//	ATHENA_TOKEN                    a bearer token (core's API_TOKEN, when nothing stands in front of it)
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client calls Athena's API.
type Client struct {
	URL            string // without /api
	User, Password string
	Token          string
	HTTP           *http.Client
}

// FromEnv reads the client's settings from the environment.
func FromEnv() (*Client, error) {
	c := &Client{URL: strings.TrimRight(os.Getenv("ATHENA_URL"), "/"), User: os.Getenv("ATHENA_USER"),
		Password: os.Getenv("ATHENA_PASSWORD"), Token: os.Getenv("ATHENA_TOKEN")}
	if c.URL == "" {
		return nil, errors.New("ATHENA_URL is not set (the address of Athena's page)")
	}
	return c, nil
}

func (c *Client) hc() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Request builds a request to path under /api.
func (c *Client) Request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.URL+"/api"+path, body)
	if err != nil {
		return nil, err
	}
	switch {
	case c.Token != "":
		req.Header.Set("Authorization", "Bearer "+c.Token)
	case c.User != "":
		req.SetBasicAuth(c.User, c.Password)
	}
	return req, nil
}

// Error is an answer that is not a success.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("athena: %d %s", e.Status, e.Message) }

// Do sends a request and checks the status; the caller closes the body.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	res, err := c.hc().Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(b))
			if len(e.Error) > 200 {
				e.Error = e.Error[:200]
			}
		}
		return nil, &Error{res.StatusCode, e.Error}
	}
	return res, nil
}

// JSON sends body (nil for none) as JSON and decodes the answer into out (nil to ignore it).
func (c *Client) JSON(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := c.Request(ctx, method, path, r)
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
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Get fetches path and returns the body (for audio and such).
func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	req, err := c.Request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(io.LimitReader(res.Body, 64<<20))
}

// Event is one Server-Sent Event.
type Event struct {
	Type string
	Data []byte
}

// Stream reads the event stream at path and calls on for each event until ctx ends or the stream does. It returns
// the reason it stopped.
func (c *Client) Stream(ctx context.Context, path string, on func(Event)) error {
	req, err := c.Request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := c.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := ReadEvents(res.Body, on); err != nil {
		return err
	}
	return io.EOF
}

// ReadEvents reads Server-Sent Events from r and calls on for each, until r ends (nil) or fails.
func ReadEvents(r io.Reader, on func(Event)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	ev := Event{Type: "message"}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev.Data != nil {
				on(ev)
			}
			ev = Event{Type: "message"}
		case strings.HasPrefix(line, "event:"):
			ev.Type = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			if ev.Data != nil {
				ev.Data = append(ev.Data, '\n')
			}
			ev.Data = append(ev.Data, d...)
		}
	}
	return sc.Err()
}

// Forever runs Stream again whenever it stops, waiting a little longer each time (up to a minute), until ctx ends.
// onErr is told why each time (may be nil).
func (c *Client) Forever(ctx context.Context, path string, on func(Event), onErr func(error)) {
	wait := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := c.Stream(ctx, path, on)
		if ctx.Err() != nil {
			return
		}
		if onErr != nil {
			onErr(err)
		}
		if time.Since(start) > time.Minute {
			wait = time.Second // it ran fine for a while: start over
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, time.Minute)
	}
}
