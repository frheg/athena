package everyday

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"time"
)

// Hour is one hour of forecast.
type Hour struct {
	Time       string  `json:"time"`
	TempC      float64 `json:"temp_c"`
	WindMS     float64 `json:"wind_ms"`
	PrecipMM   float64 `json:"precip_mm"`
	Conditions string  `json:"conditions"`
}

// Day is a daily summary.
type Day struct {
	Date       string  `json:"date"`
	MinC       float64 `json:"min_c"`
	MaxC       float64 `json:"max_c"`
	PrecipMM   float64 `json:"precip_mm"`
	MaxWindMS  float64 `json:"max_wind_ms"`
	Conditions string  `json:"conditions"`
}

// Forecast is the weather result.
type Forecast struct {
	Place    string   `json:"place"`
	Lat      float64  `json:"lat"`
	Lon      float64  `json:"lon"`
	Zone     string   `json:"time_zone"`
	Hours    []Hour   `json:"hours"`
	Days     []Day    `json:"days"`
	Warnings []string `json:"quoted_warnings,omitempty"`
	Source   string   `json:"source"`
	Note     string   `json:"note"`
}

// Weather returns the forecast for a place name or "lat,lon": the next hours and a daily summary.
func (t *Tools) Weather(ctx context.Context, where string, hours, days int, zone string) (Forecast, error) {
	lat, lon, name, err := t.resolve(ctx, where)
	if err != nil {
		return Forecast{}, err
	}
	if hours <= 0 || hours > 48 {
		hours = 12
	}
	if days <= 0 || days > 9 {
		days = 3
	}
	loc, err := time.LoadLocation(zone)
	if zone == "" || err != nil {
		loc, _ = time.LoadLocation("Europe/Oslo")
	}
	// MET asks for at most 4 decimals and caching; the forecast changes about every 30 minutes
	q := url.Values{"lat": {fmt.Sprintf("%.4f", lat)}, "lon": {fmt.Sprintf("%.4f", lon)}}
	b, err := t.fetch(ctx, "api.met.no", "/weatherapi/locationforecast/2.0/compact?"+q.Encode(), nil, nil, 30*time.Minute, 0)
	if err != nil {
		return Forecast{}, err
	}
	var r struct {
		Properties struct {
			Timeseries []struct {
				Time string `json:"time"`
				Data struct {
					Instant struct {
						Details struct {
							Temp float64 `json:"air_temperature"`
							Wind float64 `json:"wind_speed"`
						} `json:"details"`
					} `json:"instant"`
					Next1 *struct {
						Summary struct {
							Symbol string `json:"symbol_code"`
						} `json:"summary"`
						Details struct {
							Precip float64 `json:"precipitation_amount"`
						} `json:"details"`
					} `json:"next_1_hours"`
					Next6 *struct {
						Summary struct {
							Symbol string `json:"symbol_code"`
						} `json:"summary"`
						Details struct {
							Precip float64 `json:"precipitation_amount"`
						} `json:"details"`
					} `json:"next_6_hours"`
				} `json:"data"`
			} `json:"timeseries"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Forecast{}, err
	}
	f := Forecast{Place: name, Lat: lat, Lon: lon, Zone: loc.String(), Hours: []Hour{}, Days: []Day{},
		Source: "MET Norway (api.met.no), CC BY 4.0", Note: Note}
	type acc struct {
		min, max, precip, wind float64
		symbols                map[string]int
	}
	daily := map[string]*acc{}
	now := time.Now().Add(-time.Hour)
	for _, s := range r.Properties.Timeseries {
		at, err := time.Parse(time.RFC3339, s.Time)
		if err != nil || at.Before(now) {
			continue
		}
		local := at.In(loc)
		d := s.Data
		if d.Next1 != nil && len(f.Hours) < hours {
			f.Hours = append(f.Hours, Hour{Time: local.Format("2006-01-02 15:04"), TempC: d.Instant.Details.Temp, WindMS: d.Instant.Details.Wind,
				PrecipMM: d.Next1.Details.Precip, Conditions: describe(d.Next1.Summary.Symbol)})
		}
		key := local.Format("2006-01-02")
		a := daily[key]
		if a == nil {
			a = &acc{min: math.Inf(1), max: math.Inf(-1), symbols: map[string]int{}}
			daily[key] = a
		}
		a.min = math.Min(a.min, d.Instant.Details.Temp)
		a.max = math.Max(a.max, d.Instant.Details.Temp)
		a.wind = math.Max(a.wind, d.Instant.Details.Wind)
		switch {
		case d.Next1 != nil:
			a.precip += d.Next1.Details.Precip
			if local.Hour() >= 8 && local.Hour() <= 20 {
				a.symbols[d.Next1.Summary.Symbol]++
			}
		case d.Next6 != nil && local.Hour()%6 == 0:
			// further ahead MET gives 6-hour blocks
			a.precip += d.Next6.Details.Precip
			a.symbols[d.Next6.Summary.Symbol]++
		}
	}
	keys := make([]string, 0, len(daily))
	for k := range daily {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if len(f.Days) == days {
			break
		}
		a := daily[k]
		best, n := "", 0
		for s, c := range a.symbols {
			if c > n || (c == n && s < best) {
				best, n = s, c
			}
		}
		f.Days = append(f.Days, Day{Date: k, MinC: a.min, MaxC: a.max, PrecipMM: math.Round(a.precip*10) / 10, MaxWindMS: a.wind, Conditions: describe(best)})
	}
	// warnings are optional: a failure here must not hide the forecast
	if b, err := t.fetch(ctx, "api.met.no", "/weatherapi/metalerts/2.0/current.json?"+q.Encode(), nil, nil, 15*time.Minute, 0); err == nil {
		var w struct {
			Features []struct {
				Properties struct {
					Title       string `json:"title"`
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"features"`
		}
		if json.Unmarshal(b, &w) == nil {
			for _, x := range w.Features {
				f.Warnings = append(f.Warnings, x.Properties.Title+": "+x.Properties.Description)
			}
		}
	}
	return f, nil
}

// describe turns a MET symbol code ("lightrainshowers_day") into words.
func describe(code string) string {
	if code == "" {
		return ""
	}
	words := map[string]string{
		"clearsky": "clear sky", "fair": "fair", "partlycloudy": "partly cloudy", "cloudy": "cloudy", "fog": "fog",
		"lightrain": "light rain", "rain": "rain", "heavyrain": "heavy rain", "lightrainshowers": "light rain showers",
		"rainshowers": "rain showers", "heavyrainshowers": "heavy rain showers", "lightsleet": "light sleet", "sleet": "sleet",
		"heavysleet": "heavy sleet", "lightsnow": "light snow", "snow": "snow", "heavysnow": "heavy snow",
		"lightsnowshowers": "light snow showers", "snowshowers": "snow showers", "heavysnowshowers": "heavy snow showers",
		"lightsleetshowers": "light sleet showers", "sleetshowers": "sleet showers", "heavysleetshowers": "heavy sleet showers",
		"rainandthunder": "rain and thunder", "heavyrainandthunder": "heavy rain and thunder", "lightrainandthunder": "light rain and thunder",
		"rainshowersandthunder": "rain showers and thunder", "snowandthunder": "snow and thunder", "sleetandthunder": "sleet and thunder",
	}
	base := code
	for _, suf := range []string{"_day", "_night", "_polartwilight"} {
		if len(base) > len(suf) && base[len(base)-len(suf):] == suf {
			base = base[:len(base)-len(suf)]
		}
	}
	if w, ok := words[base]; ok {
		return w
	}
	return base
}
