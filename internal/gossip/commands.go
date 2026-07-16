// ABOUTME: Mutating gossip commands: validate fail-closed, then append in one transaction.
// ABOUTME: The CLI is the honest path; the file boundary itself stays advisory by design.
package gossip

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/2389/gossip/internal/event"
	"github.com/2389/gossip/internal/store"
)

// ErrValidation marks domain-validation failures (exit 1 at the CLI, message shown).
var ErrValidation = errors.New("validation")

func validationErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

type Cmd struct {
	Store *store.Store
	ID    Identity
	Now   time.Time
}

func newID(prefix string) string {
	return prefix + "_" + strings.ToLower(ulid.Make().String())
}

func (c *Cmd) model(ctx context.Context) (*Model, error) {
	evs, err := c.Store.Events(ctx)
	if err != nil {
		return nil, err
	}
	return Fold(evs)
}

// resolveTTL turns a user TTL string ("" == store default) into an absolute
// expires_at, failing closed on parse errors and bounds violations.
func (c *Cmd) resolveTTL(ctx context.Context, ttl string) (time.Time, error) {
	cfg, err := c.Store.Config(ctx)
	if err != nil {
		return time.Time{}, err
	}
	d := cfg.DefaultTTL
	if ttl != "" {
		if d, err = ParseTTL(ttl); err != nil {
			return time.Time{}, validationErr("%v", err)
		}
	}
	if err := CheckTTLBounds(d, cfg.MaxTTL); err != nil {
		return time.Time{}, validationErr("%v", err)
	}
	return c.Now.Add(d), nil
}

func normalizeLabel(label string) (string, error) {
	if label == "" {
		return "rumor", nil
	}
	if label != "rumor" && label != "observed" {
		return "", validationErr("label must be rumor or observed, got %q", label)
	}
	return label, nil
}

func (c *Cmd) envelope(kind, key string, payload any) event.Envelope {
	return event.Envelope{
		ID: newID("evt"), Type: kind, SchemaVersion: 1,
		ActorID: c.ID.ActorID, PrincipalID: c.ID.PrincipalID,
		OccurredAt: c.Now.UTC(), IdempotencyKey: key,
		Payload: event.MustMarshal(payload),
	}
}

// StartThread is the compound create: thread + OP post in ONE transaction,
// same occurred_at, idempotency keys derived from one command ID. Empty
// threads are invalid, so the OP body is required.
func (c *Cmd) StartThread(ctx context.Context, title, body, label, ttl string) (string, string, error) {
	if strings.TrimSpace(title) == "" {
		return "", "", validationErr("thread title must not be empty")
	}
	if strings.TrimSpace(body) == "" {
		return "", "", validationErr("empty threads are invalid: OP body required")
	}
	lbl, err := normalizeLabel(label)
	if err != nil {
		return "", "", err
	}
	expires, err := c.resolveTTL(ctx, ttl)
	if err != nil {
		return "", "", err
	}
	cmdID := newID("cmd")
	thrID := newID("thr")
	postID := newID("post")
	_, err = c.Store.Append(ctx,
		c.envelope(event.KindThreadCreated, cmdID+"/thread",
			event.ThreadCreated{ThreadID: thrID, Title: title}),
		c.envelope(event.KindPostCreated, cmdID+"/post",
			event.PostCreated{PostID: postID, ThreadID: thrID, Body: body, Label: lbl, ExpiresAt: expires}),
	)
	if err != nil {
		return "", "", err
	}
	return thrID, postID, nil
}

// Corroborate asserts first-hand observation of a post by a DIFFERENT declared
// actor. Legal against retracted/hidden/expired posts: late witness testimony
// appends to the audit trail and views resurface nothing.
func (c *Cmd) Corroborate(ctx context.Context, postID string) error {
	m, err := c.model(ctx)
	if err != nil {
		return err
	}
	p, ok := m.Post(postID)
	if !ok {
		return validationErr("post %q not found in this store", postID)
	}
	if p.AuthorActor == c.ID.ActorID {
		return validationErr("corroborator must differ from post author (self-attestation caps at observed)")
	}
	_, err = c.Store.Append(ctx, c.envelope(event.KindPostCorroborated, newID("cmd")+"/corroborate",
		event.PostCorroborated{PostID: postID}))
	return err
}

