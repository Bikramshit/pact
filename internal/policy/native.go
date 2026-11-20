package policy

import (
	"context"
	"fmt"
	"sort"

	"github.com/gobwas/glob"
)

// Native is a plain Go implementation of the same decision chain as policy.rego.
// It is the fallback engine and the reference the OPA policy is tested against.
type Native struct{}

func (Native) Name() string { return "native" }

// Match reports whether pattern matches s with ":" as the only delimiter.
// A pattern that does not compile matches nothing.
func Match(pattern, s string) bool {
	g, err := glob.Compile(pattern, ':')
	if err != nil {
		return false
	}
	return g.Match(s)
}

// AnyMatch reports whether any pattern matches s.
func AnyMatch(patterns []string, s string) bool {
	for _, p := range patterns {
		if Match(p, s) {
			return true
		}
	}
	return false
}

func (Native) Decide(_ context.Context, in Input) (Result, error) {
	c := in.Contract
	c.Normalize()
	key := in.Request.Resource + ":" + in.Request.Op

	denies := map[string]bool{}
	for _, d := range c.Deny {
		if Match(d, in.Request.Resource) || Match(d, key) {
			denies[fmt.Sprintf("contract_deny: %s", d)] = true
		}
	}
	inScope := AnyMatch(in.Scopes, key)
	if !inScope {
		denies[fmt.Sprintf("no_capability_scope: %s", key)] = true
	}
	if inScope && !AnyMatch(c.Scopes(), key) {
		denies[fmt.Sprintf("not_in_contract: %s", key)] = true
	}
	if len(denies) > 0 {
		reasons := make([]string, 0, len(denies))
		for r := range denies {
			reasons = append(reasons, r)
		}
		sort.Strings(reasons)
		return Result{Result: "deny", Reasons: reasons}, nil
	}

	sensitive := AnyMatch(c.Sensitive, key)
	tolerant := AnyMatch(c.TaintTolerant, key)
	var gates []string
	if in.Confidential && Match("net:**:*", key) && !tolerant {
		gates = append(gates, "confidential_session")
	}
	if in.Tainted && sensitive && !tolerant {
		gates = append(gates, "tainted_session")
	}
	switch {
	case len(gates) > 0:
		onTaint := c.OnTaint
		if onTaint != "hold" && onTaint != "deny" {
			onTaint = "deny"
		}
		return Result{Result: onTaint, Reasons: gates}, nil
	case AnyMatch(c.ApprovalRequired, key):
		return Result{Result: "hold", Reasons: []string{"approval_required"}}, nil
	case in.Tainted && tolerant:
		return Result{Result: "allow", Reasons: []string{"taint_tolerant"}}, nil
	}
	return Result{Result: "allow", Reasons: []string{}}, nil
}
