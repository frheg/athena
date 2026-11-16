package main

import "testing"

func TestFindWake(t *testing.T) {
	// what speech recognition made of "Athena, ..." in tests with Piper voices, and real ways to say it
	for in, want := range map[string]string{
		"Athena, hva blir været i Bergen i morgen?": "hva blir været i Bergen i morgen?",
		"Atena: Hva blir verre i Bergen?":           "Hva blir verre i Bergen?",
		"Atenna. Hva blir været?":                   "Hva blir været?",
		"Hei, Atena. Kan du minne meg på noe?":      "Kan du minne meg på noe?",
		"Adena, hva er klokka?":                     "hva er klokka?",
		"Ethina, what is on my calendar today?":     "what is on my calendar today?",
		"Adina, slå av lyset.":                      "slå av lyset.",
		"Ok A tina, stopp.":                         "stopp.",
		"Athena Stop":                               "Stop",
		"Athena.":                                   "",
	} {
		got, ok := findWake(in, "Athena")
		if !ok || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"Vi spiser middag om en halvtime.",
		"Andre ganger er det fint.",
		"What is the weather like?",
		"Jeg tenker at vi kanskje kunne snakke om Athena senere.", // the name too far in: not a wake word
		"",
	} {
		if got, ok := findWake(in, "Athena"); ok {
			t.Errorf("%q: should not wake, got %q", in, got)
		}
	}
}

func TestHeardNothing(t *testing.T) {
	for in, want := range map[string]bool{
		"":                            true,
		"[Music]":                     true,
		"(musikk)":                    true,
		"Teksting av Nicolai Winther": true,
		"Takk for at du så på!":       true,
		"Thank you for watching.":     true,
		"Athena, hva er klokka?":      false,
		"Hei":                         false,
	} {
		if got := heardNothing(in); got != want {
			t.Errorf("%q: %v", in, got)
		}
	}
}

func TestYesNoStop(t *testing.T) {
	for in, want := range map[string][2]bool{
		"Ja, gjør det.": {true, true}, "Nei takk": {false, true}, "Yes please": {true, true}, "ikke gjør det": {false, true},
		"hva sa du?": {false, false},
	} {
		if y, ok := yesNo(in); y != want[0] || ok != want[1] {
			t.Errorf("%q: %v %v", in, y, ok)
		}
	}
	if !isStop("Stopp.") || !isStop("stop it") || isStop("stopp å snakke om været og fortell meg om noe annet") || isStop("Hva er klokka?") {
		t.Fatal("isStop")
	}
	if !norwegian("Hva blir været i morgen?") || norwegian("What is the weather tomorrow?") {
		t.Fatal("norwegian")
	}
}
