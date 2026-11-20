package contract

import (
	"fmt"
	"strings"
	"time"
)

// Defaults are the trust labels the organisation applies to every contract for a
// workspace. They are set by code, never by the model that drafts the contract.
type Defaults struct {
	Workspace     string
	TTL           time.Duration
	Untrusted     []string
	Sensitive     []string
	BaselineDeny  []string
	TaintTolerant []string
	Confidential  []string
}

// DefaultsFor returns the organisation defaults for one workspace.
func DefaultsFor(workspace string) Defaults {
	return Defaults{
		Workspace: workspace,
		TTL:       30 * time.Minute,
		Untrusted: []string{"repo:" + workspace + "/deps/**", "net:**", "issue:**"},
		Sensitive: []string{
			"repo:" + workspace + "/deploy/**:write",
			"repo:" + workspace + "/.github/**:write",
			"net:**:*",
		},
		BaselineDeny:  []string{"secret:*", "net:*", "iam:*", "infra:*"},
		TaintTolerant: []string{"deploy:" + workspace + ":deploy"},
		Confidential:  []string{"secret:**"},
	}
}

// Template is the pre-written "test and deploy" contract. It is the fallback when
// Gemini is unavailable and the baseline the drafted contract is compared with.
func Template(instruction string, d Defaults, now time.Time) Contract {
	cid, tid := NewIDs()
	c := Contract{
		ContractID:  cid,
		TaskID:      tid,
		Version:     1,
		Instruction: instruction,
		Allow: []Grant{
			{Resource: "repo:" + d.Workspace + "/**", Ops: []string{"read", "write"}},
			{Resource: "tests:" + d.Workspace, Ops: []string{"run"}},
			{Resource: "deploy:" + d.Workspace, Ops: []string{"deploy"}},
			{Resource: "llm:gemini", Ops: []string{"generate"}},
		},
		Deny:             append([]string{}, d.BaselineDeny...),
		ApprovalRequired: []string{},
		Untrusted:        append([]string{}, d.Untrusted...),
		Sensitive:        append([]string{}, d.Sensitive...),
		TaintTolerant:    append([]string{}, d.TaintTolerant...),
		Confidential:     append([]string{}, d.Confidential...),
		OnTaint:          "hold",
		ExpiresAt:        now.Add(d.TTL).UTC().Format(time.RFC3339),
	}
	c.Normalize()
	return c
}

// ApplyDefaults overlays the organisation's trust labels on a drafted contract.
// A baseline deny is dropped only when the draft explicitly allows that resource
// type (the draft then carries a warning). The model's taint_tolerant list is
// replaced by the organisation's: an exemption only matters for an action the
// gates would hold, so a model-chosen one could only switch a gate off.
func ApplyDefaults(c *Contract, d Defaults, now time.Time) {
	allowedTypes := map[string]bool{}
	for _, g := range c.Allow {
		typ, _, _ := strings.Cut(g.Resource, ":")
		allowedTypes[typ] = true
	}
	for _, deny := range d.BaselineDeny {
		typ, _, _ := strings.Cut(deny, ":")
		if !allowedTypes[typ] && !contains(c.Deny, deny) {
			c.Deny = append(c.Deny, deny)
		}
	}
	if !allowedTypes["llm"] {
		c.Allow = append(c.Allow, Grant{Resource: "llm:gemini", Ops: []string{"generate"}})
	}
	c.Untrusted = append([]string{}, d.Untrusted...)
	c.Confidential = append([]string{}, d.Confidential...)
	c.TaintTolerant = append([]string{}, d.TaintTolerant...)
	for _, s := range d.Sensitive {
		if !contains(c.Sensitive, s) {
			c.Sensitive = append(c.Sensitive, s)
		}
	}
	c.Delegation = Delegation{Allowed: false}
	if c.OnTaint != "deny" {
		c.OnTaint = "hold"
	}
	c.ExpiresAt = now.Add(d.TTL).UTC().Format(time.RFC3339)
	c.Version = 1
	c.Normalize()
}

// KeepOrgLabels applies the organisation's trust labels to a contract a person
// edited before signing. They may add untrusted, sensitive or confidential
// patterns, never remove the organisation's, and may only narrow its
// taint-tolerant list.
func KeepOrgLabels(c *Contract, d Defaults) {
	for _, pair := range []struct {
		list *[]string
		org  []string
	}{{&c.Untrusted, d.Untrusted}, {&c.Sensitive, d.Sensitive}, {&c.Confidential, d.Confidential}} {
		for _, p := range pair.org {
			if !contains(*pair.list, p) {
				*pair.list = append(*pair.list, p)
			}
		}
	}
	tolerant := []string{}
	for _, t := range c.TaintTolerant {
		if contains(d.TaintTolerant, t) {
			tolerant = append(tolerant, t)
		}
	}
	c.TaintTolerant = tolerant
	c.Delegation = Delegation{Allowed: false}
	if c.OnTaint != "deny" {
		c.OnTaint = "hold"
	}
	c.Normalize()
}

// Describe renders the contract in plain language. It is derived from the
// contract itself, so the text a user reads cannot drift from what gets signed.
func Describe(c Contract) []string {
	var out []string
	for _, g := range c.Allow {
		out = append(out, fmt.Sprintf("May %s %s", joinOps(g.Ops), describeResource(g.Resource)))
	}
	for _, d := range c.Deny {
		out = append(out, "May never touch "+describeResource(d))
	}
	for _, a := range c.ApprovalRequired {
		out = append(out, "Always needs your approval: "+a)
	}
	for _, u := range c.Untrusted {
		out = append(out, "Treated as untrusted content: "+describeResource(u))
	}
	action := "held for your approval"
	if c.OnTaint == "deny" {
		action = "denied"
	}
	for _, s := range c.Sensitive {
		out = append(out, fmt.Sprintf("After untrusted content is read, %s is %s", s, action))
	}
	for _, t := range c.TaintTolerant {
		out = append(out, "Still allowed after untrusted content is read: "+t)
	}
	for _, cf := range c.Confidential {
		out = append(out, fmt.Sprintf("After reading %s, any outbound request is %s", describeResource(cf), action))
	}
	out = append(out, "Expires at "+c.ExpiresAt)
	return out
}

func joinOps(ops []string) string { return strings.Join(ops, " and ") }

func describeResource(r string) string {
	typ, rest, _ := strings.Cut(r, ":")
	switch typ {
	case "repo":
		return "files matching " + rest
	case "tests":
		return "the test suite of " + rest
	case "deploy":
		return "the deploy target " + rest
	case "llm":
		return "the " + rest + " model"
	case "secret":
		return "secrets (" + rest + ")"
	case "net":
		return "external network hosts (" + rest + ")"
	case "iam":
		return "accounts and permissions (" + rest + ")"
	case "infra":
		return "other infrastructure (" + rest + ")"
	case "issue":
		return "issue text (" + rest + ")"
	}
	return r
}
