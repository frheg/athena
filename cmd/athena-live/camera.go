package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/frheg/athena/internal/client"
)

// analysis is one result of camview's pose and gesture analysis (homelab services/pose).
type analysis struct {
	Pose  [][3]float64 `json:"pose"`
	Poses []string     `json:"poses"`
	Hands []struct {
		Side    string       `json:"side"`
		Gesture string       `json:"gesture"`
		Score   float64      `json:"score"`
		Points  [][2]float64 `json:"points"`
	} `json:"hands"`
}

// camera follows camview (camview -web -analyze ...): a small picture to draw and what it sees. Watching its
// stream also keeps its camera and analysis on while live mode runs.
type camera struct {
	url string

	mu       sync.Mutex
	frame    image.Image
	frameAt  time.Time
	seen     analysis
	seenAt   time.Time
	gesture  string    // the gesture held now ("left hand: open palm"), "" for none
	since    time.Time // since when it is held
	reported string    // the last steady gesture sent on gestures
	gestures chan string
}

func newCamera(url string) *camera {
	return &camera{url: strings.TrimRight(url, "/"), gestures: make(chan string, 4)}
}

func (c *camera) run(ctx context.Context) {
	go c.loop(ctx, "/stream", c.readStream)
	c.loop(ctx, "/analysis", c.readAnalysis)
}

// loop keeps one connection to camview open, again and again, until ctx ends.
func (c *camera) loop(ctx context.Context, path string, read func(context.Context, io.Reader, string) error) {
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.url+path, nil)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			if res.StatusCode == 200 {
				_ = read(ctx, res.Body, res.Header.Get("Content-Type"))
			}
			res.Body.Close()
		}
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}
	}
}

// readStream decodes about every third picture of the MJPEG stream, enough for a small ASCII view.
func (c *camera) readStream(ctx context.Context, body io.Reader, ctype string) error {
	_, params, err := mime.ParseMediaType(ctype)
	if err != nil || params["boundary"] == "" {
		return fmt.Errorf("not an MJPEG stream")
	}
	mr := multipart.NewReader(body, params["boundary"])
	for n := 0; ctx.Err() == nil; n++ {
		p, err := mr.NextPart()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(p, 8<<20))
		if err != nil {
			return err
		}
		if n%3 != 0 {
			continue
		}
		img, err := jpeg.Decode(bytes.NewReader(b))
		if err != nil {
			continue
		}
		c.mu.Lock()
		c.frame, c.frameAt = img, time.Now()
		c.mu.Unlock()
	}
	return ctx.Err()
}

func (c *camera) readAnalysis(ctx context.Context, body io.Reader, _ string) error {
	return client.ReadEvents(body, func(ev client.Event) {
		var a analysis
		if json.Unmarshal(ev.Data, &a) != nil {
			return
		}
		now := time.Now()
		g := ""
		for _, h := range a.Hands {
			if h.Gesture != "" && h.Score >= 0.6 {
				g = h.Side + " hand: " + h.Gesture
				break
			}
		}
		c.mu.Lock()
		c.seen, c.seenAt = a, now
		if g != c.gesture {
			c.gesture, c.since = g, now
		}
		steady := c.gesture != "" && now.Sub(c.since) >= 600*time.Millisecond && c.gesture != c.reported
		if c.gesture == "" {
			c.reported = ""
		}
		if steady {
			c.reported = c.gesture
		}
		c.mu.Unlock()
		if steady {
			select {
			case c.gestures <- g:
			default:
			}
		}
	})
}

// summary says in words what the camera sees now, for Athena ("" when the camera or its analysis is off).
func (c *camera) summary() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.seenAt) > 2*time.Second {
		return ""
	}
	if c.seen.Pose == nil && len(c.seen.Hands) == 0 {
		return "nobody is in view"
	}
	parts := []string{}
	if c.seen.Pose != nil {
		parts = append(parts, "a person is in view")
	}
	parts = append(parts, c.seen.Poses...)
	for _, h := range c.seen.Hands {
		if h.Gesture != "" && h.Score >= 0.6 {
			parts = append(parts, h.Side+" hand: "+h.Gesture)
		}
	}
	sort.Strings(parts[1:])
	return strings.Join(parts, "; ")
}

var ramp = []byte(" .:-=+*#%@")

// ascii draws the newest picture in w x h characters, mirrored like a mirror, with the pose's joints and the hands
// marked. It returns nil when there is no recent picture.
func (c *camera) ascii(w, h int) []string {
	c.mu.Lock()
	img, at, seen, seenAt := c.frame, c.frameAt, c.seen, c.seenAt
	c.mu.Unlock()
	if img == nil || time.Since(at) > 3*time.Second || w < 4 || h < 2 {
		return nil
	}
	b := img.Bounds()
	rows := make([][]byte, h)
	for y := 0; y < h; y++ {
		rows[y] = make([]byte, w)
		for x := 0; x < w; x++ {
			px := b.Min.X + (w-1-x)*b.Dx()/w // mirrored
			py := b.Min.Y + y*b.Dy()/h
			r, g, bl, _ := img.At(px, py).RGBA()
			lum := (2126*r + 7152*g + 722*bl) / 10000 >> 8
			rows[y][x] = ramp[int(lum)*(len(ramp)-1)/255]
		}
	}
	mark := func(x, y float64, ch byte) {
		cx, cy := int((1-x)*float64(w)), int(y*float64(h))
		if cx >= 0 && cx < w && cy >= 0 && cy < h {
			rows[cy][cx] = ch
		}
	}
	if time.Since(seenAt) < time.Second {
		if seen.Pose != nil {
			for _, i := range []int{0, 11, 12, 13, 14, 15, 16, 23, 24} { // nose, shoulders, elbows, wrists, hips
				if i < len(seen.Pose) && seen.Pose[i][2] > 0.5 {
					mark(seen.Pose[i][0], seen.Pose[i][1], 'O')
				}
			}
		}
		for _, hd := range seen.Hands {
			for _, p := range hd.Points {
				mark(p[0], p[1], '+')
			}
		}
	}
	out := make([]string, h)
	for i, r := range rows {
		out[i] = string(r)
	}
	return out
}
