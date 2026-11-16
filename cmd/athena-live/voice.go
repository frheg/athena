package main

import (
	"context"
	"strings"
	"sync"
	"time"
)

// voice speaks sentences in order: one goroutine fetches the next sentence's audio from Athena while another plays the
// current one, so there is no pause between them. stop drops everything at once (a new turn, "stopp").
type voice struct {
	a *app

	mu       sync.Mutex
	queue    []string
	gen      int // raised by stop: work from an older generation is dropped
	fetching bool
	ready    chan clip
	playing  bool
	env      []float64 // the loudness of what plays, per 50 ms
	since    time.Time
	cancel   context.CancelFunc
	done     []func()
	wake     chan struct{}
	said     []said // the last minute of it
}

type clip struct {
	gen  int
	wav  []byte
	text string
}

// said is something it said, and when: the microphone hears it too.
type said struct {
	text     string
	from, to time.Time // to is zero while it plays
}

// echoTail is how long after playing the microphone can still hear it (Bluetooth delay, the room).
const echoTail = 600 * time.Millisecond

func newVoice(a *app) *voice {
	v := &voice{a: a, ready: make(chan clip, 2), wake: make(chan struct{}, 1)}
	go v.fetch()
	go v.play()
	return v
}

func (v *voice) say(text string) {
	v.mu.Lock()
	v.queue = append(v.queue, text)
	v.mu.Unlock()
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

func (v *voice) stop() {
	v.mu.Lock()
	v.gen++
	v.queue = nil
	if v.cancel != nil {
		v.cancel()
	}
	v.mu.Unlock()
	for {
		select {
		case <-v.ready:
		default:
			v.finished()
			return
		}
	}
}

// busy reports whether anything is said or about to be; the caller holds mu.
func (v *voice) busy() bool {
	return len(v.queue) > 0 || v.fetching || len(v.ready) > 0 || v.playing
}

// state reports whether it speaks, and how far its beak is open (0 to 1).
func (v *voice) state() (bool, float64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	mouth := 0.0
	if v.playing && len(v.env) > 0 {
		i := int(time.Since(v.since) / (50 * time.Millisecond))
		if i < len(v.env) {
			mouth = v.env[i]
		}
	}
	return v.busy(), mouth
}

// whenDone runs f once nothing is said any more (at once if nothing is).
func (v *voice) whenDone(f func()) {
	v.mu.Lock()
	if !v.busy() {
		v.mu.Unlock()
		f()
		return
	}
	v.done = append(v.done, f)
	v.mu.Unlock()
}

// finished runs what waited for the voice to be done, if it is.
func (v *voice) finished() {
	v.mu.Lock()
	if v.busy() {
		v.mu.Unlock()
		return
	}
	fs := v.done
	v.done = nil
	v.mu.Unlock()
	for _, f := range fs {
		f()
	}
}

func (v *voice) fetch() {
	for {
		select {
		case <-v.a.ctx.Done():
			return
		case <-v.wake:
		}
		for {
			v.mu.Lock()
			if len(v.queue) == 0 {
				v.mu.Unlock()
				break
			}
			text, gen := v.queue[0], v.gen
			v.queue = v.queue[1:]
			v.fetching = true
			v.mu.Unlock()
			w, err := say(v.a.ctx, v.a.c, text)
			v.mu.Lock()
			v.fetching = false
			stale := gen != v.gen
			v.mu.Unlock()
			if err != nil {
				v.a.note("could not speak: " + err.Error())
			} else if !stale {
				v.ready <- clip{gen, w, text}
			}
			v.finished()
		}
	}
}

func (v *voice) play() {
	for {
		var c clip
		select {
		case <-v.a.ctx.Done():
			return
		case c = <-v.ready:
		}
		env, _ := envelope(c.wav)
		ctx, cancel := context.WithCancel(v.a.ctx)
		v.mu.Lock()
		if c.gen != v.gen {
			v.mu.Unlock()
			cancel()
			continue
		}
		v.playing, v.env, v.since, v.cancel = true, env, time.Now(), cancel
		v.said = append(v.said, said{text: c.text, from: v.since})
		for len(v.said) > 0 && !v.said[0].to.IsZero() && time.Since(v.said[0].to) > time.Minute {
			v.said = v.said[1:]
		}
		v.mu.Unlock()
		if err := play(ctx, v.a.player, c.wav); err != nil && ctx.Err() == nil {
			v.a.note("could not play: " + err.Error())
		}
		cancel()
		v.mu.Lock()
		v.playing, v.env, v.cancel = false, nil, nil
		v.said[len(v.said)-1].to = time.Now().Add(echoTail)
		v.mu.Unlock()
		v.finished()
	}
}

// echo tells whether speech heard from..to overlaps what it said itself, and returns what it said then.
func (v *voice) echo(from, to time.Time) (overlaps bool, spoken string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	var b strings.Builder
	for _, s := range v.said {
		end := s.to
		if end.IsZero() {
			end = time.Now().Add(echoTail)
		}
		if s.from.Before(to) && end.After(from) {
			overlaps = true
			b.WriteString(" " + s.text)
		}
	}
	return overlaps, b.String()
}

// sameWords tells whether most of the words heard (at least half, and at least two) are among the words said.
func sameWords(heard, saidText string) bool {
	h := word.FindAllString(strings.ToLower(heard), -1)
	if len(h) < 2 {
		return false
	}
	s := map[string]bool{}
	for _, w := range word.FindAllString(strings.ToLower(saidText), -1) {
		s[w] = true
	}
	n := 0
	for _, w := range h {
		if s[w] {
			n++
		}
	}
	return n*2 >= len(h)
}
