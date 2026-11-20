// Package broker is the agent's only door. Every tool call arrives here with a
// capability and a request signature; the broker verifies both, asks the policy,
// records the decision, and only then acts with its own credentials.
package broker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/foxcolab/pact/internal/broker/executors"
	"github.com/foxcolab/pact/internal/capability"
	"github.com/foxcolab/pact/internal/contract"
	"github.com/foxcolab/pact/internal/events"
	"github.com/foxcolab/pact/internal/evidence"
	"github.com/foxcolab/pact/internal/keys"
	"github.com/foxcolab/pact/internal/policy"
	"github.com/foxcolab/pact/internal/store"
	"github.com/foxcolab/pact/internal/workspace"
)

// Modes.
const (
	ModeOn  = "on"  // full broker: capabilities, policy, taint-gating, evidence
	ModeOff = "off" // passthrough: every tool call executes unchecked (scoreboard baseline)
	// ModeSupervisor is the "AI guardrail" scoreboard baseline: no capabilities and
	// no policy, but a second model call reviews each tool call before it executes.
	ModeSupervisor = "supervisor"
)

// Config tunes the broker.
type Config struct {
	Mode         string        // default mode for new sessions
	AllowOffMode bool          // whether a session may run with PACT off (scoreboard only)
	HoldTimeout  time.Duration // how long a held request stays open
	MaxSkew      time.Duration // accepted distance between request timestamp and now
	CapTTL       time.Duration // capability lifetime
}

// CapabilityRecord is what the issuer keeps about a minted capability.
type CapabilityRecord struct {
	CapID           string             `json:"cap_id"`
	Token           string             `json:"token"`
	Payload         capability.Payload `json:"payload"`
	TaskID          string             `json:"task_id"`
	ContractID      string             `json:"contract_id"`
	Mode            string             `json:"mode"`
	Revoked         bool               `json:"revoked"`
	AutoRejectHolds bool               `json:"auto_reject_holds"`
	CreatedAt       string             `json:"created_at"`
}

// SessionTaint is one untrusted source the agent has read, with the text as it saw it.
type SessionTaint struct {
	Seq      int64  `json:"seq"`
	Resource string `json:"resource"`
	SHA256   string `json:"sha256"`
	Excerpt  string `json:"excerpt"`
}

// TestState remembers the last test run so a deploy can say whether tests passed for that exact tree.
type TestState struct {
	Version string `json:"version"`
	Passed  bool   `json:"passed"`
}

// Session is the broker's per-capability state. Taint is monotonic: once set it never clears.
type Session struct {
	SessionID    string         `json:"session_id"`
	TaskID       string         `json:"task_id"`
	AgentID      string         `json:"agent_id"`
	ContractHash string         `json:"contract_hash"`
	Mode         string         `json:"mode"`
	Tainted      bool           `json:"tainted"`
	TaintSources []SessionTaint `json:"taint_sources"`
	// Confidential is set once the session has read data the contract marks
	// confidential; like taint it never clears. Only resource names are kept.
	Confidential        bool       `json:"confidential,omitempty"`
	ConfidentialSources []string   `json:"confidential_sources,omitempty"`
	LastTest            *TestState `json:"last_test,omitempty"`
	Calls               int        `json:"calls"`
	UpdatedAt           string     `json:"updated_at"`
}

// CallMetric times one tool call so the scoreboard can report added latency.
type CallMetric struct {
	Tool     string  `json:"tool"`
	Decision string  `json:"decision"`
	TotalMS  float64 `json:"total_ms"`
	ExecMS   float64 `json:"exec_ms"`
	Reason   string  `json:"reason,omitempty"` // why the AI supervisor blocked the call
}

// Broker wires verification, policy, execution and evidence together.
type Broker struct {
	cfg       Config
	store     store.Store
	keys      *keys.Set
	contracts *contract.Registry
	policy    policy.Decider
	evidence  *evidence.Engine
	exec      *executors.Targets
	ws        *workspace.Workspace
	hub       *events.Hub

	Agents *Agents
	Holds  *HoldQueue

	now func() time.Time

	mu       sync.Mutex
	sessions map[string]*sync.Mutex
	metrics  map[string][]CallMetric
}

// New builds a broker.
func New(cfg Config, st store.Store, ks *keys.Set, reg *contract.Registry, dec policy.Decider, ev *evidence.Engine,
	exec *executors.Targets, ws *workspace.Workspace, hub *events.Hub) *Broker {
	if cfg.Mode == "" {
		cfg.Mode = ModeOn
	}
	if cfg.HoldTimeout == 0 {
		cfg.HoldTimeout = 120 * time.Second
	}
	if cfg.MaxSkew == 0 {
		cfg.MaxSkew = 60 * time.Second
	}
	if cfg.CapTTL == 0 {
		cfg.CapTTL = 30 * time.Minute
	}
	return &Broker{cfg: cfg, store: st, keys: ks, contracts: reg, policy: dec, evidence: ev, exec: exec, ws: ws, hub: hub,
		Agents: NewAgents(), Holds: NewHoldQueue(), now: time.Now, sessions: map[string]*sync.Mutex{}, metrics: map[string][]CallMetric{}}
}

// Mode returns the default mode.
func (b *Broker) Mode() string { return b.cfg.Mode }

