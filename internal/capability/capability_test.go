package capability

import (
	"testing"
	"time"

	"github.com/foxcolab/pact/internal/keys"
)

func mint(t *testing.T) (string, *keys.KeyPair) {
	t.Helper()
	issuer, _ := keys.Generate("issuer")
	agent, _ := keys.Generate("agent")
	now := time.Now().UTC()
	tok, err := Mint(issuer, Payload{
		CapID: "cap-A7", ContractHash: "sha256:9f3c", AgentID: "agent-7f3", AgentPubkey: agent.PublicString(),
		Scopes:    []string{"repo:web-app/**:read", "tests:web-app:run"},
		NotBefore: now.Format(time.RFC3339), ExpiresAt: now.Add(30 * time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok, issuer
}

func TestMintVerify(t *testing.T) {
	tok, issuer := mint(t)
	p, err := Verify(issuer.Public, tok)
	if err != nil {
		t.Fatal(err)
	}
	if p.CapID != "cap-A7" || len(p.Scopes) != 2 || !p.ValidAt(time.Now()) {
		t.Fatalf("unexpected payload %+v", p)
	}
}

// Phase 1 exit test: flipping any one byte of a capability makes verification fail.
func TestFlippingAnyByteFails(t *testing.T) {
	tok, issuer := mint(t)
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	for i := 0; i < len(tok); i++ {
		if tok[i] == '.' {
			continue
		}
		for _, repl := range []byte{alphabet[(i*7)%64], alphabet[(i*13+5)%64]} {
			if repl == tok[i] {
				continue
			}
			mutated := tok[:i] + string(repl) + tok[i+1:]
			if _, err := Verify(issuer.Public, mutated); err == nil {
				t.Fatalf("token still verifies after changing byte %d from %q to %q", i, tok[i], repl)
			}
		}
	}
}

func TestWrongIssuerRejected(t *testing.T) {
	tok, _ := mint(t)
	other, _ := keys.Generate("other")
	if _, err := Verify(other.Public, tok); err == nil {
		t.Fatal("token verified under the wrong issuer key")
	}
}

func TestExpiredWindow(t *testing.T) {
	p := Payload{NotBefore: "2020-01-01T00:00:00Z", ExpiresAt: "2020-01-01T00:30:00Z"}
	if p.ValidAt(time.Now()) {
		t.Fatal("expired capability reported valid")
	}
}
