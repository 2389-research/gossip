// ABOUTME: The GOssip event envelope and the closed registry of six event kinds.
// ABOUTME: Envelope owns declared identity and time; payloads carry domain data only.
package event

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	KindThreadCreated    = "gossip.thread.created"
	KindPostCreated      = "gossip.post.created"
	KindReceiptAttached  = "gossip.receipt.attached"
	KindPostCorroborated = "gossip.post.corroborated"
	KindPostRetracted    = "gossip.post.retracted"
	KindPostHidden       = "gossip.post.hidden"
)

// knownKinds is the closed set of appendable event types. Fail closed on anything else.
var knownKinds = map[string]bool{
	KindThreadCreated: true, KindPostCreated: true, KindReceiptAttached: true,
	KindPostCorroborated: true, KindPostRetracted: true, KindPostHidden: true,
}

// KnownKind reports whether kind is in the closed set of appendable event types.
func KnownKind(kind string) bool { return knownKinds[kind] }

// Envelope carries declared provenance for every event. Identity is declared
// via environment, not authenticated; views must speak accordingly.
type Envelope struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	SchemaVersion  int             `json:"schema_version"`
	ActorID        string          `json:"actor_id"`
	PrincipalID    string          `json:"principal_id"`
	OccurredAt     time.Time       `json:"occurred_at"`
	IdempotencyKey string          `json:"idempotency_key"`
	Payload        json.RawMessage `json:"payload"`
}

func (e Envelope) Validate() error {
	switch {
	case e.ID == "":
		return fmt.Errorf("envelope: missing id")
	case !KnownKind(e.Type):
		return fmt.Errorf("envelope: unknown type %q", e.Type)
	case e.SchemaVersion < 1:
		return fmt.Errorf("envelope: schema_version must be >= 1")
	case e.ActorID == "":
		return fmt.Errorf("envelope: missing actor_id")
	case e.PrincipalID == "":
		return fmt.Errorf("envelope: missing principal_id")
	case e.OccurredAt.IsZero():
		return fmt.Errorf("envelope: missing occurred_at")
	case e.IdempotencyKey == "":
		return fmt.Errorf("envelope: missing idempotency_key")
	case len(e.Payload) == 0 || !json.Valid(e.Payload):
		return fmt.Errorf("envelope: payload must be valid non-empty JSON")
	}
	return nil
}

type ThreadCreated struct {
	ThreadID string `json:"thread_id"`
	Title    string `json:"title"`
}

type PostCreated struct {
	PostID    string    `json:"post_id"`
	ThreadID  string    `json:"thread_id"`
	Body      string    `json:"body"`
	Label     string    `json:"label"`
	ExpiresAt time.Time `json:"expires_at"`
	Refs      []string  `json:"refs,omitempty"`
}

type ReceiptAttached struct {
	PostID     string `json:"post_id"`
	ReceiptRef string `json:"receipt_ref"`
}

type PostCorroborated struct {
	PostID string `json:"post_id"`
}

type PostRetracted struct {
	PostID string `json:"post_id"`
	Reason string `json:"reason"`
}

type PostHidden struct {
	PostID string `json:"post_id"`
	Reason string `json:"reason"`
}

// MustMarshal is for payloads we define ourselves; a marshal failure is a bug.
func MustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("event: marshal payload: %v", err))
	}
	return b
}
