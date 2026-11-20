package policy

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/foxcolab/pact/internal/contract"
)

func testContract() contract.Contract {
	return contract.Template("Test and deploy my application.", contract.DefaultsFor("web-app"), time.Now())
}

type tc struct {
	name         string
	resource     string
	op           string
	tainted      bool
	confidential bool
	edit         func(*contract.Contract, *[]string)
	want         string
	reasons      []string
}

var cases = []tc{
	{name: "read source", resource: "repo:web-app/app/cart.py", op: "read", want: "allow"},
	{name: "list root", resource: "repo:web-app/", op: "read", want: "allow"},
	{name: "write source", resource: "repo:web-app/app/cart.py", op: "write", want: "allow"},
	{name: "run tests", resource: "tests:web-app", op: "run", want: "allow"},
	{name: "deploy", resource: "deploy:web-app", op: "deploy", want: "allow"},
	{name: "llm", resource: "llm:gemini", op: "generate", want: "allow"},

	// Deny by contract and by missing scope (S2, S3, S4).
	{name: "secret read", resource: "secret:prod/db-password", op: "read", want: "deny",
		reasons: []string{"contract_deny: secret:*", "no_capability_scope: secret:prod/db-password:read"}},
	{name: "egress to sink", resource: "net:sink", op: "POST", want: "deny",
		reasons: []string{"contract_deny: net:*", "no_capability_scope: net:sink:POST"}},
	{name: "egress to dotted host", resource: "net:evil.example.com", op: "POST", want: "deny",
		reasons: []string{"contract_deny: net:*", "no_capability_scope: net:evil.example.com:POST"}},
	{name: "admin creation", resource: "iam:user", op: "create_admin", want: "deny",
		reasons: []string{"contract_deny: iam:*", "no_capability_scope: iam:user:create_admin"}},
	{name: "other repo", resource: "repo:other-app/x", op: "read", want: "deny",
		reasons: []string{"no_capability_scope: repo:other-app/x:read"}},
	{name: "other deploy target", resource: "deploy:prod", op: "deploy", want: "deny",
		reasons: []string{"no_capability_scope: deploy:prod:deploy"}},

	// Taint-gating (S6, S7, S8).
	{name: "clean pipeline edit", resource: "repo:web-app/deploy/pipeline.yaml", op: "write", want: "allow"},
	{name: "tainted pipeline edit", resource: "repo:web-app/deploy/pipeline.yaml", op: "write", tainted: true, want: "hold", reasons: []string{"tainted_session"}},
	{name: "tainted ci edit", resource: "repo:web-app/.github/workflows/ci.yml", op: "write", tainted: true, want: "hold", reasons: []string{"tainted_session"}},
	{name: "tainted pipeline read", resource: "repo:web-app/deploy/pipeline.yaml", op: "read", tainted: true, want: "allow"},
	{name: "tainted source edit", resource: "repo:web-app/app/cart.py", op: "write", tainted: true, want: "allow"},
	{name: "tainted tests", resource: "tests:web-app", op: "run", tainted: true, want: "allow"},
	{name: "tainted deploy is tolerated", resource: "deploy:web-app", op: "deploy", tainted: true, want: "allow", reasons: []string{"taint_tolerant"}},
	{name: "tainted secret read stays a contract deny", resource: "secret:prod/db-password", op: "read", tainted: true, want: "deny",
		reasons: []string{"contract_deny: secret:*", "no_capability_scope: secret:prod/db-password:read"}},
	{name: "on_taint deny", resource: "repo:web-app/deploy/pipeline.yaml", op: "write", tainted: true,
		edit: func(c *contract.Contract, _ *[]string) { c.OnTaint = "deny" }, want: "deny", reasons: []string{"tainted_session"}},

	// Egress to an allowlisted host: allowed clean, held once tainted.
	{name: "allowlisted egress clean", resource: "net:api.partner.example", op: "POST", want: "allow", edit: allowPartner},
	{name: "allowlisted egress tainted", resource: "net:api.partner.example", op: "POST", tainted: true, want: "hold", reasons: []string{"tainted_session"}, edit: allowPartner},
	{name: "other host still denied", resource: "net:evil.example.com", op: "POST", want: "deny",
		reasons: []string{"no_capability_scope: net:evil.example.com:POST"}, edit: allowPartner},

	// Exfiltration gate: after confidential data is read, egress is held even to an allowlisted host.
	{name: "allowlisted egress after confidential read", resource: "net:api.partner.example", op: "POST", confidential: true, want: "hold",
		reasons: []string{"confidential_session"}, edit: allowPartner},
	{name: "confidential and tainted", resource: "net:api.partner.example", op: "POST", confidential: true, tainted: true, want: "hold",
		reasons: []string{"confidential_session", "tainted_session"}, edit: allowPartner},
	{name: "confidential session may still edit files", resource: "repo:web-app/app/cart.py", op: "write", confidential: true, want: "allow"},
	{name: "confidential session may still deploy", resource: "deploy:web-app", op: "deploy", confidential: true, want: "allow"},
	{name: "confidential session, egress denied by contract stays a deny", resource: "net:sink", op: "POST", confidential: true, want: "deny"},

	// Capability and contract must both cover the request.
	{name: "capability wider than contract", resource: "secret:prod/db-password", op: "read", want: "deny",
		edit:    func(c *contract.Contract, scopes *[]string) { c.Deny = nil; *scopes = append(*scopes, "secret:*:read") },
		reasons: []string{"not_in_contract: secret:prod/db-password:read"}},
	{name: "capability narrower than contract", resource: "repo:web-app/app/cart.py", op: "write", want: "deny",
		edit:    func(_ *contract.Contract, scopes *[]string) { *scopes = []string{"repo:web-app/**:read"} },
		reasons: []string{"no_capability_scope: repo:web-app/app/cart.py:write"}},
	{name: "op-specific deny", resource: "repo:web-app/app/cart.py", op: "write", want: "deny",
		edit:    func(c *contract.Contract, _ *[]string) { c.Deny = append(c.Deny, "repo:web-app/app/**:write") },
		reasons: []string{"contract_deny: repo:web-app/app/**:write"}},
	{name: "approval required", resource: "deploy:web-app", op: "deploy", want: "hold", reasons: []string{"approval_required"},
		edit: func(c *contract.Contract, _ *[]string) { c.ApprovalRequired = []string{"deploy:web-app:deploy"} }},
	{name: "empty contract denies", resource: "repo:web-app/app/cart.py", op: "read", want: "deny",
		edit:    func(c *contract.Contract, scopes *[]string) { *c = contract.Contract{}; *scopes = nil },
		reasons: []string{"no_capability_scope: repo:web-app/app/cart.py:read"}},
}

