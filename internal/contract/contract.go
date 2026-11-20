// Package contract defines the intent contract: what a user confirmed an agent
// may do for one task. A contract is immutable once signed with the org key.
package contract

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/foxcolab/pact/internal/canon"
	"github.com/foxcolab/pact/internal/keys"
)

// Grant allows a set of operations on a resource pattern.
type Grant struct {
	Resource string   `json:"resource"`
	Ops      []string `json:"ops"`
}

// Delegation is always false in MVP 0.1; the field is kept for the schema.
type Delegation struct {
	Allowed bool `json:"allowed"`
}

// Contract is the signed, user-confirmed scope of one task.
type Contract struct {
	ContractID       string     `json:"contract_id"`
	TaskID           string     `json:"task_id"`
	Version          int        `json:"version"`
	Instruction      string     `json:"instruction"`
	AgentID          string     `json:"agent_id"`
	Allow            []Grant    `json:"allow"`
	Deny             []string   `json:"deny"`
	ApprovalRequired []string   `json:"approval_required"`
	Delegation       Delegation `json:"delegation"`
	Untrusted        []string   `json:"untrusted"`
	Sensitive        []string   `json:"sensitive"`
	TaintTolerant    []string   `json:"taint_tolerant"`
	// Confidential lists resources whose content must not leave: once the session
	// has read one, outbound requests are gated. Omitted when empty so contracts
	// signed before this field existed keep their hash.
	Confidential []string `json:"confidential,omitempty"`
	OnTaint      string   `json:"on_taint"`
	ExpiresAt    string   `json:"expires_at"`
	ConfirmedBy  string   `json:"confirmed_by"`
	ConfirmedAt  string   `json:"confirmed_at"`
	Hash         string   `json:"hash,omitempty"`
	Sig          string   `json:"sig,omitempty"`
}

// ResourceTypes is the catalogue of resource types and the operations each supports.
// A contract naming anything outside it is rejected.
var ResourceTypes = map[string][]string{
	"repo":   {"read", "write"},
	"tests":  {"run"},
	"deploy": {"deploy"},
	"llm":    {"generate"},
	"secret": {"read"},
	"net":    {"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"},
	"iam":    {"create_admin", "create_user"},
	"issue":  {"read"},
	"infra":  {"read", "write"},
}

var patternRe = regexp.MustCompile(`^[A-Za-z0-9_.*/-]+$`)

// NewIDs returns a fresh contract id and task id sharing one suffix.
func NewIDs() (contractID, taskID string) {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	s := hex.EncodeToString(b)
	return "c-" + s, "task-" + s
}

// Normalize replaces nil slices with empty ones and sorts ops so that
// equal contracts always canonicalise to equal bytes.
func (c *Contract) Normalize() {
	if c.Allow == nil {
		c.Allow = []Grant{}
	}
	for i := range c.Allow {
		if c.Allow[i].Ops == nil {
			c.Allow[i].Ops = []string{}
		}
		sort.Strings(c.Allow[i].Ops)
	}
	for _, p := range []*[]string{&c.Deny, &c.ApprovalRequired, &c.Untrusted, &c.Sensitive, &c.TaintTolerant} {
		if *p == nil {
			*p = []string{}
		}
	}
	if c.OnTaint == "" {
		c.OnTaint = "hold"
	}
}

// Validator checks a candidate contract against the resource catalogue.
type Validator struct {
	Workspaces []string         // known repo / tests / deploy names, e.g. "web-app"
	MaxTTL     time.Duration    // longest allowed lifetime
	Now        func() time.Time // injectable clock
}

func (v Validator) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func splitType(p string) (typ, rest string, err error) {
	typ, rest, ok := strings.Cut(p, ":")
	if !ok || rest == "" {
		return "", "", fmt.Errorf("%q is not of the form type:name", p)
	}
	if _, known := ResourceTypes[typ]; !known {
		return "", "", fmt.Errorf("unknown resource type %q in %q", typ, p)
	}
	return typ, rest, nil
}

func checkPatternChars(segments ...string) error {
	for _, s := range segments {
		if !patternRe.MatchString(s) {
			return fmt.Errorf("pattern segment %q has characters outside [A-Za-z0-9_.*/-]", s)
		}
	}
	return nil
}

func (v Validator) knownWorkspace(name string) bool {
	for _, w := range v.Workspaces {
		if name == w {
			return true
		}
	}
	return false
}

// checkResource validates a resource pattern such as "repo:web-app/**" or "secret:*".
func (v Validator) checkResource(p string, mustBeKnownTarget bool) (string, error) {
	typ, rest, err := splitType(p)
	if err != nil {
		return "", err
	}
	if strings.Contains(rest, ":") {
		return "", fmt.Errorf("resource %q must not contain a second ':'", p)
	}
	if err := checkPatternChars(rest); err != nil {
		return "", fmt.Errorf("resource %q: %w", p, err)
	}
	if mustBeKnownTarget {
		switch typ {
		case "repo":
			name, _, _ := strings.Cut(rest, "/")
			if !v.knownWorkspace(name) {
				return "", fmt.Errorf("unknown repository %q in %q", name, p)
			}
		case "tests", "deploy":
			if !v.knownWorkspace(rest) {
				return "", fmt.Errorf("unknown %s target %q", typ, rest)
			}
		case "llm":
			if rest != "gemini" {
				return "", fmt.Errorf("unknown model provider %q", rest)
			}
		}
	}
	return typ, nil
}

