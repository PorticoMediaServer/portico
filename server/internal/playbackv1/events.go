package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"sync"
	"time"
)

// JSONValue is an opaque JSON value on the wire (an event's data). It is
// documented as "any value" and never serialises as null.
type JSONValue json.RawMessage

func (v JSONValue) MarshalJSON() ([]byte, error) {
	if len(v) == 0 {
		return []byte("{}"), nil
	}
	return v, nil
}
func (v *JSONValue) UnmarshalJSON(b []byte) error { *v = append((*v)[:0], b...); return nil }
func (JSONValue) JSONSchema() map[string]any      { return map[string]any{} }

// EventResource names what an event is about.
type EventResource struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Event is one entry of GET /v1/events (spec §15, §17.11).
type Event struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	At       string         `json:"at"`
	Resource *EventResource `json:"resource,omitempty"`
	Revision string         `json:"revision,omitempty"`
	Data     JSONValue      `json:"data,omitempty"`
}

// EventRing is how many events the feed keeps for resuming cursors (the shared
// ring, apievents.RingSize). A cursor older than the ring gets one
// stream.resync (spec §17.11).
const EventRing = apievents.RingSize

// Audiences an event can be addressed to.
func DeviceAudience(device string) string { return "device:" + device }
func ProfileAudience(authority, account, profile string) string {
	return "profile:" + authority + ":" + account + ":" + profile
}
func GroupAudience(group string) string { return "group:" + group }

const AdminAudience = "admin"

// eventHub wakes parked readers when events commit. Writers call wake after
// their transaction commits; nothing polls.
type eventHub struct {
	mu      sync.Mutex
	waiters map[chan struct{}]struct{}
}

func (h *eventHub) wake() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// publishTx appends one event inside the caller's transaction through the
// single api_events writer (lane C's apievents.Append, which also trims the
// ring). The caller wakes readers after commit.
func publishTx(_ context.Context, tx *sql.Tx, _ time.Time, audience, kind string, resource *EventResource, revision string, data any) error {
	resourceKind, resourceID := "", ""
	if resource != nil {
		resourceKind, resourceID = resource.Kind, resource.ID
	}
	return apievents.Append(tx, audience, kind, resourceKind, resourceID, revision, data)
}
