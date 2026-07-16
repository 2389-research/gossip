// ABOUTME: Tests for all mutating commands: threads, posts, corroborate, receipt, retract, hide.
// ABOUTME: Every test runs against a real SQLite store in t.TempDir(); no mocks.
package gossip

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/gossip/internal/store"
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

func TestLateEvidenceIsLegalAndResurfacesNothing(t *testing.T) {
	c, s := testCmd(t, "a1", "p1")
	ctx := context.Background()
	thrID, postID, _ := c.StartThread(ctx, "t", "op", "", "")
	if err := c.Retract(ctx, postID, "wrong"); err != nil {
		t.Fatalf("retract: %v", err)
	}
	// Evidence against a retracted post still appends (witness testimony is
	// the witness's statement) and the view stays retracted.
	c2 := &Cmd{Store: s, ID: Identity{ActorID: "a2", PrincipalID: "p2", Source: "env"}, Now: c.Now}
	if err := c2.Corroborate(ctx, postID); err != nil {
		t.Fatalf("late corroboration rejected: %v", err)
	}
	if err := c2.Receipt(ctx, postID, "late-receipt"); err != nil {
		t.Fatalf("late receipt rejected: %v", err)
	}
	m := modelOf(t, s)
	p, _ := m.Post(postID)
	if p.Retracted == nil {
		t.Fatal("late evidence un-retracted the post")
	}
	tv, _ := m.Thread(thrID, c.Now.Add(time.Minute))
	for _, pv := range tv.Posts {
		if pv.Post.ID == postID && pv.Tombstone {
			t.Fatal("retracted post tombstoned — retracted stays visible, badged")
		}
	}

	// PIN (a): badge counts rise on retracted-but-visible posts.
	// The different-principal actor (c2, "p2") corroborated above — count must be 1.
	// Retraction is the author's statement and cannot bind witnesses.
	if b := p.Badges(); b.DifferentPrincipal != 1 {
		t.Fatalf("PIN(a): DifferentPrincipal badge = %d, want 1 after late corroboration by p2", b.DifferentPrincipal)
	}
	// Post must appear in thread view non-tombstoned with Retracted state set.
	found := false
	for _, pv := range tv.Posts {
		if pv.Post.ID == postID {
			found = true
			if pv.Tombstone {
				t.Fatal("PIN(a): retracted post must not be tombstoned in thread view")
			}
			if pv.Post.Retracted == nil {
				t.Fatal("PIN(a): post in view must have Retracted state set")
			}
		}
	}
	if !found {
		t.Fatal("PIN(a): retracted post must appear in thread view")
	}
}

func TestRetractAuthorOnlyWithReason(t *testing.T) {
	c, s := testCmd(t, "a1", "p1")
	ctx := context.Background()
	_, postID, _ := c.StartThread(ctx, "t", "op", "", "")

	other := &Cmd{Store: s, ID: Identity{ActorID: "a2", PrincipalID: "p1", Source: "env"}, Now: c.Now}
	if err := other.Retract(ctx, postID, "nope"); !errors.Is(err, ErrValidation) {
		t.Fatalf("non-author retraction accepted: %v", err)
	}
	if err := c.Retract(ctx, postID, ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty reason accepted: %v", err)
	}
	if err := c.Retract(ctx, postID, "jumped to conclusions"); err != nil {
		t.Fatalf("valid retraction rejected: %v", err)
	}
	if err := c.Retract(ctx, postID, "again"); !errors.Is(err, ErrValidation) {
		t.Fatalf("double retraction accepted: %v", err)
	}
}

