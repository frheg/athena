package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/frheg/athena/internal/engine"
	"github.com/frheg/athena/internal/files"
	"github.com/frheg/athena/internal/gate"
	"github.com/frheg/athena/internal/llm"
	"github.com/frheg/athena/internal/mcpx"
	"github.com/frheg/athena/internal/pg"
	"github.com/frheg/athena/internal/store"
	"github.com/frheg/athena/internal/toolbox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFilesOverHTTP(t *testing.T) {
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
	db, _ := pg.Open(ctx, url, 2, func(string, ...any) {})
	sub, _ := fs.Sub(files.Migrations, "migrations")
	if err := pg.Migrate(ctx, db, sub, "files_migrations"); err != nil {
		t.Fatal(err)
	}
	fsvc := &files.Service{DB: db}
	fsrv := httptest.NewServer(mcpx.Handler(mcp.NewServer(&mcp.Implementation{Name: "files"}, nil), mcpx.Config{Name: "files", Routes: fsvc.Routes}, map[string]string{"core": "ftok"}, slog.Default()))
	defer fsrv.Close()

	// the model's request arrives on the server's goroutine
	seenc := make(chan []byte, 4)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seenc <- b
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"fake","choices":[{"message":{"role":"assistant","content":"A grey square."}}]}`))
	}))
	defer model.Close()
	pol, _ := gate.Parse([]byte("{}"))
	e := &engine.Engine{Store: st, LLM: &llm.Client{Primary: llm.Endpoint{URL: model.URL, Model: "fake"}, HTTP: http.DefaultClient}, Tools: toolbox.New(nil, slog.Default()),
		Policy: func() *gate.Policy { return pol }, Hub: engine.NewHub(time.Minute), Log: slog.Default(), Zone: time.UTC, Name: "Athena",
		Vision: func(string) bool { return true }}
	fc := &files.Client{URL: fsrv.URL, Token: "ftok"}
	e.Files = fc
	a := &API{E: e, Scheduler: engine.NewScheduler(e, nil), Files: fc, Owner: "files-" + engine.NewID()[:8]}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()

	upload := func(name string, body []byte) map[string]any {
		res, err := http.Post(srv.URL+"/api/files?name="+name, "application/octet-stream", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != 201 {
			t.Fatal(name, res.StatusCode, out)
		}
		return out
	}
	var pb bytes.Buffer
	_ = png.Encode(&pb, image.NewGray(image.Rect(0, 0, 40, 30)))
	img := upload("square.png", pb.Bytes())
	page := upload("page.html", []byte("<script>alert(1)</script>"))
	if img["kind"] != "image" || img["preview"] != true || page["kind"] != "text" {
		t.Fatal(img, page)
	}

	get := func(path string) *http.Response {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res
	}
	if res := get("/api/files/" + img["id"].(string) + "/content?preview=1"); res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" ||
		!strings.HasPrefix(res.Header.Get("Content-Disposition"), "inline") {
		t.Fatal("an image preview is shown inline", res.StatusCode, res.Header)
	}
	if res := get("/api/files/" + page["id"].(string) + "/content"); res.Header.Get("Content-Type") != "application/octet-stream" ||
		!strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("an HTML upload must only ever download", res.Header)
	}

	// a message with both: the model gets the image and the page as quoted text
	res, err := http.Post(srv.URL+"/api/sessions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]any
	_ = json.NewDecoder(res.Body).Decode(&created)
	res.Body.Close()
	sid := created["session"].(map[string]any)["id"].(string)
	body := `{"text":"what is this?","files":["` + img["id"].(string) + `","` + page["id"].(string) + `"]}`
	res, _ = http.Post(srv.URL+"/api/sessions/"+sid+"/messages", "application/json", strings.NewReader(body))
	if res.StatusCode != 202 {
		t.Fatal(res.StatusCode)
	}
	res.Body.Close()
	var seen []byte
	select {
	case seen = <-seenc:
	case <-time.After(10 * time.Second):
		t.Fatal("the model was never asked")
	}
	if !bytes.Contains(seen, []byte("data:image/jpeg;base64,")) || !bytes.Contains(seen, []byte("attachment: page.html")) {
		t.Fatalf("%.2000s", seen)
	}
	res, _ = http.Post(srv.URL+"/api/sessions/"+sid+"/messages", "application/json", strings.NewReader(`{"text":"x","files":["00000000-0000-4000-8000-000000000000"]}`))
	if res.StatusCode != 400 {
		t.Fatal("an unknown file is refused", res.StatusCode)
	}
}
