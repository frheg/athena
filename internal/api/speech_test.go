package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frheg/athena/internal/engine"
	"github.com/frheg/athena/internal/gate"
	"github.com/frheg/athena/internal/llm"
	"github.com/frheg/athena/internal/speech"
	"github.com/frheg/athena/internal/store"
	"github.com/frheg/athena/internal/toolbox"
)

func TestSpeechOverHTTP(t *testing.T) {
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
	var mu sync.Mutex
	var asked []map[string]any
	tts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stok" {
			http.Error(w, `{"error":"unknown caller"}`, 401)
			return
		}
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"no_NO-a","language":"no","speakers":10},{"id":"en_GB-b","language":"en","speakers":1,"default":true}]}`))
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		asked = append(asked, in)
		mu.Unlock()
		w.Header().Set("X-Voice", "en_GB-b")
		_, _ = w.Write([]byte("RIFF" + in["input"].(string)))
	}))
	defer tts.Close()
	pol, _ := gate.Parse([]byte("{}"))
	e := &engine.Engine{Store: st, LLM: &llm.Client{Primary: llm.Endpoint{URL: "http://unused"}}, Tools: toolbox.New(nil, slog.Default()),
		Policy: func() *gate.Policy { return pol }, Hub: engine.NewHub(time.Minute), Log: slog.Default(), Zone: time.UTC, Name: "Athena"}
	a := &API{E: e, Scheduler: engine.NewScheduler(e, nil), Speech: &speech.Client{URL: tts.URL + "/v1", Token: "stok"}, Owner: "speech-" + engine.NewID()[:8]}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	s, _ := st.CreateSession(ctx, engine.NewID(), a.Owner, "user", "")
	answer := "## Plan\n\n" + strings.Repeat("We leave on Friday at half past three in the afternoon. ", 12) + "\n\n```sh\nrm -rf /\n```"
	id, _ := st.AddMessage(ctx, s.ID, nil, store.Message{Role: "assistant", Content: answer})
	other, _ := st.CreateSession(ctx, engine.NewID(), "someone-else", "user", "")
	otherID, _ := st.AddMessage(ctx, other.ID, nil, store.Message{Role: "assistant", Content: "secret"})

	put := func(body string) {
		req, _ := http.NewRequest("PUT", srv.URL+"/api/speech", strings.NewReader(body))
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != 200 {
			t.Fatal("put", res.StatusCode, err)
		}
		res.Body.Close()
	}
	put(`{"no":"no_NO-a:3","speed":1.25}`)

	get := func(path string) (*http.Response, string) {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, string(b)
	}
	res, body := get("/api/messages/" + strconv.FormatInt(id, 10) + "/speech?part=0")
	n, _ := strconv.Atoi(res.Header.Get("X-Speech-Parts"))
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "audio/wav" || n < 2 || !strings.HasPrefix(body, "RIFFPlan.") {
		t.Fatal(res.StatusCode, res.Header, body)
	}
	all := body
	for p := 1; p < n; p++ {
		_, b := get("/api/messages/" + strconv.FormatInt(id, 10) + "/speech?part=" + strconv.Itoa(p))
		all += b
	}
	if strings.Contains(all, "rm -rf") || strings.Count(all, "half past three") != 12 {
		t.Fatal("all sentences, no code:", all)
	}
	if res, _ := get("/api/messages/" + strconv.FormatInt(id, 10) + "/speech?part=" + strconv.Itoa(n)); res.StatusCode != 404 {
		t.Fatal("after the last part comes 404:", res.StatusCode)
	}
	if res, _ := get("/api/messages/" + strconv.FormatInt(otherID, 10) + "/speech?part=0"); res.StatusCode != 404 {
		t.Fatal("another user's message must not be read:", res.StatusCode)
	}
	mu.Lock()
	first := asked[0]
	mu.Unlock()
	if first["speed"] != 1.25 || first["voices"].(map[string]any)["no"] != "no_NO-a:3" {
		t.Fatal("settings go to the speech server:", first)
	}
	_, body = get("/api/speech")
	var set struct {
		Configured bool
		Voices     []speech.Voice
		Chosen     map[string]string
		Speed      float64
	}
	_ = json.Unmarshal([]byte(body), &set)
	if !set.Configured || len(set.Voices) != 2 || set.Chosen["no"] != "no_NO-a:3" || set.Speed != 1.25 {
		t.Fatal(body)
	}
	if res, body := get("/api/speech/sample?lang=no"); res.StatusCode != 200 || !strings.Contains(body, "Athena") {
		t.Fatal(res.StatusCode, body)
	}
}
