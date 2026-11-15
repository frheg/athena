package everyday

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // time zones without relying on the image having them
	"unicode"
)

// ---------- time ----------

// Now is the time in a zone.
type Now struct {
	Zone    string `json:"zone"`
	Time    string `json:"time"`
	Weekday string `json:"weekday"`
	Week    int    `json:"iso_week"`
	Offset  string `json:"utc_offset"`
}

// TimeNow returns the current time in an IANA zone ("Europe/Oslo" by default).
func TimeNow(zone string, now time.Time) (Now, error) {
	if zone == "" {
		zone = "Europe/Oslo"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return Now{}, fmt.Errorf("unknown time zone %q (use names like Europe/Oslo, America/New_York)", zone)
	}
	t := now.In(loc)
	_, w := t.ISOWeek()
	return Now{Zone: loc.String(), Time: t.Format("2006-01-02 15:04:05"), Weekday: t.Weekday().String(), Week: w, Offset: t.Format("-07:00")}, nil
}

// Holiday is a public holiday.
type Holiday struct {
	Date string `json:"date"`
	Name string `json:"name"`
	NO   string `json:"name_no"`
}

// Holidays returns Norway's public holidays for a year.
func Holidays(year int, country string) ([]Holiday, error) {
	if c := strings.ToUpper(strings.TrimSpace(country)); c != "" && c != "NO" {
		return nil, fmt.Errorf("only Norway (NO) is known so far")
	}
	if year < 1900 || year > 2200 {
		return nil, fmt.Errorf("year out of range")
	}
	e := easter(year)
	d := func(t time.Time) string { return t.Format("2006-01-02") }
	fixed := func(m time.Month, day int) time.Time { return time.Date(year, m, day, 0, 0, 0, 0, time.UTC) }
	return []Holiday{
		{d(fixed(1, 1)), "New Year's Day", "Første nyttårsdag"},
		{d(e.AddDate(0, 0, -3)), "Maundy Thursday", "Skjærtorsdag"},
		{d(e.AddDate(0, 0, -2)), "Good Friday", "Langfredag"},
		{d(e), "Easter Sunday", "Første påskedag"},
		{d(e.AddDate(0, 0, 1)), "Easter Monday", "Andre påskedag"},
		{d(fixed(5, 1)), "Labour Day", "Arbeidernes dag"},
		{d(fixed(5, 17)), "Constitution Day", "Grunnlovsdag"},
		{d(e.AddDate(0, 0, 39)), "Ascension Day", "Kristi himmelfartsdag"},
		{d(e.AddDate(0, 0, 49)), "Whit Sunday", "Første pinsedag"},
		{d(e.AddDate(0, 0, 50)), "Whit Monday", "Andre pinsedag"},
		{d(fixed(12, 25)), "Christmas Day", "Første juledag"},
		{d(fixed(12, 26)), "Boxing Day", "Andre juledag"},
	}, nil
}

