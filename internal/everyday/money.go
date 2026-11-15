package everyday

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Converted is the result of money_convert.
type Converted struct {
	Amount   float64 `json:"amount"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Result   float64 `json:"result"`
	Rate     float64 `json:"rate"`
	RateDate string  `json:"rate_date"`
	Source   string  `json:"source"`
}

var curRe = regexp.MustCompile(`^[A-Z]{3}$`)

// rateNOK returns how many NOK one unit of cur costs, and the date of the rate (Norges Bank, business days).
func (t *Tools) rateNOK(ctx context.Context, cur, date string) (float64, string, error) {
	if cur == "NOK" {
		return 1, "", nil
	}
	v := url.Values{"format": {"sdmx-json"}, "locale": {"en"}}
	if date == "" {
		v.Set("lastNObservations", "1")
	} else {
		// a range of a week back finds the last business day on or before the date
		d, err := time.Parse("2006-01-02", date)
		if err != nil {
			return 0, "", fmt.Errorf("date must look like 2026-11-13")
		}
		v.Set("startPeriod", d.AddDate(0, 0, -7).Format("2006-01-02"))
		v.Set("endPeriod", date)
	}
	b, err := t.fetch(ctx, "data.norges-bank.no", "/api/data/EXR/B."+cur+".NOK.SP?"+v.Encode(), nil, nil, 6*time.Hour, 0)
	if err != nil {
		return 0, "", fmt.Errorf("no rate for %s (Norges Bank publishes about 40 currencies): %w", cur, err)
	}
	var r struct {
		Data struct {
			DataSets []struct {
				Series map[string]struct {
					Attributes   []int               `json:"attributes"`
					Observations map[string][]string `json:"observations"`
				} `json:"series"`
			} `json:"dataSets"`
			Structure struct {
				Dimensions struct {
					Observation []struct {
						Values []struct {
							ID string `json:"id"`
						} `json:"values"`
					} `json:"observation"`
				} `json:"dimensions"`
				Attributes struct {
					Series []struct {
						ID     string `json:"id"`
						Values []struct {
							ID string `json:"id"`
						} `json:"values"`
					} `json:"series"`
				} `json:"attributes"`
			} `json:"structure"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return 0, "", err
	}
	if len(r.Data.DataSets) == 0 {
		return 0, "", fmt.Errorf("no rate for %s", cur)
	}
	for _, s := range r.Data.DataSets[0].Series {
		// the newest observation has the highest index
		best, val := -1, ""
		for k, o := range s.Observations {
			i, _ := strconv.Atoi(k)
			if i > best && len(o) > 0 {
				best, val = i, o[0]
			}
		}
		rate, err := strconv.ParseFloat(val, 64)
		if err != nil {
			continue
		}
		// UNIT_MULT 2 means the rate is per 100 units (JPY, SEK, DKK...)
		for ai, a := range r.Data.Structure.Attributes.Series {
			if a.ID == "UNIT_MULT" && ai < len(s.Attributes) && s.Attributes[ai] < len(a.Values) {
				if m, err := strconv.Atoi(a.Values[s.Attributes[ai]].ID); err == nil {
					rate /= math.Pow(10, float64(m))
				}
			}
		}
		day := ""
		if obs := r.Data.Structure.Dimensions.Observation; len(obs) > 0 && best < len(obs[0].Values) {
			day = obs[0].Values[best].ID
		}
		return rate, day, nil
	}
	return 0, "", fmt.Errorf("no rate for %s", cur)
}

// Convert converts between currencies through NOK, with Norges Bank's official rates.
func (t *Tools) Convert(ctx context.Context, amount float64, from, to, date string) (Converted, error) {
	from, to = strings.ToUpper(strings.TrimSpace(from)), strings.ToUpper(strings.TrimSpace(to))
	if !curRe.MatchString(from) || !curRe.MatchString(to) {
		return Converted{}, fmt.Errorf("currencies are three-letter codes such as EUR, USD, NOK")
	}
	rf, df, err := t.rateNOK(ctx, from, date)
	if err != nil {
		return Converted{}, err
	}
	rt, dt, err := t.rateNOK(ctx, to, date)
	if err != nil {
		return Converted{}, err
	}
	rate := rf / rt
	day := df
	if day == "" {
		day = dt
	}
	return Converted{Amount: amount, From: from, To: to, Rate: round(rate, 6), Result: round(amount*rate, 2), RateDate: day,
		Source: "Norges Bank exchange rates"}, nil
}

func round(v float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(v*p) / p
}