func allowPartner(c *contract.Contract, scopes *[]string) {
	c.Allow = append(c.Allow, contract.Grant{Resource: "net:api.partner.example", Ops: []string{"POST"}})
	c.Deny = []string{"secret:*", "iam:*", "infra:*"}
	*scopes = c.Scopes()
}

func buildInput(c tc) Input {
	ct := testContract()
	scopes := ct.Scopes()
	if c.edit != nil {
		c.edit(&ct, &scopes)
	}
	return Input{Scopes: scopes, Contract: ct, Request: Request{Resource: c.resource, Op: c.op}, Tainted: c.tainted, Confidential: c.confidential}
}

func engines(t *testing.T) []Decider {
	t.Helper()
	opa, err := NewOPA(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return []Decider{opa, Native{}}
}

func TestDecisionTable(t *testing.T) {
	for _, eng := range engines(t) {
		for _, c := range cases {
			got, err := eng.Decide(context.Background(), buildInput(c))
			if err != nil {
				t.Errorf("[%s] %s: %v", eng.Name(), c.name, err)
				continue
			}
			if got.Result != c.want {
				t.Errorf("[%s] %s: got %s %v, want %s", eng.Name(), c.name, got.Result, got.Reasons, c.want)
			}
			if c.reasons != nil && !reflect.DeepEqual(got.Reasons, c.reasons) {
				t.Errorf("[%s] %s: reasons %v, want %v", eng.Name(), c.name, got.Reasons, c.reasons)
			}
		}
	}
}

// The OPA policy and the native matcher must agree on every case, in full.
func TestEnginesAgree(t *testing.T) {
	es := engines(t)
	resources := []string{"repo:web-app/", "repo:web-app/app/cart.py", "repo:web-app/deploy/pipeline.yaml", "repo:web-app/.github/workflows/ci.yml",
		"repo:web-app/deps/payments-sdk/README.md", "repo:other/x", "tests:web-app", "deploy:web-app", "deploy:prod", "llm:gemini",
		"secret:prod/db-password", "secret:staging/key", "net:sink", "net:a.b.c", "net:api.partner.example", "iam:user", "infra:db"}
	ops := []string{"read", "write", "run", "deploy", "generate", "POST", "GET", "create_admin"}
	n := 0
	for _, r := range resources {
		for _, op := range ops {
			for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
				for _, edit := range []func(*contract.Contract, *[]string){nil, allowPartner} {
					in := buildInput(tc{resource: r, op: op, tainted: flags[0], confidential: flags[1], edit: edit})
					a, errA := es[0].Decide(context.Background(), in)
					b, errB := es[1].Decide(context.Background(), in)
					if errA != nil || errB != nil {
						t.Fatalf("%s:%s: %v / %v", r, op, errA, errB)
					}
					if !reflect.DeepEqual(a, b) {
						t.Errorf("%s:%s tainted=%v confidential=%v: opa=%+v native=%+v", r, op, flags[0], flags[1], a, b)
					}
					n++
				}
			}
		}
	}
	t.Logf("compared %d inputs", n)
}