// easter returns Easter Sunday (Gregorian, anonymous algorithm).
func easter(y int) time.Time {
	a, b, c := y%19, y/100, y%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(y, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// ---------- calculator: a small parser, never code execution ----------

// Eval computes an arithmetic expression: + - * / % ^, parentheses, pi, e, and functions sqrt, abs, round, floor,
// ceil, exp, ln, log (base 10), log2, sin, cos, tan, asin, acos, atan (radians), min, max.
func Eval(expr string) (float64, error) {
	p := &parser{s: []rune(strings.TrimSpace(expr))}
	if len(p.s) == 0 {
		return 0, fmt.Errorf("expression is empty")
	}
	if len(p.s) > 500 {
		return 0, fmt.Errorf("expression is too long")
	}
	v, err := p.sum()
	if err != nil {
		return 0, err
	}
	p.space()
	if p.i != len(p.s) {
		return 0, fmt.Errorf("unexpected %q at position %d", string(p.s[p.i]), p.i+1)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("the result is not a finite number")
	}
	return v, nil
}

type parser struct {
	s     []rune
	i     int
	depth int
}

func (p *parser) space() {
	for p.i < len(p.s) && unicode.IsSpace(p.s[p.i]) {
		p.i++
	}
}

func (p *parser) eat(r rune) bool {
	p.space()
	if p.i < len(p.s) && p.s[p.i] == r {
		p.i++
		return true
	}
	return false
}

func (p *parser) sum() (float64, error) {
	v, err := p.product()
	for err == nil {
		switch {
		case p.eat('+'):
			var w float64
			w, err = p.product()
			v += w
		case p.eat('-'):
			var w float64
			w, err = p.product()
			v -= w
		default:
			return v, nil
		}
	}
	return v, err
}

func (p *parser) product() (float64, error) {
	v, err := p.power()
	for err == nil {
		switch {
		case p.eat('*'), p.eat('×'):
			var w float64
			w, err = p.power()
			v *= w
		case p.eat('/'), p.eat('÷'):
			var w float64
			if w, err = p.power(); err == nil && w == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			v /= w
		case p.eat('%'):
			var w float64
			if w, err = p.power(); err == nil && w == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			v = math.Mod(v, w)
		default:
			return v, nil
		}
	}
	return v, err
}

func (p *parser) power() (float64, error) {
	b, err := p.unary()
	if err != nil {
		return 0, err
	}
	if p.eat('^') {
		e, err := p.power() // right-associative
		if err != nil {
			return 0, err
		}
		return math.Pow(b, e), nil
	}
	return b, nil
}

func (p *parser) unary() (float64, error) {
	if p.eat('-') {
		v, err := p.unary()
		return -v, err
	}
	if p.eat('+') {
		return p.unary()
	}
	return p.atom()
}

func (p *parser) atom() (float64, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 50 {
		return 0, fmt.Errorf("too deeply nested")
	}
	p.space()
	if p.eat('(') {
		v, err := p.sum()
		if err != nil {
			return 0, err
		}
		if !p.eat(')') {
			return 0, fmt.Errorf("missing )")
		}
		return v, nil
	}
	if p.i >= len(p.s) {
		return 0, fmt.Errorf("expression ends too early")
	}
	start := p.i
	if unicode.IsDigit(p.s[p.i]) || p.s[p.i] == '.' {
		for p.i < len(p.s) && (unicode.IsDigit(p.s[p.i]) || p.s[p.i] == '.' || p.s[p.i] == '_') {
			p.i++
		}
		// scientific notation: 1.5e3
		if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') && p.i+1 < len(p.s) &&
			(unicode.IsDigit(p.s[p.i+1]) || ((p.s[p.i+1] == '-' || p.s[p.i+1] == '+') && p.i+2 < len(p.s) && unicode.IsDigit(p.s[p.i+2]))) {
			p.i += 2
			for p.i < len(p.s) && unicode.IsDigit(p.s[p.i]) {
				p.i++
			}
		}
		return strconv.ParseFloat(strings.ReplaceAll(string(p.s[start:p.i]), "_", ""), 64)
	}
	for p.i < len(p.s) && (unicode.IsLetter(p.s[p.i]) || unicode.IsDigit(p.s[p.i])) {
		p.i++
	}
	name := strings.ToLower(string(p.s[start:p.i]))
	switch name {
	case "pi":
		return math.Pi, nil
	case "e":
		return math.E, nil
	case "":
		return 0, fmt.Errorf("unexpected %q at position %d", string(p.s[p.i]), p.i+1)
	}
	if !p.eat('(') {
		return 0, fmt.Errorf("unknown name %q", name)
	}
	var args []float64
	if !p.eat(')') {
		for {
			v, err := p.sum()
			if err != nil {
				return 0, err
			}
			args = append(args, v)
			if p.eat(')') {
				break
			}
			if !p.eat(',') {
				return 0, fmt.Errorf("expected , or ) in %s()", name)
			}
		}
	}
	one := map[string]func(float64) float64{"sqrt": math.Sqrt, "abs": math.Abs, "round": math.Round, "floor": math.Floor,
		"ceil": math.Ceil, "exp": math.Exp, "ln": math.Log, "log": math.Log10, "log2": math.Log2, "sin": math.Sin, "cos": math.Cos,
		"tan": math.Tan, "asin": math.Asin, "acos": math.Acos, "atan": math.Atan}
	if f, ok := one[name]; ok {
		if len(args) != 1 {
			return 0, fmt.Errorf("%s() takes one argument", name)
		}
		return f(args[0]), nil
	}
	if name == "min" || name == "max" {
		if len(args) == 0 {
			return 0, fmt.Errorf("%s() needs arguments", name)
		}
		v := args[0]
		for _, a := range args[1:] {
			if name == "min" {
				v = math.Min(v, a)
			} else {
				v = math.Max(v, a)
			}
		}
		return v, nil
	}
	return 0, fmt.Errorf("unknown function %s()", name)
}

// ---------- units ----------

type unit struct {
	dim    string
	factor float64 // to the SI base of the dimension
}

var units = map[string]unit{
	// length (m)
	"m": {"length", 1}, "km": {"length", 1000}, "cm": {"length", 0.01}, "mm": {"length", 0.001}, "um": {"length", 1e-6},
	"in": {"length", 0.0254}, "inch": {"length", 0.0254}, "ft": {"length", 0.3048}, "yd": {"length", 0.9144},
	"mi": {"length", 1609.344}, "nmi": {"length", 1852}, "mil": {"length", 10000}, // Scandinavian mile
	// mass (kg)
	"kg": {"mass", 1}, "g": {"mass", 0.001}, "mg": {"mass", 1e-6}, "t": {"mass", 1000}, "lb": {"mass", 0.45359237}, "oz": {"mass", 0.028349523125},
	// time (s)
	"s": {"time", 1}, "ms": {"time", 0.001}, "min": {"time", 60}, "h": {"time", 3600}, "day": {"time", 86400}, "week": {"time", 604800},
	// speed (m/s)
	"m/s": {"speed", 1}, "km/h": {"speed", 1 / 3.6}, "mph": {"speed", 0.44704}, "kn": {"speed", 1852.0 / 3600}, "knot": {"speed", 1852.0 / 3600},
	// volume (m3)
	"m3": {"volume", 1}, "l": {"volume", 0.001}, "dl": {"volume", 0.0001}, "ml": {"volume", 1e-6}, "gal": {"volume", 0.003785411784},
	"bbl": {"volume", 0.158987294928}, "ft3": {"volume", 0.028316846592},
	// area (m2)
	"m2": {"area", 1}, "km2": {"area", 1e6}, "ha": {"area", 1e4}, "daa": {"area", 1000}, "ft2": {"area", 0.09290304}, "acre": {"area", 4046.8564224},
	// pressure (Pa)
	"pa": {"pressure", 1}, "kpa": {"pressure", 1000}, "mpa": {"pressure", 1e6}, "bar": {"pressure", 1e5}, "psi": {"pressure", 6894.757293168},
	"atm": {"pressure", 101325},
	// energy (J)
	"j": {"energy", 1}, "kj": {"energy", 1000}, "kwh": {"energy", 3.6e6}, "cal": {"energy", 4.184}, "kcal": {"energy", 4184},
	// power (W)
	"w": {"power", 1}, "kw": {"power", 1000}, "hp": {"power", 745.69987158227},
	// torque (N·m)
	"nm": {"torque", 1}, "n.m": {"torque", 1}, "knm": {"torque", 1000}, "kn.m": {"torque", 1000}, "ft.lb": {"torque", 1.3558179483314}, "ftlb": {"torque", 1.3558179483314},
	"lbf.ft": {"torque", 1.3558179483314}, "in.lb": {"torque", 0.1129848290276},
	// data (byte)
	"b": {"data", 1}, "kb": {"data", 1000}, "mb": {"data", 1e6}, "gb": {"data", 1e9}, "tb": {"data", 1e12}, "kib": {"data", 1024},
	"mib": {"data", 1 << 20}, "gib": {"data", 1 << 30}, "tib": {"data", 1 << 40},
}

func unitKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("·", ".", "²", "2", "³", "3", "µ", "u", " ", "", "-", ".").Replace(s)
	return s
}

// ConvertUnit converts a value between units of the same kind (including °C, °F and K).
func ConvertUnit(v float64, from, to string) (float64, string, error) {
	f, t := unitKey(from), unitKey(to)
	temps := map[string]bool{"c": true, "°c": true, "f": true, "°f": true, "k": true}
	if temps[f] || temps[t] {
		if !(temps[f] && temps[t]) {
			return 0, "", fmt.Errorf("cannot convert %s to %s", from, to)
		}
		k := map[string]func(float64) float64{"c": func(x float64) float64 { return x + 273.15 }, "f": func(x float64) float64 { return (x-32)*5/9 + 273.15 },
			"k": func(x float64) float64 { return x }}
		back := map[string]func(float64) float64{"c": func(x float64) float64 { return x - 273.15 }, "f": func(x float64) float64 { return (x-273.15)*9/5 + 32 },
			"k": func(x float64) float64 { return x }}
		return back[strings.TrimPrefix(t, "°")](k[strings.TrimPrefix(f, "°")](v)), "temperature", nil
	}
	a, ok1 := units[f]
	b, ok2 := units[t]
	if !ok1 || !ok2 {
		return 0, "", fmt.Errorf("unknown unit %q or %q", from, to)
	}
	if a.dim != b.dim {
		return 0, "", fmt.Errorf("cannot convert %s (%s) to %s (%s)", from, a.dim, to, b.dim)
	}
	return v * a.factor / b.factor, a.dim, nil
}
