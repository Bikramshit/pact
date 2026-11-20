// Package evidence keeps the signed, hash-chained decision log. Every record
// carries the previous record's hash, so editing any stored record breaks the
// chain at that sequence number.
package evidence

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/foxcolab/pact/internal/canon"
	"github.com/foxcolab/pact/internal/keys"
	"github.com/foxcolab/pact/internal/store"
)

// Genesis is the prev_hash of the first record.
const Genesis = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// RequestInfo names what was asked for, as derived by the broker.
type RequestInfo struct {
	Tool     string `json:"tool"`
	Resource string `json:"resource"`
	Op       string `json:"op"`
}

// TaintSource is one piece of untrusted content the agent has read.
type TaintSource struct {
	Seq      int64  `json:"seq"`
	Resource string `json:"resource"`
	SHA256   string `json:"sha256"`
}

// Approval is a human decision on a held action.
type Approval struct {
	By     string `json:"by"`
	At     string `json:"at"`
	Result string `json:"result"` // approved | rejected | timeout
}

// Record is one decision. Records are appended in order and never updated.
type Record struct {
	Seq            int64           `json:"seq"`
	TS             string          `json:"ts"`
	TaskID         string          `json:"task_id"`
	ContractHash   string          `json:"contract_hash"`
	AgentID        string          `json:"agent_id"`
	CapID          string          `json:"cap_id"`
	Request        RequestInfo     `json:"request"`
	DecisionID     string          `json:"decision_id"`
	Decision       string          `json:"decision"` // allow | deny | hold
	Reasons        []string        `json:"reasons"`
	Checks         map[string]bool `json:"checks,omitempty"`
	Outcome        string          `json:"outcome"` // executed | execution_failed | not_executed | held
	SessionTainted bool            `json:"session_tainted"`
	TaintSources   []TaintSource   `json:"taint_sources,omitempty"`
	TaintAdded     *TaintSource    `json:"taint_added,omitempty"`
	// SessionConfidential is true when the session had read confidential data
	// before this decision; ConfidentialAdded names the resource this read added.
	SessionConfidential bool      `json:"session_confidential,omitempty"`
	ConfidentialAdded   string    `json:"confidential_added,omitempty"`
	HoldID              string    `json:"hold_id,omitempty"`
	Approval            *Approval `json:"approval,omitempty"`
	PrevHash            string    `json:"prev_hash"`
	Hash                string    `json:"hash"`
	Sig                 string    `json:"sig"`
}

// Engine appends and verifies records.
type Engine struct {
	store store.Store
	key   *keys.KeyPair
	// checkpointPath, when set, is a file outside the database that records how
	// long the chain is and what its newest hash is. Without it, deleting the
	// newest records would leave a shorter chain that still verifies.
	checkpointPath string
	// orgPublicKey and certificate tie the evidence key to the org key.
	orgPublicKey string
	certificate  string

	mu        sync.RWMutex
	listeners []func(Record)
}

// New returns an engine signing with the evidence key.
func New(s store.Store, key *keys.KeyPair) *Engine { return &Engine{store: s, key: key} }

// WithCheckpoint makes the engine keep a signed checkpoint file at path.
func (e *Engine) WithCheckpoint(path string) *Engine {
	e.checkpointPath = path
	return e
}

// WithCertificate records the org key's signature over the evidence key, so an
// exported chain can be traced back to the org key.
func (e *Engine) WithCertificate(orgPublicKey, certificate string) *Engine {
	e.orgPublicKey, e.certificate = orgPublicKey, certificate
	return e
}

// Checkpoint is a signed statement of the chain's length and newest hash.
type Checkpoint struct {
	Count    int64  `json:"count"`
	HeadHash string `json:"head_hash"`
	At       string `json:"at"`
	Sig      string `json:"sig"`
}

func checkpointMessage(count int64, head string) []byte {
	return []byte(fmt.Sprintf("pact-checkpoint:%d:%s", count, head))
}

func (e *Engine) writeCheckpoint(count int64, head string) error {
	if e.checkpointPath == "" {
		return nil
	}
	sig, err := e.key.Sign(checkpointMessage(count, head))
	if err != nil {
		return err
	}
	cp := Checkpoint{Count: count, HeadHash: head, At: time.Now().UTC().Format(time.RFC3339Nano), Sig: sig}
	raw, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	tmp := e.checkpointPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.checkpointPath)
}

