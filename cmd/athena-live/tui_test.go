package main

import (
	"strings"
	"testing"
	"time"
)

func TestRenderFits(t *testing.T) {
	start := time.Now()
	v := &view{mood: speaking, mouth: 0.8, started: start, seen: "a person is in view; right hand: thumbs up",
		camera: []string{strings.Repeat("#", 56)},
		lines:  []line{{"you", "Athena, hva blir været i morgen?"}, {"athena", strings.Repeat("Det blir sol og fint vær. ", 20)}}}
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 20}} {
		out := render(v, start.Add(time.Second), size[0], size[1])
		rows := strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n")
		if len(rows) != size[1] {
			t.Fatalf("%dx%d: %d rows", size[0], size[1], len(rows))
		}
		for i, r := range rows {
			if i == 0 {
				r = strings.TrimPrefix(r, "\x1b[H")
			}
			if w := width(r); w != size[0] {
				t.Fatalf("%dx%d: row %d is %d wide: %q", size[0], size[1], i, w, r)
			}
		}
	}
}

func TestOwl(t *testing.T) {
	v := &view{started: time.Now()}
	for _, m := range []mood{asleep, listening, hearing, thinking, speaking, asking} {
		v.mood = m
		rows := owl(v, v.started.Add(1500*time.Millisecond))
		for _, r := range rows[1:] {
			if width(r) != 22 && width(r) != 23 {
				t.Fatalf("mood %d: row %q is %d wide", m, r, width(r))
			}
		}
	}
	if w := wrap("ett to tre fire", 7); len(w) != 3 || w[0] != "ett to" {
		t.Fatal(w)
	}
}
