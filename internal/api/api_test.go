package api

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/frheg/athena/internal/engine"
	"github.com/frheg/athena/internal/gate"
	"github.com/frheg/athena/internal/llm"
	"github.com/frheg/athena/internal/store"
	"github.com/frheg/athena/internal/toolbox"
)

func TestChatOverHTTP(t *testing.T) {
	url := os.Getenv("ATHENA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ATHENA_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, url, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, part := range []string{"Hel", "lo"} {
			b, _ := json.Marshal(map[string]any{"model": "fake", "choices": []any{map[string]any{"delta": map[string]string{"content": part}}}})
			_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer model.Close()
	pol, _ := gate.Parse([]byte("{}"))
	e := &engine.Engine{Store: st, LLM: &llm.Client{Primary: llm.Endpoint{URL: model.URL}, HTTP: http.DefaultClient}, Tools: toolbox.New(nil, slog.Default()),
		Policy: func() *gate.Policy { return pol }, Hub: engine.NewHub(time.Minute), Log: slog.Default(), Zone: time.UTC, Name: "Athena"}
	a := &API{E: e, Scheduler: engine.NewScheduler(e, nil), Owner: "api-" + engine.NewID()[:8], Token: "secret"}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	do := func(method, path, body string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
			_ = json.NewDecoder(res.Body).Decode(&out)
			res.Body.Close()
		}
		return res, out
	}
	if res, _ := http.Get(srv.URL + "/api/sessions"); res.StatusCode != 401 {
		t.Fatal("no token should be refused:", res.StatusCode)
	}
	res, out := do("POST", "/api/sessions", `{"text":"hi there"}`)
	if res.StatusCode != 201 || out["turn_id"] == nil {
		t.Fatal(res.StatusCode, out)
	}
	turn := out["turn_id"].(string)
	sid := out["session"].(map[string]any)["id"].(string)

	res, _ = do("GET", "/api/turns/"+turn+"/events", "")
	sc := bufio.NewScanner(res.Body)
	var types []string
	text := ""
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: ") {
			types = append(types, strings.TrimPrefix(line, "event: "))
		}
		if strings.HasPrefix(line, "data: ") && types[len(types)-1] == "text" {
			var d map[string]string
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &d)
			text += d["delta"]
		}
	}
	res.Body.Close()
	if types[len(types)-1] != "done" || text != "Hello" {
		t.Fatalf("events %v, text %q", types, text)
	}
	// reconnecting after the end replays and closes at once
	res, _ = do("GET", "/api/turns/"+turn+"/events", "")
	res.Body.Close()

	_, out = do("GET", "/api/sessions/"+sid, "")
	msgs := out["messages"].([]any)
	if len(msgs) != 2 || msgs[1].(map[string]any)["content"] != "Hello" {
		t.Fatalf("messages: %v", msgs)
	}
	if out["session"].(map[string]any)["title"] != "hi there" {
		t.Fatal("title", out["session"])
	}
	res, _ = do("POST", "/api/jobs", `{"name":"brief","schedule":"0 7 * * 1-5","prompt":"weather","enabled":true}`)
	if res.StatusCode != 200 {
		t.Fatal("job", res.StatusCode)
	}
	if res, out := do("POST", "/api/jobs", `{"name":"bad","schedule":"every day","prompt":"x"}`); res.StatusCode != 400 {
		t.Fatal("a bad schedule should be refused", out)
	}
	_, out = do("GET", "/api/jobs", "")
	if jobs := out["jobs"].([]any); len(jobs) != 1 || jobs[0].(map[string]any)["next_run"] == nil {
		t.Fatal(out)
	}
	if res, _ := do("POST", "/api/approvals/"+engine.NewID(), `{"answer":"once"}`); res.StatusCode != 404 {
		t.Fatal("unknown approval", res.StatusCode)
	}
	if res, _ := do("GET", "/api/memory/items", ""); res.StatusCode != 503 {
		t.Fatal("memory without a service", res.StatusCode)
	}
}
