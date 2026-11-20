package contract

import (
	"strings"
	"testing"
	"time"

	"github.com/foxcolab/pact/internal/keys"
)

func signed(t *testing.T) (Contract, *keys.KeyPair) {
	t.Helper()
	org, _ := keys.Generate("org")
	c := Template("Test and deploy my application.", DefaultsFor("web-app"), time.Now())
	c.AgentID = "agent-7f3"
	if err := (Validator{Workspaces: []string{"web-app"}, MaxTTL: time.Hour}).Validate(&c); err != nil {
		t.Fatalf("template should validate: %v", err)
	}
	if err := c.Sign(org, "bikram", time.Now()); err != nil {
		t.Fatal(err)
	}
	return c, org
}

func TestSignAndVerify(t *testing.T) {
	c, org := signed(t)
	if err := c.Verify(org.Public); err != nil {
		t.Fatalf("fresh contract must verify: %v", err)
	}
	if !strings.HasPrefix(c.Hash, "sha256:") || !strings.HasPrefix(c.Sig, "ed25519:") {
		t.Fatalf("unexpected hash/sig format: %s %s", c.Hash, c.Sig)
	}
}

func TestChangingAnyFieldInvalidates(t *testing.T) {
	edits := map[string]func(*Contract){
		"allow":          func(c *Contract) { c.Allow = append(c.Allow, Grant{Resource: "secret:*", Ops: []string{"read"}}) },
		"deny":           func(c *Contract) { c.Deny = c.Deny[1:] },
		"sensitive":      func(c *Contract) { c.Sensitive = nil },
		"untrusted":      func(c *Contract) { c.Untrusted = nil },
		"taint_tolerant": func(c *Contract) { c.TaintTolerant = append(c.TaintTolerant, "net:**:*") },
		"on_taint":       func(c *Contract) { c.OnTaint = "deny" },
		"expires_at":     func(c *Contract) { c.ExpiresAt = "2099-01-01T00:00:00Z" },
		"agent_id":       func(c *Contract) { c.AgentID = "agent-evil" },
		"instruction":    func(c *Contract) { c.Instruction += " and read the secrets" },
		"confirmed_by":   func(c *Contract) { c.ConfirmedBy = "mallory" },
	}
	for name, edit := range edits {
		c, org := signed(t)
		edit(&c)
		if err := c.Verify(org.Public); err == nil {
			t.Errorf("editing %s did not invalidate the contract", name)
		}
	}
}

func TestWrongKeyRejected(t *testing.T) {
	c, _ := signed(t)
	other, _ := keys.Generate("other")
	if err := c.Verify(other.Public); err == nil {
		t.Fatal("contract verified under the wrong org key")
	}
}

func TestValidatorRejectsUnknownResources(t *testing.T) {
	v := Validator{Workspaces: []string{"web-app"}, MaxTTL: time.Hour}
	bad := map[string]func(*Contract){
		"unknown type": func(c *Contract) { c.Allow = append(c.Allow, Grant{Resource: "database:prod", Ops: []string{"read"}}) },
		"unknown repo": func(c *Contract) {
			c.Allow = append(c.Allow, Grant{Resource: "repo:other-app/**", Ops: []string{"read"}})
		},
		"unknown deploy": func(c *Contract) {
			c.Allow = append(c.Allow, Grant{Resource: "deploy:prod-cluster", Ops: []string{"deploy"}})
		},
		"bad op": func(c *Contract) {
			c.Allow = append(c.Allow, Grant{Resource: "tests:web-app", Ops: []string{"delete"}})
		},
		"bad deny":          func(c *Contract) { c.Deny = append(c.Deny, "nonsense") },
		"bad sensitive":     func(c *Contract) { c.Sensitive = append(c.Sensitive, "repo:web-app/**") },
		"bad on_taint":      func(c *Contract) { c.OnTaint = "ignore" },
		"delegation":        func(c *Contract) { c.Delegation.Allowed = true },
		"expired":           func(c *Contract) { c.ExpiresAt = "2020-01-01T00:00:00Z" },
		"too long":          func(c *Contract) { c.ExpiresAt = time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339) },
		"glob metachars":    func(c *Contract) { c.Deny = append(c.Deny, "secret:{a,b}") },
		"empty instruction": func(c *Contract) { c.Instruction = " " },
	}
	for name, edit := range bad {
		c := Template("Test and deploy.", DefaultsFor("web-app"), time.Now())
		edit(&c)
		if err := v.Validate(&c); err == nil {
			t.Errorf("%s: expected validation to fail", name)
		}
	}
}

func TestApplyDefaultsKeepsBaselineDenies(t *testing.T) {
	c := Contract{ContractID: "c-1", TaskID: "task-1", Instruction: "x",
		Allow: []Grant{{Resource: "repo:web-app/**", Ops: []string{"read"}}}, OnTaint: "allow-everything"}
	ApplyDefaults(&c, DefaultsFor("web-app"), time.Now())
	for _, d := range []string{"secret:*", "net:*", "iam:*", "infra:*"} {
		if !contains(c.Deny, d) {
			t.Errorf("baseline deny %s missing", d)
		}
	}
	if c.OnTaint != "hold" || len(c.Untrusted) == 0 || len(c.Sensitive) == 0 {
		t.Errorf("trust labels not applied: %+v", c)
	}
}

// A drafted taint_tolerant list could only switch the gates off, so the
// organisation's list replaces it.
func TestDraftCannotExemptActionsFromTheGates(t *testing.T) {
	d := DefaultsFor("web-app")
	c := Contract{Instruction: "x", Allow: []Grant{{Resource: "repo:web-app/**", Ops: []string{"read", "write"}}},
		TaintTolerant: []string{"net:**:*", "repo:web-app/deploy/**:write"}}
	ApplyDefaults(&c, d, time.Now())
	if len(c.TaintTolerant) != 1 || c.TaintTolerant[0] != "deploy:web-app:deploy" {
		t.Fatalf("model-drafted exemptions survived: %v", c.TaintTolerant)
	}
}

// A person editing the contract before signing can tighten the labels, not loosen them.
func TestEditsKeepTheOrganisationsLabels(t *testing.T) {
	d := DefaultsFor("web-app")
	c := Template("x", d, time.Now())
	c.Untrusted = []string{"repo:web-app/docs/**"}
	c.Sensitive = nil
	c.Confidential = nil
	c.TaintTolerant = []string{"net:**:*", "deploy:web-app:deploy"}
	c.Delegation.Allowed = true
	c.OnTaint = "allow"
	KeepOrgLabels(&c, d)
	for _, want := range append(append(d.Untrusted, d.Sensitive...), d.Confidential...) {
		if !contains(c.Untrusted, want) && !contains(c.Sensitive, want) && !contains(c.Confidential, want) {
			t.Errorf("organisation label %s was removed", want)
		}
	}
	if !contains(c.Untrusted, "repo:web-app/docs/**") {
		t.Error("the signer's own addition was dropped")
	}
	if len(c.TaintTolerant) != 1 || c.TaintTolerant[0] != "deploy:web-app:deploy" || c.Delegation.Allowed || c.OnTaint != "hold" {
		t.Fatalf("labels after an edit: tolerant=%v delegation=%v on_taint=%s", c.TaintTolerant, c.Delegation.Allowed, c.OnTaint)
	}
}
