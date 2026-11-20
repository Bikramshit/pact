package evidence

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/foxcolab/pact/internal/keys"
	"github.com/foxcolab/pact/internal/store"
)

func chain(t *testing.T, n int) (*Engine, *store.Memory) {
	t.Helper()
	mem := store.NewMemory()
	key, _ := keys.Generate("evidence")
	e := New(mem, key).WithCheckpoint(filepath.Join(t.TempDir(), "checkpoint.json"))
	for i := 0; i < n; i++ {
		dec := "allow"
		if i%3 == 2 {
			dec = "deny"
		}
		if _, err := e.Append(context.Background(), Record{TaskID: "task-1", AgentID: "agent-7f3", CapID: "cap-A7",
			Request: RequestInfo{Tool: "read_file", Resource: "repo:web-app/app/cart.py", Op: "read"}, Decision: dec, Outcome: "executed"}); err != nil {
			t.Fatal(err)
		}
	}
	return e, mem
}

func TestChainVerifies(t *testing.T) {
	e, _ := chain(t, 10)
	res, err := e.Verify(context.Background())
	if err != nil || !res.OK || res.Count != 10 {
		t.Fatalf("expected a clean chain, got %+v %v", res, err)
	}
}

// Phase 4 exit test: editing a stored record makes verify report its sequence number.
func TestEditedRecordIsLocated(t *testing.T) {
	for _, seq := range []int64{1, 4, 10} {
		e, mem := chain(t, 10)
		mem.TamperEvidence(seq, func(js string) string {
			if strings.Contains(js, `"decision":"deny"`) {
				return strings.Replace(js, `"decision":"deny"`, `"decision":"allow"`, 1)
			}
			return strings.Replace(js, `"decision":"allow"`, `"decision":"deny"`, 1)
		})
		res, _ := e.Verify(context.Background())
		if res.OK || res.FirstBadSeq != seq {
			t.Fatalf("tampered seq %d: got %+v", seq, res)
		}
	}
}

func TestAddedFieldIsDetected(t *testing.T) {
	e, mem := chain(t, 5)
	mem.TamperEvidence(3, func(js string) string { return strings.Replace(js, `{"agent_id"`, `{"added":"x","agent_id"`, 1) })
	if res, _ := e.Verify(context.Background()); res.OK || res.FirstBadSeq != 3 {
		t.Fatalf("an added field went unnoticed: %+v", res)
	}
}

func TestWrongKeyFailsAtFirstRecord(t *testing.T) {
	e, _ := chain(t, 3)
	ex, _ := e.Export(context.Background())
	other, _ := keys.Generate("other")
	res, err := VerifyExport(ex, other.PublicString())
	if err != nil || res.OK || res.FirstBadSeq != 1 {
		t.Fatalf("expected failure at record 1 under a different key: %+v %v", res, err)
	}
	if res, _ := VerifyExport(ex, ""); !res.OK {
		t.Fatalf("export should verify with its own key: %+v", res)
	}
}

// Deleting the newest records leaves a chain that is internally consistent.
// The signed checkpoint, kept outside the database, is what exposes it.
func TestTruncationIsDetected(t *testing.T) {
	mem := store.NewMemory()
	key, _ := keys.Generate("evidence")
	e := New(mem, key).WithCheckpoint(filepath.Join(t.TempDir(), "checkpoint.json"))
	for i := 0; i < 8; i++ {
		if _, err := e.Append(context.Background(), Record{TaskID: "t", Decision: "deny", Outcome: "not_executed"}); err != nil {
			t.Fatal(err)
		}
	}
	if res, _ := e.Verify(context.Background()); !res.OK || res.Checkpoint == nil || res.Checkpoint.Count != 8 {
		t.Fatalf("clean chain with checkpoint: %+v", res)
	}
	ex, _ := e.Export(context.Background())

	mem.DropNewestEvidence(3)
	res, _ := e.Verify(context.Background())
	if res.OK || !res.Truncated || res.FirstBadSeq != 6 {
		t.Fatalf("truncation went unnoticed: %+v", res)
	}

	// The same holds for an exported bundle verified offline.
	if r, _ := VerifyExport(ex, ""); !r.OK {
		t.Fatalf("full export should verify: %+v", r)
	}
	ex.Records = ex.Records[:5]
	if r, _ := VerifyExport(ex, ""); r.OK || !r.Truncated {
		t.Fatalf("truncated export verified: %+v", r)
	}
	// A checkpoint rewritten to match the shorter chain fails its signature.
	ex.Checkpoint.Count = 5
	if r, _ := VerifyExport(ex, ""); r.OK {
		t.Fatalf("forged checkpoint accepted: %+v", r)
	}
}