// ReadCheckpoint returns the stored checkpoint, or nil if there is none.
func (e *Engine) ReadCheckpoint() (*Checkpoint, error) {
	if e.checkpointPath == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(e.checkpointPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(raw, &cp); err != nil {
		return nil, errors.New("evidence: the checkpoint file is not valid JSON")
	}
	return &cp, nil
}

// PublicKey returns the key records are verified against.
func (e *Engine) PublicKey() ed25519.PublicKey { return e.key.Public }

// OnAppend registers a callback invoked after each successful append.
func (e *Engine) OnAppend(fn func(Record)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners = append(e.listeners, fn)
}

// Append assigns the next sequence number, links, hashes, signs and stores r.
// An error means the record was not stored; callers must then fail closed.
func (e *Engine) Append(ctx context.Context, r Record) (Record, error) {
	if r.Reasons == nil {
		r.Reasons = []string{}
	}
	_, err := e.store.AppendEvidence(ctx, func(seq int64, prev string) (store.EvidenceRow, error) {
		if prev == "" {
			prev = Genesis
		}
		now := time.Now().UTC()
		r.Seq = seq
		r.TS = now.Format(time.RFC3339Nano)
		r.PrevHash = prev
		if r.DecisionID == "" {
			r.DecisionID = fmt.Sprintf("d-%04d", seq)
		}
		if r.TaintAdded != nil {
			r.TaintAdded.Seq = seq
		}
		r.Hash, r.Sig = "", ""
		body, err := canonicalBody(r)
		if err != nil {
			return store.EvidenceRow{}, err
		}
		r.Hash = canon.HashBytes(body)
		if r.Sig, err = e.key.Sign([]byte(r.Hash)); err != nil {
			return store.EvidenceRow{}, err
		}
		full, err := canon.Marshal(r)
		if err != nil {
			return store.EvidenceRow{}, err
		}
		return store.EvidenceRow{Seq: seq, TaskID: r.TaskID, TS: now, JSON: string(full), Hash: r.Hash}, nil
	})
	if err != nil {
		return Record{}, err
	}
	if err := e.writeCheckpoint(r.Seq, r.Hash); err != nil {
		// The record is stored, but the caller must still fail closed: a chain
		// whose checkpoint cannot advance would later look truncated.
		return Record{}, fmt.Errorf("evidence: writing checkpoint: %w", err)
	}
	e.mu.RLock()
	ls := append([]func(Record){}, e.listeners...)
	e.mu.RUnlock()
	for _, fn := range ls {
		fn(r)
	}
	return r, nil
}

// canonicalBody is the canonical record without hash and sig: the bytes that are hashed.
func canonicalBody(r Record) ([]byte, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return bodyOf(raw)
}

func bodyOf(raw []byte) ([]byte, error) {
	m, err := canon.Parse(raw)
	if err != nil {
		return nil, err
	}
	delete(m, "hash")
	delete(m, "sig")
	return canon.Marshal(m)
}

// VerifyResult is the outcome of walking the chain.
type VerifyResult struct {
	OK          bool   `json:"ok"`
	Count       int    `json:"count"`
	FirstBadSeq int64  `json:"first_bad_seq"`
	Problem     string `json:"problem,omitempty"`
	HeadHash    string `json:"head_hash,omitempty"`
	PublicKey   string `json:"public_key"`
	// Truncated is set when records the checkpoint knows about are missing.
	Truncated  bool        `json:"truncated,omitempty"`
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`
}

// Verify walks the stored chain.
func (e *Engine) Verify(ctx context.Context) (VerifyResult, error) {
	rows, err := e.store.Evidence(ctx, "")
	if err != nil {
		return VerifyResult{}, err
	}
	raw := make([]string, len(rows))
	for i, r := range rows {
		raw[i] = r.JSON
	}
	cp, err := e.ReadCheckpoint()
	if err != nil {
		return VerifyResult{}, err
	}
	return CheckAgainst(VerifyChain(raw, e.key.Public), raw, cp, e.key.Public), nil
}

// CheckAgainst compares a verified chain with a checkpoint. A chain that is
// internally consistent but shorter than the checkpoint, or whose record at the
// checkpoint's position has a different hash, has had records removed or replaced.
func CheckAgainst(res VerifyResult, records []string, cp *Checkpoint, pub ed25519.PublicKey) VerifyResult {
	if cp == nil || !res.OK {
		return res
	}
	res.Checkpoint = cp
	fail := func(seq int64, format string, a ...any) VerifyResult {
		res.OK, res.FirstBadSeq, res.Problem = false, seq, fmt.Sprintf(format, a...)
		return res
	}
	if !keys.Verify(pub, checkpointMessage(cp.Count, cp.HeadHash), cp.Sig) {
		return fail(0, "the checkpoint's signature is not valid for the evidence key")
	}
	have := int64(len(records))
	if have < cp.Count {
		res.Truncated = true
		return fail(have+1, "records are missing: the signed checkpoint covers %d records but only %d are stored (the newest were deleted)", cp.Count, have)
	}
	if cp.Count > 0 {
		m, err := canon.Parse([]byte(records[cp.Count-1]))
		if hash, _ := m["hash"].(string); err != nil || hash != cp.HeadHash {
			return fail(cp.Count, "record %d does not match the signed checkpoint (the end of the chain was replaced)", cp.Count)
		}
	}
	return res
}

// VerifyChain checks sequence numbers, hash links, hashes and signatures of
// records given as stored JSON text. It needs only the evidence public key,
// so it can run outside pact-core.
func VerifyChain(records []string, pub ed25519.PublicKey) VerifyResult {
	res := VerifyResult{Count: len(records), PublicKey: keys.EncodePublic(pub)}
	prev := Genesis
	for i, raw := range records {
		want := int64(i + 1)
		bad := func(format string, a ...any) VerifyResult {
			res.FirstBadSeq = want
			res.Problem = fmt.Sprintf(format, a...)
			return res
		}
		m, err := canon.Parse([]byte(raw))
		if err != nil {
			return bad("record is not valid JSON")
		}
		seq, _ := m["seq"].(json.Number)
		if seq.String() != fmt.Sprint(want) {
			return bad("sequence number is %s, expected %d (a record is missing or reordered)", seq, want)
		}
		hash, _ := m["hash"].(string)
		sig, _ := m["sig"].(string)
		prevHash, _ := m["prev_hash"].(string)
		body, err := bodyOf([]byte(raw))
		if err != nil {
			return bad("record cannot be canonicalised: %v", err)
		}
		if canon.HashBytes(body) != hash {
			return bad("content does not match its hash (the record was edited)")
		}
		if prevHash != prev {
			return bad("prev_hash does not match the hash of record %d", want-1)
		}
		if !keys.Verify(pub, []byte(hash), sig) {
			return bad("signature is not valid for the evidence key")
		}
		prev = hash
	}
	res.OK = true
	if len(records) > 0 {
		res.HeadHash = prev
	}
	return res
}

// List returns decoded records, optionally for one task.
func (e *Engine) List(ctx context.Context, taskID string) ([]Record, error) {
	rows, err := e.store.Evidence(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(rows))
	for _, row := range rows {
		var r Record
		if err := json.Unmarshal([]byte(row.JSON), &r); err != nil {
			// A record edited into invalid JSON still has to show up so the viewer can flag it.
			r = Record{Seq: row.Seq, TaskID: row.TaskID, Decision: "unreadable", Outcome: "unreadable", Reasons: []string{"stored record is not valid JSON"}}
		}
		out = append(out, r)
	}
	return out, nil
}

// Export is a self-contained bundle for offline verification.
type Export struct {
	PublicKey  string      `json:"public_key"`
	ExportedAt string      `json:"exported_at"`
	Records    []string    `json:"records"`
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`
	// OrgPublicKey vouches for PublicKey through KeyCertificate.
	OrgPublicKey   string `json:"org_public_key,omitempty"`
	KeyCertificate string `json:"key_certificate,omitempty"`
}

// Export returns every stored record as its exact stored text.
func (e *Engine) Export(ctx context.Context) (Export, error) {
	rows, err := e.store.Evidence(ctx, "")
	if err != nil {
		return Export{}, err
	}
	ex := Export{PublicKey: e.key.PublicString(), ExportedAt: time.Now().UTC().Format(time.RFC3339), Records: make([]string, len(rows)),
		OrgPublicKey: e.orgPublicKey, KeyCertificate: e.certificate}
	for i, r := range rows {
		ex.Records[i] = r.JSON
	}
	if ex.Checkpoint, err = e.ReadCheckpoint(); err != nil {
		return Export{}, err
	}
	return ex, nil
}

// VerifyExport checks an exported bundle. If pinnedKey is non-empty the bundle
// must have been signed by that key rather than the one it names itself.
func VerifyExport(ex Export, pinnedKey string) (VerifyResult, error) {
	keyStr := ex.PublicKey
	if strings.TrimSpace(pinnedKey) != "" {
		keyStr = strings.TrimSpace(pinnedKey)
	}
	pub, err := keys.DecodePublic(keyStr)
	if err != nil {
		return VerifyResult{}, err
	}
	return CheckAgainst(VerifyChain(ex.Records, pub), ex.Records, ex.Checkpoint, pub), nil
}
