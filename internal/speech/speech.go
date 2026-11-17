// Package speech turns answers into speech: markdown into text worth reading aloud, that text into parts short
// enough to start playing quickly, and a client for an OpenAI-compatible speech server (POST /v1/audio/speech, and
// /v1/audio/transcriptions for speech to text).
package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	fence     = regexp.MustCompile("(?s)```.*?(```|$)")
	image     = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	link      = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	bareURL   = regexp.MustCompile(`https?://\S+`)
	tag       = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
	emphasis  = regexp.MustCompile(`(\*\*|__|\*|~~|` + "`" + `)`)
	listMark  = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s+`)
	heading   = regexp.MustCompile(`^\s*#{1,6}\s+`)
	quote     = regexp.MustCompile(`^\s*>\s?`)
	tableRule = regexp.MustCompile(`^\s*\|?\s*:?-{2,}`)
	spaces    = regexp.MustCompile(`[ \t]+`)
	endsLine  = regexp.MustCompile(`[.!?:;,]$`)
)

// Speakable turns a markdown answer into plain text to read aloud: code blocks, link targets and markup are left
// out, list items and headings become sentences, table rows become comma-separated lines.
func Speakable(md string) string {
	md = fence.ReplaceAllString(md, "\n")
	md = image.ReplaceAllString(md, "$1")
	md = link.ReplaceAllString(md, "$1")
	md = bareURL.ReplaceAllString(md, "")
	md = tag.ReplaceAllString(md, "")
	var out []string
	for _, line := range strings.Split(md, "\n") {
		if tableRule.MatchString(line) {
			continue
		}
		if strings.Count(line, "|") >= 2 {
			var cells []string
			for _, c := range strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|") {
				if c = strings.TrimSpace(c); c != "" {
					cells = append(cells, c)
				}
			}
			line = strings.Join(cells, ", ")
		}
		line = heading.ReplaceAllString(line, "")
		line = quote.ReplaceAllString(line, "")
		line = listMark.ReplaceAllString(line, "")
		line = emphasis.ReplaceAllString(line, "")
		line = strings.TrimSpace(spaces.ReplaceAllString(line, " "))
		if line == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		// a line without its own ending (a heading, a list item) still gets a pause
		if !endsLine.MatchString(line) {
			line += "."
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

var sentenceEnd = regexp.MustCompile(`[.!?:;]+["')\]]*\s+|\n+`)

// Parts splits text into pieces of whole sentences, at most max characters each (a longer sentence is its own
// piece). The first piece is kept short, so playing can start while the rest is made.
func Parts(text string, max int) []string {
	if max <= 0 {
		max = 400
	}
	var sentences []string
	last := 0
	for _, m := range sentenceEnd.FindAllStringIndex(text, -1) {
		sentences = append(sentences, strings.TrimSpace(text[last:m[1]]))
		last = m[1]
	}
	sentences = append(sentences, strings.TrimSpace(text[last:]))
	var parts []string
	var cur strings.Builder
	limit := min(max, 160)
	for _, s := range sentences {
		if s == "" {
			continue
		}
		if cur.Len() > 0 && cur.Len()+1+len(s) > limit {
			parts = append(parts, cur.String())
			cur.Reset()
			limit = max
		}
		if cur.Len() > 0 {
			cur.WriteString(" ")
		}
		cur.WriteString(s)
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// Client talks to the speech server.
type Client struct {
	URL   string // base URL ending in /v1
	Token string
	HTTP  *http.Client
}

// Voice is one voice the server has.
type Voice struct {
	ID       string `json:"id"`
	Language string `json:"language"`
	Speakers int    `json:"speakers"`
	Default  bool   `json:"default"`
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: time.Minute}
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		var e struct {
			Error string `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		_ = json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(b))
		}
		return nil, fmt.Errorf("speech: %d %s", res.StatusCode, e.Error)
	}
	return res, nil
}

// Voices lists the server's voices.
func (c *Client) Voices(ctx context.Context) ([]Voice, error) {
	res, err := c.do(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var out struct {
		Data []Voice `json:"data"`
	}
	return out.Data, json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
}

// Options for speaking: the voice per language ("" = the server's default; "id:3" picks speaker 3 of a voice with
// several), and the speed (1 = normal).
type Options struct {
	Voices map[string]string
	Speed  float64
}

// Speak returns WAV audio of text, and the voice that spoke it. The language is worked out by the server.
func (c *Client) Speak(ctx context.Context, text string, o Options) ([]byte, string, error) {
	body := map[string]any{"input": text, "voice": "auto", "response_format": "wav"}
	if len(o.Voices) > 0 {
		body["voices"] = o.Voices
	}
	if o.Speed > 0 {
		body["speed"] = o.Speed
	}
	res, err := c.do(ctx, http.MethodPost, "/audio/speech", body)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	return b, res.Header.Get("X-Voice"), err
}

// Transcribe writes down the speech in a WAV (POST /audio/transcriptions). The prompt helps spell names, like the
// wake word.
func (c *Client) Transcribe(ctx context.Context, wav []byte, prompt string) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "speech.wav")
	_, _ = fw.Write(wav)
	_ = mw.WriteField("response_format", "json")
	_ = mw.WriteField("temperature", "0")
	if prompt != "" {
		_ = mw.WriteField("prompt", prompt)
	}
	mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: time.Minute}
	}
	res, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err := json.Unmarshal(b, &out); err != nil || res.StatusCode >= 300 {
		if out.Error == "" {
			out.Error = strings.TrimSpace(string(b))
		}
		return "", fmt.Errorf("speech recognition: %d %s", res.StatusCode, out.Error)
	}
	return strings.TrimSpace(out.Text), nil
}
