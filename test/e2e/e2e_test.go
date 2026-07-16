// ABOUTME: End-to-end test: builds the real gossip binary and drives a full lifecycle.
// ABOUTME: Real binary, real SQLite file, real env vars — no mocks of any kind.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gossip")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/2389/gossip/cmd/gossip")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(wd)) // test/e2e -> repo root
}

type cli struct {
	t   *testing.T
	bin string
	env []string
}

func (c cli) run(args ...string) (string, error) {
	cmd := exec.Command(c.bin, args...)
	cmd.Env = c.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (c cli) mustRun(args ...string) string {
	c.t.Helper()
	out, err := c.run(args...)
	if err != nil {
		c.t.Fatalf("gossip %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func idWithPrefix(t *testing.T, s, prefix string) string {
	t.Helper()
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, prefix) {
			return strings.TrimRight(f, ".,:\n")
		}
	}
	t.Fatalf("no %q id in:\n%s", prefix, s)
	return ""
}

func TestDBFlagBeatsEnv(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	flagDB := filepath.Join(dir, "flag.db")
	envDB := filepath.Join(dir, "env.db")

	base := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"GOSSIP_DB=" + envDB,
		"GOSSIP_ACTOR_ID=mona",
		"GOSSIP_PRINCIPAL_ID=team_mod",
	}
	c := cli{t, bin, base}

	// Run init with --db pointing at flagDB; GOSSIP_DB points at envDB.
	// The flag must win: flagDB gets created, envDB must not exist.
	out := c.mustRun("--db", flagDB, "init", "--moderator", "team_mod")

	if !strings.Contains(out, flagDB) {
		t.Errorf("init output does not mention flagDB path; got:\n%s", out)
	}
	if _, err := os.Stat(flagDB); err != nil {
		t.Errorf("flagDB was not created: %v", err)
	}
	if _, err := os.Stat(envDB); !os.IsNotExist(err) {
		t.Errorf("envDB should not exist but stat returned: %v", err)
	}
}

func TestFullLifecycle(t *testing.T) {
	bin := buildBinary(t)
	db := filepath.Join(t.TempDir(), "watercooler.db")
	base := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GOSSIP_DB=" + db}
	alice := cli{t, bin, append([]string{"GOSSIP_ACTOR_ID=alice", "GOSSIP_PRINCIPAL_ID=team_a"}, base...)}
	bob := cli{t, bin, append([]string{"GOSSIP_ACTOR_ID=bob", "GOSSIP_PRINCIPAL_ID=team_b"}, base...)}
	mod := cli{t, bin, append([]string{"GOSSIP_ACTOR_ID=mona", "GOSSIP_PRINCIPAL_ID=team_mod"}, base...)}

	mod.mustRun("init", "--moderator", "team_mod")

	out := alice.mustRun("start", "the deploy script is cursed", "three failures this week, all full moons", "--ttl", "7d")
	thr := idWithPrefix(t, out, "thr_")
	op := idWithPrefix(t, out, "post_")

	// Self-corroboration must fail.
	if out, err := alice.run("corroborate", op, "--seen"); err == nil {
		t.Fatalf("self-corroboration accepted:\n%s", out)
	}
	bob.mustRun("corroborate", op, "--seen")
	bob.mustRun("receipt", op, "ci/logs/run-4471")

	out = bob.mustRun("post", thr, "confirmed, saw it fail at midnight", "--label", "observed", "--ref", op)
	reply := idWithPrefix(t, out, "post_")

	// Moderator hides bob's reply; alice retracts her OP.
	mod.mustRun("hide", reply, "--reason", "contains an internal hostname")
	alice.mustRun("retract", op, "--reason", "it was DNS all along")

	read := alice.mustRun("read", thr)
	for _, want := range []string{"RETRACTED", "it was DNS all along", "[hidden: contains an internal hostname]",
		"receipts: 1", "different declared principal"} {
		if !strings.Contains(read, want) {
			t.Fatalf("read missing %q:\n%s", want, read)
		}
	}
	if strings.Contains(read, "saw it fail at midnight") {
		t.Fatalf("hidden body leaked into ordinary view:\n%s", read)
	}
	if strings.Contains(strings.ToLower(read), "independent") || strings.Contains(strings.ToLower(read), "verified") {
		t.Fatalf("forbidden vocabulary in output:\n%s", read)
	}

	// The audit log retains the hidden body (file access is the gate).
	logOut := alice.mustRun("log")
	if !strings.Contains(logOut, "saw it fail at midnight") {
		t.Fatalf("audit log lost the hidden body:\n%s", logOut)
	}

	// Threads list shows the thread with only the OP visible (reply hidden).
	threads := alice.mustRun("threads")
	if !strings.Contains(threads, thr) || !strings.Contains(threads, "1 post(s)") {
		t.Fatalf("threads summary wrong:\n%s", threads)
	}
}
