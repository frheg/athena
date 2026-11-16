package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "me" || p != "pw" {
			http.Error(w, `{"error":"sign in"}`, 401)
			return
		}
		switch r.URL.Path {
		case "/athena/api/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: job\ndata: {\"a\":1}\n\n: comment\ndata: line one\ndata: line two\n\nevent: ping\ndata: {}\n\n"))
		case "/athena/api/thing":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.Error(w, `{"error":"no such endpoint"}`, 404)
		}
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL + "/athena", User: "me", Password: "pw"}
	var got []Event
	err := c.Stream(context.Background(), "/stream", func(e Event) { got = append(got, e) })
	if err == nil || len(got) != 3 || got[0].Type != "job" || string(got[0].Data) != `{"a":1}` ||
		got[1].Type != "message" || string(got[1].Data) != "line one\nline two" || got[2].Type != "ping" {
		t.Fatalf("%v %q", err, got)
	}
	var out struct{ OK bool }
	if err := c.JSON(context.Background(), "GET", "/thing", nil, &out); err != nil || !out.OK {
		t.Fatal(out, err)
	}
	var ae *Error
	if err := c.JSON(context.Background(), "GET", "/nope", nil, nil); !errors.As(err, &ae) || ae.Status != 404 || ae.Message != "no such endpoint" {
		t.Fatal(err)
	}
	c.Password = "wrong"
	if err := c.JSON(context.Background(), "GET", "/thing", nil, nil); !errors.As(err, &ae) || ae.Status != 401 {
		t.Fatal(err)
	}
}
