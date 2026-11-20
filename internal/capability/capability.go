// Package capability mints and verifies scoped, expiring tokens bound to one
// agent's public key. A token is base64url(payload).base64url(signature).
package capability

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/foxcolab/pact/internal/canon"
	"github.com/foxcolab/pact/internal/keys"
)

// Payload is the signed content of a capability.
type Payload struct {
	CapID        string   `json:"cap_id"`
	ContractHash string   `json:"contract_hash"`
	AgentID      string   `json:"agent_id"`
	AgentPubkey  string   `json:"agent_pubkey"`
	Scopes       []string `json:"scopes"`
	NotBefore    string   `json:"not_before"`
	ExpiresAt    string   `json:"expires_at"`
}

// NewID returns a fresh capability id.
func NewID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return "cap-" + strings.ToUpper(hex.EncodeToString(b))
}

// Mint signs the payload with the issuer key and returns the token.
func Mint(issuer *keys.KeyPair, p Payload) (string, error) {
	if p.CapID == "" || p.ContractHash == "" || p.AgentID == "" || p.AgentPubkey == "" {
		return "", errors.New("capability: cap_id, contract_hash, agent_id and agent_pubkey are required")
	}
	if _, err := keys.DecodePublic(p.AgentPubkey); err != nil {
		return "", err
	}
	if p.Scopes == nil {
		p.Scopes = []string{}
	}
	body, err := canon.Marshal(p)
	if err != nil {
		return "", err
	}
	seg := base64.RawURLEncoding.EncodeToString(body)
	sig, err := issuer.SignRaw([]byte(seg))
	if err != nil {
		return "", err
	}
	return seg + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Decode parses a token without checking its signature. Use only to read the
// claimed cap_id for logging; never to authorise.
func Decode(token string) (Payload, error) {
	var p Payload
	seg, _, ok := strings.Cut(token, ".")
	if !ok {
		return p, errors.New("capability: malformed token")
	}
	body, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return p, errors.New("capability: malformed payload encoding")
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return p, errors.New("capability: malformed payload")
	}
	return p, nil
}

// Verify checks the issuer signature over the exact payload bytes received,
// so changing any byte of the token invalidates it.
func Verify(issuerPub ed25519.PublicKey, token string) (Payload, error) {
	var p Payload
	seg, sigSeg, ok := strings.Cut(token, ".")
	if !ok || seg == "" || sigSeg == "" {
		return p, errors.New("capability: malformed token")
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(sigSeg)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return p, errors.New("capability: malformed signature")
	}
	if !ed25519.Verify(issuerPub, []byte(seg), sig) {
		return p, errors.New("capability: bad issuer signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return p, errors.New("capability: malformed payload encoding")
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, errors.New("capability: malformed payload")
	}
	return p, nil
}

// ValidAt reports whether now falls inside the capability's validity window.
func (p Payload) ValidAt(now time.Time) bool {
	nb, err1 := time.Parse(time.RFC3339, p.NotBefore)
	exp, err2 := time.Parse(time.RFC3339, p.ExpiresAt)
	if err1 != nil || err2 != nil {
		return false
	}
	return !now.Before(nb.Add(-5*time.Second)) && now.Before(exp)
}
