package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frheg/athena/internal/netguard"
)

func TestSources(t *testing.T) {
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/no/w/api.php" && q.Get("list") == "search":
			_, _ = w.Write([]byte(`{"query":{"search":[{"title":"Bergen","snippet":"<span>city</span> on the west coast"}]}}`))
		case r.URL.Path == "/en/w/api.php" && q.Get("prop") == "extracts":
			_, _ = w.Write([]byte(`{"query":{"pages":{"1":{"title":"Torque","extract":"Torque is the rotational equivalent of force."}}}}`))
		case r.URL.Path == "/arxiv":
			if q.Get("search_query") != "all:retrieval AND all:time" && q.Get("id_list") != "2401.00001" {
				http.Error(w, "bad query "+q.Encode(), 400)
				return
			}
			_, _ = w.Write([]byte(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>http://arxiv.org/abs/2401.00001v1</id>
<title>Time series
 retrieval</title><summary> Abstract here. </summary><published>2024-01-01T00:00:00Z</published>
<author><name>A. Author</name></author><link href="http://arxiv.org/pdf/2401.00001v1" title="pdf" type="application/pdf"/>
<category term="cs.LG"/></entry></feed>`))
		case r.URL.Path == "/search/repositories":
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, "no token", 401)
				return
			}
			_, _ = w.Write([]byte(`{"items":[{"full_name":"a/b","description":"d","stargazers_count":5,"language":"Go","html_url":"https://github.com/a/b"}]}`))
		case r.URL.Path == "/repos/a/b/readme":
			_, _ = w.Write([]byte("# Hello"))
		default:
			http.Error(w, "unknown "+r.URL.String(), 404)
		}
	}))
	defer srv.Close()
	tools := New(Config{GitHubToken: "tok", WikipediaBase: func(l string) string { return srv.URL + "/" + l + "/w/api.php" },
		ArxivBase: srv.URL + "/arxiv", GitHubBase: srv.URL, Guard: netguard.Options{AllowPrivate: true}})
	ctx := context.Background()

	hits, err := tools.WikipediaSearch(ctx, WikiSearchIn{Query: "Bergen", Lang: "nb"})
	if err != nil || len(hits) != 1 || hits[0].Snippet != "city on the west coast" || !strings.HasPrefix(hits[0].URL, "https://no.wikipedia.org/wiki/Bergen") {
		t.Fatalf("wiki search: %+v %v", hits, err)
	}
	page, err := tools.WikipediaPage(ctx, WikiPageIn{Title: "Torque"})
	if err != nil || !strings.Contains(page, "rotational equivalent") || !strings.Contains(page, "<untrusted") {
		t.Fatalf("wiki page: %s %v", page, err)
	}
	_, _ = tools.WikipediaPage(ctx, WikiPageIn{Title: "Torque"})
	if calls["/en/w/api.php"] != 1 {
		t.Errorf("cache missed: %d calls", calls["/en/w/api.php"])
	}
	if _, err := tools.WikipediaSearch(ctx, WikiSearchIn{Query: "x", Lang: "evil.example/"}); err == nil {
		t.Error("a host injected through lang was accepted")
	}

	ps, err := tools.ArxivSearch(ctx, ArxivSearchIn{Query: "retrieval time"})
	if err != nil || len(ps) != 1 || ps[0].ID != "2401.00001v1" || ps[0].Title != "Time series retrieval" || ps[0].PDF == "" || ps[0].Authors[0] != "A. Author" {
		t.Fatalf("arxiv: %+v %v", ps, err)
	}

	repos, err := tools.GitHubSearch(ctx, GitHubSearchIn{Query: "mcp"})
	if err != nil || len(repos) != 1 || repos[0].Stars != 5 {
		t.Fatalf("github search: %+v %v", repos, err)
	}
	readme, err := tools.GitHubFile(ctx, "a/b", "", 0)
	if err != nil || !strings.Contains(readme, "# Hello") {
		t.Fatalf("readme: %s %v", readme, err)
	}
	for _, bad := range []string{"../../etc", "a/b/c/d e", "http://evil/x"} {
		if _, err := tools.GitHubFile(ctx, bad, "", 0); err == nil {
			t.Errorf("repo %q accepted", bad)
		}
	}
	if _, err := tools.GitHubFile(ctx, "a/b", "../../../user", 0); err == nil {
		t.Error("path with .. accepted")
	}

	// the real client only talks to the source hosts
	real := New(Config{})
	if _, err := real.get(ctx, "x", "https://example.com/", nil, 0, 0); err == nil || !strings.Contains(err.Error(), "host example.com") {
		t.Errorf("other host reachable: %v", err)
	}
}