// PolicyEngine names the decision engine in use.
func (b *Broker) PolicyEngine() string { return b.policy.Name() }

// MintOptions adjusts one capability.
type MintOptions struct {
	Mode            string // "" means the broker default
	AutoRejectHolds bool   // resolve holds as rejected at once (scoreboard runs)
}

// MintCapability issues a scoped, expiring capability for a confirmed contract,
// bound to the public key the agent registered.
func (b *Broker) MintCapability(ctx context.Context, c contract.Contract, opts MintOptions) (CapabilityRecord, error) {
	if err := c.Verify(b.keys.Org.Public); err != nil {
		return CapabilityRecord{}, err
	}
	now := b.now().UTC()
	if c.Expired(now) {
		return CapabilityRecord{}, errors.New("contract has expired")
	}
	agent, ok := b.Agents.Get(c.AgentID)
	if !ok {
		return CapabilityRecord{}, fmt.Errorf("agent %s is not registered", c.AgentID)
	}
	mode := opts.Mode
	if mode == "" {
		mode = b.cfg.Mode
	}
	if mode != ModeOn && mode != ModeOff && mode != ModeSupervisor {
		return CapabilityRecord{}, fmt.Errorf("unknown mode %q", mode)
	}
	if mode != ModeOn && b.cfg.Mode != ModeOff && !b.cfg.AllowOffMode {
		return CapabilityRecord{}, errors.New("running a session with PACT off is not enabled on this broker")
	}
	if mode == ModeSupervisor && !b.exec.LLMConfigured() {
		return CapabilityRecord{}, errors.New("the AI-supervisor baseline needs a Gemini API key")
	}
	exp := now.Add(b.cfg.CapTTL)
	if cexp, err := time.Parse(time.RFC3339, c.ExpiresAt); err == nil && cexp.Before(exp) {
		exp = cexp
	}
	p := capability.Payload{
		CapID: capability.NewID(), ContractHash: c.Hash, AgentID: agent.AgentID, AgentPubkey: agent.Pubkey,
		Scopes: c.Scopes(), NotBefore: now.Format(time.RFC3339), ExpiresAt: exp.Format(time.RFC3339),
	}
	token, err := capability.Mint(b.keys.Issuer, p)
	if err != nil {
		return CapabilityRecord{}, err
	}
	rec := CapabilityRecord{CapID: p.CapID, Token: token, Payload: p, TaskID: c.TaskID, ContractID: c.ContractID,
		Mode: mode, AutoRejectHolds: opts.AutoRejectHolds, CreatedAt: now.Format(time.RFC3339)}
	if err := store.PutJSON(ctx, b.store, store.KindCapability, rec.CapID, rec, true); err != nil {
		return CapabilityRecord{}, err
	}
	sess := Session{SessionID: rec.CapID, TaskID: c.TaskID, AgentID: agent.AgentID, ContractHash: c.Hash, Mode: mode,
		TaintSources: []SessionTaint{}, UpdatedAt: now.Format(time.RFC3339)}
	if err := store.PutJSON(ctx, b.store, store.KindSession, sess.SessionID, sess, false); err != nil {
		return CapabilityRecord{}, err
	}
	b.hub.Publish("session", sess)
	return rec, nil
}

// RevokeCapability adds a capability to the revocation list.
func (b *Broker) RevokeCapability(ctx context.Context, capID string) error {
	rec, err := store.GetJSON[CapabilityRecord](ctx, b.store, store.KindCapability, capID)
	if err != nil {
		return err
	}
	rec.Revoked = true
	return store.PutJSON(ctx, b.store, store.KindCapability, capID, rec, false)
}

// Session returns the state of one session.
func (b *Broker) Session(ctx context.Context, id string) (Session, error) {
	return store.GetJSON[Session](ctx, b.store, store.KindSession, id)
}

// Sessions returns every session, oldest first.
func (b *Broker) Sessions(ctx context.Context) ([]Session, error) {
	return store.ListJSON[Session](ctx, b.store, store.KindSession)
}

func (b *Broker) sessionLock(id string) *sync.Mutex {
	b.mu.Lock()
	defer b.mu.Unlock()
	m, ok := b.sessions[id]
	if !ok {
		m = &sync.Mutex{}
		b.sessions[id] = m
	}
	return m
}

// updateSession applies fn to the stored session under the session lock.
func (b *Broker) updateSession(ctx context.Context, id string, fn func(*Session)) (Session, error) {
	l := b.sessionLock(id)
	l.Lock()
	defer l.Unlock()
	s, err := store.GetJSON[Session](ctx, b.store, store.KindSession, id)
	if err != nil {
		return Session{}, err
	}
	fn(&s)
	s.UpdatedAt = b.now().UTC().Format(time.RFC3339)
	if err := store.PutJSON(ctx, b.store, store.KindSession, id, s, false); err != nil {
		return Session{}, err
	}
	return s, nil
}

func (b *Broker) addMetric(capID string, m CallMetric) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.metrics[capID]) < 2000 {
		b.metrics[capID] = append(b.metrics[capID], m)
	}
}

// Metrics returns the call timings recorded for one session.
func (b *Broker) Metrics(capID string) []CallMetric {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]CallMetric(nil), b.metrics[capID]...)
}
