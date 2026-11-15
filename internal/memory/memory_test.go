package memory

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frheg/athena/internal/pg"
)

func testService(t *testing.T) *Service {
	url := os.Getenv("ATHENA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ATHENA_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pg.Open(ctx, url, 4, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := fs.Sub(Migrations, "migrations")
	if err := pg.Migrate(ctx, db, sub, "memory_migrations"); err != nil {
		t.Fatal(err)
	}
	return &Service{DB: db, Log: slog.Default()}
}

func TestTextMode(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	tenant := "t-" + newID()[:8]
	a, replaced, err := s.Remember(ctx, tenant, "me", "place", "Home is in Bergen, Norway", "test")
	if err != nil || replaced {
		t.Fatal(err, replaced)
	}
	if _, replaced, _ := s.Remember(ctx, tenant, "me", "place", "home is in bergen, norway", "test"); !replaced {
		t.Fatal("an exact repeat should update, not add")
	}
	_, _, _ = s.Remember(ctx, tenant, "me", "preference", "Prefers tea over coffee", "test")
	_, _, _ = s.Remember(ctx, tenant, "other", "fact", "Lives in Bergen too", "test")
	if _, _, err := s.Remember(ctx, tenant, "me", "secret", "x", ""); err == nil {
		t.Fatal("unknown kind accepted")
	}
	found, err := s.Search(ctx, tenant, "me", "What is the weather where I live in Bergen?", 5, 0)
	if err != nil || len(found) != 1 || found[0].ID != a.ID {
		t.Fatalf("search: %v %+v", err, found)
	}
	list, _ := s.List(ctx, tenant, "me", "", "", 0)
	if len(list) != 2 {
		t.Fatalf("list: %+v", list)
	}
	if _, err := s.Update(ctx, tenant, "other", a.ID, "place", "hijack"); err != ErrNotFound {
		t.Fatal("another subject must not change the item")
	}
	if err := s.Forget(ctx, "someone-else", "me", a.ID); err != ErrNotFound {
		t.Fatal("another tenant must not delete the item")
	}
	if err := s.Forget(ctx, tenant, "me", a.ID); err != nil {
		t.Fatal(err)
	}
}

// fakeVectors embeds by counting a few keywords and serves a tiny in-memory Qdrant.
type fakeVectors struct {
	mu     sync.Mutex
	points map[string]point
}

type point struct {
	vec     []float32
	payload map[string]string
}

var vocab = []string{"bergen", "home", "tea", "coffee", "sister", "anna"}

func fakeEmbed(text string) []float32 {
	v := make([]float32, len(vocab))
	for i, w := range vocab {
		if strings.Contains(strings.ToLower(text), w) {
			v[i] = 1
		}
	}
	return v
}

func (f *fakeVectors) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]json.RawMessage
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case strings.HasSuffix(r.URL.Path, "/embeddings"):
		var in []string
		_ = json.Unmarshal(body["input"], &in)
		data := []map[string]any{}
		for i, s := range in {
			data = append(data, map[string]any{"index": i, "embedding": fakeEmbed(s)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/collections/"):
		if f.points == nil {
			http.Error(w, "{}", 404)
			return
		}
		_, _ = w.Write([]byte(`{"result":{"config":{"params":{"vectors":{"size":6}}}}}`))
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/points"):
		var pts []struct {
			ID      string            `json:"id"`
			Vector  []float32         `json:"vector"`
			Payload map[string]string `json:"payload"`
		}
		_ = json.Unmarshal(body["points"], &pts)
		for _, p := range pts {
			f.points[p.ID] = point{p.Vector, p.Payload}
		}
		_, _ = w.Write([]byte(`{}`))
	case r.Method == http.MethodPut:
		if f.points == nil {
			f.points = map[string]point{}
		}
		_, _ = w.Write([]byte(`{}`))
	case strings.HasSuffix(r.URL.Path, "/search"):
		var vec []float32
		var min float64
		_ = json.Unmarshal(body["vector"], &vec)
		_ = json.Unmarshal(body["score_threshold"], &min)
		filter := string(body["filter"])
		res := []hit{}
		for id, p := range f.points {
			if !strings.Contains(filter, `"`+p.payload["tenant"]+`"`) || !strings.Contains(filter, `"`+p.payload["subject"]+`"`) {
				continue
			}
			if sc := cos(vec, p.vec); sc >= min {
				res = append(res, hit{id, sc})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": res})
	case strings.HasSuffix(r.URL.Path, "/delete"):
		var ids []string
		_ = json.Unmarshal(body["points"], &ids)
		for _, id := range ids {
			delete(f.points, id)
		}
		_, _ = w.Write([]byte(`{}`))
	default:
		_, _ = w.Write([]byte(`{}`))
	}
}

func cos(a, b []float32) float64 {
	var d, x, y float64
	for i := range a {
		d += float64(a[i] * b[i])
		x += float64(a[i] * a[i])
		y += float64(b[i] * b[i])
	}
	if x == 0 || y == 0 {
		return 0
	}
	return d / math.Sqrt(x*y)
}

func TestVectorMode(t *testing.T) {
	s := testService(t)
	f := &fakeVectors{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	s.Embed = &Embedder{URL: srv.URL + "/v1", HTTP: srv.Client()}
	s.Vectors = &Qdrant{URL: srv.URL, Collection: "memory", HTTP: srv.Client()}
	ctx := context.Background()
	tenant := "v-" + newID()[:8]
	a, _, err := s.Remember(ctx, tenant, "me", "person", "My sister is Anna", "")
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = s.Remember(ctx, tenant, "me", "preference", "Likes tea", "")
	if _, replaced, _ := s.Remember(ctx, tenant, "me", "person", "Anna is my sister", ""); !replaced {
		t.Fatal("a near-duplicate should replace")
	}
	found, err := s.Search(ctx, tenant, "me", "what is my sister called", 3, 0.3)
	if err != nil || len(found) != 1 || found[0].ID != a.ID || found[0].Text != "Anna is my sister" {
		t.Fatalf("search: %v %+v", err, found)
	}
	if found, _ := s.Search(ctx, tenant, "other", "sister", 3, 0.3); len(found) != 0 {
		t.Fatal("another subject must not see the item")
	}
	_ = s.Reindex(ctx)
	if n, err := s.Backfill(ctx); err != nil || n < 2 {
		t.Fatalf("backfill: %d %v", n, err)
	}
}
