package everyday

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/frheg/athena/internal/netguard"
)

func TestEval(t *testing.T) {
	ok := map[string]float64{"1+2*3": 7, "(1+2)*3": 9, "2^3^2": 512, "-2^2": 4, "10 % 4": 2, "sqrt(16)+abs(-3)": 7,
		"max(1, 5, 3)": 5, "1.5e3 / 3": 500, "round(pi*100)/100": 3.14, "2 × 3 ÷ 4": 1.5, "1_000 + 1": 1001}
	for e, want := range ok {
		if got, err := Eval(e); err != nil || math.Abs(got-want) > 1e-9 {
			t.Errorf("Eval(%q) = %v, %v; want %v", e, got, err, want)
		}
	}
	for _, e := range []string{"", "1/0", "2+", "(1", "import os", "x+1", "1 2", strings.Repeat("(", 60) + "1" + strings.Repeat(")", 60), "sqrt(1,2)"} {
		if _, err := Eval(e); err == nil {
			t.Errorf("Eval(%q) did not fail", e)
		}
	}
}

func TestUnitsTimeHolidays(t *testing.T) {
	for _, c := range []struct {
		v        float64
		from, to string
		want     float64
	}{{1000, "ft.lb", "kN.m", 1.3558179483314}, {100, "km/h", "m/s", 27.7777777778}, {1, "mil", "km", 10}, {100, "°C", "°F", 212},
		{32, "F", "C", 0}, {1, "bar", "psi", 14.5037737730}, {1, "GiB", "MiB", 1024}} {
		got, _, err := ConvertUnit(c.v, c.from, c.to)
		if err != nil || math.Abs(got-c.want) > 1e-6*math.Max(1, c.want) {
			t.Errorf("%v %s -> %s = %v, %v; want %v", c.v, c.from, c.to, got, err, c.want)
		}
	}
	if _, _, err := ConvertUnit(1, "kg", "m"); err == nil {
		t.Error("kg to m converted")
	}
	h, err := Holidays(2026, "NO")
	if err != nil || h[3].Date != "2026-04-05" || h[7].Date != "2026-05-14" || h[9].Date != "2026-05-25" {
		t.Errorf("holidays 2026: %+v %v", h, err)
	}
	n, err := TimeNow("Europe/Oslo", time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC))
	if err != nil || n.Time != "2026-11-15 13:00:00" || n.Weekday != "Sunday" || n.Week != 46 {
		t.Errorf("time now: %+v %v", n, err)
	}
	if _, err := TimeNow("Mars/Olympus", time.Now()); err == nil {
		t.Error("unknown zone accepted")
	}
}

