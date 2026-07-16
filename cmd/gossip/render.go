// ABOUTME: Text rendering for GOssip views: threads, posts, badges, whoami, audit log.
// ABOUTME: Language rule: identity is "declared"; the word "independent" is forbidden here.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/2389/gossip/internal/gossip"
	"github.com/2389/gossip/internal/store"
)

func foldStore(ctx context.Context, s *store.Store) (*gossip.Model, error) {
	evs, err := s.Events(ctx)
	if err != nil {
		return nil, err
	}
	return gossip.Fold(evs)
}

func renderThreads(w io.Writer, sums []gossip.ThreadSummary) {
	if len(sums) == 0 {
		fmt.Fprintln(w, "no threads yet — start one: gossip start <title> <body>")
		return
	}
	for _, s := range sums {
		fmt.Fprintf(w, "%s  %q  %d post(s)  last activity %s\n",
			s.ID, s.Title, s.Visible, s.LastActivity.Format("2006-01-02 15:04"))
	}
}

func badgeLine(p *gossip.Post) string {
	b := p.Badges()
	var parts []string
	if b.Receipts > 0 {
		parts = append(parts, fmt.Sprintf("receipts: %d", b.Receipts))
	}
	var corr []string
	if b.SamePrincipal > 0 {
		corr = append(corr, fmt.Sprintf("%d same declared principal", b.SamePrincipal))
	}
	if b.DifferentPrincipal > 0 {
		corr = append(corr, fmt.Sprintf("%d different declared principal", b.DifferentPrincipal))
	}
	if len(corr) > 0 {
		parts = append(parts, "corroborated: "+strings.Join(corr, ", "))
	}
	return strings.Join(parts, " · ")
}

func renderThread(w io.Writer, tv *gossip.ThreadView) {
	fmt.Fprintf(w, "%s  %q\n", tv.Thread.ID, tv.Thread.Title)
	for _, pv := range tv.Posts {
		p := pv.Post
		if pv.Tombstone {
			fmt.Fprintf(w, "  %s  [hidden: %s]\n", p.ID, p.Hidden.Reason)
			continue
		}
		head := fmt.Sprintf("  %s  [%s] by %s/%s (declared)  expires %s",
			p.ID, p.Label, p.AuthorActor, p.AuthorPrincipal, p.ExpiresAt.Format("2006-01-02 15:04"))
		if p.Retracted != nil {
			head += fmt.Sprintf("  [RETRACTED by author: %s]", p.Retracted.Reason)
		}
		fmt.Fprintln(w, head)
		fmt.Fprintf(w, "      %s\n", p.Body)
		if len(p.Refs) > 0 {
			fmt.Fprintf(w, "      refs: %s\n", strings.Join(p.Refs, ", "))
		}
		if bl := badgeLine(p); bl != "" {
			fmt.Fprintf(w, "      %s\n", bl)
		}
	}
	if tv.Decayed > 0 {
		fmt.Fprintf(w, "  %d post(s) decayed from view (expired; audit log retains them)\n", tv.Decayed)
	}
}

func renderWhoami(w io.Writer, getenv func(string) string, path string, cfg store.Config) {
	id, err := gossip.ResolveIdentity(getenv)
	if err != nil {
		fmt.Fprintf(w, "identity: not declared (%v)\n", err)
		fmt.Fprintf(w, "store:    %s\n", path)
		return
	}
	isMod := "no"
	for _, m := range cfg.Moderators {
		if m == id.PrincipalID {
			isMod = "yes"
		}
	}
	fmt.Fprintf(w, "actor:     %s (declared, source: %s)\n", id.ActorID, id.Source)
	fmt.Fprintf(w, "principal: %s (declared, source: %s)\n", id.PrincipalID, id.Source)
	fmt.Fprintf(w, "store:     %s\n", path)
	fmt.Fprintf(w, "moderator: %s (advisory — declared principal vs store moderator list)\n", isMod)
}

func renderLog(w io.Writer, ctx context.Context, s *store.Store) error {
	evs, err := s.Events(ctx)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	for _, e := range evs {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}
