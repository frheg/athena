package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frheg/athena/internal/pose"
)

func TestCamera(t *testing.T) {
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ptok" || r.URL.Path != "/v1/pose" {
			http.Error(w, `{"error":"unknown caller"}`, 401)
			return
		}
		_, _ = w.Write([]byte(`{"pose":null,"poses":[],"hands":[{"side":"left","gesture":"thumbs up","score":0.9,"points":[]}],"ms":30}`))
	}))
	defer svc.Close()
	a := &API{Pose: &pose.Client{URL: svc.URL, Token: "ptok"}}

	w := httptest.NewRecorder()
	a.camera(w, httptest.NewRequest("POST", "/api/camera", strings.NewReader("\xff\xd8\xff\xe0 a picture")))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "thumbs up") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	a.camera(w, httptest.NewRequest("POST", "/api/camera", strings.NewReader("GIF89a")))
	if w.Code != 400 {
		t.Fatalf("not a JPEG: %d", w.Code)
	}
	w = httptest.NewRecorder()
	(&API{}).camera(w, httptest.NewRequest("POST", "/api/camera", strings.NewReader("\xff\xd8\xff")))
	if w.Code != 503 {
		t.Fatalf("without a pose service: %d", w.Code)
	}
}
