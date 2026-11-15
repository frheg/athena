package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed five-field schedule: minute hour day-of-month month day-of-week.
// Fields take *, numbers, ranges (1-5), lists (1,15) and steps (*/10, 8-18/2). Day of week: 0 or 7 is Sunday.
// As in classic cron, when both day fields are restricted, a day matches if either matches.
type Cron struct {
	min, hour, dom, month, dow [64]bool
	domAny, dowAny             bool
}

// ParseCron parses a schedule. The shortcuts @hourly, @daily, @weekly and @monthly work too.
func ParseCron(s string) (*Cron, error) {
	switch strings.TrimSpace(s) {
	case "@hourly":
		s = "0 * * * *"
	case "@daily":
		s = "0 0 * * *"
	case "@weekly":
		s = "0 0 * * 0"
	case "@monthly":
		s = "0 0 1 * *"
	}
	f := strings.Fields(s)
	if len(f) != 5 {
		return nil, fmt.Errorf("schedule %q: want 5 fields (minute hour day month weekday)", s)
	}
	c := &Cron{domAny: f[2] == "*", dowAny: f[4] == "*"}
	specs := []struct {
		set      *[64]bool
		lo, hi   int
		name     string
		field    string
		sundayIs bool
	}{
		{&c.min, 0, 59, "minute", f[0], false},
		{&c.hour, 0, 23, "hour", f[1], false},
		{&c.dom, 1, 31, "day", f[2], false},
		{&c.month, 1, 12, "month", f[3], false},
		{&c.dow, 0, 7, "weekday", f[4], true},
	}
	for _, sp := range specs {
		if err := parseField(sp.field, sp.lo, sp.hi, sp.set); err != nil {
			return nil, fmt.Errorf("schedule %q, %s: %v", s, sp.name, err)
		}
		if sp.sundayIs && sp.set[7] {
			sp.set[0] = true
		}
	}
	return c, nil
}

func parseField(f string, lo, hi int, set *[64]bool) error {
	for _, part := range strings.Split(f, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return fmt.Errorf("bad step in %q", part)
			}
			step, part = n, part[:i]
		}
		a, b := lo, hi
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			x := strings.SplitN(part, "-", 2)
			var err1, err2 error
			a, err1 = strconv.Atoi(x[0])
			b, err2 = strconv.Atoi(x[1])
			if err1 != nil || err2 != nil {
				return fmt.Errorf("bad range %q", part)
			}
		default:
			n, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("bad value %q", part)
			}
			a, b = n, n
			if step > 1 {
				b = hi
			}
		}
		if a < lo || b > hi || a > b {
			return fmt.Errorf("%q is outside %d-%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			set[v] = true
		}
	}
	return nil
}

// Match reports whether the schedule fires in the minute of t.
func (c *Cron) Match(t time.Time) bool {
	if !c.min[t.Minute()] || !c.hour[t.Hour()] || !c.month[int(t.Month())] {
		return false
	}
	dom, dow := c.dom[t.Day()], c.dow[int(t.Weekday())]
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	default:
		return dom || dow
	}
}

// Next returns the first minute after t that the schedule fires (within a year).
func (c *Cron) Next(t time.Time) (time.Time, bool) {
	t = t.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if c.Match(t) {
			return t, true
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, false
}
