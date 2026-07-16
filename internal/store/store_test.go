// ABOUTME: Tests for the SQLite event store: schema creation and watercooler config.
// ABOUTME: Every test uses a real SQLite file in t.TempDir(); no mocks.
package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/2389-research/gossip/internal/event"
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

func testEnvelope(key, id string) event.Envelope {
	return event.Envelope{
		ID: id, Type: event.KindThreadCreated, SchemaVersion: 1,
		ActorID: "agent_three", PrincipalID: "operator",
		OccurredAt:     time.Date(2026, 7, 16, 3, 0, 0, 0, time.UTC),
		IdempotencyKey: key,
		Payload:        event.MustMarshal(event.ThreadCreated{ThreadID: "thr_" + id, Title: "t"}),
	}
}

func TestAppendAndReadBack(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	stored, err := s.Append(ctx, testEnvelope("k1", "evt_1"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if len(stored) != 1 || stored[0].ID != "evt_1" {
		t.Fatalf("stored = %+v", stored)
	}
	evs, err := s.Events(ctx)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(evs) != 1 || evs[0].ID != "evt_1" || evs[0].Type != event.KindThreadCreated ||
		evs[0].ActorID != "agent_three" || evs[0].PrincipalID != "operator" ||
		!evs[0].OccurredAt.Equal(time.Date(2026, 7, 16, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("read back mismatch: %+v", evs)
	}
}

func TestAppendRejectsInvalidEnvelope(t *testing.T) {
	s := openTemp(t)
	bad := testEnvelope("k1", "evt_1")
	bad.Type = "gossip.made.up"
	if _, err := s.Append(context.Background(), bad); err == nil {
		t.Fatal("append accepted unknown event type")
	}
	evs, _ := s.Events(context.Background())
	if len(evs) != 0 {
		t.Fatalf("invalid event persisted: %+v", evs)
	}
}

func TestAppendIdempotentRetryReturnsStoredOriginal(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	first, err := s.Append(ctx, testEnvelope("same-key", "evt_a"))
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	// Retry with the same idempotency key and same content but a fresh event ID:
	// the canonical stored event must come back and no duplicate row may exist.
	retry := testEnvelope("same-key", "evt_b")
	retry.Payload = first[0].Payload
	stored, err := s.Append(ctx, retry)
	if err != nil {
		t.Fatalf("retry append: %v", err)
	}
	if stored[0].ID != "evt_a" {
		t.Fatalf("retry returned %q, want stored original evt_a", stored[0].ID)
	}
	evs, _ := s.Events(ctx)
	if len(evs) != 1 {
		t.Fatalf("duplicate persisted: %d events", len(evs))
	}
}

func TestAppendIdempotencyKeyReuseWithDifferentContentFails(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if _, err := s.Append(ctx, testEnvelope("same-key", "evt_a")); err != nil {
		t.Fatalf("first append: %v", err)
	}
	other := testEnvelope("same-key", "evt_b")
	other.Payload = event.MustMarshal(event.ThreadCreated{ThreadID: "thr_other", Title: "different"})
	if _, err := s.Append(ctx, other); err == nil {
		t.Fatal("key reuse with different content accepted")
	}
}

func TestAppendBatchIsAtomic(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if _, err := s.Append(ctx, testEnvelope("existing", "evt_1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	good := testEnvelope("fresh", "evt_2")
	conflicting := testEnvelope("existing", "evt_3")
	conflicting.Payload = event.MustMarshal(event.ThreadCreated{ThreadID: "thr_x", Title: "different"})
	if _, err := s.Append(ctx, good, conflicting); err == nil {
		t.Fatal("batch with conflicting member accepted")
	}
	evs, _ := s.Events(ctx)
	if len(evs) != 1 {
		t.Fatalf("partial batch persisted: %d events, want 1", len(evs))
	}
}
