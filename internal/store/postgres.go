package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

// evidenceLock is the advisory lock that serialises evidence appends.
const evidenceLock = 0x50414354 // "PACT"

// Postgres is the PostgreSQL-backed Store.
type Postgres struct {
	pool *pgxpool.Pool
}

// OpenPostgres connects, waits for the database to accept connections and applies the schema.
func OpenPostgres(ctx context.Context, url string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if err = pool.Ping(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("store: database not reachable: %w", err)
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: applying schema: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Put(ctx context.Context, kind, id string, doc []byte, immutable bool) error {
	if immutable {
		_, err := p.pool.Exec(ctx, `INSERT INTO documents (kind, id, doc) VALUES ($1, $2, $3)`, kind, id, doc)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrExists
		}
		return err
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO documents (kind, id, doc) VALUES ($1, $2, $3)
		ON CONFLICT (kind, id) DO UPDATE SET doc = EXCLUDED.doc, updated_at = now()`, kind, id, doc)
	return err
}

func (p *Postgres) Get(ctx context.Context, kind, id string) ([]byte, error) {
	var doc []byte
	err := p.pool.QueryRow(ctx, `SELECT doc FROM documents WHERE kind = $1 AND id = $2`, kind, id).Scan(&doc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return doc, err
}

func (p *Postgres) List(ctx context.Context, kind string) ([][]byte, error) {
	rows, err := p.pool.Query(ctx, `SELECT doc FROM documents WHERE kind = $1 ORDER BY created_at, id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var doc []byte
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

func (p *Postgres) UseNonce(ctx context.Context, capID, nonce string, at time.Time) (bool, error) {
	tag, err := p.pool.Exec(ctx, `INSERT INTO nonces (cap_id, nonce, seen_at) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, capID, nonce, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (p *Postgres) AppendEvidence(ctx context.Context, fn AppendFunc) (EvidenceRow, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return EvidenceRow{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(evidenceLock)); err != nil {
		return EvidenceRow{}, err
	}
	var seq int64
	var prev string
	err = tx.QueryRow(ctx, `SELECT seq, hash FROM evidence ORDER BY seq DESC LIMIT 1`).Scan(&seq, &prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return EvidenceRow{}, err
	}
	row, err := fn(seq+1, prev)
	if err != nil {
		return EvidenceRow{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO evidence (seq, task_id, ts, record_json, hash) VALUES ($1, $2, $3, $4, $5)`,
		row.Seq, row.TaskID, row.TS, row.JSON, row.Hash); err != nil {
		return EvidenceRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return EvidenceRow{}, err
	}
	return row, nil
}

func (p *Postgres) Evidence(ctx context.Context, taskID string) ([]EvidenceRow, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT seq, task_id, ts, record_json, hash FROM evidence
		WHERE $1 = '' OR task_id = $1 ORDER BY seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EvidenceRow
	for rows.Next() {
		var r EvidenceRow
		if err := rows.Scan(&r.Seq, &r.TaskID, &r.TS, &r.JSON, &r.Hash); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) Close() { p.pool.Close() }
