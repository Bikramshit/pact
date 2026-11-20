package broker

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// Hold is a sensitive action paused for a human decision. Nothing executes while it is pending.
type Hold struct {
	ID           string         `json:"hold_id"`
	TaskID       string         `json:"task_id"`
	AgentID      string         `json:"agent_id"`
	CapID        string         `json:"cap_id"`
	Tool         string         `json:"tool"`
	Resource     string         `json:"resource"`
	Op           string         `json:"op"`
	Args         map[string]any `json:"args"`
	Reasons      []string       `json:"reasons"`
	Before       *string        `json:"before,omitempty"` // file content before a held write
	After        *string        `json:"after,omitempty"`  // content the agent wants to write
	TaintSources []SessionTaint `json:"taint_sources"`
	// ConfidentialSources names the confidential resources read before this action.
	ConfidentialSources []string `json:"confidential_sources,omitempty"`
	OpenedAt            string   `json:"opened_at"`
	Deadline            string   `json:"deadline"`
	Status              string   `json:"status"` // pending | approved | rejected | timeout
	ResolvedBy          string   `json:"resolved_by,omitempty"`
	ResolvedAt          string   `json:"resolved_at,omitempty"`

	ch chan resolution
}

type resolution struct {
	result string
	by     string
}

// HoldQueue tracks pending and recently resolved holds.
type HoldQueue struct {
	mu    sync.Mutex
	holds map[string]*Hold
}

// NewHoldQueue returns an empty queue.
func NewHoldQueue() *HoldQueue { return &HoldQueue{holds: map[string]*Hold{}} }

// open registers a hold and returns a copy that is safe to publish.
func (q *HoldQueue) open(h *Hold) Hold {
	h.ch = make(chan resolution, 1)
	h.Status = "pending"
	q.mu.Lock()
	defer q.mu.Unlock()
	q.holds[h.ID] = h
	return q.copyLocked(h.ID)
}

// get returns a copy of a hold's current state.
func (q *HoldQueue) get(id string) Hold {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.copyLocked(id)
}

func (q *HoldQueue) copyLocked(id string) Hold {
	cp := *q.holds[id]
	cp.ch = nil
	return cp
}

// Resolve records a human decision ("approved" or "rejected") on a pending hold.
func (q *HoldQueue) Resolve(id, result, by string) error {
	if result != "approved" && result != "rejected" {
		return errors.New("result must be approved or rejected")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	h, ok := q.holds[id]
	if !ok {
		return errors.New("no such hold")
	}
	if h.Status != "pending" {
		return errors.New("hold is already " + h.Status)
	}
	h.Status = result
	h.ResolvedBy = by
	h.ResolvedAt = time.Now().UTC().Format(time.RFC3339)
	h.ch <- resolution{result: result, by: by}
	return nil
}

// expire marks a hold as timed out unless a decision already arrived, and
// returns the result that stands.
func (q *HoldQueue) expire(id, by string) resolution {
	q.mu.Lock()
	defer q.mu.Unlock()
	h := q.holds[id]
	if h.Status != "pending" {
		return <-h.ch
	}
	h.Status = "timeout"
	h.ResolvedBy = by
	h.ResolvedAt = time.Now().UTC().Format(time.RFC3339)
	return resolution{result: "timeout", by: by}
}

// List returns holds, pending first, newest first within each group.
func (q *HoldQueue) List() []Hold {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Hold, 0, len(q.holds))
	for _, h := range q.holds {
		cp := *h
		cp.ch = nil
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := out[i].Status == "pending", out[j].Status == "pending"
		if pi != pj {
			return pi
		}
		return out[i].OpenedAt > out[j].OpenedAt
	})
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}
