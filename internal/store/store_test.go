// ABOUTME: Tests for the SQLite event store: schema creation and watercooler config.
// ABOUTME: Every test uses a real SQLite file in t.TempDir(); no mocks.
package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "gossip.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesStoreWithDefaultConfig(t *testing.T) {
	s := openTemp(t)
	cfg, err := s.Config(context.Background())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.DefaultTTL != 168*time.Hour {
		t.Fatalf("DefaultTTL = %v, want 168h", cfg.DefaultTTL)
	}
	if cfg.MaxTTL != 720*time.Hour {
		t.Fatalf("MaxTTL = %v, want 720h", cfg.MaxTTL)
	}
	if len(cfg.Moderators) != 0 {
		t.Fatalf("fresh store has moderators: %v", cfg.Moderators)
	}
}

func TestSetConfigPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gossip.db")
	ctx := context.Background()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := Config{DefaultTTL: 24 * time.Hour, MaxTTL: 96 * time.Hour, Moderators: []string{"operator"}}
	if err := s.SetConfig(ctx, want); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, err := s2.Config(ctx)
	if err != nil {
		t.Fatalf("Config after reopen: %v", err)
	}
	if got.DefaultTTL != want.DefaultTTL || got.MaxTTL != want.MaxTTL ||
		len(got.Moderators) != 1 || got.Moderators[0] != "operator" {
		t.Fatalf("config after reopen = %+v, want %+v", got, want)
	}
}

func TestOpenDoesNotResetExistingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gossip.db")
	ctx := context.Background()
	s, _ := Open(path)
	s.SetConfig(ctx, Config{DefaultTTL: time.Hour, MaxTTL: 2 * time.Hour, Moderators: nil})
	s.Close()
	s2, _ := Open(path) // second Open must not overwrite with defaults
	defer s2.Close()
	got, _ := s2.Config(ctx)
	if got.DefaultTTL != time.Hour {
		t.Fatalf("reopen reset config: %+v", got)
	}
}
