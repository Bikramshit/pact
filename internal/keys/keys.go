// Package keys holds the three Ed25519 key pairs of the trusted core:
// org (contracts), issuer (capabilities) and evidence (records).
package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const prefix = "ed25519:"

// KeyPair is a named signing key. A local key keeps its private half inside
// this package. A remote key (Cloud KMS) has no private half here at all:
// signing is a call to the service that holds it.
type KeyPair struct {
	Name    string
	Public  ed25519.PublicKey
	Backend string // "local" or "cloud-kms"
	private ed25519.PrivateKey
	remote  func(msg []byte) ([]byte, error)
}

// Generate creates a fresh key pair.
func Generate(name string) (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &KeyPair{Name: name, Public: pub, Backend: "local", private: priv}, nil
}

// Remote returns a key whose signatures come from sign. Every signature is
// checked against pub before it is returned, so a misbehaving signer cannot
// hand back something that will not verify.
func Remote(name, backend string, pub ed25519.PublicKey, sign func(msg []byte) ([]byte, error)) *KeyPair {
	return &KeyPair{Name: name, Public: pub, Backend: backend, remote: sign}
}

// Sign returns "ed25519:<base64url signature>" over msg.
func (k *KeyPair) Sign(msg []byte) (string, error) {
	raw, err := k.SignRaw(msg)
	if err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// SignRaw returns the raw signature bytes over msg.
func (k *KeyPair) SignRaw(msg []byte) ([]byte, error) {
	if k.remote == nil {
		return ed25519.Sign(k.private, msg), nil
	}
	sig, err := k.remote(msg)
	if err != nil {
		return nil, fmt.Errorf("keys: %s key (%s): %w", k.Name, k.Backend, err)
	}
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(k.Public, msg, sig) {
		return nil, fmt.Errorf("keys: %s key (%s) returned a signature that does not verify", k.Name, k.Backend)
	}
	return sig, nil
}

// PublicString returns "ed25519:<base64url public key>".
func (k *KeyPair) PublicString() string { return EncodePublic(k.Public) }

// EncodePublic formats a public key as "ed25519:<base64url>".
func EncodePublic(pub ed25519.PublicKey) string {
	return prefix + base64.RawURLEncoding.EncodeToString(pub)
}

// DecodePublic parses "ed25519:<base64url>".
func DecodePublic(s string) (ed25519.PublicKey, error) {
	if !strings.HasPrefix(s, prefix) {
		return nil, errors.New("keys: public key must start with ed25519:")
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, prefix))
	if err != nil {
		return nil, fmt.Errorf("keys: bad public key encoding: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, errors.New("keys: wrong public key length")
	}
	return ed25519.PublicKey(b), nil
}

// Verify checks an "ed25519:<base64url>" signature. Any malformed input is false.
func Verify(pub ed25519.PublicKey, msg []byte, sig string) bool {
	if len(pub) != ed25519.PublicKeySize || !strings.HasPrefix(sig, prefix) {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(sig, prefix))
	if err != nil || len(b) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, msg, b)
}

// Set is the trusted core's key material.
type Set struct {
	Org      *KeyPair
	Issuer   *KeyPair
	Evidence *KeyPair
	// EvidenceCert is the org key's signature over the evidence public key. The
	// evidence key stays local so that recording a decision costs microseconds;
	// this certificate ties it to the org key, which can live in a KMS.
	EvidenceCert string
}

// EvidenceCertMessage is what the org key signs to vouch for an evidence key.
func EvidenceCertMessage(evidencePub string) []byte {
	return []byte("pact-evidence-key:" + evidencePub)
}

// Certify signs the evidence key with the org key. Call it after any key is replaced.
func (s *Set) Certify() error {
	sig, err := s.Org.Sign(EvidenceCertMessage(s.Evidence.PublicString()))
	if err != nil {
		return err
	}
	s.EvidenceCert = sig
	return nil
}

// VerifyEvidenceCert checks that orgPub vouches for evidencePub.
func VerifyEvidenceCert(orgPub ed25519.PublicKey, evidencePub, cert string) bool {
	return Verify(orgPub, EvidenceCertMessage(evidencePub), cert)
}

// LoadOrGenerate reads the three keys from dir, creating any that are missing.
// With an empty dir the keys are ephemeral (tests, local runs without a volume).
func LoadOrGenerate(dir string) (*Set, error) {
	s := &Set{}
	for _, slot := range []struct {
		name string
		dst  **KeyPair
	}{{"org", &s.Org}, {"issuer", &s.Issuer}, {"evidence", &s.Evidence}} {
		kp, err := loadOrGenerateOne(dir, slot.name)
		if err != nil {
			return nil, err
		}
		*slot.dst = kp
	}
	if err := s.Certify(); err != nil {
		return nil, err
	}
	return s, nil
}

func loadOrGenerateOne(dir, name string) (*KeyPair, error) {
	if dir == "" {
		return Generate(name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".key")
	if b, err := os.ReadFile(path); err == nil {
		seed, derr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(string(b)))
		if derr != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("keys: %s is not a valid key file", path)
		}
		priv := ed25519.NewKeyFromSeed(seed)
		return &KeyPair{Name: name, Public: priv.Public().(ed25519.PublicKey), Backend: "local", private: priv}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	kp, err := Generate(name)
	if err != nil {
		return nil, err
	}
	seed := base64.RawURLEncoding.EncodeToString(kp.private.Seed())
	if err := os.WriteFile(path, []byte(seed+"\n"), 0o600); err != nil {
		return nil, err
	}
	return kp, nil
}
