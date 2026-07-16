// ABOUTME: Tests for declared identity resolution from environment variables.
// ABOUTME: Identity is declared, not authenticated; missing declarations fail closed.
package gossip

import (
	"strings"
	"testing"
)

func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveIdentityFromEnv(t *testing.T) {
	id, err := ResolveIdentity(fakeEnv(map[string]string{
		"GOSSIP_ACTOR_ID": "agent_three", "GOSSIP_PRINCIPAL_ID": "operator",
	}))
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.ActorID != "agent_three" || id.PrincipalID != "operator" || id.Source != "env" {
		t.Fatalf("identity = %+v", id)
	}
}

func TestResolveIdentityFailsClosedWhenMissing(t *testing.T) {
	cases := []map[string]string{
		{},
		{"GOSSIP_ACTOR_ID": "agent_three"},
		{"GOSSIP_PRINCIPAL_ID": "operator"},
	}
	for _, env := range cases {
		if _, err := ResolveIdentity(fakeEnv(env)); err == nil {
			t.Fatalf("env %v accepted without full declared identity", env)
		} else if !strings.Contains(err.Error(), "GOSSIP_") {
			t.Fatalf("error %q does not name the missing variable", err)
		}
	}
}
