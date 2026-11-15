// Package llm is an OpenAI-compatible chat client with streaming and tool calls, a primary and a fallback endpoint,
// and patience for cold starts: a 503 with Retry-After (a gateway waking a model) is waited out, not treated as an error.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Endpoint is one OpenAI-compatible server.
type Endpoint struct {
	URL   string // base URL ending in /v1
	Model string
	Key   string
}

// Message in the chat format. A message with Parts (text and images) is sent with those as its content instead
// of Content.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Parts      []Part     `json:"-"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// Part is one piece of a message with several: text, or an image as a URL (a data: URL works everywhere).
type Part struct {
	Type     string    `json:"type"` // text or image_url
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL is where an image part is.
type ImageURL struct {
	URL string `json:"url"`
}

// MarshalJSON sends Parts as the content when there are any.
func (m Message) MarshalJSON() ([]byte, error) {
	type plain Message
	if len(m.Parts) == 0 {
		return json.Marshal(plain(m))
	}
	return json.Marshal(struct {
		plain
		Content []Part `json:"content"`
	}{plain(m), m.Parts})
}

// ToolCall is a call the model asks for.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Tool is a function offered to the model.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a tool.
type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters"`
}

// Result of one model call.
type Result struct {
	Content   string
	ToolCalls []ToolCall
	Finish    string
	Model     string
	TokensIn  int
	TokensOut int
	Endpoint  string // "primary" or "fallback"
}

// Events reports progress while a call runs. All fields may be nil.
type Events struct {
	Text    func(delta string)                   // answer text as it arrives
	Waiting func(reason string, d time.Duration) // the model is starting or busy
}

// Client calls the model.
type Client struct {
	Primary, Fallback Endpoint
	HTTP              *http.Client
	WakeDeadline      time.Duration // how long to wait for a starting model, default 3 min
	Temperature       float64
	MaxTokens         int
}

// ErrUnavailable means neither endpoint answered.
var ErrUnavailable = errors.New("no model available")

// Chat runs one completion with tools, streaming text through ev.
func (c *Client) Chat(ctx context.Context, msgs []Message, tools []Tool, ev Events) (Result, error) {
	if c.HTTP == nil {
		c.HTTP = &http.Client{} // no total timeout: answers stream for as long as they take
	}
	if c.WakeDeadline == 0 {
		c.WakeDeadline = 3 * time.Minute
	}
	res, err := c.call(ctx, c.Primary, msgs, tools, ev)
	if err == nil {
		res.Endpoint = "primary"
		return res, nil
	}
	if ctx.Err() != nil || c.Fallback.URL == "" || !retryable(err) {
		return res, err
	}
	res, err2 := c.call(ctx, c.Fallback, msgs, tools, ev)
	if err2 != nil {
		return res, fmt.Errorf("%w: primary: %v; fallback: %v", ErrUnavailable, err, err2)
	}
	res.Endpoint = "fallback"
	return res, nil
}

type statusErr struct {
	code int
	body string
}

func (e *statusErr) Error() string {
	return fmt.Sprintf("model endpoint answered %d: %s", e.code, e.body)
}

// retryable: worth trying the fallback (connection errors, 5xx, a wake that took too long), not a bad request.
func retryable(err error) bool {
	var se *statusErr
	if errors.As(err, &se) {
		return se.code >= 500 || se.code == 429
	}
	return true
}

func (c *Client) call(ctx context.Context, ep Endpoint, msgs []Message, tools []Tool, ev Events) (Result, error) {
	body := map[string]any{"model": ep.Model, "messages": msgs, "stream": true, "stream_options": map[string]any{"include_usage": true}}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	if c.Temperature > 0 {
		body["temperature"] = c.Temperature
	}
	if c.MaxTokens > 0 {
		body["max_tokens"] = c.MaxTokens
	}
	payload, _ := json.Marshal(body)
	deadline := time.Now().Add(c.WakeDeadline)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(ep.URL, "/")+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			return Result{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if ep.Key != "" {
			req.Header.Set("Authorization", "Bearer "+ep.Key)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return Result{}, err
		}
		if resp.StatusCode == http.StatusServiceUnavailable && time.Now().Before(deadline) {
			// a gateway is waking the model, or the GPU is busy: wait as asked
			wait := 10 * time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
				wait = time.Duration(min(s, 60)) * time.Second
			}
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
			resp.Body.Close()
			if ev.Waiting != nil {
				ev.Waiting(strings.TrimSpace(string(msg)), wait)
			}
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		if resp.StatusCode/100 != 2 {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
			resp.Body.Close()
			return Result{}, &statusErr{resp.StatusCode, strings.TrimSpace(string(msg))}
		}
		defer resp.Body.Close()
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
			return parseWhole(resp.Body, ev)
		}
		return parseStream(resp.Body, ev)
	}
}

