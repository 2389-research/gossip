// ABOUTME: Tests for read-time folding: model, badges by declared principal, filters.
// ABOUTME: Views derive everything; expired decays, hidden tombstones, retracted stays visible.
package gossip

import (
	"testing"
	"time"

	"github.com/2389/gossip/internal/event"
)

var t0 = time.Date(2026, 7, 16, 3, 0, 0, 0, time.UTC)

func env(id, kind, actor, principal string, at time.Time, payload any) event.Envelope {
	return event.Envelope{
		ID: id, Type: kind, SchemaVersion: 1, ActorID: actor, PrincipalID: principal,
		OccurredAt: at, IdempotencyKey: "k_" + id, Payload: event.MustMarshal(payload),
	}
}

func fixture(t *testing.T) *Model {
	t.Helper()
	evs := []event.Envelope{
		env("e1", event.KindThreadCreated, "a1", "p1", t0,
			event.ThreadCreated{ThreadID: "thr_1", Title: "the deploy script is cursed"}),
		env("e2", event.KindPostCreated, "a1", "p1", t0,
			event.PostCreated{PostID: "post_1", ThreadID: "thr_1", Body: "it is cursed",
				Label: "rumor", ExpiresAt: t0.Add(168 * time.Hour)}),
		env("e3", event.KindPostCreated, "a2", "p1", t0.Add(time.Minute),
			event.PostCreated{PostID: "post_2", ThreadID: "thr_1", Body: "i saw it happen",
				Label: "observed", ExpiresAt: t0.Add(time.Hour), Refs: []string{"post_1"}}),
		env("e4", event.KindPostCorroborated, "a2", "p1", t0.Add(2*time.Minute),
			event.PostCorroborated{PostID: "post_1"}),
		env("e5", event.KindPostCorroborated, "a3", "p2", t0.Add(3*time.Minute),
			event.PostCorroborated{PostID: "post_1"}),
		env("e6", event.KindReceiptAttached, "a3", "p2", t0.Add(4*time.Minute),
			event.ReceiptAttached{PostID: "post_1", ReceiptRef: "test/TestCursed"}),
		env("e7", event.KindPostCreated, "a1", "p1", t0.Add(5*time.Minute),
			event.PostCreated{PostID: "post_3", ThreadID: "thr_1", Body: "leaked a token once",
				Label: "rumor", ExpiresAt: t0.Add(168 * time.Hour)}),
		env("e8", event.KindPostHidden, "a9", "p9", t0.Add(6*time.Minute),
			event.PostHidden{PostID: "post_3", Reason: "credential leakage"}),
		env("e9", event.KindPostRetracted, "a1", "p1", t0.Add(7*time.Minute),
			event.PostRetracted{PostID: "post_1", Reason: "jumped to conclusions"}),
	}
	m, err := Fold(evs)
	if err != nil {
		t.Fatalf("Fold: %v", err)
	}
	return m
}

func TestFoldBuildsThreadsAndPosts(t *testing.T) {
	m := fixture(t)
	th, ok := m.ThreadByID("thr_1")
	if !ok {
		t.Fatal("thr_1 missing")
	}
	if th.Title != "the deploy script is cursed" || len(th.PostIDs) != 3 {
		t.Fatalf("thread = %+v", th)
	}
	p, ok := m.Post("post_1")
	if !ok || p.AuthorActor != "a1" || p.AuthorPrincipal != "p1" || p.Label != "rumor" {
		t.Fatalf("post_1 = %+v", p)
	}
}

func TestBadgesPartitionByDeclaredPrincipal(t *testing.T) {
	m := fixture(t)
	p, _ := m.Post("post_1")
	b := p.Badges()
	// a2/p1 shares the author's declared principal; a3/p2 differs.
	if b.Receipts != 1 || b.SamePrincipal != 1 || b.DifferentPrincipal != 1 {
		t.Fatalf("badges = %+v", b)
	}
}

func TestRetractedStaysVisible(t *testing.T) {
	m := fixture(t)
	tv, err := m.Thread("thr_1", t0.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	var found bool
	for _, pv := range tv.Posts {
		if pv.Post.ID == "post_1" {
			found = true
			if pv.Tombstone {
				t.Fatal("retracted post rendered as tombstone")
			}
			if pv.Post.Retracted == nil || pv.Post.Retracted.Reason != "jumped to conclusions" {
				t.Fatalf("retraction not surfaced: %+v", pv.Post.Retracted)
			}
		}
	}
	if !found {
		t.Fatal("retracted post_1 missing from thread view — retraction must not remove")
	}
}

func TestHiddenBecomesTombstone(t *testing.T) {
	m := fixture(t)
	tv, _ := m.Thread("thr_1", t0.Add(10*time.Minute))
	for _, pv := range tv.Posts {
		if pv.Post.ID == "post_3" {
			if !pv.Tombstone {
				t.Fatal("hidden post not tombstoned")
			}
			return
		}
	}
	t.Fatal("hidden post absent entirely — tombstone must preserve continuity")
}

func TestExpiredDecaysFromViewNotFromModel(t *testing.T) {
	m := fixture(t)
	now := t0.Add(2 * time.Hour) // post_2 (ttl 1h) has expired
	tv, _ := m.Thread("thr_1", now)
	for _, pv := range tv.Posts {
		if pv.Post.ID == "post_2" {
			t.Fatal("expired post still in ordinary view")
		}
	}
	if tv.Decayed != 1 {
		t.Fatalf("Decayed = %d, want 1", tv.Decayed)
	}
	if _, ok := m.Post("post_2"); !ok {
		t.Fatal("expired post gone from model — decay must not erase")
	}
}

func TestThreadsSummarySortsByActivityAndCounts(t *testing.T) {
	m := fixture(t)
	sums := m.Threads(t0.Add(10 * time.Minute))
	if len(sums) != 1 {
		t.Fatalf("summaries = %+v", sums)
	}
	// post_1 visible, post_2 visible (not yet expired at +10m), post_3 hidden.
	if sums[0].Visible != 2 {
		t.Fatalf("Visible = %d, want 2", sums[0].Visible)
	}
}

func TestFoldSkipsUnknownKinds(t *testing.T) {
	evs := []event.Envelope{
		env("e1", event.KindThreadCreated, "a1", "p1", t0, event.ThreadCreated{ThreadID: "thr_1", Title: "x"}),
	}
	evs = append(evs, event.Envelope{
		ID: "e2", Type: "gossip.future.kind", SchemaVersion: 1, ActorID: "a", PrincipalID: "p",
		OccurredAt: t0, IdempotencyKey: "k_e2", Payload: event.MustMarshal(map[string]string{}),
	})
	if _, err := Fold(evs); err != nil {
		t.Fatalf("Fold must skip unknown kinds on read (forward compatible): %v", err)
	}
}
