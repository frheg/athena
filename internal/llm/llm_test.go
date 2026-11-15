package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sse(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, c := range chunks {
		fmt.Fprintf(w, "data: %s\n\n", c)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func TestStreamToolCallsWakeAndFallback(t *testing.T) {
	var calls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "big" || body["stream"] != true {
			http.Error(w, "bad body", 400)
			return
		}
		if n == 1 { // the gateway is waking the model
			w.Header().Set("Retry-After", "1")
			http.Error(w, "llm is starting", http.StatusServiceUnavailable)
			return
		}
		sse(w,
			`{"model":"big","choices":[{"delta":{"content":"<thi"}}]}`,
			`{"choices":[{"delta":{"content":"nk>secret plan</think>Hel"}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"web__search","arguments":"{\"que"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ry\":\"x\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7}}`)
	}))
	defer primary.Close()
	c := &Client{Primary: Endpoint{URL: primary.URL + "/v1", Model: "big"}}
	var text strings.Builder
	var waited []string
	res, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, []Tool{{Type: "function", Function: ToolFunction{Name: "web__search", Parameters: map[string]any{"type": "object"}}}},
		Events{Text: func(s string) { text.WriteString(s) }, Waiting: func(r string, d time.Duration) { waited = append(waited, r) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "Hello" || text.String() != "Hello" {
		t.Errorf("think block not removed: %q / %q", res.Content, text.String())
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Function.Arguments != `{"query":"x"}` || res.ToolCalls[0].ID != "c1" {
		t.Errorf("tool calls: %+v", res.ToolCalls)
	}
	if res.TokensIn != 11 || res.TokensOut != 7 || res.Finish != "tool_calls" || len(waited) != 1 || waited[0] != "llm is starting" {
		t.Errorf("result: %+v waited %v", res, waited)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }))
	defer down.Close()
	fb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"small","choices":[{"message":{"content":"from cpu"},"finish_reason":"stop"}]}`))
	}))
	defer fb.Close()
	c2 := &Client{Primary: Endpoint{URL: down.URL, Model: "big"}, Fallback: Endpoint{URL: fb.URL, Model: "small"}}
	res, err = c2.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, Events{})
	if err != nil || res.Content != "from cpu" || res.Endpoint != "fallback" {
		t.Errorf("fallback: %+v %v", res, err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "bad request", 400) }))
	defer bad.Close()
	c3 := &Client{Primary: Endpoint{URL: bad.URL}, Fallback: Endpoint{URL: fb.URL}}
	if _, err := c3.Chat(context.Background(), nil, nil, Events{}); err == nil {
		t.Error("a 400 went to the fallback instead of failing")
	}
}

func TestThinkFilterAcrossChunks(t *testing.T) {
	f := &thinkFilter{}
	out := ""
	for _, s := range []string{"a<", "th", "ink>x", "</thi", "nk>b<t", "ag>"} {
		out += f.feed(s)
	}
	out += f.flush()
	if out != "ab<tag>" {
		t.Errorf("got %q", out)
	}
}
