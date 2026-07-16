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

func (s *Store) ensureDefaultConfig(ctx context.Context) error {
	defaults := map[string]string{
		"default_ttl": (168 * time.Hour).String(),
		"max_ttl":     (720 * time.Hour).String(),
		"moderators":  "[]",
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
