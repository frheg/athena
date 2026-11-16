package main

import (
	"context"
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
}

type clip struct {
	gen int
	wav []byte
}

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
				v.ready <- clip{gen, w}
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
		v.mu.Unlock()
		if err := play(ctx, v.a.player, c.wav); err != nil && ctx.Err() == nil {
			v.a.note("could not play: " + err.Error())
		}
		cancel()
		v.mu.Lock()
		v.playing, v.env, v.cancel = false, nil, nil
		v.mu.Unlock()
		v.finished()
	}
}
