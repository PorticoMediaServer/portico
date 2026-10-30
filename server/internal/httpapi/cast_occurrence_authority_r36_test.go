package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func castOccurrenceFixture(t *testing.T) (*socialFixture, string, identity.Principal, playbackv1.QueueReply, playbackv1.SessionView) {
	t.Helper()
	base := newTL6V1Fixture(t, 3)
	f := &socialFixture{tl6V1Fixture: base, d: base.deps, h: base.handler, token: base.owner.AccessToken, item: base.items[0], profile: base.owner.Viewer.ProfileID}
	target := pairCastTarget(t, f, f.token, "cast-current-resource")
	bearer := target.Session.AccessToken
	var queue playbackv1.QueueReply
	f.callAs(f.token, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "cast-queue-owner-create"}, map[string]any{"owner": "device", "segments": []any{map[string]any{"source": queueItems(base.items[0], base.items[1])}}}, 201, &queue)
	for _, path := range []string{"/v1/items/" + base.items[0], "/v1/queues/" + queue.Queue.ID} {
		if w := f.as(bearer, "GET", path, nil); w.Code != 403 {
			t.Fatalf("before adoption %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	f.callAs(bearer, "PUT", "/v1/me/devices/current/capabilities", nil, tl6WebCapabilities(), 204, nil)
	var session playbackv1.SessionView
	f.callAs(bearer, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "cast-target-adopt-occurrence"}, map[string]any{"queue": map[string]string{"queueId": queue.Queue.ID, "entryId": queue.Queue.Current.EntryID}, "state": "playing"}, 201, &session)
	p, err := f.id.AuthenticateContext(identity.WithPlaybackRequest(context.Background()), bearer)
	if err != nil {
		t.Fatal(err)
	}
	return f, bearer, p, queue, session
}

func TestCastOccurrenceAllowsOnlyAdoptedItemAndQueueCompletion(t *testing.T) {
	f, bearer, _, queue, session := castOccurrenceFixture(t)
	for _, path := range []string{"/v1/items/" + session.ItemID, "/v1/queues/" + queue.Queue.ID} {
		if w := f.as(bearer, "GET", path, nil); w.Code != 200 {
			t.Fatalf("adopted %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	var other playbackv1.QueueReply
	otherDevice := f.device("another-owner-phone")
	f.callAs(otherDevice.AccessToken, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "another-owner-queue"}, map[string]any{"owner": "device", "segments": []any{map[string]any{"source": queueItems(f.items[2])}}}, 201, &other)
	// Creating another queue on the owner's device must not grant that queue to
	// the receiver, even though its ordinary owner/profile scope is the same.
	for _, path := range []string{"/v1/items/" + f.items[2], "/v1/items", "/v1/queues/" + other.Queue.ID, "/v1/queues/" + queue.Queue.ID + "/entries", "/v1/queues/" + queue.Queue.ID + "/window"} {
		if w := f.as(bearer, "GET", path, nil); w.Code != 403 {
			t.Fatalf("unadopted %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	var head playbackv1.QueueView
	f.callAs(bearer, "GET", "/v1/queues/"+queue.Queue.ID, nil, nil, 200, &head)
	for _, reason := range []string{"next", "previous", "entry"} {
		w := f.raw("POST", "/v1/queues/"+queue.Queue.ID+":advance", bearer, map[string]string{"If-Match": head.Revision}, map[string]string{"reason": reason})
		if w.Code != 403 {
			t.Fatalf("Cast %s mutation: %d %s", reason, w.Code, w.Body.String())
		}
	}
	// The receiver reports the genuine end before asking for automatic next.
	// Metadata ends with that occurrence; its adopted queue can still complete
	// while the original lease remains current.
	h := newHTTPObserver(t, f.db, func(method, path string, body any) *httptest.ResponseRecorder {
		return f.raw(method, path, bearer, nil, body)
	})
	h.report(session.ID, session.Presentation.Generation, map[string]any{"state": "ended", "positionMs": 600_000})
	if w := f.as(bearer, "GET", "/v1/items/"+session.ItemID, nil); w.Code != 403 {
		t.Fatal("ended item metadata remained readable", w.Code)
	}
	f.callAs(bearer, "GET", "/v1/queues/"+queue.Queue.ID, nil, nil, 200, &head)
	var next playbackv1.QueueReply
	f.callAs(bearer, "POST", "/v1/queues/"+queue.Queue.ID+":advance", map[string]string{"If-Match": head.Revision, "Idempotency-Key": "cast-complete-current"}, map[string]any{"reason": "completion", "completionSessionId": session.ID, "completionGeneration": session.Presentation.Generation}, 200, &next)
	if next.Session == nil || next.Session.ID == session.ID || next.Session.ItemID != f.items[1] {
		t.Fatal("completion did not adopt real next occurrence")
	}
	if w := f.as(bearer, "GET", "/v1/items/"+session.ItemID, nil); w.Code != 403 {
		t.Fatal("replaced occurrence still readable", w.Code, w.Body.String())
	}
	if w := f.as(bearer, "GET", "/v1/items/"+next.Session.ItemID, nil); w.Code != 200 {
		t.Fatal("new occurrence metadata refused", w.Code, w.Body.String())
	}
	f.callAs(bearer, "DELETE", "/v1/playback/sessions/"+next.Session.ID, nil, nil, 204, nil)
	for _, path := range []string{"/v1/items/" + next.Session.ItemID, "/v1/queues/" + queue.Queue.ID} {
		if w := f.as(bearer, "GET", path, nil); w.Code != 403 {
			t.Fatal("stopped occurrence remained accessible", path, w.Code)
		}
	}
}

func TestCastOccurrenceLiveTransactionFences(t *testing.T) {
	f, _, p, queue, session := castOccurrenceFixture(t)
	tests := []struct {
		name, mutation string
		args           []any
	}{
		{"expired lease", `UPDATE playback_v1_sessions SET lease_expires_ms=0 WHERE id=?`, []any{session.ID}},
		{"stopped", `UPDATE playback_v1_sessions SET ended_ms=1,end_reason='stopped' WHERE id=?`, []any{session.ID}},
		{"replaced", `UPDATE playback_v1_sessions SET ended_ms=1,end_reason='replaced' WHERE id=?`, []any{session.ID}},
		{"foreign profile", `UPDATE playback_v1_sessions SET profile_id='other' WHERE id=?`, []any{session.ID}},
		{"foreign device", `UPDATE playback_v1_sessions SET device_id='other' WHERE id=?`, []any{session.ID}},
		{"foreign queue profile", `UPDATE queues_v1 SET profile_id='other' WHERE id=?`, []any{queue.Queue.ID}},
		{"foreign queue session", `UPDATE queues_v1 SET session_id='other' WHERE id=?`, []any{queue.Queue.ID}},
		{"retired queue", `UPDATE queues_v1 SET owner_id='!'||owner_id WHERE id=?`, []any{queue.Queue.ID}},
		{"revoked pairing", `UPDATE social_cast_devices SET state='revoked'`, nil},
		{"replaced pairing family", `UPDATE social_cast_devices SET session_family_id='other'`, nil},
		{"expired pairing", `UPDATE social_cast_devices SET expires_at_ms=0`, nil},
		{"revoked family", `UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, []any{p.Hash}},
		{"expired token", `UPDATE authorization_family_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE token_hash=?`, []any{p.Hash}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := f.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err = f.id.CastPlaybackResourceTx(context.Background(), tx, p, "", queue.Queue.ID); err != nil {
				t.Fatal("initial current queue", err)
			}
			if _, err = tx.Exec(tt.mutation, tt.args...); err != nil {
				t.Fatal(err)
			}
			if _, err = f.id.CastPlaybackResourceTx(context.Background(), tx, p, "", queue.Queue.ID); err == nil {
				t.Fatal("changed current queue authority admitted")
			}
		})
	}
	// Same-transaction item mutation never grants the old item's metadata.
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE playback_v1_sessions SET item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) WHERE id=?`, f.items[2], session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.id.CastPlaybackResourceTx(context.Background(), tx, p, session.ItemID, ""); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("old item remained current", err)
	}
}

func TestCastCompletionRechecksCurrentFamilyInQueueWriter(t *testing.T) {
	f, bearer, p, queue, session := castOccurrenceFixture(t)
	previous := f.v1.CastQueueAuthority
	called := false
	f.v1.CastQueueAuthority = func(ctx context.Context, tx *sql.Tx, c playbackv1.Caller, id string) error {
		called = true
		if _, err := tx.ExecContext(ctx, `UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, p.Hash); err != nil {
			return err
		}
		return previous(ctx, tx, c, id)
	}
	w := f.raw("POST", "/v1/queues/"+queue.Queue.ID+":advance", bearer, map[string]string{"If-Match": queue.Queue.Revision}, map[string]any{"reason": "completion", "completionSessionId": session.ID, "completionGeneration": session.Presentation.Generation})
	if !called || w.Code < 400 || w.Code >= 500 {
		t.Fatalf("late family revocation not refused: called=%v %d %s", called, w.Code, w.Body.String())
	}
	var position, revoked int
	if err := f.db.QueryRow(`SELECT current_position FROM queues_v1 WHERE id=?`, queue.Queue.ID).Scan(&position); err != nil || position != 0 {
		t.Fatal("refused completion advanced queue", err)
	}
	if err := f.db.QueryRow(`SELECT revoked FROM authorization_session_families WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, p.Hash).Scan(&revoked); err != nil || revoked != 0 {
		t.Fatal("queue transaction did not roll back", err)
	}
	// A principal cached on an ordinary allowed receiver read cannot conceal
	// family retirement at either current-resource edge.
	f.callAs(bearer, "GET", "/v1/me", nil, nil, 200, nil)
	if _, err := f.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, p.Hash); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/queues/" + queue.Queue.ID, "/v1/items/" + f.items[0]} {
		if w := f.as(bearer, "GET", path, nil); w.Code < 400 || w.Code >= 500 {
			t.Fatal("cached family retained current resource", path, w.Code, w.Body.String())
		}
	}
}
