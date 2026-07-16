// ABOUTME: Tests for the GOssip event envelope: kind registry and fail-closed validation.
// ABOUTME: The envelope owns identity and time; payloads carry domain data only.
package event

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validEnvelope() Envelope {
	return Envelope{
		ID: "evt_01TEST", Type: KindThreadCreated, SchemaVersion: 1,
		ActorID: "agent_three", PrincipalID: "operator",
		OccurredAt:     time.Date(2026, 7, 16, 3, 0, 0, 0, time.UTC),
		IdempotencyKey: "cmd_01TEST/thread",
		Payload:        MustMarshal(ThreadCreated{ThreadID: "thr_01TEST", Title: "t"}),
	}
}

func TestValidateAcceptsCompleteEnvelope(t *testing.T) {
	if err := validEnvelope().Validate(); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
}

func TestValidateFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Envelope)
		want   string
	}{
		{"missing id", func(e *Envelope) { e.ID = "" }, "id"},
		{"unknown type", func(e *Envelope) { e.Type = "gossip.post.verified" }, "type"},
		{"zero schema", func(e *Envelope) { e.SchemaVersion = 0 }, "schema_version"},
		{"missing actor", func(e *Envelope) { e.ActorID = "" }, "actor_id"},
		{"missing principal", func(e *Envelope) { e.PrincipalID = "" }, "principal_id"},
		{"zero time", func(e *Envelope) { e.OccurredAt = time.Time{} }, "occurred_at"},
		{"missing idempotency", func(e *Envelope) { e.IdempotencyKey = "" }, "idempotency_key"},
		{"empty payload", func(e *Envelope) { e.Payload = nil }, "payload"},
		{"invalid json payload", func(e *Envelope) { e.Payload = json.RawMessage("{nope") }, "payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := validEnvelope()
			tc.mutate(&e)
			err := e.Validate()
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestAllSixKindsRegistered(t *testing.T) {
	want := []string{
		"gossip.thread.created", "gossip.post.created", "gossip.receipt.attached",
		"gossip.post.corroborated", "gossip.post.retracted", "gossip.post.hidden",
	}
	if len(Kinds) != len(want) {
		t.Fatalf("Kinds has %d entries, want %d", len(Kinds), len(want))
	}
	for _, k := range want {
		if !Kinds[k] {
			t.Fatalf("kind %q not registered", k)
		}
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	exp := time.Date(2026, 7, 23, 3, 0, 0, 0, time.UTC)
	raw := MustMarshal(PostCreated{PostID: "post_1", ThreadID: "thr_1", Body: "b",
		Label: "rumor", ExpiresAt: exp, Refs: []string{"post_0"}})
	var got PostCreated
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.PostID != "post_1" || got.ThreadID != "thr_1" || !got.ExpiresAt.Equal(exp) || len(got.Refs) != 1 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}
