// Package gate decides, for every tool call, whether it may run: allow, ask (wait for the user) or deny.
//
// The model is not trusted, so nothing here comes from the model or from the tool servers: the class of each tool is
// set in Athena's own policy file, and a tool the policy does not know is denied. The decision is made in this order:
//
//  1. the tool's class from the policy (unknown tool: deny)
//  2. explicit rules, matched on the tool and its arguments (first match wins)
//  3. a standing permission for exactly these arguments (from an earlier "always" or "this session" answer)
//  4. the default for the class
//  5. who started the turn: a job may only do what the policy lets jobs do on their own; the rest is queued for you
//  6. taint: once a session has read untrusted content, acting and communicating always ask
package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"reflect"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Classes of tools.
const (
	Read        = "read"
	Write       = "write"
	Act         = "act"
	Communicate = "communicate"
	Remember    = "remember"
)

// Decisions.
const (
	Allow = "allow"
	Ask   = "ask"
	Deny  = "deny"
)

// Policy is the permission policy file.
type Policy struct {
	Tools    []ToolRule        `yaml:"tools"`
	Defaults map[string]string `yaml:"defaults"`
	Rules    []ArgRule         `yaml:"rules"`
	Jobs     struct {
		Allow []string `yaml:"allow"`
	} `yaml:"jobs"`
	Limits struct {
		RoundsPerTurn  int `yaml:"rounds_per_turn" json:"rounds_per_turn"`
		CallsPerMinute int `yaml:"calls_per_minute" json:"calls_per_minute"`
	} `yaml:"limits"`
}

// ToolRule gives tools matching a pattern their class.
type ToolRule struct {
	Match     string `yaml:"match"` // glob on the full name, for example web__*
	Class     string `yaml:"class"`
	Untrusted bool   `yaml:"untrusted"` // results come from outside (web, mail, documents): reading them taints the session
}

// ArgRule decides for a tool when its arguments match.
type ArgRule struct {
	Tool     string         `yaml:"tool" json:"tool"`
	Args     map[string]any `yaml:"args" json:"args,omitempty"` // each listed argument must equal the value; a string "re:<regexp>" matches by pattern
	Decision string         `yaml:"decision" json:"decision"`
	Note     string         `yaml:"note" json:"note,omitempty"`
}

var validClass = map[string]bool{Read: true, Write: true, Act: true, Communicate: true, Remember: true}
var validDefault = map[string]bool{Allow: true, Ask: true, Deny: true, "ask_once": true}

// Load reads and checks a policy file.
func Load(file string) (*Policy, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse checks a policy.
func Parse(b []byte) (*Policy, error) {
	var p Policy
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	for _, t := range p.Tools {
		if !validClass[t.Class] {
			return nil, fmt.Errorf("policy: tool %q has unknown class %q", t.Match, t.Class)
		}
		if _, err := path.Match(t.Match, ""); err != nil {
			return nil, fmt.Errorf("policy: bad pattern %q", t.Match)
		}
	}
	def := map[string]string{Read: Allow, Write: "ask_once", Act: Ask, Communicate: Ask, Remember: Allow}
	for c, d := range p.Defaults {
		if !validClass[c] || !validDefault[d] {
			return nil, fmt.Errorf("policy: default %s: %s is not valid", c, d)
		}
		def[c] = d
	}
	p.Defaults = def
	for _, r := range p.Rules {
		if r.Decision != Allow && r.Decision != Ask && r.Decision != Deny {
			return nil, fmt.Errorf("policy: rule for %q has decision %q", r.Tool, r.Decision)
		}
		for k, v := range r.Args {
			if s, ok := v.(string); ok && strings.HasPrefix(s, "re:") {
				if _, err := regexp.Compile("^(?:" + s[3:] + ")$"); err != nil {
					return nil, fmt.Errorf("policy: rule for %q, argument %s: %v", r.Tool, k, err)
				}
			}
		}
	}
	if p.Jobs.Allow == nil {
		p.Jobs.Allow = []string{Read}
	}
	if p.Limits.RoundsPerTurn <= 0 {
		p.Limits.RoundsPerTurn = 8
	}
	if p.Limits.CallsPerMinute <= 0 {
		p.Limits.CallsPerMinute = 30
	}
	return &p, nil
}

// Info is what the policy says about a tool.
type Info struct {
	Known     bool
	Class     string
	Untrusted bool
}

// Classify returns the class of a tool (first matching pattern).
func (p *Policy) Classify(tool string) Info {
	for _, t := range p.Tools {
		if ok, _ := path.Match(t.Match, tool); ok {
			return Info{Known: true, Class: t.Class, Untrusted: t.Untrusted}
		}
	}
	return Info{}
}

// Call is what the gate decides on.
type Call struct {
	Tool      string
	Args      json.RawMessage
	Principal string // user, or job:<name>
	Tainted   bool
	// Permitted reports whether a standing permission covers this tool with exactly these arguments.
	Permitted func() (bool, error)
}

// Decision is the gate's answer.
type Decision struct {
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	Class     string `json:"class"`
	Untrusted bool   `json:"untrusted"`
}

// Decide applies the policy to a call.
func (p *Policy) Decide(c Call) Decision {
	info := p.Classify(c.Tool)
	if !info.Known {
		return Decision{Deny, "the policy does not list this tool", "", false}
	}
	d := Decision{Class: info.Class, Untrusted: info.Untrusted}
	var args map[string]any
	_ = json.Unmarshal(c.Args, &args)

	matched := false
	for _, r := range p.Rules {
		if ok, _ := path.Match(r.Tool, c.Tool); ok && argsMatch(r.Args, args) {
			d.Decision, d.Reason, matched = r.Decision, "policy rule", true
			if r.Note != "" {
				d.Reason += ": " + r.Note
			}
			break
		}
	}
	if !matched && c.Permitted != nil {
		if ok, err := c.Permitted(); err == nil && ok {
			d.Decision, d.Reason, matched = Allow, "standing permission", true
		}
	}
	if !matched {
		switch p.Defaults[info.Class] {
		case Allow:
			d.Decision, d.Reason = Allow, "default for "+info.Class
		case Deny:
			d.Decision, d.Reason = Deny, "default for "+info.Class
		default: // ask, ask_once (a session permission, checked above, stands in for "once per session")
			d.Decision, d.Reason = Ask, "default for "+info.Class+": needs your approval"
		}
	}
	if d.Decision == Deny {
		return d
	}
	if strings.HasPrefix(c.Principal, "job:") && d.Decision == Allow && !contains(p.Jobs.Allow, info.Class) {
		d.Decision, d.Reason = Ask, "a scheduled job may not do "+info.Class+" on its own: queued for your approval"
	}
	if c.Tainted && d.Decision == Allow && (info.Class == Act || info.Class == Communicate) {
		d.Decision, d.Reason = Ask, "this session has read untrusted content (web, mail or documents), so "+info.Class+" needs a fresh approval"
	}
	return d
}

func argsMatch(want, got map[string]any) bool {
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			return false
		}
		if s, ok := w.(string); ok && strings.HasPrefix(s, "re:") {
			gs, ok := g.(string)
			if !ok || !regexp.MustCompile("^(?:"+s[3:]+")$").MatchString(gs) {
				return false
			}
			continue
		}
		if !equal(w, g) {
			return false
		}
	}
	return true
}

// equal compares a YAML value with a JSON value (numbers may come as int on one side, float on the other).
func equal(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	var x, y any
	_ = json.Unmarshal(ja, &x)
	_ = json.Unmarshal(jb, &y)
	return reflect.DeepEqual(x, y)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
