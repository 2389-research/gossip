// ABOUTME: CLI-level tests: run cobra commands against a real temp store.
// ABOUTME: Covers identity plumbing, --seen enforcement, and declared-language rendering.
package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389/gossip/internal/gossip"
	"github.com/2389/gossip/internal/store"
)

func runCLI(t *testing.T, env map[string]string, now time.Time, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRootCmd(func(k string) string { return env[k] }, func() time.Time { return now }, &out)
	root.SetArgs(args)
	root.SetErr(&out)
	err := root.Execute()
	return out.String(), err
}

func TestCLILifecycle(t *testing.T) {
	db := filepath.Join(t.TempDir(), "gossip.db")
	now := time.Date(2026, 7, 16, 3, 0, 0, 0, time.UTC)
	envA := map[string]string{"GOSSIP_ACTOR_ID": "a1", "GOSSIP_PRINCIPAL_ID": "p1", "GOSSIP_DB": db}
	envB := map[string]string{"GOSSIP_ACTOR_ID": "a2", "GOSSIP_PRINCIPAL_ID": "p2", "GOSSIP_DB": db}

	out, err := runCLI(t, envA, now, "start", "cursed deploys", "the script is cursed")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	thrID := extractID(t, out, "thr_")
	postID := extractID(t, out, "post_")

	if out, err = runCLI(t, envB, now, "corroborate", postID); err == nil {
		t.Fatalf("corroborate without --seen accepted:\n%s", out)
	}
	if out, err = runCLI(t, envB, now, "corroborate", postID, "--seen"); err != nil {
		t.Fatalf("corroborate --seen: %v\n%s", err, out)
	}

	out, err = runCLI(t, envA, now, "read", thrID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(out, "different declared principal") {
		t.Fatalf("badge language must say 'different declared principal':\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), "independent") {
		t.Fatalf("'independent' is forbidden in output:\n%s", out)
	}
}

func extractID(t *testing.T, s, prefix string) string {
	t.Helper()
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, prefix) {
			return strings.TrimRight(f, ".,:\n")
		}
	}
	t.Fatalf("no %q id in output:\n%s", prefix, s)
	return ""
}

func TestCLIWhoamiDeclaredLanguage(t *testing.T) {
	db := filepath.Join(t.TempDir(), "gossip.db")
	env := map[string]string{"GOSSIP_ACTOR_ID": "a1", "GOSSIP_PRINCIPAL_ID": "p1", "GOSSIP_DB": db}
	out, err := runCLI(t, env, time.Now(), "whoami")
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	for _, want := range []string{"a1", "p1", "declared", "env", db, "advisory"} {
		if !strings.Contains(out, want) {
			t.Fatalf("whoami missing %q:\n%s", want, out)
		}
	}
}

func TestCLIMutationsRequireIdentity(t *testing.T) {
	db := filepath.Join(t.TempDir(), "gossip.db")
	env := map[string]string{"GOSSIP_DB": db} // no identity declared
	if out, err := runCLI(t, env, time.Now(), "start", "t", "b"); err == nil {
		t.Fatalf("mutation without declared identity accepted:\n%s", out)
	}
}