// Appending after a restart keeps the checkpoint in step with the chain.
func TestCheckpointFollowsAppends(t *testing.T) {
	mem := store.NewMemory()
	key, _ := keys.Generate("evidence")
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	for round := 0; round < 2; round++ {
		e := New(mem, key).WithCheckpoint(path)
		for i := 0; i < 3; i++ {
			if _, err := e.Append(context.Background(), Record{TaskID: "t", Decision: "allow", Outcome: "executed"}); err != nil {
				t.Fatal(err)
			}
		}
		if res, _ := e.Verify(context.Background()); !res.OK || res.Checkpoint.Count != int64(3*(round+1)) {
			t.Fatalf("round %d: %+v", round, res)
		}
	}
}

// After the newest records are deleted, appending would reuse their sequence
// numbers and re-sign the checkpoint, hiding the deletion. The engine refuses,
// also after a restart, and accepts appends again once the chain is restored.
func TestNoAppendAfterTruncation(t *testing.T) {
	mem := store.NewMemory()
	key, _ := keys.Generate("evidence")
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	e := New(mem, key).WithCheckpoint(path)
	rec := Record{TaskID: "t", Decision: "deny", Outcome: "not_executed"}
	for i := 0; i < 5; i++ {
		if _, err := e.Append(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
	gone := mem.DropNewestEvidence(2)
	if _, err := e.Append(context.Background(), rec); !errors.Is(err, ErrBehindCheckpoint) {
		t.Fatalf("append after truncation: %v", err)
	}
	restarted := New(mem, key).WithCheckpoint(path)
	if _, err := restarted.Append(context.Background(), rec); !errors.Is(err, ErrBehindCheckpoint) {
		t.Fatalf("append after truncation and restart: %v", err)
	}
	if res, _ := e.Verify(context.Background()); res.OK || !res.Truncated || res.Count != 3 {
		t.Fatalf("truncation must stay visible: %+v", res)
	}
	mem.RestoreEvidence(gone)
	if _, err := e.Append(context.Background(), rec); err != nil {
		t.Fatalf("append after restore: %v", err)
	}
	if res, _ := e.Verify(context.Background()); !res.OK || res.Count != 6 || res.Checkpoint.Count != 6 {
		t.Fatalf("restored chain: %+v", res)
	}
}

// Concurrent appends each get their own sequence number, and the checkpoint
// ends at the newest record.
func TestConcurrentAppends(t *testing.T) {
	e, _ := chain(t, 0)
	var wg sync.WaitGroup
	errs := make(chan error, 400)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if _, err := e.Append(context.Background(), Record{TaskID: "t", Decision: "allow", Outcome: "executed"}); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if res, _ := e.Verify(context.Background()); !res.OK || res.Count != 400 || res.Checkpoint.Count != 400 {
		t.Fatalf("after concurrent appends: %+v", res)
	}
}

// Edits that leave every signed field intact still change the stored text.
func TestEditsOutsideSignedFieldsAreDetected(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"trailing text": func(js string) string { return js + ` {"decision":"allow"}` },
		"stray bracket": func(js string) string { return js + `}` },
		"repeated key": func(js string) string {
			return strings.Replace(js, `"decision":"deny"`, `"decision":"allow","decision":"deny"`, 1)
		},
		"spacing": func(js string) string { return strings.Replace(js, `"decision":"deny"`, `"decision": "deny"`, 1) },
	} {
		e, mem := chain(t, 6)
		mem.TamperEvidence(3, edit)
		if res, _ := e.Verify(context.Background()); res.OK || res.FirstBadSeq != 3 {
			t.Fatalf("%s went unnoticed: %+v", name, res)
		}
	}
}

// A bundle without its checkpoint cannot show that nothing was cut off.
func TestExportWithoutCheckpointFails(t *testing.T) {
	e, _ := chain(t, 4)
	ex, _ := e.Export(context.Background())
	ex.Records = ex.Records[:2]
	ex.Checkpoint = nil
	if res, _ := VerifyExport(ex, ""); res.OK {
		t.Fatalf("a bundle with no checkpoint verified: %+v", res)
	}
}
