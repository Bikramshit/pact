package contract

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/foxcolab/pact/internal/keys"
	"github.com/foxcolab/pact/internal/store"
)

// Registry stores confirmed contracts. Entries are immutable: a contract is
// keyed by its hash, and a contract id can be bound to one hash only.
type Registry struct {
	Store     store.Store
	Org       *keys.KeyPair
	Validator Validator
}

// Confirm validates, signs and stores a contract the user has confirmed.
func (r *Registry) Confirm(ctx context.Context, c Contract, confirmedBy string) (Contract, error) {
	c.Hash, c.Sig = "", ""
	c.Normalize()
	if c.AgentID == "" {
		return Contract{}, errors.New("contract: agent_id is required")
	}
	if err := r.Validator.Validate(&c); err != nil {
		return Contract{}, fmt.Errorf("contract rejected: %w", err)
	}
	if err := c.Sign(r.Org, confirmedBy, time.Now()); err != nil {
		return Contract{}, err
	}
	if err := store.PutJSON(ctx, r.Store, store.KindContractRef, c.ContractID, map[string]string{"hash": c.Hash}, true); err != nil {
		if errors.Is(err, store.ErrExists) {
			return Contract{}, fmt.Errorf("contract %s is already confirmed and cannot be changed", c.ContractID)
		}
		return Contract{}, err
	}
	if err := store.PutJSON(ctx, r.Store, store.KindContract, c.Hash, c, true); err != nil {
		return Contract{}, err
	}
	return c, nil
}

// ByHash loads a contract and verifies its org signature before returning it.
func (r *Registry) ByHash(ctx context.Context, hash string) (Contract, error) {
	c, err := store.GetJSON[Contract](ctx, r.Store, store.KindContract, hash)
	if err != nil {
		return Contract{}, err
	}
	if c.Hash != hash {
		return Contract{}, errors.New("contract: stored under the wrong hash")
	}
	if err := c.Verify(r.Org.Public); err != nil {
		return Contract{}, err
	}
	return c, nil
}

// ByID loads a contract by its contract id.
func (r *Registry) ByID(ctx context.Context, id string) (Contract, error) {
	ref, err := store.GetJSON[map[string]string](ctx, r.Store, store.KindContractRef, id)
	if err != nil {
		return Contract{}, err
	}
	return r.ByHash(ctx, ref["hash"])
}

// List returns every confirmed contract, oldest first.
func (r *Registry) List(ctx context.Context) ([]Contract, error) {
	return store.ListJSON[Contract](ctx, r.Store, store.KindContract)
}