// Receipt attaches machine-checkable evidence BY REFERENCE. The ref is an
// opaque string: stored and displayed, never fetched, never resolved in-store.
func (c *Cmd) Receipt(ctx context.Context, postID, ref string) error {
	if strings.TrimSpace(ref) == "" {
		return validationErr("receipt ref must not be empty")
	}
	m, err := c.model(ctx)
	if err != nil {
		return err
	}
	if _, ok := m.Post(postID); !ok {
		return validationErr("post %q not found in this store", postID)
	}
	_, err = c.Store.Append(ctx, c.envelope(event.KindReceiptAttached, newID("cmd")+"/receipt",
		event.ReceiptAttached{PostID: postID, ReceiptRef: ref}))
	return err
}

// Retract is the author's own correction: visible forever, badged retracted.
// Author-only compares declared actor IDs — the strongest claim v1 can make.
func (c *Cmd) Retract(ctx context.Context, postID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return validationErr("retraction reason is required")
	}
	m, err := c.model(ctx)
	if err != nil {
		return err
	}
	p, ok := m.Post(postID)
	if !ok {
		return validationErr("post %q not found in this store", postID)
	}
	if p.AuthorActor != c.ID.ActorID {
		return validationErr("only the post author may retract (author: %s)", p.AuthorActor)
	}
	if p.Retracted != nil {
		return validationErr("post %q is already retracted", postID)
	}
	_, err = c.Store.Append(ctx, c.envelope(event.KindPostRetracted, newID("cmd")+"/retract",
		event.PostRetracted{PostID: postID, Reason: reason}))
	return err
}

// Hide is a safety action gated on the store's moderator list. The gate is
// ADVISORY: comparing a declared principal to a config list is not
// authenticated, and anyone with file access can bypass the CLI entirely.
func (c *Cmd) Hide(ctx context.Context, postID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return validationErr("hide reason is required")
	}
	cfg, err := c.Store.Config(ctx)
	if err != nil {
		return err
	}
	isMod := false
	for _, mod := range cfg.Moderators {
		if mod == c.ID.PrincipalID {
			isMod = true
			break
		}
	}
	if !isMod {
		return validationErr("declared principal %q is not on this store's moderator list (advisory gate)", c.ID.PrincipalID)
	}
	m, err := c.model(ctx)
	if err != nil {
		return err
	}
	p, ok := m.Post(postID)
	if !ok {
		return validationErr("post %q not found in this store", postID)
	}
	if p.Hidden != nil {
		return validationErr("post %q is already hidden", postID)
	}
	_, err = c.Store.Append(ctx, c.envelope(event.KindPostHidden, newID("cmd")+"/hide",
		event.PostHidden{PostID: postID, Reason: reason}))
	return err
}

// Post appends one post. Refs must resolve to a post or thread in THIS store;
// missing or foreign refs fail closed (the confused-deputy lesson).
func (c *Cmd) Post(ctx context.Context, threadID, body, label, ttl string, refs []string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", validationErr("post body must not be empty")
	}
	lbl, err := normalizeLabel(label)
	if err != nil {
		return "", err
	}
	expires, err := c.resolveTTL(ctx, ttl)
	if err != nil {
		return "", err
	}
	m, err := c.model(ctx)
	if err != nil {
		return "", err
	}
	if _, ok := m.ThreadByID(threadID); !ok {
		return "", validationErr("thread %q not found in this store", threadID)
	}
	for _, r := range refs {
		_, isPost := m.Post(r)
		_, isThread := m.ThreadByID(r)
		if !isPost && !isThread {
			return "", validationErr("ref %q does not resolve in this store", r)
		}
	}
	postID := newID("post")
	_, err = c.Store.Append(ctx, c.envelope(event.KindPostCreated, newID("cmd")+"/post",
		event.PostCreated{PostID: postID, ThreadID: threadID, Body: body, Label: lbl,
			ExpiresAt: expires, Refs: refs}))
	if err != nil {
		return "", err
	}
	return postID, nil
}