type delta struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		Message *struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Model string `json:"model"`
	Usage *struct {
		Prompt     int `json:"prompt_tokens"`
		Completion int `json:"completion_tokens"`
	} `json:"usage"`
}

// thinkFilter drops <think>...</think> blocks that some models put into the visible answer.
type thinkFilter struct {
	in  bool
	buf string
}

func (f *thinkFilter) feed(s string) string {
	f.buf += s
	out := ""
	for {
		if f.in {
			i := strings.Index(f.buf, "</think>")
			if i < 0 {
				// keep a tail that might be the start of the closing tag
				if len(f.buf) > 8 {
					f.buf = f.buf[len(f.buf)-8:]
				}
				return out
			}
			f.buf, f.in = f.buf[i+len("</think>"):], false
			continue
		}
		i := strings.Index(f.buf, "<think>")
		if i < 0 {
			// hold back a possible partial "<think" at the end
			keep := 0
			for k := 1; k < len("<think>") && k <= len(f.buf); k++ {
				if strings.HasPrefix("<think>", f.buf[len(f.buf)-k:]) {
					keep = k
				}
			}
			out += f.buf[:len(f.buf)-keep]
			f.buf = f.buf[len(f.buf)-keep:]
			return out
		}
		out += f.buf[:i]
		f.buf, f.in = f.buf[i+len("<think>"):], true
	}
}

func (f *thinkFilter) flush() string {
	if f.in {
		return ""
	}
	s := f.buf
	f.buf = ""
	return s
}

func parseStream(r io.Reader, ev Events) (Result, error) {
	var res Result
	var text strings.Builder
	calls := map[int]*ToolCall{}
	order := []int{}
	tf := &thinkFilter{}
	emit := func(s string) {
		if s == "" {
			return
		}
		text.WriteString(s)
		if ev.Text != nil {
			ev.Text(s)
		}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var d delta
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			continue
		}
		if d.Model != "" {
			res.Model = d.Model
		}
		if d.Usage != nil {
			res.TokensIn, res.TokensOut = d.Usage.Prompt, d.Usage.Completion
		}
		for _, ch := range d.Choices {
			emit(tf.feed(ch.Delta.Content))
			for _, tc := range ch.Delta.ToolCalls {
				c, ok := calls[tc.Index]
				if !ok {
					c = &ToolCall{Type: "function"}
					calls[tc.Index] = c
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					c.ID = tc.ID
				}
				c.Function.Name += tc.Function.Name
				c.Function.Arguments += tc.Function.Arguments
			}
			if ch.FinishReason != "" {
				res.Finish = ch.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("reading the model's stream: %w", err)
	}
	emit(tf.flush())
	res.Content = strings.TrimSpace(text.String())
	for i, idx := range order {
		c := calls[idx]
		if c.ID == "" {
			c.ID = fmt.Sprintf("call_%d", i)
		}
		res.ToolCalls = append(res.ToolCalls, *c)
	}
	return res, nil
}

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

func parseWhole(r io.Reader, ev Events) (Result, error) {
	var d delta
	if err := json.NewDecoder(r).Decode(&d); err != nil {
		return Result{}, err
	}
	res := Result{Model: d.Model}
	if d.Usage != nil {
		res.TokensIn, res.TokensOut = d.Usage.Prompt, d.Usage.Completion
	}
	for _, ch := range d.Choices {
		if ch.Message != nil {
			res.Content = strings.TrimSpace(thinkRe.ReplaceAllString(ch.Message.Content, ""))
			res.ToolCalls = ch.Message.ToolCalls
		}
		res.Finish = ch.FinishReason
	}
	if ev.Text != nil && res.Content != "" {
		ev.Text(res.Content)
	}
	return res, nil
}
