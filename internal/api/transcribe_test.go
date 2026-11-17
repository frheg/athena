package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frheg/athena/internal/speech"
)

func TestTranscribe(t *testing.T) {
	stt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stok" || r.URL.Path != "/v1/audio/transcriptions" {
			http.Error(w, `{"error":"no"}`, 401)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, `{"error":"no file"}`, 400)
			return
		}
		b, _ := io.ReadAll(f)
		if !strings.HasPrefix(string(b), "RIFF") || r.FormValue("prompt") != "Athena." {
			http.Error(w, `{"error":"bad audio or prompt"}`, 400)
			return
		}
		_, _ = w.Write([]byte(`{"text":" Athena, hva er klokka? "}`))
	}))
	defer stt.Close()
	a := &API{Speech: &speech.Client{URL: stt.URL + "/v1", Token: "stok"}}

	wav := "RIFF\x00\x00\x00\x00WAVEfmt " + strings.Repeat("\x00", 40)
	for _, c := range []struct {
		body string
		code int
		text string
	}{
		{wav, 200, "Athena, hva er klokka?"},
		{"not audio at all, but long enough to pass the length check", 400, ""},
	} {
		w := httptest.NewRecorder()
		a.transcribe(w, httptest.NewRequest("POST", "/api/transcribe?prompt=Athena.", strings.NewReader(c.body)))
		if w.Code != c.code {
			t.Fatalf("status %d, want %d: %s", w.Code, c.code, w.Body)
		}
		var out map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out["text"] != c.text {
			t.Fatalf("text %q, want %q", out["text"], c.text)
		}
	}

	w := httptest.NewRecorder()
	(&API{}).transcribe(w, httptest.NewRequest("POST", "/api/transcribe", strings.NewReader(wav)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("without a speech server: %d", w.Code)
	}
}
