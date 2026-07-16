// ABOUTME: Declared identity for GOssip: read from environment, never authenticated.
// ABOUTME: v1 records provenance; it does not prove it. Docs and UI must say "declared".
package gossip

import "fmt"

type Identity struct {
	ActorID     string
	PrincipalID string
	Source      string
}

// ResolveIdentity reads the declared identity. getenv is injected so tests
// stay hermetic (pass os.Getenv in production).
func ResolveIdentity(getenv func(string) string) (Identity, error) {
	actor := getenv("GOSSIP_ACTOR_ID")
	principal := getenv("GOSSIP_PRINCIPAL_ID")
	if actor == "" {
		return Identity{}, fmt.Errorf("identity: GOSSIP_ACTOR_ID is not set")
	}
	if principal == "" {
		return Identity{}, fmt.Errorf("identity: GOSSIP_PRINCIPAL_ID is not set")
	}
	return Identity{ActorID: actor, PrincipalID: principal, Source: "env"}, nil
}
