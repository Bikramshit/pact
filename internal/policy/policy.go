// Package policy turns (capability, contract, request, session) into
// allow / deny / hold. Two interchangeable engines implement Decider: the
// embedded OPA policy (default) and a native Go matcher (fallback and
// cross-check). Any evaluation error is returned so the broker can deny.
package policy

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/foxcolab/pact/internal/contract"
	"github.com/open-policy-agent/opa/v1/rego"
)

//go:embed policy.rego
var regoSource string

// Request is the action as derived by the broker, never as claimed by the agent.
type Request struct {
	Tool     string `json:"tool"`
	Resource string `json:"resource"`
	Op       string `json:"op"`
}

// Input is everything a decision depends on.
type Input struct {
	Scopes   []string
	Contract contract.Contract
	Request  Request
	Tainted  bool
	// Confidential is true once the session has read data the contract marks confidential.
	Confidential bool
}

// Result is the policy's answer.
type Result struct {
	Result  string   `json:"result"` // allow | deny | hold
	Reasons []string `json:"reasons"`
}

// Decider evaluates one request.
type Decider interface {
	Decide(ctx context.Context, in Input) (Result, error)
	Name() string
}

// OPA evaluates the embedded Rego policy.
type OPA struct {
	query rego.PreparedEvalQuery
}

// NewOPA compiles the embedded policy once.
func NewOPA(ctx context.Context) (*OPA, error) {
	q, err := rego.New(
		rego.Query("data.pact.decision"),
		rego.Module("policy.rego", regoSource),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("policy: compiling rego: %w", err)
	}
	return &OPA{query: q}, nil
}

func (o *OPA) Name() string { return "opa" }

func (o *OPA) Decide(ctx context.Context, in Input) (Result, error) {
	c := in.Contract
	c.Normalize()
	grants := make([]any, 0, len(c.Allow))
	for _, g := range c.Allow {
		grants = append(grants, map[string]any{"resource": g.Resource, "ops": toAny(g.Ops)})
	}
	input := map[string]any{
		"capability": map[string]any{"scopes": toAny(in.Scopes)},
		"contract": map[string]any{
			"allow":             grants,
			"deny":              toAny(c.Deny),
			"sensitive":         toAny(c.Sensitive),
			"taint_tolerant":    toAny(c.TaintTolerant),
			"approval_required": toAny(c.ApprovalRequired),
			"on_taint":          c.OnTaint,
		},
		"request": map[string]any{"tool": in.Request.Tool, "resource": in.Request.Resource, "op": in.Request.Op},
		"session": map[string]any{"tainted": in.Tainted, "confidential": in.Confidential},
	}
	rs, err := o.query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return Result{}, fmt.Errorf("policy: evaluation failed: %w", err)
	}
	if len(rs) != 1 || len(rs[0].Expressions) != 1 {
		return Result{}, errors.New("policy: decision is undefined")
	}
	m, ok := rs[0].Expressions[0].Value.(map[string]any)
	if !ok {
		return Result{}, errors.New("policy: decision has the wrong shape")
	}
	res := Result{Reasons: []string{}}
	res.Result, _ = m["result"].(string)
	if rr, ok := m["reasons"].([]any); ok {
		for _, r := range rr {
			if s, ok := r.(string); ok {
				res.Reasons = append(res.Reasons, s)
			}
		}
	}
	return check(res)
}

func check(res Result) (Result, error) {
	switch res.Result {
	case "allow", "deny", "hold":
		return res, nil
	}
	return Result{}, fmt.Errorf("policy: unexpected result %q", res.Result)
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// New returns the named engine: "opa" (default) or "native".
func New(ctx context.Context, name string) (Decider, error) {
	switch name {
	case "", "opa":
		return NewOPA(ctx)
	case "native":
		return Native{}, nil
	}
	return nil, fmt.Errorf("policy: unknown engine %q", name)
}