func TestHideModeratorGatedWithReason(t *testing.T) {
	c, s := testCmd(t, "a1", "p1")
	ctx := context.Background()
	thrID, postID, _ := c.StartThread(ctx, "t", "leaked token here", "", "")

	// Not a moderator: advisory gate denies at the honest-client boundary.
	if err := c.Hide(ctx, postID, "credential leakage"); !errors.Is(err, ErrValidation) {
		t.Fatalf("non-moderator hide accepted: %v", err)
	}
	cfg, _ := s.Config(ctx)
	cfg.Moderators = []string{"p_mod"}
	if err := s.SetConfig(ctx, cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	mod := &Cmd{Store: s, ID: Identity{ActorID: "a9", PrincipalID: "p_mod", Source: "env"}, Now: c.Now}
	if err := mod.Hide(ctx, postID, ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty hide reason accepted: %v", err)
	}
	if err := mod.Hide(ctx, postID, "credential leakage"); err != nil {
		t.Fatalf("moderator hide rejected: %v", err)
	}
	if err := mod.Hide(ctx, postID, "again"); !errors.Is(err, ErrValidation) {
		t.Fatalf("double hide accepted: %v", err)
	}
	m := modelOf(t, s)
	tv, _ := m.Thread(thrID, c.Now.Add(time.Minute))
	if len(tv.Posts) != 1 || !tv.Posts[0].Tombstone {
		t.Fatalf("hidden post not tombstoned: %+v", tv.Posts)
	}
}

func TestHideModeratorGateCheckedBeforeReason(t *testing.T) {
	c, s := testCmd(t, "a1", "p1")
	ctx := context.Background()
	_, postID, _ := c.StartThread(ctx, "t", "op", "", "")

	// c is NOT on the moderator list. Empty reason: gate should fire first.
	cfg, _ := s.Config(ctx)
	cfg.Moderators = []string{"p_mod"}
	if err := s.SetConfig(ctx, cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	err := c.Hide(ctx, postID, "") // non-moderator, empty reason
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
	// Must name the moderator gate, NOT the reason-required check.
	const gateMsg = "is not on this store's moderator list"
	const reasonMsg = "hide reason is required"
	if !strings.Contains(err.Error(), gateMsg) {
		t.Errorf("expected error to contain %q (moderator gate), got: %q", gateMsg, err.Error())
	}
	if strings.Contains(err.Error(), reasonMsg) {
		t.Errorf("expected error NOT to contain %q (reason check should not run before gate), got: %q", reasonMsg, err.Error())
	}
}

func TestHiddenPostTombstoneImmuneToLateEvidence(t *testing.T) {
	c, s := testCmd(t, "a1", "p1")
	ctx := context.Background()
	thrID, postID, _ := c.StartThread(ctx, "t", "sensitive content", "", "")

	// Set up moderator and hide the post.
	cfg, _ := s.Config(ctx)
	cfg.Moderators = []string{"p_mod"}
	if err := s.SetConfig(ctx, cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	mod := &Cmd{Store: s, ID: Identity{ActorID: "a9", PrincipalID: "p_mod", Source: "env"}, Now: c.Now}
	if err := mod.Hide(ctx, postID, "safety"); err != nil {
		t.Fatalf("hide: %v", err)
	}

	// PIN (b) model half: thread view must tombstone the hidden post.
	m := modelOf(t, s)
	tv, _ := m.Thread(thrID, c.Now.Add(time.Minute))
	if len(tv.Posts) != 1 || !tv.Posts[0].Tombstone {
		t.Fatalf("PIN(b): hidden post not tombstoned before late evidence: %+v", tv.Posts)
	}
	pv := tv.Posts[0]
	if pv.Post.Hidden == nil {
		t.Fatal("PIN(b): post view must have Hidden state set before late evidence")
	}

	// Late evidence (corroborate + receipt) against a hidden post must succeed.
	c2 := &Cmd{Store: s, ID: Identity{ActorID: "a2", PrincipalID: "p2", Source: "env"}, Now: c.Now}
	if err := c2.Corroborate(ctx, postID); err != nil {
		t.Fatalf("PIN(b): late corroboration against hidden post rejected: %v", err)
	}
	if err := c2.Receipt(ctx, postID, "late-receipt-hidden"); err != nil {
		t.Fatalf("PIN(b): late receipt against hidden post rejected: %v", err)
	}

	// PIN (b) model half: tombstone must survive late evidence.
	m2 := modelOf(t, s)
	tv2, _ := m2.Thread(thrID, c.Now.Add(time.Minute))
	if len(tv2.Posts) != 1 || !tv2.Posts[0].Tombstone {
		t.Fatalf("PIN(b): tombstone lost after late evidence: %+v", tv2.Posts)
	}
	pv2 := tv2.Posts[0]
	if pv2.Post.Hidden == nil {
		t.Fatal("PIN(b): post view must have Hidden state set after late evidence")
	}
}
