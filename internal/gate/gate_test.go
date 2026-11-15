package gate

import (
	"encoding/json"
	"strings"
	"testing"
)

const policy = `
tools:
  - {match: "web__*", class: read, untrusted: true}
  - {match: "everyday__*", class: read}
  - {match: "notes__add", class: write}
  - {match: "cluster_act__*", class: act}
  - {match: "notify__send", class: communicate}
  - {match: "memory__remember", class: remember}
rules:
  - {tool: "cluster_act__acquire_gpu", args: {consumer: llm}, decision: allow, note: "the default GPU holder"}
  - {tool: "cluster_act__restart", args: {namespace: "re:kube-.*|flux-system"}, decision: deny}
  - {tool: "notify__send", args: {to: owner}, decision: allow}
jobs:
  allow: [read, remember]
`

func TestDecide(t *testing.T) {
	p, err := Parse([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	permitted := func(ok bool) func() (bool, error) { return func() (bool, error) { return ok, nil } }
	args := func(s string) json.RawMessage { return json.RawMessage(s) }
	cases := []struct {
		name string
		call Call
		want string
		why  string
	}{
		{"unknown tool", Call{Tool: "shell__exec"}, Deny, "does not list"},
		{"read", Call{Tool: "everyday__weather_forecast", Principal: "user"}, Allow, "default for read"},
		{"write asks once", Call{Tool: "notes__add", Principal: "user"}, Ask, "default for write"},
		{"write with session permission", Call{Tool: "notes__add", Principal: "user", Permitted: permitted(true)}, Allow, "standing"},
		{"act asks", Call{Tool: "cluster_act__start_profile", Args: args(`{"name":"x"}`), Principal: "user"}, Ask, "approval"},
		{"rule allows exact arg", Call{Tool: "cluster_act__acquire_gpu", Args: args(`{"consumer":"llm"}`), Principal: "user"}, Allow, "default GPU holder"},
		{"rule needs the exact arg", Call{Tool: "cluster_act__acquire_gpu", Args: args(`{"consumer":"strata"}`), Principal: "user"}, Ask, ""},
		{"deny rule by pattern", Call{Tool: "cluster_act__restart", Args: args(`{"namespace":"kube-system"}`), Principal: "user", Permitted: permitted(true)}, Deny, "rule"},
		{"pattern must match whole value", Call{Tool: "cluster_act__restart", Args: args(`{"namespace":"my-kube-system"}`), Principal: "user"}, Ask, ""},
		{"tainted: act allowed by rule asks again", Call{Tool: "cluster_act__acquire_gpu", Args: args(`{"consumer":"llm"}`), Principal: "user", Tainted: true}, Ask, "untrusted"},
		{"tainted: communicate with permission asks again", Call{Tool: "notify__send", Args: args(`{"to":"x@y"}`), Principal: "user", Tainted: true, Permitted: permitted(true)}, Ask, "untrusted"},
		{"tainted: reading is still fine", Call{Tool: "web__fetch", Principal: "user", Tainted: true}, Allow, ""},
		{"job may read", Call{Tool: "web__search", Principal: "job:brief"}, Allow, ""},
		{"job may not write on its own", Call{Tool: "notes__add", Principal: "job:brief", Permitted: permitted(true)}, Ask, "scheduled job"},
		{"job sends to owner by rule, but tainted asks", Call{Tool: "notify__send", Args: args(`{"to":"owner"}`), Principal: "job:brief", Tainted: true}, Ask, ""},
		{"remember is allowed", Call{Tool: "memory__remember", Principal: "user"}, Allow, ""},
	}
	for _, c := range cases {
		d := p.Decide(c.call)
		if d.Decision != c.want || (c.why != "" && !strings.Contains(d.Reason, c.why)) {
			t.Errorf("%s: got %s (%s), want %s (%s)", c.name, d.Decision, d.Reason, c.want, c.why)
		}
	}
	if !p.Classify("web__fetch").Untrusted || p.Classify("everyday__time_now").Untrusted {
		t.Error("untrusted flag wrong")
	}
}

func TestParseRejectsMistakes(t *testing.T) {
	for _, bad := range []string{
		`tools: [{match: "x", class: admin}]`,
		`defaults: {read: maybe}`,
		`rules: [{tool: "x", decision: yes}]`,
		`rules: [{tool: "x", decision: allow, args: {a: "re:("}}]`,
		`toolz: []`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
}

func TestExamplePolicy(t *testing.T) {
	p, err := Load("../../policy/policy.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for tool, want := range map[string]string{"web__fetch": Allow, "memory__remember": Allow, "memory__forget": Ask, "cluster_act__x": Ask, "shell__run": Deny} {
		if d := p.Decide(Call{Tool: tool, Args: json.RawMessage(`{}`), Principal: "user"}); d.Decision != want {
			t.Errorf("%s: %s (%s), want %s", tool, d.Decision, d.Reason, want)
		}
	}
	if d := p.Decide(Call{Tool: "notify__send", Args: json.RawMessage(`{"to":"owner"}`), Principal: "user"}); d.Decision != Allow {
		t.Error("mail to owner", d)
	}
	if d := p.Decide(Call{Tool: "notify__send", Args: json.RawMessage(`{"to":"owner"}`), Principal: "user", Tainted: true}); d.Decision != Ask {
		t.Error("tainted mail to owner should ask", d)
	}
}
