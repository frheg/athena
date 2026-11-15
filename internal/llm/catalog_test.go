package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCatalog(t *testing.T) {
	gpu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"big","description":"GPU","status":{"value":"ready"}},{"id":"coder"}]}`))
	}))
	defer gpu.Close()
	cpu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"small"},{"id":"big"}]}`))
	}))
	defer cpu.Close()
	down := NamedEndpoint{Name: "third", Endpoint: Endpoint{URL: "http://127.0.0.1:1/v1"}}
	c := &Catalog{Endpoints: []NamedEndpoint{{Name: "primary", Endpoint: Endpoint{URL: gpu.URL, Model: "big"}},
		{Name: "fallback", Endpoint: Endpoint{URL: cpu.URL, Model: "small"}}, down}}
	models, errs := c.Models(context.Background())
	if len(models) != 3 || models[0].ID != "big" || !models[0].Loaded || !models[0].Default || models[2].Endpoint != "fallback" {
		t.Fatalf("%+v", models)
	}
	if errs["third"] == "" {
		t.Fatal("an endpoint that does not answer should be reported")
	}
	ep, ok := c.Resolve(context.Background(), "small")
	if !ok || ep.URL != cpu.URL || ep.Model != "small" {
		t.Fatal(ep, ok)
	}
	if _, ok := c.Resolve(context.Background(), "nope"); ok {
		t.Fatal("an unknown model resolved")
	}
	base := &Client{Primary: Endpoint{URL: gpu.URL, Model: "big"}, Fallback: Endpoint{URL: cpu.URL, Model: "small"}}
	if cl := base.With(ep); cl.Primary.Model != "small" || cl.Fallback.URL != "" || base.Primary.Model != "big" {
		t.Fatalf("%+v %+v", cl, base)
	}
}
