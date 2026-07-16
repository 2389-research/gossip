// ABOUTME: Read-time folding of the event log into threads, posts, and badges.
// ABOUTME: No derived state is ever stored; every view folds fresh and filters at read.
package gossip

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/2389-research/gossip/internal/event"
)

type Receipt struct {
	Ref, ActorID, PrincipalID string
	At                        time.Time
}

type Corroboration struct {
	ActorID, PrincipalID string
	At                   time.Time
}

type Retraction struct {
	Reason string
	At     time.Time
}

type Hiding struct {
	Reason, ActorID, PrincipalID string
	At                           time.Time
}

type Post struct {
	ID, ThreadID, Body, Label, AuthorActor, AuthorPrincipal string
	CreatedAt, ExpiresAt                                    time.Time
	Refs                                                    []string
	Receipts                                                []Receipt
	Corroborations                                          []Corroboration
	Retracted                                               *Retraction
	Hidden                                                  *Hiding
}

type Thread struct {
	ID, Title, AuthorActor, AuthorPrincipal string
	CreatedAt                               time.Time
	PostIDs                                 []string
}

type Model struct {
	threads     map[string]*Thread
	posts       map[string]*Post
	threadOrder []string
}

// Fold replays the whole log into a model. Unknown kinds are skipped so old
// binaries can still read logs written by newer ones; writes stay fail-closed
// at Append.
func Fold(evs []event.Envelope) (*Model, error) {
	m := &Model{threads: map[string]*Thread{}, posts: map[string]*Post{}}
	for _, e := range evs {
		switch e.Type {
		case event.KindThreadCreated:
			var p event.ThreadCreated
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, fmt.Errorf("fold %s: %w", e.ID, err)
			}
			if _, dup := m.threads[p.ThreadID]; dup {
				continue
			}
			m.threads[p.ThreadID] = &Thread{ID: p.ThreadID, Title: p.Title,
				AuthorActor: e.ActorID, AuthorPrincipal: e.PrincipalID, CreatedAt: e.OccurredAt}
			m.threadOrder = append(m.threadOrder, p.ThreadID)
		case event.KindPostCreated:
			var p event.PostCreated
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, fmt.Errorf("fold %s: %w", e.ID, err)
			}
			if _, dup := m.posts[p.PostID]; dup {
				continue
			}
			th, ok := m.threads[p.ThreadID]
			if !ok {
				continue // orphan post: skip on read; writes prevent this
			}
			m.posts[p.PostID] = &Post{ID: p.PostID, ThreadID: p.ThreadID, Body: p.Body,
				Label: p.Label, AuthorActor: e.ActorID, AuthorPrincipal: e.PrincipalID,
				CreatedAt: e.OccurredAt, ExpiresAt: p.ExpiresAt, Refs: p.Refs}
			th.PostIDs = append(th.PostIDs, p.PostID)
		case event.KindReceiptAttached:
			var p event.ReceiptAttached
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, fmt.Errorf("fold %s: %w", e.ID, err)
			}
			if post, ok := m.posts[p.PostID]; ok {
				post.Receipts = append(post.Receipts,
					Receipt{Ref: p.ReceiptRef, ActorID: e.ActorID, PrincipalID: e.PrincipalID, At: e.OccurredAt})
			}
		case event.KindPostCorroborated:
			var p event.PostCorroborated
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, fmt.Errorf("fold %s: %w", e.ID, err)
			}
			if post, ok := m.posts[p.PostID]; ok {
				post.Corroborations = append(post.Corroborations,
					Corroboration{ActorID: e.ActorID, PrincipalID: e.PrincipalID, At: e.OccurredAt})
			}
		case event.KindPostRetracted:
			var p event.PostRetracted
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, fmt.Errorf("fold %s: %w", e.ID, err)
			}
			if post, ok := m.posts[p.PostID]; ok && post.Retracted == nil {
				post.Retracted = &Retraction{Reason: p.Reason, At: e.OccurredAt}
			}
		case event.KindPostHidden:
			var p event.PostHidden
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, fmt.Errorf("fold %s: %w", e.ID, err)
			}
			if post, ok := m.posts[p.PostID]; ok && post.Hidden == nil {
				post.Hidden = &Hiding{Reason: p.Reason, ActorID: e.ActorID,
					PrincipalID: e.PrincipalID, At: e.OccurredAt}
			}
		default:
			// Unknown kind: skip on read, forward-compatible. Append fails closed.
		}
	}
	return m, nil
}

func (m *Model) Post(id string) (*Post, bool) {
	p, ok := m.posts[id]
	return p, ok
}

func (m *Model) ThreadByID(id string) (*Thread, bool) {
	t, ok := m.threads[id]
	return t, ok
}

// Badges partitions evidence by DECLARED principal relative to the author.
// Identity is declared, not proven — badge counts derive from envelopes only.
type Badges struct {
	Receipts, SamePrincipal, DifferentPrincipal int
}

func (p *Post) Badges() Badges {
	b := Badges{Receipts: len(p.Receipts)}
	for _, c := range p.Corroborations {
		if c.PrincipalID == p.AuthorPrincipal {
			b.SamePrincipal++
		} else {
			b.DifferentPrincipal++
		}
	}
	return b
}

func (p *Post) expired(now time.Time) bool { return !now.Before(p.ExpiresAt) }

type PostView struct {
	Post      *Post
	Tombstone bool
}

type ThreadView struct {
	Thread  *Thread
	Posts   []PostView
	Decayed int
}

// Thread renders one thread: hidden posts tombstone (continuity), expired
// posts decay out of view and are counted, retracted posts stay visible.
func (m *Model) Thread(id string, now time.Time) (*ThreadView, error) {
	th, ok := m.threads[id]
	if !ok {
		return nil, fmt.Errorf("view: thread %q not found", id)
	}
	tv := &ThreadView{Thread: th}
	for _, pid := range th.PostIDs {
		p := m.posts[pid]
		switch {
		case p.Hidden != nil:
			tv.Posts = append(tv.Posts, PostView{Post: p, Tombstone: true})
		case p.expired(now):
			tv.Decayed++
		default:
			tv.Posts = append(tv.Posts, PostView{Post: p})
		}
	}
	return tv, nil
}

type ThreadSummary struct {
	ID, Title    string
	Visible      int
	LastActivity time.Time
}

func (m *Model) Threads(now time.Time) []ThreadSummary {
	var out []ThreadSummary
	for _, tid := range m.threadOrder {
		th := m.threads[tid]
		sum := ThreadSummary{ID: th.ID, Title: th.Title, LastActivity: th.CreatedAt}
		for _, pid := range th.PostIDs {
			p := m.posts[pid]
			if p.Hidden != nil || p.expired(now) {
				continue
			}
			sum.Visible++
			if p.CreatedAt.After(sum.LastActivity) {
				sum.LastActivity = p.CreatedAt
			}
		}
		out = append(out, sum)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastActivity.After(out[j].LastActivity) })
	return out
}
