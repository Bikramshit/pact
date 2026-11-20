package broker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/foxcolab/pact/internal/keys"
	"github.com/foxcolab/pact/internal/scenario"
)

// Agent is a registered agent instance: an id and the public key it proved to hold.
type Agent struct {
	AgentID      string `json:"agent_id"`
	Pubkey       string `json:"pubkey"`
	RegisteredAt string `json:"registered_at"`
	LastSeen     string `json:"last_seen"`
	Online       bool   `json:"online"`
}

// AgentIDFor derives an agent id from its public key, so an id cannot be claimed with another key.
func AgentIDFor(pubkey string) string {
	sum := sha256.Sum256([]byte(pubkey))
	return "agent-" + hex.EncodeToString(sum[:3])
}

// Assignment is one unit of work handed to an agent: a task, a capability and
// either a live instruction or a scripted list of tool calls.
type Assignment struct {
	ID          string          `json:"assignment_id"`
	TaskID      string          `json:"task_id"`
	AgentID     string          `json:"agent_id"`
	CapID       string          `json:"cap_id"`
	Capability  string          `json:"capability"`
	Kind        string          `json:"kind"` // live | replay
	Instruction string          `json:"instruction"`
	Steps       []scenario.Step `json:"steps,omitempty"`
	MaxSteps    int             `json:"max_steps"`
	Seed        int             `json:"seed"`
	Label       string          `json:"label,omitempty"`
	Status      string          `json:"status"` // queued | running | done | failed
	Report      *Report         `json:"report,omitempty"`
	CreatedAt   string          `json:"created_at"`
	FinishedAt  string          `json:"finished_at,omitempty"`

	done chan struct{}
}

// StepReport is what the agent observed for one tool call.
type StepReport struct {
	Tool     string         `json:"tool"`
	Args     map[string]any `json:"args,omitempty"`
	Status   int            `json:"status"`
	Decision string         `json:"decision"`
	Reasons  []string       `json:"reasons,omitempty"`
	Held     bool           `json:"held,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// Report is the agent's account of a finished assignment. It is untrusted:
// the scoreboard grades from the targets' own logs, never from this.
type Report struct {
	Status   string       `json:"status"` // completed | failed
	Summary  string       `json:"summary,omitempty"`
	Steps    []StepReport `json:"steps"`
	LLMCalls int          `json:"llm_calls"`
	Error    string       `json:"error,omitempty"`
}

// Agents tracks registered agents and their work queue.
type Agents struct {
	mu          sync.Mutex
	agents      map[string]*Agent
	seen        map[string]time.Time
	queue       map[string][]*Assignment
	assignments map[string]*Assignment
	wake        map[string]chan struct{}
}

// NewAgents returns an empty registry.
func NewAgents() *Agents {
	return &Agents{agents: map[string]*Agent{}, seen: map[string]time.Time{}, queue: map[string][]*Assignment{},
		assignments: map[string]*Assignment{}, wake: map[string]chan struct{}{}}
}

// Register records an agent's public key and returns its derived id.
func (a *Agents) Register(pubkey string) (Agent, error) {
	if _, err := keys.DecodePublic(pubkey); err != nil {
		return Agent{}, err
	}
	now := time.Now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	id := AgentIDFor(pubkey)
	ag, ok := a.agents[id]
	if !ok {
		ag = &Agent{AgentID: id, Pubkey: pubkey, RegisteredAt: now.Format(time.RFC3339)}
		a.agents[id] = ag
	}
	a.seen[id] = now
	ag.LastSeen = now.Format(time.RFC3339)
	cp := *ag
	cp.Online = true
	return cp, nil
}

// Get returns a registered agent.
func (a *Agents) Get(id string) (Agent, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ag, ok := a.agents[id]
	if !ok {
		return Agent{}, false
	}
	cp := *ag
	cp.Online = time.Since(a.seen[id]) < 45*time.Second
	return cp, true
}

// List returns agents, most recently seen first.
func (a *Agents) List() []Agent {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Agent, 0, len(a.agents))
	for id, ag := range a.agents {
		cp := *ag
		cp.Online = time.Since(a.seen[id]) < 45*time.Second
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	return out
}

// Touch records that an agent was just heard from. A working agent makes tool
// calls rather than polling for work, so those count as signs of life too.
func (a *Agents) Touch(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.agents[id]; ok {
		a.touch(id)
	}
}

func (a *Agents) touch(id string) {
	now := time.Now().UTC()
	a.seen[id] = now
	if ag, ok := a.agents[id]; ok {
		ag.LastSeen = now.Format(time.RFC3339)
	}
}

// Enqueue hands an assignment to its agent.
func (a *Agents) Enqueue(as *Assignment) {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	as.ID = "as-" + hex.EncodeToString(b)
	as.Status = "queued"
	as.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	as.done = make(chan struct{})
	a.mu.Lock()
	a.queue[as.AgentID] = append(a.queue[as.AgentID], as)
	a.assignments[as.ID] = as
	if ch, ok := a.wake[as.AgentID]; ok {
		close(ch)
		delete(a.wake, as.AgentID)
	}
	a.mu.Unlock()
}

// Next blocks until the agent has an assignment or wait elapses.
func (a *Agents) Next(ctx context.Context, agentID string, wait time.Duration) (*Assignment, error) {
	deadline := time.After(wait)
	for {
		a.mu.Lock()
		if _, ok := a.agents[agentID]; !ok {
			a.mu.Unlock()
			return nil, errors.New("agent is not registered")
		}
		a.touch(agentID)
		if q := a.queue[agentID]; len(q) > 0 {
			as := q[0]
			a.queue[agentID] = q[1:]
			as.Status = "running"
			cp := *as
			a.mu.Unlock()
			return &cp, nil
		}
		ch, ok := a.wake[agentID]
		if !ok {
			ch = make(chan struct{})
			a.wake[agentID] = ch
		}
		a.mu.Unlock()
		select {
		case <-ch:
		case <-deadline:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Finish records the agent's report for an assignment.
func (a *Agents) Finish(agentID, id string, rep Report) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	as, ok := a.assignments[id]
	if !ok || as.AgentID != agentID {
		return errors.New("no such assignment")
	}
	if as.Status == "done" || as.Status == "failed" {
		return errors.New("assignment already finished")
	}
	a.touch(agentID)
	as.Report = &rep
	as.Status = "done"
	if rep.Status != "completed" {
		as.Status = "failed"
	}
	as.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	close(as.done)
	return nil
}

// Wait blocks until the assignment finishes or ctx ends, then returns its current state.
func (a *Agents) Wait(ctx context.Context, id string) (Assignment, error) {
	a.mu.Lock()
	as, ok := a.assignments[id]
	a.mu.Unlock()
	if !ok {
		return Assignment{}, errors.New("no such assignment")
	}
	select {
	case <-as.done:
	case <-ctx.Done():
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := *as
	return cp, ctx.Err()
}

// Assignment returns one assignment by id.
func (a *Agents) Assignment(id string) (Assignment, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	as, ok := a.assignments[id]
	if !ok {
		return Assignment{}, false
	}
	return *as, true
}

// Assignments returns all assignments, newest first.
func (a *Agents) Assignments() []Assignment {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Assignment, 0, len(a.assignments))
	for _, as := range a.assignments {
		cp := *as
		cp.Capability = ""
		cp.Steps = nil
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}
