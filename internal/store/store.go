// ABOUTME: SQLite-backed append-only event store: one file == one watercooler.
// ABOUTME: Filesystem access to the file IS membership; the file is the trust boundary.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/2389/gossip/internal/event"
)

type Store struct {
	db *sql.DB
}

// Config is watercooler-level policy stored inside the file. Moderators is a
// list of declared principal IDs; comparing against it is advisory, not
// authentication.
type Config struct {
	DefaultTTL time.Duration
	MaxTTL     time.Duration
	Moderators []string
}

const schema = `
CREATE TABLE IF NOT EXISTS events (
	seq             INTEGER PRIMARY KEY AUTOINCREMENT,
	id              TEXT NOT NULL UNIQUE,
	type            TEXT NOT NULL,
	schema_version  INTEGER NOT NULL,
	actor_id        TEXT NOT NULL,
	principal_id    TEXT NOT NULL,
	occurred_at     TEXT NOT NULL,
	idempotency_key TEXT NOT NULL UNIQUE,
	payload         TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS config (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: init schema: %w", err)
	}
	s := &Store{db: db}
	if err := s.ensureDefaultConfig(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DefaultConfig returns the out-of-the-box watercooler config used when
// opening a fresh store. It is the single source of truth for seed values.
func DefaultConfig() Config {
	return Config{
		DefaultTTL: 168 * time.Hour,
		MaxTTL:     720 * time.Hour,
		Moderators: []string{},
	}
}

func (s *Store) ensureDefaultConfig(ctx context.Context) error {
	dc := DefaultConfig()
	mods, err := json.Marshal(dc.Moderators)
	if err != nil {
		return fmt.Errorf("store: marshal default moderators: %w", err)
	}
	defaults := map[string]string{
		"default_ttl": dc.DefaultTTL.String(),
		"max_ttl":     dc.MaxTTL.String(),
		"moderators":  string(mods),
	}
	for k, v := range defaults {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO config(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING`, k, v); err != nil {
			return fmt.Errorf("store: seed config %s: %w", k, err)
		}
	}
	return nil
}

func (s *Store) configValue(ctx context.Context, key string) (string, error) {
	var v string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, key).Scan(&v); err != nil {
		return "", fmt.Errorf("store: read config %s: %w", key, err)
	}
	return v, nil
}

func (s *Store) Config(ctx context.Context) (Config, error) {
	var cfg Config
	dv, err := s.configValue(ctx, "default_ttl")
	if err != nil {
		return cfg, err
	}
	mv, err := s.configValue(ctx, "max_ttl")
	if err != nil {
		return cfg, err
	}
	js, err := s.configValue(ctx, "moderators")
	if err != nil {
		return cfg, err
	}
	if cfg.DefaultTTL, err = time.ParseDuration(dv); err != nil {
		return cfg, fmt.Errorf("store: bad default_ttl %q: %w", dv, err)
	}
	if cfg.MaxTTL, err = time.ParseDuration(mv); err != nil {
		return cfg, fmt.Errorf("store: bad max_ttl %q: %w", mv, err)
	}
	if err := json.Unmarshal([]byte(js), &cfg.Moderators); err != nil {
		return cfg, fmt.Errorf("store: bad moderators %q: %w", js, err)
	}
	if cfg.Moderators == nil {
		cfg.Moderators = []string{}
	}
	return cfg, nil
}

func (s *Store) SetConfig(ctx context.Context, cfg Config) error {
	mods := cfg.Moderators
	if mods == nil {
		mods = []string{}
	}
	js, err := json.Marshal(mods)
	if err != nil {
		return fmt.Errorf("store: marshal moderators: %w", err)
	}
	pairs := map[string]string{
		"default_ttl": cfg.DefaultTTL.String(),
		"max_ttl":     cfg.MaxTTL.String(),
		"moderators":  string(js),
	}
	for k, v := range pairs {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO config(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return fmt.Errorf("store: write config %s: %w", k, err)
		}
	}
	return nil
}

// Append validates and inserts all events in one transaction. A retry with an
// already-used idempotency key and identical (type, payload) returns the
// canonical stored event; different content under a used key is an error.
// Any failure rolls back the entire batch.
func (s *Store) Append(ctx context.Context, evs ...event.Envelope) ([]event.Envelope, error) {
	for _, e := range evs {
		if err := e.Validate(); err != nil {
			return nil, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	out := make([]event.Envelope, 0, len(evs))
	for _, e := range evs {
		var existing event.Envelope
		var occurred, payload string
		err := tx.QueryRowContext(ctx,
			`SELECT id, type, schema_version, actor_id, principal_id, occurred_at, idempotency_key, payload
			 FROM events WHERE idempotency_key = ?`, e.IdempotencyKey).
			Scan(&existing.ID, &existing.Type, &existing.SchemaVersion, &existing.ActorID,
				&existing.PrincipalID, &occurred, &existing.IdempotencyKey, &payload)
		switch {
		case err == nil:
			if existing.Type != e.Type || payload != string(e.Payload) {
				return nil, fmt.Errorf("store: idempotency key %q reused with different content", e.IdempotencyKey)
			}
			existing.OccurredAt, err = time.Parse(time.RFC3339Nano, occurred)
			if err != nil {
				return nil, fmt.Errorf("store: stored occurred_at %q: %w", occurred, err)
			}
			existing.Payload = json.RawMessage(payload)
			out = append(out, existing)
			continue
		case err != sql.ErrNoRows:
			return nil, fmt.Errorf("store: idempotency lookup: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events(id, type, schema_version, actor_id, principal_id, occurred_at, idempotency_key, payload)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, e.Type, e.SchemaVersion, e.ActorID, e.PrincipalID,
			e.OccurredAt.UTC().Format(time.RFC3339Nano), e.IdempotencyKey, string(e.Payload)); err != nil {
			return nil, fmt.Errorf("store: insert %s: %w", e.ID, err)
		}
		out = append(out, e)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return out, nil
}

// Events returns every event in append order. Views fold this; nothing is
// filtered here — the audit trail is the whole log.
func (s *Store) Events(ctx context.Context) ([]event.Envelope, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, schema_version, actor_id, principal_id, occurred_at, idempotency_key, payload
		 FROM events ORDER BY seq`)
	if err != nil {
		return nil, fmt.Errorf("store: query events: %w", err)
	}
	defer rows.Close()
	var out []event.Envelope
	for rows.Next() {
		var e event.Envelope
		var occurred, payload string
		if err := rows.Scan(&e.ID, &e.Type, &e.SchemaVersion, &e.ActorID, &e.PrincipalID,
			&occurred, &e.IdempotencyKey, &payload); err != nil {
			return nil, fmt.Errorf("store: scan: %w", err)
		}
		if e.OccurredAt, err = time.Parse(time.RFC3339Nano, occurred); err != nil {
			return nil, fmt.Errorf("store: stored occurred_at %q: %w", occurred, err)
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}
