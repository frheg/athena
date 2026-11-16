package api

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frheg/athena/internal/engine"
	"github.com/frheg/athena/internal/gate"
	"github.com/frheg/athena/internal/llm"
	"github.com/frheg/athena/internal/speech"
	"github.com/frheg/athena/internal/store"
	"github.com/frheg/athena/internal/toolbox"
)

func TestSpeakers(t *testing.T) {
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
	pol, _ := gate.Parse([]byte("{}"))
	e := &engine.Engine{Store: st, LLM: &llm.Client{Primary: llm.Endpoint{URL: "http://unused"}}, Tools: toolbox.New(nil, slog.Default()),
		Policy: func() *gate.Policy { return pol }, Hub: engine.NewHub(time.Minute), Log: slog.Default(), Zone: time.UTC, Name: "Athena"}
	a := &API{E: e, Scheduler: engine.NewScheduler(e, nil), Speech: &speech.Client{URL: "http://unused/v1"}, Owner: "spk-" + engine.NewID()[:8]}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	s, _ := st.CreateSession(ctx, engine.NewID(), a.Owner, "user", "")
	id, _ := st.AddMessage(ctx, s.ID, nil, store.Message{Role: "assistant", Content: strings.Repeat("A sentence to read aloud here. ", 30)})

	post := func(path, body string) (int, string) {
		res, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var b strings.Builder
		_, _ = bufio.NewReader(res.Body).WriteTo(&b)
		return res.StatusCode, b.String()
	}
	if code, _ := post("/api/messages/"+strconv.FormatInt(id, 10)+"/speak", `{"to":"kratos"}`); code != 400 {
		t.Fatal("no speaker connected yet:", code)
	}

	// the speaker connects and waits for jobs
	sctx, cancel := context.WithCancel(ctx)
	req, _ := http.NewRequestWithContext(sctx, "GET", srv.URL+"/api/speakers/kratos/jobs", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	jobs := make(chan map[string]any, 4)
	go func() {
		sc := bufio.NewScanner(res.Body)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") && event == "job" {
				var j map[string]any
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &j)
				jobs <- j
			}
		}
		close(jobs)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, _ := http.Get(srv.URL + "/api/speakers")
		var l struct{ Speakers []speaker }
		_ = json.NewDecoder(r.Body).Decode(&l)
		r.Body.Close()
		if len(l.Speakers) == 1 && l.Speakers[0].Name == "kratos" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the speaker is listed once connected", l)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if code, body := post("/api/messages/"+strconv.FormatInt(id, 10)+"/speak", `{"to":"kratos"}`); code != 202 {
		t.Fatal(code, body)
	}
	j := <-jobs
	if j["type"] != "play" || j["message_id"] != float64(id) || j["parts"].(float64) < 2 {
		t.Fatal("a play job with the message and its number of parts:", j)
	}
	if code, _ := post("/api/speakers/kratos/state", `{"playing":`+strconv.FormatInt(id, 10)+`}`); code != 200 {
		t.Fatal(code)
	}
	r, _ := http.Get(srv.URL + "/api/speakers")
	var l struct{ Speakers []speaker }
	_ = json.NewDecoder(r.Body).Decode(&l)
	r.Body.Close()
	if l.Speakers[0].Playing != id {
		t.Fatal("the page sees what it plays", l)
	}
	if code, _ := post("/api/speakers/kratos/stop", `{}`); code != 200 {
		t.Fatal(code)
	}
	if j := <-jobs; j["type"] != "stop" {
		t.Fatal(j)
	}
	cancel()
	res.Body.Close()
	deadline = time.Now().Add(5 * time.Second)
	for a.speakers.get(a.Owner, "kratos") != nil {
		if time.Now().After(deadline) {
			t.Fatal("a speaker that goes away is no longer listed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
