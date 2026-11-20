// Package store persists PACT's state. PostgreSQL is the system of record; the
// in-memory implementation backs unit tests and key-less local runs.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound is returned when a document does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrExists is returned when an immutable document would be overwritten.
var ErrExists = errors.New("store: already exists")

// Document kinds.
const (
	KindContract    = "contract"     // id: contract hash, immutable
	KindContractRef = "contract_ref" // id: contract id -> hash, immutable
	KindAgent       = "agent"        // id: agent id
	KindCapability  = "capability"   // id: cap id
	KindSession     = "session"      // id: cap id
	KindRun         = "scoreboard_run"
)

// EvidenceRow is one stored evidence record. JSON holds the exact canonical
// bytes that were hashed and signed.
type EvidenceRow struct {
	Seq    int64
	TaskID string
	TS     time.Time
	JSON   string
	Hash   string
}

// AppendFunc builds the next record given its sequence number and the previous hash.
type AppendFunc func(seq int64, prevHash string) (EvidenceRow, error)

// Store is everything pact-core needs from its database.
type Store interface {
	// Put writes a document. With immutable set, an existing id is an error.
	Put(ctx context.Context, kind, id string, doc []byte, immutable bool) error
	Get(ctx context.Context, kind, id string) ([]byte, error)
	List(ctx context.Context, kind string) ([][]byte, error)

	// UseNonce records a nonce and reports whether it was unused.
	UseNonce(ctx context.Context, capID, nonce string, at time.Time) (bool, error)

	// AppendEvidence serialises appends: fn sees the true previous hash.
	AppendEvidence(ctx context.Context, fn AppendFunc) (EvidenceRow, error)
	// Evidence returns records in sequence order; taskID "" means all.
	Evidence(ctx context.Context, taskID string) ([]EvidenceRow, error)

	Close()
}

// PutJSON marshals v and stores it.
func PutJSON(ctx context.Context, s Store, kind, id string, v any, immutable bool) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.Put(ctx, kind, id, b, immutable)
}

// GetJSON loads a document into T.
func GetJSON[T any](ctx context.Context, s Store, kind, id string) (T, error) {
	var v T
	b, err := s.Get(ctx, kind, id)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(b, &v)
	return v, err
}

// ListJSON loads every document of a kind into []T, oldest first.
func ListJSON[T any](ctx context.Context, s Store, kind string) ([]T, error) {
	docs, err := s.List(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(docs))
	for _, d := range docs {
		var v T
		if err := json.Unmarshal(d, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