func TestProviders(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	ts := func(h int) string { return now.Add(time.Duration(h) * time.Hour).Format(time.RFC3339) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/stedsnavn/v1/navn":
			_, _ = w.Write([]byte(`{"navn":[{"skrivemåte":"Bergen","navneobjekttype":"By","representasjonspunkt":{"nord":60.39323,"øst":5.3245},"kommuner":[{"kommunenavn":"Bergen"}],"fylker":[{"fylkesnavn":"Vestland"}]}]}`))
		case r.URL.Path == "/weatherapi/locationforecast/2.0/compact":
			if r.URL.Query().Get("lat") != "60.3932" || r.Header.Get("User-Agent") == "" {
				http.Error(w, "need 4 decimals and a user agent", 403)
				return
			}
			_, _ = w.Write([]byte(`{"properties":{"timeseries":[
{"time":"` + ts(0) + `","data":{"instant":{"details":{"air_temperature":4.3,"wind_speed":1.4}},"next_1_hours":{"summary":{"symbol_code":"lightrain_day"},"details":{"precipitation_amount":0.4}}}},
{"time":"` + ts(1) + `","data":{"instant":{"details":{"air_temperature":6.1,"wind_speed":3}},"next_1_hours":{"summary":{"symbol_code":"cloudy"},"details":{"precipitation_amount":0}}}}]}}`))
		case r.URL.Path == "/weatherapi/metalerts/2.0/current.json":
			_, _ = w.Write([]byte(`{"features":[{"properties":{"title":"Wind warning","description":"Strong wind."}}]}`))
		case r.URL.Path == "/api/data/EXR/B.EUR.NOK.SP" || r.URL.Path == "/api/data/EXR/B.JPY.NOK.SP":
			v, mult := "11.5", 1
			if strings.Contains(r.URL.Path, "JPY") {
				v, mult = "7.0", 0
			}
			_, _ = w.Write([]byte(`{"data":{"dataSets":[{"series":{"0:0:0:0":{"attributes":[0,0,` + string(rune('0'+mult)) + `,0],"observations":{"0":["` + v + `"]}}}}],
"structure":{"dimensions":{"observation":[{"values":[{"id":"2026-11-13"}]}]},"attributes":{"series":[{"id":"DECIMALS","values":[{"id":"4"}]},{"id":"CALCULATED","values":[{"id":"false"}]},{"id":"UNIT_MULT","values":[{"id":"2"},{"id":"0"}]},{"id":"COLLECTION","values":[{"id":"C"}]}]}}}}`))
		case r.URL.Path == "/geocoder/v1/autocomplete":
			_, _ = w.Write([]byte(`{"features":[{"geometry":{"coordinates":[5.334608,60.388106]},"properties":{"id":"NSR:StopPlace:62356","label":"Bergen busstasjon, Bergen"}}]}`))
		case r.URL.Path == "/journey-planner/v3/graphql":
			if r.Header.Get("ET-Client-Name") == "" {
				http.Error(w, "no client name", 400)
				return
			}
			_, _ = w.Write([]byte(`{"data":{"stopPlace":{"name":"Bergen busstasjon","estimatedCalls":[{"realtime":true,"aimedDepartureTime":"2026-11-15T18:00:00+01:00","expectedDepartureTime":"2026-11-15T18:02:00+01:00","destinationDisplay":{"frontText":"Fyllingsdalen"},"quay":{"publicCode":"A"},"serviceJourney":{"line":{"publicCode":"2","transportMode":"tram"}}}]}}}`))
		default:
			http.Error(w, "unknown "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()
	base := map[string]string{}
	for _, h := range Hosts {
		base[h] = srv.URL
	}
	tools := New(Config{UserAgent: "test", Base: base, Guard: netguard.Options{AllowPrivate: true}})
	ctx := context.Background()

	f, err := tools.Weather(ctx, "Bergen", 2, 1, "")
	if err != nil || f.Place != "Bergen" || len(f.Hours) != 2 || f.Hours[0].Conditions != "light rain" || len(f.Days) != 1 ||
		f.Days[0].MaxC != 6.1 || f.Days[0].PrecipMM != 0.4 || len(f.Warnings) != 1 {
		t.Fatalf("weather: %+v %v", f, err)
	}
	c, err := tools.Convert(ctx, 1000, "jpy", "EUR", "")
	if err != nil || math.Abs(c.Result-1000*0.07/11.5) > 0.01 || c.RateDate != "2026-11-13" {
		t.Fatalf("money: %+v %v", c, err)
	}
	if _, err := tools.Convert(ctx, 1, "EU R", "NOK", ""); err == nil {
		t.Error("bad currency accepted")
	}
	d, err := tools.Departures(ctx, "Bergen busstasjon", 3)
	if err != nil || d.Stop != "Bergen busstasjon" || len(d.Departures) != 1 || d.Departures[0].Time != "2026-11-15 18:02" || d.Departures[0].Platform != "A" {
		t.Fatalf("departures: %+v %v", d, err)
	}
	if tools.ParcelEnabled() {
		t.Error("parcel tracking on without a key")
	}
	real := New(Config{})
	if _, err := real.fetch(ctx, "example.com", "/", nil, nil, 0, 0); err == nil {
		t.Error("other host reachable")
	}
}