func TestCLIInitConfiguresModerators(t *testing.T) {
	db := filepath.Join(t.TempDir(), "gossip.db")
	now := time.Date(2026, 7, 16, 3, 0, 0, 0, time.UTC)
	envMod := map[string]string{"GOSSIP_ACTOR_ID": "a9", "GOSSIP_PRINCIPAL_ID": "p_mod", "GOSSIP_DB": db}
	if out, err := runCLI(t, envMod, now, "init", "--moderator", "p_mod", "--default-ttl", "24h", "--max-ttl", "96h"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	out, err := runCLI(t, envMod, now, "start", "t", "b", "--ttl", "999h")
	if err == nil {
		t.Fatalf("ttl over configured max accepted:\n%s", out)
	}
	out, _ = runCLI(t, envMod, now, "whoami")
	if !strings.Contains(out, "moderator: yes") {
		t.Fatalf("whoami must show moderator status:\n%s", out)
	}
}

// TestHiddenTombstoneRendersIdenticallyAfterLateEvidence is the Addition test
// from the design room's PIN (b) ruling. It asserts that a tombstone line is
// stable: late evidence (corroborate + receipt from another actor) appended
// after a hide MUST NOT change the rendered output of read <thread>.
func TestHiddenTombstoneRendersIdenticallyAfterLateEvidence(t *testing.T) {
	db := filepath.Join(t.TempDir(), "gossip.db")
	now := time.Date(2026, 7, 16, 3, 0, 0, 0, time.UTC)

	// Set up: envMod is both author and moderator.
	envMod := map[string]string{"GOSSIP_ACTOR_ID": "actor_mod", "GOSSIP_PRINCIPAL_ID": "p_mod", "GOSSIP_DB": db}
	envOther := map[string]string{"GOSSIP_ACTOR_ID": "actor_other", "GOSSIP_PRINCIPAL_ID": "p_other", "GOSSIP_DB": db}

	// Configure the store with p_mod as moderator.
	if out, err := runCLI(t, envMod, now, "init", "--moderator", "p_mod"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}

	// Start a thread (mod is the author).
	out, err := runCLI(t, envMod, now, "start", "cursed subject", "cursed body")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	thrID := extractID(t, out, "thr_")
	postID := extractID(t, out, "post_")

	// Moderator hides the post.
	if out, err = runCLI(t, envMod, now, "hide", postID, "--reason", "off-topic"); err != nil {
		t.Fatalf("hide: %v\n%s", err, out)
	}

	// First render: capture bytes.
	out1, err := runCLI(t, envMod, now, "read", thrID)
	if err != nil {
		t.Fatalf("read (before late evidence): %v", err)
	}

	// Append late evidence: corroborate + receipt from another actor.
	// Use direct Cmd calls to avoid going through the moderator path for corroborate.
	s, err := store.Open(db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	otherID := gossip.Identity{ActorID: "actor_other", PrincipalID: "p_other", Source: "env"}
	cmd := &gossip.Cmd{Store: s, ID: otherID, Now: now}
	if err := cmd.Corroborate(context.Background(), postID); err != nil {
		t.Fatalf("corroborate (late evidence): %v", err)
	}
	if err := cmd.Receipt(context.Background(), postID, "https://example.com/evidence/42"); err != nil {
		t.Fatalf("receipt (late evidence): %v", err)
	}
	s.Close()

	// Second render with the same injected now.
	out2, err := runCLI(t, envMod, now, "read", thrID)
	if err != nil {
		t.Fatalf("read (after late evidence): %v", err)
	}

	// The two rendered outputs must be BYTE-IDENTICAL.
	if out1 != out2 {
		t.Fatalf("tombstone changed after late evidence:\nbefore:\n%s\nafter:\n%s", out1, out2)
	}

	// Confirm the tombstone is actually present and uses correct language.
	if !strings.Contains(out1, "[hidden:") {
		t.Fatalf("expected tombstone line [hidden: ...] in output:\n%s", out1)
	}

	// Confirm no forbidden language.
	if strings.Contains(strings.ToLower(out1), "independent") {
		t.Fatalf("'independent' is forbidden in tombstone output:\n%s", out1)
	}

	// Verify late evidence is in the audit log but NOT in the read view.
	logOut, err := runCLI(t, envMod, now, "log")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(logOut, "gossip.post.corroborated") {
		t.Fatalf("audit log must contain corroboration:\n%s", logOut)
	}
	// Also confirm "p_other" appears in the corroboration event to verify correct actor.
	if !strings.Contains(logOut, "actor_other") {
		t.Fatalf("audit log must show actor_other in corroboration:\n%s", logOut)
	}
	_ = envOther // used above via direct Cmd
}
