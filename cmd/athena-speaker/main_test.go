package main

import "testing"

func TestParseVolume(t *testing.T) {
	for in, want := range map[string][2]any{
		"Volume: 0.85\n":         {0.85, false},
		"Volume: 0.40 [MUTED]\n": {0.40, true},
		"Volume: 1.00":           {1.0, false},
	} {
		v, muted, ok := parseVolume(in)
		if !ok || v != want[0].(float64) || muted != want[1].(bool) {
			t.Errorf("%q: %v %v %v", in, v, muted, ok)
		}
	}
	if _, _, ok := parseVolume("Object not found"); ok {
		t.Fatal("no volume in that")
	}
}
