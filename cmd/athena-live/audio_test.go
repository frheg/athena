package main

import (
	"math"
	"testing"
	"time"
)

func tone(ms int, amp float64) [][]int16 {
	var out [][]int16
	for f := 0; f < ms/20; f++ {
		fr := make([]int16, frameLen)
		for i := range fr {
			fr[i] = int16(amp * 32767 * math.Sin(float64(f*frameLen+i)*2*math.Pi*220/rate))
		}
		out = append(out, fr)
	}
	return out
}

func TestVAD(t *testing.T) {
	v := newVAD()
	var got [][]int16
	feed := func(frames [][]int16) {
		for _, f := range frames {
			if s, _ := v.add(f); s != nil {
				got = append(got, s)
			}
		}
	}
	feed(tone(2000, 0.001)) // quiet room: learns the background
	feed(tone(1200, 0.2))   // someone speaks for 1.2 s
	feed(tone(1000, 0.001))
	if len(got) != 1 {
		t.Fatalf("one stretch of speech, got %d", len(got))
	}
	secs := float64(len(got[0])) / rate
	if secs < 1.2 || secs > 2.4 { // the speech, the 300 ms before it and the 700 ms after
		t.Fatalf("length %.2f s", secs)
	}
	feed(tone(100, 0.2)) // a knock: too short
	feed(tone(1000, 0.001))
	if len(got) != 1 {
		t.Fatal("a 100 ms sound is not speech")
	}
}

func TestWAVAndEnvelope(t *testing.T) {
	var s []int16
	for _, f := range append(tone(500, 0.5), tone(500, 0)...) {
		s = append(s, f...)
	}
	w := wav(s, rate)
	env, err := envelope(w)
	if err != nil || len(env) != 20 {
		t.Fatal(len(env), err)
	}
	if env[2] < 0.5 || env[15] > 0.01 {
		t.Fatal("loud first half, silent second half:", env)
	}
}

func TestSentences(t *testing.T) {
	var s sentences
	var got []string
	for _, chunk := range []string{"Hei! Det blir r", "egn i Bergen i morgen, rundt åtte grader. Ta", " med paraply. Ok.", " Ha en fin dag"} {
		got = append(got, s.add(chunk)...)
	}
	rest := s.flush()
	want := []string{"Hei!", "Det blir regn i Bergen i morgen, rundt åtte grader.", "Ta med paraply."}
	if len(got) != len(want) || rest != "Ok. Ha en fin dag" { // "Ok." is too short alone: it waits for the next
		t.Fatalf("%q / %q", got, rest)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%q", got)
		}
	}
	if s.add("Klokka er 3.5 nå") != nil || s.flush() != "Klokka er 3.5 nå" {
		t.Fatal("a point inside a number does not end a sentence")
	}
}

func TestEcho(t *testing.T) {
	v := &voice{}
	t0 := time.Now().Add(-10 * time.Second)
	v.said = []said{{text: "Klokka er halv tre om natta, og det er på tide å sove.", from: t0, to: t0.Add(4 * time.Second)}}
	if over, _ := v.echo(t0.Add(5*time.Second), t0.Add(6*time.Second)); over {
		t.Fatal("after it spoke (and the tail) is not an overlap")
	}
	over, spoken := v.echo(t0.Add(time.Second), t0.Add(3*time.Second))
	if !over {
		t.Fatal("while it spoke is an overlap")
	}
	if !sameWords("Klokka er halv tre om natta", spoken) {
		t.Fatal("its own words, heard back, are its own voice")
	}
	if sameWords("Athena, stopp nå", spoken) || sameWords("Hva med været?", spoken) {
		t.Fatal("other words are someone speaking over it")
	}
	// still playing: the end is open
	v.said = append(v.said, said{text: "Vi kan snakke mer i morgen.", from: time.Now().Add(-time.Second)})
	if over, _ := v.echo(time.Now().Add(-500*time.Millisecond), time.Now()); !over {
		t.Fatal("what plays now overlaps")
	}
}
