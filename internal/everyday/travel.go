package everyday

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Stop is a public transport stop or address found by Entur.
type Stop struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

func (t *Tools) entur() map[string]string {
	return map[string]string{"ET-Client-Name": t.cfg.EnturClient}
}

// findStop resolves a name to an Entur place (stop or address).
func (t *Tools) findStop(ctx context.Context, name string, stopsOnly bool) (Stop, error) {
	v := url.Values{"text": {name}, "lang": {"no"}, "size": {"1"}}
	if stopsOnly {
		v.Set("layers", "venue")
	}
	b, err := t.fetch(ctx, "api.entur.io", "/geocoder/v1/autocomplete?"+v.Encode(), t.entur(), nil, 24*time.Hour, 0)
	if err != nil {
		return Stop{}, err
	}
	var r struct {
		Features []struct {
			Geometry struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
			Properties struct {
				ID    string `json:"id"`
				Label string `json:"label"`
			} `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Stop{}, err
	}
	if len(r.Features) == 0 || len(r.Features[0].Geometry.Coordinates) < 2 {
		return Stop{}, fmt.Errorf("Entur found no stop or address called %q", name)
	}
	f := r.Features[0]
	return Stop{ID: f.Properties.ID, Name: f.Properties.Label, Lat: f.Geometry.Coordinates[1], Lon: f.Geometry.Coordinates[0]}, nil
}

func (t *Tools) graphql(ctx context.Context, query string, out any, ttl time.Duration) error {
	body, _ := json.Marshal(map[string]string{"query": query})
	b, err := t.fetch(ctx, "api.entur.io", "/journey-planner/v3/graphql", t.entur(), body, ttl, 0)
	if err != nil {
		return err
	}
	var r struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	if len(r.Errors) > 0 {
		return fmt.Errorf("Entur: %s", r.Errors[0].Message)
	}
	return json.Unmarshal(r.Data, out)
}

func placeArg(s Stop) string {
	if strings.HasPrefix(s.ID, "NSR:StopPlace:") {
		return fmt.Sprintf(`{place: %q}`, s.ID)
	}
	return fmt.Sprintf(`{coordinates: {latitude: %f, longitude: %f}}`, s.Lat, s.Lon)
}

func (t *Tools) place(ctx context.Context, where string) (Stop, error) {
	if p := strings.Split(where, ","); len(p) == 2 {
		lat, e1 := strconv.ParseFloat(strings.TrimSpace(p[0]), 64)
		lon, e2 := strconv.ParseFloat(strings.TrimSpace(p[1]), 64)
		if e1 == nil && e2 == nil {
			return Stop{Name: where, Lat: lat, Lon: lon}, nil
		}
	}
	return t.findStop(ctx, where, false)
}

// Leg is one part of a trip.
type Leg struct {
	Mode  string `json:"mode"`
	Line  string `json:"line,omitempty"`
	From  string `json:"from"`
	To    string `json:"to"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// Trip is one way to travel.
type Trip struct {
	Start      string `json:"start"`
	End        string `json:"end"`
	Minutes    int    `json:"minutes"`
	WalkMeters int    `json:"walk_meters"`
	Legs       []Leg  `json:"legs"`
}

// TripsOut is the result of travel_trip.
type TripsOut struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Trips  []Trip `json:"trips"`
	Source string `json:"source"`
}

// Trips plans public transport trips (Norway) between two places, names or "lat,lon".
func (t *Tools) Trips(ctx context.Context, from, to, when string, arriveBy bool, n int) (TripsOut, error) {
	a, err := t.place(ctx, from)
	if err != nil {
		return TripsOut{}, err
	}
	b, err := t.place(ctx, to)
	if err != nil {
		return TripsOut{}, err
	}
	if n <= 0 || n > 6 {
		n = 3
	}
	extra := ""
	if when != "" {
		at, err := time.Parse(time.RFC3339, when)
		if err != nil {
			loc, _ := time.LoadLocation("Europe/Oslo")
			if at, err = time.ParseInLocation("2006-01-02 15:04", when, loc); err != nil {
				return TripsOut{}, fmt.Errorf("when must look like 2026-11-16 08:30 (Norwegian time) or RFC 3339")
			}
		}
		extra = fmt.Sprintf(", dateTime: %q", at.Format(time.RFC3339))
		if arriveBy {
			extra += ", arriveBy: true"
		}
	}
	q := fmt.Sprintf(`{ trip(from: %s, to: %s, numTripPatterns: %d%s) { tripPatterns { expectedStartTime expectedEndTime duration walkDistance
	  legs { mode expectedStartTime expectedEndTime fromPlace { name } toPlace { name } line { publicCode name } } } } }`, placeArg(a), placeArg(b), n, extra)
	var r struct {
		Trip struct {
			Patterns []struct {
				Duration int     `json:"duration"`
				Walk     float64 `json:"walkDistance"`
				Legs     []struct {
					Mode  string                `json:"mode"`
					Start string                `json:"expectedStartTime"`
					End   string                `json:"expectedEndTime"`
					From  struct{ Name string } `json:"fromPlace"`
					To    struct{ Name string } `json:"toPlace"`
					Line  *struct {
						PublicCode string `json:"publicCode"`
						Name       string `json:"name"`
					} `json:"line"`
				} `json:"legs"`
			} `json:"tripPatterns"`
		} `json:"trip"`
	}
	if err := t.graphql(ctx, q, &r, 2*time.Minute); err != nil {
		return TripsOut{}, err
	}
	out := TripsOut{From: a.Name, To: b.Name, Trips: []Trip{}, Source: "Entur"}
	for _, p := range r.Trip.Patterns {
		tr := Trip{Minutes: (p.Duration + 59) / 60, WalkMeters: int(p.Walk)}
		for i, l := range p.Legs {
			leg := Leg{Mode: l.Mode, From: l.From.Name, To: l.To.Name, Start: clock(l.Start), End: clock(l.End)}
			if l.Line != nil {
				leg.Line = strings.TrimSpace(l.Line.PublicCode + " " + l.Line.Name)
			}
			if i == 0 {
				tr.Start = clock(l.Start)
			}
			tr.End = clock(l.End)
			tr.Legs = append(tr.Legs, leg)
		}
		out.Trips = append(out.Trips, tr)
	}
	return out, nil
}

// Departure is one departure from a stop.
type Departure struct {
	Time        string `json:"time"`
	Scheduled   string `json:"scheduled"`
	Realtime    bool   `json:"realtime"`
	Line        string `json:"line"`
	Mode        string `json:"mode"`
	Destination string `json:"destination"`
	Platform    string `json:"platform,omitempty"`
}

// DeparturesOut is the result of travel_departures.
type DeparturesOut struct {
	Stop       string      `json:"stop"`
	Departures []Departure `json:"departures"`
	Source     string      `json:"source"`
}

// Departures lists the next departures from a stop (name or NSR:StopPlace id).
func (t *Tools) Departures(ctx context.Context, stop string, n int) (DeparturesOut, error) {
	id := strings.TrimSpace(stop)
	if !strings.HasPrefix(id, "NSR:StopPlace:") {
		s, err := t.findStop(ctx, stop, true)
		if err != nil {
			return DeparturesOut{}, err
		}
		id = s.ID
	}
	if n <= 0 || n > 20 {
		n = 8
	}
	q := fmt.Sprintf(`{ stopPlace(id: %q) { name estimatedCalls(numberOfDepartures: %d) { realtime aimedDepartureTime expectedDepartureTime
	  destinationDisplay { frontText } quay { publicCode } serviceJourney { line { publicCode transportMode } } } } }`, id, n)
	var r struct {
		StopPlace *struct {
			Name  string `json:"name"`
			Calls []struct {
				Realtime bool   `json:"realtime"`
				Aimed    string `json:"aimedDepartureTime"`
				Expected string `json:"expectedDepartureTime"`
				Dest     struct {
					FrontText string `json:"frontText"`
				} `json:"destinationDisplay"`
				Quay *struct {
					PublicCode string `json:"publicCode"`
				} `json:"quay"`
				SJ struct {
					Line struct {
						PublicCode    string `json:"publicCode"`
						TransportMode string `json:"transportMode"`
					} `json:"line"`
				} `json:"serviceJourney"`
			} `json:"estimatedCalls"`
		} `json:"stopPlace"`
	}
	if err := t.graphql(ctx, q, &r, 30*time.Second); err != nil {
		return DeparturesOut{}, err
	}
	if r.StopPlace == nil {
		return DeparturesOut{}, fmt.Errorf("no stop %s", id)
	}
	out := DeparturesOut{Stop: r.StopPlace.Name, Departures: []Departure{}, Source: "Entur"}
	for _, c := range r.StopPlace.Calls {
		d := Departure{Time: clock(c.Expected), Scheduled: clock(c.Aimed), Realtime: c.Realtime, Line: c.SJ.Line.PublicCode,
			Mode: c.SJ.Line.TransportMode, Destination: c.Dest.FrontText}
		if c.Quay != nil {
			d.Platform = c.Quay.PublicCode
		}
		out.Departures = append(out.Departures, d)
	}
	return out, nil
}

// clock shortens an RFC 3339 time to "2006-01-02 15:04" in its own zone.
func clock(s string) string {
	if at, err := time.Parse(time.RFC3339, s); err == nil {
		return at.Format("2006-01-02 15:04")
	}
	return s
}
