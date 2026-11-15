package speech

import (
	"strings"
	"testing"
)

func TestSpeakable(t *testing.T) {
	md := "## Weather tomorrow\n\n- **Bergen**: rain, 8 °C\n- Oslo: sun\n\nSee [yr.no](https://www.yr.no/) or https://met.no.\n\n" +
		"```go\nfmt.Println(\"skip me\")\n```\n\n| City | Temp |\n|---|---|\n| Bergen | 8 |\n\n> Bring an `umbrella`"
	got := Speakable(md)
	want := "Weather tomorrow.\n\nBergen: rain, 8 °C.\nOslo: sun.\n\nSee yr.no or.\n\nCity, Temp.\nBergen, 8.\n\nBring an umbrella."
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(Speakable("snake_case_name stays"), "snakecase") {
		t.Fatal("underscores inside words must stay")
	}
}

func TestParts(t *testing.T) {
	text := strings.Repeat("This is one sentence of a long answer. ", 30) + "Last one!"
	parts := Parts(text, 400)
	if len(parts[0]) > 160 {
		t.Fatal("the first part must be short, so playing starts quickly:", len(parts[0]))
	}
	total := 0
	for i, p := range parts {
		if len(p) > 400 {
			t.Fatal("part too long", i, len(p))
		}
		if !strings.HasSuffix(p, ".") && !strings.HasSuffix(p, "!") {
			t.Fatal("parts end on whole sentences:", p)
		}
		total += len(p)
	}
	if !strings.HasSuffix(parts[len(parts)-1], "Last one!") || total < len(strings.TrimSpace(text))-len(parts) {
		t.Fatal("nothing is lost", parts)
	}
	if p := Parts("Hei.\nHvordan går det?", 400); len(p) != 1 || p[0] != "Hei. Hvordan går det?" {
		t.Fatalf("%q", p)
	}
	if p := Parts("", 400); len(p) != 0 {
		t.Fatal(p)
	}
}
