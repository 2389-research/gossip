// ABOUTME: Tests for mutating commands: compound thread creation and fail-closed posting.
// ABOUTME: Every test runs against a real SQLite store in t.TempDir(); no mocks.
package gossip

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389/gossip/internal/store"
)

func testCmd(t *testing.T, actor, principal string) (*Cmd, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "gossip.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return &Cmd{Store: s, ID: Identity{ActorID: actor, PrincipalID: principal, Source: "env"},
		Now: time.Date(2026, 7, 16, 3, 0, 0, 0, time.UTC)}, s
}

func modelOf(t *testing.T, s *store.Store) *Model {
	t.Helper()
	evs, err := s.Events(context.Background())
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	m, err := Fold(evs)
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	return m
}

func TestStartThreadCompoundCreate(t *testing.T) {
	c, s := testCmd(t, "a1", "p1")
	ctx := context.Background()
	thrID, postID, err := c.StartThread(ctx, "cursed deploys", "the script is cursed", "", "")
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	evs, _ := s.Events(ctx)
	if len(evs) != 2 {
		t.Fatalf("%d events, want 2", len(evs))
	}
	if !evs[0].OccurredAt.Equal(evs[1].OccurredAt) {
		t.Fatal("compound events must share occurred_at")
	}
	base := strings.TrimSuffix(evs[0].IdempotencyKey, "/thread")
	if evs[1].IdempotencyKey != base+"/post" {
		t.Fatalf("keys not derived from one command key: %q, %q",
			evs[0].IdempotencyKey, evs[1].IdempotencyKey)
	}
	m := modelOf(t, s)
	p, ok := m.Post(postID)
	if !ok || p.ThreadID != thrID || p.Label != "rumor" {
		t.Fatalf("OP post = %+v", p)
	}
	// Default TTL is store default (168h).
	if want := c.Now.Add(168 * time.Hour); !p.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", p.ExpiresAt, want)
	}
}

func TestStartThreadRejectsEmptyTitleOrBody(t *testing.T) {
	c, _ := testCmd(t, "a1", "p1")
	ctx := context.Background()
	if _, _, err := c.StartThread(ctx, "", "body", "", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty title: %v", err)
	}
	if _, _, err := c.StartThread(ctx, "title", "", "", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty body (empty threads are invalid): %v", err)
	}
}

func TestPostValidatesLabelTTLAndThread(t *testing.T) {
	c, _ := testCmd(t, "a1", "p1")
	ctx := context.Background()
	thrID, _, _ := c.StartThread(ctx, "t", "op", "", "")

	if _, err := c.Post(ctx, thrID, "b", "verified", "", nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("label 'verified' must be unauthorable: %v", err)
	}
	if _, err := c.Post(ctx, thrID, "b", "rumor", "9999h", nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("over-max ttl must error, never clamp: %v", err)
	}
	if _, err := c.Post(ctx, "thr_nope", "b", "rumor", "", nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing thread must fail closed: %v", err)
	}
	if _, err := c.Post(ctx, thrID, "seen it", "observed", "72h", nil); err != nil {
		t.Fatalf("valid observed post rejected: %v", err)
	}
}

func TestPostRefsFailClosed(t *testing.T) {
	c, s := testCmd(t, "a1", "p1")
	ctx := context.Background()
	thrID, opID, _ := c.StartThread(ctx, "t", "op", "", "")

	// Missing ref: fail closed.
	if _, err := c.Post(ctx, thrID, "b", "rumor", "", []string{"post_missing"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unresolvable ref accepted: %v", err)
	}
	// Foreign ref: an ID that exists only in a DIFFERENT store must not resolve here.
	other, _ := testCmd(t, "a1", "p1")
	_, foreignPost, _ := other.StartThread(ctx, "other", "op", "", "")
	if _, err := c.Post(ctx, thrID, "b", "rumor", "", []string{foreignPost}); !errors.Is(err, ErrValidation) {
		t.Fatalf("foreign-store ref accepted — confused deputy: %v", err)
	}
	// Same-store refs to a post and to the thread itself both resolve.
	if _, err := c.Post(ctx, thrID, "quoting", "rumor", "", []string{opID, thrID}); err != nil {
		t.Fatalf("valid refs rejected: %v", err)
	}
	_ = s
}

func TestCorroborateRejectsSelfAndMissingPost(t *testing.T) {
	c, _ := testCmd(t, "a1", "p1")
	ctx := context.Background()
	_, postID, _ := c.StartThread(ctx, "t", "op", "", "")

	if err := c.Corroborate(ctx, postID); !errors.Is(err, ErrValidation) {
		t.Fatalf("self-corroboration accepted: %v", err)
	}
	if err := c.Corroborate(ctx, "post_missing"); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing post accepted: %v", err)
	}

	// A different declared actor (same store) may corroborate — even same principal.
	c2 := &Cmd{Store: c.Store, ID: Identity{ActorID: "a2", PrincipalID: "p1", Source: "env"}, Now: c.Now}
	if err := c2.Corroborate(ctx, postID); err != nil {
		t.Fatalf("valid corroboration rejected: %v", err)
	}
	m := modelOf(t, c.Store)
	p, _ := m.Post(postID)
	if b := p.Badges(); b.SamePrincipal != 1 {
		t.Fatalf("badges = %+v, want one same-declared-principal corroboration", b)
	}
}

func TestReceiptRequiresPostAndNonEmptyRef(t *testing.T) {
	c, _ := testCmd(t, "a1", "p1")
	ctx := context.Background()
	_, postID, _ := c.StartThread(ctx, "t", "op", "", "")

	if err := c.Receipt(ctx, postID, ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty receipt ref accepted: %v", err)
	}
	if err := c.Receipt(ctx, "post_missing", "x"); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing post accepted: %v", err)
	}
	if err := c.Receipt(ctx, postID, "cmd/palace/main.go static inspection"); err != nil {
		t.Fatalf("valid receipt rejected: %v", err)
	}
}
