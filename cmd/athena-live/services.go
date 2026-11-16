package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/frheg/athena/internal/client"
)

// transcribe sends a stretch of speech to a whisper.cpp server (POST /inference) and returns the text. The prompt
// helps it spell the wake word.
func transcribe(ctx context.Context, url, prompt string, samples []int16) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "speech.wav")
	_, _ = fw.Write(wav(samples, rate))
	_ = mw.WriteField("response_format", "json")
	_ = mw.WriteField("temperature", "0")
	if prompt != "" {
		_ = mw.WriteField("prompt", prompt)
	}
	mw.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(url, "/")+"/inference", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("speech recognition: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != 200 {
		return "", fmt.Errorf("speech recognition: %d %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", fmt.Errorf("speech recognition: %s", out.Error)
	}
	return strings.TrimSpace(strings.ReplaceAll(out.Text, "\n", " ")), nil
}

// say makes speech of text with Athena's voices (POST /api/speech) and returns the WAV.
func say(ctx context.Context, c *client.Client, text string) ([]byte, error) {
	b, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return nil, err
	}
	req, err := c.Request(ctx, http.MethodPost, "/speech", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(io.LimitReader(res.Body, 32<<20))
}

// sentences cuts streaming text into whole sentences to speak: it returns those that are complete and keeps the
// rest. A sentence ends at . ! ? or : followed by a space, or at a line break; short ones wait for the next, except
// the first, so speech starts early.
type sentences struct {
	buf    strings.Builder
	spoken int // how many have been handed out
}

func (s *sentences) add(text string) []string {
	s.buf.WriteString(text)
	all := s.buf.String()
	var out []string
	start := 0
	for i := 0; i < len(all); i++ {
		c := all[i]
		end := -1
		switch {
		case c == '\n':
			end = i
		case (c == '.' || c == '!' || c == '?' || c == ':') && i+1 < len(all) && (all[i+1] == ' ' || all[i+1] == '\n'):
			end = i + 1
		}
		if end < 0 {
			continue
		}
		piece := strings.TrimSpace(all[start:end])
		if piece == "" {
			start = end
			continue
		}
		if len(piece) < 12 && s.spoken+len(out) > 0 {
			continue // too short to stand alone: let it grow with the next sentence
		}
		out = append(out, piece)
		start = end
	}
	rest := all[start:]
	s.buf.Reset()
	s.buf.WriteString(rest)
	s.spoken += len(out)
	return out
}

// flush returns what is left when the text has ended.
func (s *sentences) flush() string {
	rest := strings.TrimSpace(s.buf.String())
	s.buf.Reset()
	s.spoken = 0
	return rest
}

func (s *sentences) reset() {
	s.buf.Reset()
	s.spoken = 0
}
