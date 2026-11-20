package store

import (
	"context"
	"sync"
	"time"
)

// Memory is an in-process Store.
type Memory struct {
	mu       sync.Mutex
	docs     map[string]map[string][]byte
	order    map[string][]string
	nonces   map[string]bool
	evidence []EvidenceRow
	// FailAppend makes AppendEvidence fail; tests use it to prove fail-closed behaviour.
	FailAppend error
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{docs: map[string]map[string][]byte{}, order: map[string][]string{}, nonces: map[string]bool{}}
}

func (m *Memory) Put(_ context.Context, kind, id string, doc []byte, immutable bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.docs[kind] == nil {
		m.docs[kind] = map[string][]byte{}
	}
	if _, ok := m.docs[kind][id]; ok {
		if immutable {
			return ErrExists
		}
	} else {
		m.order[kind] = append(m.order[kind], id)
	}
	m.docs[kind][id] = append([]byte(nil), doc...)
	return nil
}

func (m *Memory) Get(_ context.Context, kind, id string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.docs[kind][id]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), d...), nil
}

func (m *Memory) List(_ context.Context, kind string) ([][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, 0, len(m.order[kind]))
	for _, id := range m.order[kind] {
		out = append(out, append([]byte(nil), m.docs[kind][id]...))
	}
	return out, nil
}

func (m *Memory) UseNonce(_ context.Context, capID, nonce string, _ time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := capID + "\x00" + nonce
	if m.nonces[k] {
		return false, nil
	}
	m.nonces[k] = true
	return true, nil
}

func (m *Memory) AppendEvidence(ctx context.Context, fn AppendFunc) (EvidenceRow, error) {
	// Like PostgreSQL, refuse a cancelled request, so tests see what production does.
	if err := ctx.Err(); err != nil {
		return EvidenceRow{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailAppend != nil {
		return EvidenceRow{}, m.FailAppend
	}
	var seq int64
	prev := ""
	if n := len(m.evidence); n > 0 {
		seq, prev = m.evidence[n-1].Seq, m.evidence[n-1].Hash
	}
	row, err := fn(seq+1, prev)
	if err != nil {
		return EvidenceRow{}, err
	}
	m.evidence = append(m.evidence, row)
	return row, nil
}

func (m *Memory) Evidence(_ context.Context, taskID string) ([]EvidenceRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]EvidenceRow, 0, len(m.evidence))
	for _, r := range m.evidence {
		if taskID == "" || r.TaskID == taskID {
			out = append(out, r)
		}
	}
	return out, nil
}

// TamperEvidence rewrites a stored record in place, as an attacker with database access would.
func (m *Memory) TamperEvidence(seq int64, edit func(json string) string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.evidence {
		if m.evidence[i].Seq == seq {
			m.evidence[i].JSON = edit(m.evidence[i].JSON)
		}
	}
}

// DropNewestEvidence deletes the newest n records, as an attacker hiding
// recent activity would, and returns them.
func (m *Memory) DropNewestEvidence(n int) []EvidenceRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n > len(m.evidence) {
		n = len(m.evidence)
	}
	gone := append([]EvidenceRow(nil), m.evidence[len(m.evidence)-n:]...)
	m.evidence = m.evidence[:len(m.evidence)-n]
	return gone
}

// RestoreEvidence puts back rows removed by DropNewestEvidence, as `make untamper` does.
func (m *Memory) RestoreEvidence(rows []EvidenceRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evidence = append(m.evidence, rows...)
}

func (m *Memory) Close() {}