// checkKeyPattern validates a "resource:op" pattern such as "repo:web-app/deploy/**:write" or "net:**:*".
func (v Validator) checkKeyPattern(p string) error {
	parts := strings.Split(p, ":")
	if len(parts) != 3 {
		return fmt.Errorf("%q must be of the form type:name:op", p)
	}
	if _, known := ResourceTypes[parts[0]]; !known {
		return fmt.Errorf("unknown resource type %q in %q", parts[0], p)
	}
	return checkPatternChars(parts[1], parts[2])
}

// Validate reports every problem with a candidate contract.
func (v Validator) Validate(c *Contract) error {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if c.ContractID == "" || c.TaskID == "" {
		add("contract_id and task_id are required")
	}
	if c.Version < 1 {
		add("version must be at least 1")
	}
	if strings.TrimSpace(c.Instruction) == "" {
		add("instruction is required")
	}
	if len(c.Allow) == 0 {
		add("allow must grant at least one resource")
	}
	for _, g := range c.Allow {
		typ, err := v.checkResource(g.Resource, true)
		if err != nil {
			add("allow: %v", err)
			continue
		}
		if len(g.Ops) == 0 {
			add("allow: %q has no operations", g.Resource)
		}
		for _, op := range g.Ops {
			if !contains(ResourceTypes[typ], op) {
				add("allow: operation %q is not valid for %q", op, g.Resource)
			}
		}
	}
	for _, d := range c.Deny {
		if _, err := v.checkResource(d, false); err != nil {
			add("deny: %v", err)
		}
	}
	for _, u := range c.Untrusted {
		if _, err := v.checkResource(u, false); err != nil {
			add("untrusted: %v", err)
		}
	}
	for _, u := range c.Confidential {
		if _, err := v.checkResource(u, false); err != nil {
			add("confidential: %v", err)
		}
	}
	for name, list := range map[string][]string{"sensitive": c.Sensitive, "taint_tolerant": c.TaintTolerant, "approval_required": c.ApprovalRequired} {
		for _, p := range list {
			if err := v.checkKeyPattern(p); err != nil {
				add("%s: %v", name, err)
			}
		}
	}
	if c.OnTaint != "hold" && c.OnTaint != "deny" {
		add("on_taint must be \"hold\" or \"deny\"")
	}
	if c.Delegation.Allowed {
		add("delegation is not supported in MVP 0.1")
	}
	exp, err := time.Parse(time.RFC3339, c.ExpiresAt)
	switch {
	case err != nil:
		add("expires_at must be an RFC 3339 time")
	case !exp.After(v.now()):
		add("expires_at is in the past")
	case v.MaxTTL > 0 && exp.After(v.now().Add(v.MaxTTL+time.Minute)):
		add("expires_at is more than %s away", v.MaxTTL)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Scopes lists the "resource:op" capability scopes the contract grants.
func (c *Contract) Scopes() []string {
	var out []string
	for _, g := range c.Allow {
		for _, op := range g.Ops {
			out = append(out, g.Resource+":"+op)
		}
	}
	return out
}

func (c *Contract) unsignedHash() (string, error) {
	cp := *c
	cp.Hash, cp.Sig = "", ""
	cp.Normalize()
	return canon.Hash(cp)
}

// Sign records who confirmed the contract, then hashes and signs it with the org key.
func (c *Contract) Sign(org *keys.KeyPair, confirmedBy string, at time.Time) error {
	if strings.TrimSpace(confirmedBy) == "" {
		return errors.New("contract: confirmed_by is required before signing")
	}
	c.Normalize()
	c.ConfirmedBy = confirmedBy
	c.ConfirmedAt = at.UTC().Format(time.RFC3339)
	h, err := c.unsignedHash()
	if err != nil {
		return err
	}
	sig, err := org.Sign([]byte(h))
	if err != nil {
		return err
	}
	c.Hash, c.Sig = h, sig
	return nil
}

// Verify recomputes the hash and checks the org signature.
func (c *Contract) Verify(orgPub ed25519.PublicKey) error {
	h, err := c.unsignedHash()
	if err != nil {
		return err
	}
	if h != c.Hash {
		return errors.New("contract: hash does not match content")
	}
	if !keys.Verify(orgPub, []byte(c.Hash), c.Sig) {
		return errors.New("contract: bad org signature")
	}
	return nil
}

// Expired reports whether the contract's lifetime has ended.
func (c *Contract) Expired(now time.Time) bool {
	exp, err := time.Parse(time.RFC3339, c.ExpiresAt)
	return err != nil || !now.Before(exp)
}