// Same input, same answer.
func TestDeterministic(t *testing.T) {
	for _, eng := range engines(t) {
		in := buildInput(cases[6])
		first, _ := eng.Decide(context.Background(), in)
		for i := 0; i < 50; i++ {
			got, _ := eng.Decide(context.Background(), in)
			if !reflect.DeepEqual(first, got) {
				t.Fatalf("[%s] decision changed between evaluations", eng.Name())
			}
		}
	}
}

// Pins the glob semantics the contract relies on.
func TestGlobSemantics(t *testing.T) {
	yes := [][2]string{
		{"secret:*", "secret:prod/db-password"},
		{"net:*", "net:evil.example.com"},
		{"repo:web-app/**:read", "repo:web-app/app/cart.py:read"},
		{"repo:web-app/**:read", "repo:web-app/:read"},
		{"repo:web-app/deploy/**:write", "repo:web-app/deploy/pipeline.yaml:write"},
		{"net:**:*", "net:api.partner.example:POST"},
		{"repo:web-app/deps/**", "repo:web-app/deps/payments-sdk/README.md"},
	}
	no := [][2]string{
		{"secret:*", "secret:prod/db-password:read:extra"},
		{"repo:web-app/**:read", "repo:web-app/app/cart.py:write"},
		{"repo:web-app/deploy/**:write", "repo:web-app/deployment/x:write"},
		{"repo:web-app/deps/**", "repo:web-app/app/deps.py"},
		{"net:*", "secret:prod"},
	}
	for _, p := range yes {
		if !Match(p[0], p[1]) {
			t.Errorf("%q should match %q", p[0], p[1])
		}
	}
	for _, p := range no {
		if Match(p[0], p[1]) {
			t.Errorf("%q should not match %q", p[0], p[1])
		}
	}
}
