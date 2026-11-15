package everyday

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Parcel is a tracked shipment.
type Parcel struct {
	Number string   `json:"number"`
	Status string   `json:"quoted_status"`
	Events []string `json:"quoted_events"`
	Source string   `json:"source"`
	Note   string   `json:"note"`
}

// TrackParcel asks Bring/Posten (needs a Mybring API key).
func (t *Tools) TrackParcel(ctx context.Context, number string) (Parcel, error) {
	if !t.ParcelEnabled() {
		return Parcel{}, fmt.Errorf("parcel tracking is not set up (no Bring API key)")
	}
	number = strings.TrimSpace(number)
	h := map[string]string{"X-Mybring-API-Uid": t.cfg.BringUID, "X-Mybring-API-Key": t.cfg.BringKey, "X-Bring-Client-URL": "https://github.com/frheg/athena"}
	b, err := t.fetch(ctx, "api.bring.com", "/tracking/api/v2/tracking.json?"+url.Values{"q": {number}, "lang": {"en"}}.Encode(), h, nil, 10*time.Minute, 0)
	if err != nil {
		return Parcel{}, err
	}
	var r struct {
		ConsignmentSet []struct {
			PackageSet []struct {
				StatusDescription string `json:"statusDescription"`
				EventSet          []struct {
					Description string `json:"description"`
					DisplayDate string `json:"displayDate"`
					DisplayTime string `json:"displayTime"`
					City        string `json:"city"`
				} `json:"eventSet"`
			} `json:"packageSet"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"consignmentSet"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Parcel{}, err
	}
	p := Parcel{Number: number, Source: "Bring/Posten", Note: Note, Events: []string{}}
	for _, c := range r.ConsignmentSet {
		if c.Error != nil {
			return Parcel{}, fmt.Errorf("Bring: %s", c.Error.Message)
		}
		for _, pk := range c.PackageSet {
			p.Status = pk.StatusDescription
			for _, e := range pk.EventSet {
				p.Events = append(p.Events, strings.TrimSpace(fmt.Sprintf("%s %s %s: %s", e.DisplayDate, e.DisplayTime, e.City, e.Description)))
			}
		}
	}
	return p, nil
}

// Register adds the tools to an MCP server.
func (t *Tools) Register(s *mcp.Server) {
	type whereIn struct {
		Where string `json:"where" jsonschema:"a place name, or coordinates as lat,lon"`
		Hours int    `json:"hours,omitempty" jsonschema:"hourly forecast for this many hours, default 12, at most 48"`
		Days  int    `json:"days,omitempty" jsonschema:"daily summary for this many days, default 3, at most 9"`
		Zone  string `json:"zone,omitempty" jsonschema:"IANA time zone for the times, default Europe/Oslo"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "weather_forecast", Description: "Weather forecast anywhere (MET Norway): the next hours, a daily summary and warnings."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in whereIn) (*mcp.CallToolResult, Forecast, error) {
			f, err := t.Weather(ctx, in.Where, in.Hours, in.Days, in.Zone)
			return nil, f, err
		})
	type nameIn struct {
		Name string `json:"name"`
		N    int    `json:"n,omitempty" jsonschema:"1 to 10, default 5"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "place_lookup", Description: "Find a place and its coordinates (Kartverket for Norway, OpenStreetMap elsewhere)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, struct {
			Places []Place `json:"places"`
		}, error) {
			ps, err := t.PlaceLookup(ctx, in.Name, in.N)
			return nil, struct {
				Places []Place `json:"places"`
			}{ps}, err
		})
	type tripIn struct {
		From     string `json:"from" jsonschema:"stop, address or place name, or lat,lon"`
		To       string `json:"to"`
		When     string `json:"when,omitempty" jsonschema:"2026-11-16 08:30 (Norwegian time); empty for now"`
		ArriveBy bool   `json:"arrive_by,omitempty" jsonschema:"true: arrive by 'when' instead of leaving at it"`
		N        int    `json:"n,omitempty" jsonschema:"1 to 6, default 3"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "travel_trip", Description: "Plan a public transport trip in Norway (Entur): times, lines, transfers."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in tripIn) (*mcp.CallToolResult, TripsOut, error) {
			r, err := t.Trips(ctx, in.From, in.To, in.When, in.ArriveBy, in.N)
			return nil, r, err
		})
	type stopIn struct {
		Stop string `json:"stop" jsonschema:"stop name or NSR:StopPlace id"`
		N    int    `json:"n,omitempty" jsonschema:"1 to 20, default 8"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "travel_departures", Description: "Next departures from a stop in Norway, with real-time where available (Entur)."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in stopIn) (*mcp.CallToolResult, DeparturesOut, error) {
			r, err := t.Departures(ctx, in.Stop, in.N)
			return nil, r, err
		})
	type moneyIn struct {
		Amount float64 `json:"amount"`
		From   string  `json:"from" jsonschema:"currency code, for example EUR"`
		To     string  `json:"to" jsonschema:"currency code, for example NOK"`
		Date   string  `json:"date,omitempty" jsonschema:"2026-11-13 for a past rate; empty for the latest"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "money_convert", Description: "Convert between currencies with Norges Bank's official rates."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in moneyIn) (*mcp.CallToolResult, Converted, error) {
			r, err := t.Convert(ctx, in.Amount, in.From, in.To, in.Date)
			return nil, r, err
		})
	type zoneIn struct {
		Zone string `json:"zone,omitempty" jsonschema:"IANA time zone, default Europe/Oslo"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "time_now", Description: "The current date and time in a time zone."},
		func(_ context.Context, _ *mcp.CallToolRequest, in zoneIn) (*mcp.CallToolResult, Now, error) {
			r, err := TimeNow(in.Zone, time.Now())
			return nil, r, err
		})
	type yearIn struct {
		Year    int    `json:"year"`
		Country string `json:"country,omitempty" jsonschema:"NO (the only one so far)"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "time_holidays", Description: "Public holidays in Norway for a year."},
		func(_ context.Context, _ *mcp.CallToolRequest, in yearIn) (*mcp.CallToolResult, struct {
			Holidays []Holiday `json:"holidays"`
		}, error) {
			h, err := Holidays(in.Year, in.Country)
			return nil, struct {
				Holidays []Holiday `json:"holidays"`
			}{h}, err
		})
	type exprIn struct {
		Expression string `json:"expression" jsonschema:"for example (12.5 * 3)^2 / sqrt(2)"`
	}
	type exprOut struct {
		Expression string  `json:"expression"`
		Result     float64 `json:"result"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "calc_eval", Description: "Calculate an arithmetic expression exactly: + - * / % ^, parentheses, " +
		"pi, e, sqrt, abs, round, floor, ceil, exp, ln, log, log2, sin, cos, tan, asin, acos, atan, min, max."},
		func(_ context.Context, _ *mcp.CallToolRequest, in exprIn) (*mcp.CallToolResult, exprOut, error) {
			v, err := Eval(in.Expression)
			return nil, exprOut{in.Expression, v}, err
		})
	type unitIn struct {
		Value float64 `json:"value"`
		From  string  `json:"from" jsonschema:"unit, for example ft.lb, kN.m, km/h, psi, °F"`
		To    string  `json:"to"`
	}
	type unitOut struct {
		Value  float64 `json:"value"`
		From   string  `json:"from"`
		Result float64 `json:"result"`
		To     string  `json:"to"`
		Kind   string  `json:"kind"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "calc_convert", Description: "Convert a value between units: length, mass, time, speed, volume, area, " +
		"pressure, energy, power, torque, data size, temperature."},
		func(_ context.Context, _ *mcp.CallToolRequest, in unitIn) (*mcp.CallToolResult, unitOut, error) {
			v, kind, err := ConvertUnit(in.Value, in.From, in.To)
			return nil, unitOut{in.Value, in.From, round(v, 10), in.To, kind}, err
		})
	if t.ParcelEnabled() {
		type parcelIn struct {
			Number string `json:"number" jsonschema:"tracking number"`
		}
		mcp.AddTool(s, &mcp.Tool{Name: "parcel_track", Description: "Track a parcel sent with Bring/Posten."},
			func(ctx context.Context, _ *mcp.CallToolRequest, in parcelIn) (*mcp.CallToolResult, Parcel, error) {
				p, err := t.TrackParcel(ctx, in.Number)
				return nil, p, err
			})
	}
}
