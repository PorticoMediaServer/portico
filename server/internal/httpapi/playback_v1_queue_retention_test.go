package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func retainedQueueBody(ids []string, keep ...string) map[string]any {
	body := map[string]any{"segments": []any{map[string]any{"source": queueItems(ids...)}}}
	if keep != nil {
		body["retainQueueIds"] = keep
	}
	return body
}

func retainedQueueCreate(f *v1Fixture, key string, body map[string]any) playbackv1.QueueReply {
	f.t.Helper()
	var reply playbackv1.QueueReply
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": key}, body, 201, &reply)
	return reply
}

func TestPlaybackV1QueueRetentionPreservesOccurrencesAndFreshPausedRestore(t *testing.T) {
	f := newV1Fixture(t, 3)
	body := retainedQueueBody([]string{f.items[0], f.items[1], f.items[0]})
	body["segments"].([]any)[0].(map[string]any)["anchor"] = map[string]any{"position": 1}
	body["startPlayback"] = map[string]any{"state": "playing"}
	original := retainedQueueCreate(f, "retain-original-0001", body)
	base := "/v1/queues/" + original.Queue.ID
	var before playbackv1.EntryPage
	f.call("GET", base+"/entries?from=0&limit=10", nil, nil, 200, &before)
	otherBody := retainedQueueBody([]string{f.items[2]}, original.Queue.ID)
	otherBody["startPlayback"] = map[string]any{"state": "playing"}
	other := retainedQueueCreate(f, "retain-other-000001", otherBody)
	var header playbackv1.QueueView
	f.call("GET", base, nil, nil, 200, &header)
	if header.ID != original.Queue.ID || header.Revision != original.Queue.Revision || header.Total != 3 || !reflect.DeepEqual(header.Current, original.Queue.Current) || !reflect.DeepEqual(header.Segments, original.Queue.Segments) {
		t.Fatalf("retention changed the actual header: %+v", header)
	}
	var after playbackv1.EntryPage
	f.call("GET", base+"/entries?from=0&limit=10", nil, nil, 200, &after)
	if !reflect.DeepEqual(before, after) || before.Items[0].EntryID == before.Items[2].EntryID {
		t.Fatalf("retention changed occurrence identity/order: before %+v after %+v", before, after)
	}
	var linked string
	if err := f.db.QueryRow(`SELECT session_id FROM queues_v1 WHERE id=?`, original.Queue.ID).Scan(&linked); err != nil || linked != "" {
		t.Fatalf("parked queue retained a live session link %q: %v", linked, err)
	}
	for _, session := range []*playbackv1.SessionView{original.Session, other.Session} {
		if session == nil {
			t.Fatal("create did not start its real session")
		}
		f.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204, nil)
	}
	var restored playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "retain-restore-00001"}, map[string]any{
		"queue": map[string]any{"queueId": original.Queue.ID, "entryId": original.Queue.Current.EntryID}, "state": "paused", "startPositionMs": 37250,
	}, 201, &restored)
	if restored.ID == original.Session.ID || restored.ID == other.Session.ID || restored.State != "paused" || restored.Presentation.StartPositionMs != 37250 || restored.Queue == nil || restored.Queue.QueueID != original.Queue.ID || restored.Queue.EntryID != original.Queue.Current.EntryID {
		t.Fatalf("restoration reused or changed playback: %+v", restored)
	}
	var appended playbackv1.QueueReply
	f.call("POST", base+"/segments", map[string]string{"If-Match": header.Revision}, map[string]any{"placement": "end", "source": queueItems(f.items[2])}, 200, &appended)
	f.call("GET", base+"/entries?from=0&limit=10", nil, nil, 200, &after)
	if appended.Queue.ID != original.Queue.ID || len(after.Items) != 4 || !reflect.DeepEqual(after.Items[:3], before.Items) {
		t.Fatalf("append replaced the retained queue: %+v", after)
	}
	// A create replay returns its exact old receipt, even after the queue was edited.
	replay := retainedQueueCreate(f, "retain-original-0001", body)
	if !reflect.DeepEqual(replay, original) {
		t.Fatalf("replay changed the original receipt: %+v", replay)
	}
	f.call("GET", "/v1/queues/"+other.Queue.ID, nil, nil, 200, nil)
	// Omission still retires every old queue, including explicitly retained ones.
	latest := retainedQueueCreate(f, "retain-default-00001", retainedQueueBody(f.items[:1]))
	f.call("GET", base, nil, nil, 404, nil)
	f.call("GET", "/v1/queues/"+other.Queue.ID, nil, nil, 404, nil)
	late := retainedQueueCreate(f, "retain-other-000001", otherBody)
	if !reflect.DeepEqual(late, other) {
		t.Fatal("late retention replay lost its exact old receipt")
	}
	f.call("GET", "/v1/queues/"+latest.Queue.ID, nil, nil, 200, nil)
	f.call("GET", base, nil, nil, 404, nil)
}

func TestPlaybackV1QueueRetentionKeepsThreeAndDigestsTheSemanticSet(t *testing.T) {
	f := newV1Fixture(t, 1)
	var ids []string
	for i := 0; i < 3; i++ {
		reply := retainedQueueCreate(f, fmt.Sprintf("retain-bound-%06d", i), retainedQueueBody(f.items, ids...))
		ids = append(ids, reply.Queue.ID)
	}
	body := retainedQueueBody(f.items, ids...)
	first := retainedQueueCreate(f, "retain-three-000001", body)
	for _, id := range append(append([]string{}, ids...), first.Queue.ID) {
		f.call("GET", "/v1/queues/"+id, nil, nil, 200, nil)
	}
	reversed := retainedQueueBody(f.items, ids[2], ids[0], ids[1])
	if replay := retainedQueueCreate(f, "retain-three-000001", reversed); !reflect.DeepEqual(first, replay) {
		t.Fatal("reordered retention set did not replay the exact receipt")
	}
	for _, keep := range [][]string{nil, ids[:2], {ids[0], ids[1], first.Queue.ID}} {
		w := f.raw("POST", "/v1/queues", f.owner.AccessToken, map[string]string{"Idempotency-Key": "retain-three-000001"}, retainedQueueBody(f.items, keep...))
		if w.Code != 422 || v1Code(w) != "idempotency_key_reused" {
			t.Fatalf("different retention body: %d %s", w.Code, w.Body.String())
		}
	}
	// The next normalized set drops the superseded candidate, leaving at most four.
	retainedQueueCreate(f, "retain-normal-00001", retainedQueueBody(f.items, ids[1], ids[2], first.Queue.ID))
	f.call("GET", "/v1/queues/"+ids[0], nil, nil, 404, nil)
	for _, id := range []string{ids[1], ids[2], first.Queue.ID} {
		f.call("GET", "/v1/queues/"+id, nil, nil, 200, nil)
	}
}

func TestPlaybackV1QueueRetentionRejectsUnownedOrInvalidWithoutChangingCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	cases := []struct {
		name   string
		change string
		list   func(protected, candidate string) any
		status int
	}{
		{"four", "", func(p, c string) any { return []string{p, c, "missing-one", "missing-two"} }, 400},
		{"duplicate", "", func(p, c string) any { return []string{p, c, p} }, 400},
		{"empty-id", "", func(p, c string) any { return []string{p, ""} }, 400},
		{"malicious-id", "", func(p, c string) any { return []string{p, "../queue"} }, 400},
		{"newline", "", func(p, c string) any { return []string{p, c + "\n"} }, 400},
		{"long-id", "", func(p, c string) any { return []string{p, strings.Repeat("q", 129)} }, 400},
		{"not-array", "", func(p, c string) any { return c }, 400},
		{"missing", "", func(p, c string) any { return []string{p, "missing-queue"} }, 404},
		{"retired", "owner_id='!'||owner_id", nil, 404},
		{"staged", "owner_id='~'||owner_id", nil, 404},
		{"group", "owner_kind='group'", nil, 404},
		{"cross-device", "owner_id='another-device'", nil, 404},
		{"profile", "profile_id='another-profile'", nil, 404},
		{"account", "account_id='another-account'", nil, 404},
		{"authority", "authority='hosted'", nil, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newV1Fixture(t, 1)
			protected := retainedQueueCreate(f, "retain-protect-0001", retainedQueueBody(f.items))
			candidate := retainedQueueCreate(f, "retain-candidate-01", retainedQueueBody(f.items, protected.Queue.ID))
			body := retainedQueueBody(f.items, protected.Queue.ID, candidate.Queue.ID)
			body["startPlayback"] = map[string]any{"state": "playing"}
			current := retainedQueueCreate(f, "retain-current-001", body)
			if tc.change != "" {
				if _, err := f.db.Exec(`UPDATE queues_v1 SET `+tc.change+` WHERE id=?`, candidate.Queue.ID); err != nil {
					t.Fatal(err)
				}
			}
			invalid := retainedQueueBody(f.items, protected.Queue.ID, candidate.Queue.ID)
			invalid["startPlayback"] = map[string]any{"state": "playing"}
			if tc.list != nil {
				invalid["retainQueueIds"] = tc.list(protected.Queue.ID, candidate.Queue.ID)
			}
			f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "retain-rejected-001"}, invalid, tc.status, nil)
			for _, q := range []playbackv1.QueueReply{protected, current} {
				var header playbackv1.QueueView
				f.call("GET", "/v1/queues/"+q.Queue.ID, nil, nil, 200, &header)
				if header.ID != q.Queue.ID || header.Revision != q.Queue.Revision || !reflect.DeepEqual(header.Current, q.Queue.Current) {
					t.Fatalf("failed create changed a protected/current queue: %+v", header)
				}
			}
			var linked string
			if err := f.db.QueryRow(`SELECT session_id FROM queues_v1 WHERE id=?`, current.Queue.ID).Scan(&linked); err != nil || linked != current.Session.ID {
				t.Fatalf("failed create changed the current session link: %q %v", linked, err)
			}
			var receipts int
			if err := f.db.QueryRow(`SELECT count(*) FROM queue_v1_create_receipts WHERE create_key='retain-rejected-001'`).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("rejected create committed a receipt: %d %v", receipts, err)
			}
		})
	}
}

func TestPlaybackV1QueueRetentionRollsBackRetirementAndSessionDetach(t *testing.T) {
	f := newV1Fixture(t, 1)
	unlisted := retainedQueueCreate(f, "retain-rollback-000", retainedQueueBody(f.items))
	body := retainedQueueBody(f.items, unlisted.Queue.ID)
	body["startPlayback"] = map[string]any{"state": "playing"}
	current := retainedQueueCreate(f, "retain-rollback-001", body)
	if _, err := f.db.Exec(`CREATE TRIGGER reject_retained_segment BEFORE INSERT ON queue_v1_segments BEGIN SELECT RAISE(ABORT,'retention rollback'); END`); err != nil {
		t.Fatal(err)
	}
	failed := retainedQueueBody(f.items, current.Queue.ID)
	f.call("POST", "/v1/queues", map[string]string{"Idempotency-Key": "retain-rollback-002"}, failed, 500, nil)
	var linked string
	if err := f.db.QueryRow(`SELECT session_id FROM queues_v1 WHERE id=?`, current.Queue.ID).Scan(&linked); err != nil || linked != current.Session.ID {
		t.Fatalf("rolled-back create detached the session: %q %v", linked, err)
	}
	f.call("GET", "/v1/queues/"+current.Queue.ID, nil, nil, 200, nil)
	f.call("GET", "/v1/queues/"+unlisted.Queue.ID, nil, nil, 200, nil)
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM queue_v1_create_receipts WHERE create_key='retain-rollback-002'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed create kept its receipt: %d %v", count, err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER reject_retained_segment`); err != nil {
		t.Fatal(err)
	}
	retainedQueueCreate(f, "retain-rollback-002", failed)
	f.call("GET", "/v1/queues/"+unlisted.Queue.ID, nil, nil, 404, nil)
}

func TestPlaybackV1QueueRetentionReceiptsAreBoundToTheOriginalViewer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"omitted", "empty", "retained"} {
		t.Run(mode, func(t *testing.T) {
			f := newV1Fixture(t, 1)
			first := retainedQueueCreate(f, "retain-viewer-00001", retainedQueueBody(f.items))
			body := retainedQueueBody(f.items, first.Queue.ID)
			if mode == "omitted" {
				delete(body, "retainQueueIds")
			} else if mode == "empty" {
				body["retainQueueIds"] = []string{}
			}
			created := retainedQueueCreate(f, "retain-viewer-00002", body)
			var caller playbackv1.Caller
			if err := f.db.QueryRow(`SELECT owner_id,authority,account_id,profile_id FROM queues_v1 WHERE id=?`, created.Queue.ID).Scan(&caller.DeviceID, &caller.Principal.Authority, &caller.Principal.AccountID, &caller.Principal.ProfileID); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			var req playbackv1.CreateQueueRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"authority", "account", "profile"} {
				t.Run(field, func(t *testing.T) {
					changed := caller
					switch field {
					case "authority":
						changed.Principal.Authority = "hosted"
					case "account":
						changed.Principal.AccountID = "another-account"
					case "profile":
						changed.Principal.ProfileID = "another-profile"
					}
					out, err := f.v1.CreateQueue(context.Background(), changed, "retain-viewer-00002", req)
					if !errors.Is(err, playbackv1.ErrIdempotencyMismatch) || out.Queue.ID != "" || out.Session != nil {
						t.Fatalf("another viewer received the old receipt: %+v %v", out, err)
					}
				})
			}
		})
	}
}

func TestPlaybackV1QueueRetentionCancellationAndCleanupRemainOrdinary(t *testing.T) {
	f := newV1Fixture(t, 1)
	first := retainedQueueCreate(f, "retain-cleanup-0001", retainedQueueBody(f.items))
	second := retainedQueueCreate(f, "retain-cleanup-0002", retainedQueueBody(f.items, first.Queue.ID))
	var caller playbackv1.Caller
	if err := f.db.QueryRow(`SELECT owner_id,authority,account_id,profile_id FROM queues_v1 WHERE id=?`, second.Queue.ID).Scan(&caller.DeviceID, &caller.Principal.Authority, &caller.Principal.AccountID, &caller.Principal.ProfileID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// HTTP/1 intentionally detaches an already-canceled connection context.
	// Exercise cancellation at the service boundary before any retained write.
	result, err := f.v1.CreateQueue(ctx, caller, "retain-cancel-00001", playbackv1.CreateQueueRequest{
		RetainQueueIDs: []string{first.Queue.ID, second.Queue.ID},
		Segments:       []playbackv1.SegmentInput{{Source: playbackv1.Selector{Items: &playbackv1.ItemList{IDs: f.items}}}},
	})
	if !errors.Is(err, context.Canceled) || result.Queue.ID != "" || result.Session != nil {
		t.Fatalf("canceled create returned a receipt: %+v %v", result, err)
	}
	var receipts int
	if err := f.db.QueryRow(`SELECT count(*) FROM queue_v1_create_receipts WHERE create_key='retain-cancel-00001'`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("canceled create committed a receipt: %d %v", receipts, err)
	}
	for _, id := range []string{first.Queue.ID, second.Queue.ID} {
		f.call("GET", "/v1/queues/"+id, nil, nil, 200, nil)
	}
	// Discard and the normal idle lifetime still remove retained snapshots/keys.
	f.call("DELETE", "/v1/queues/"+first.Queue.ID, nil, nil, 204, nil)
	if _, err := f.db.Exec(`UPDATE queues_v1 SET updated_ms=0 WHERE id=?`, second.Queue.ID); err != nil {
		t.Fatal(err)
	}
	f.v1.SweepQueues(context.Background())
	for _, id := range []string{first.Queue.ID, second.Queue.ID} {
		f.call("GET", "/v1/queues/"+id, nil, nil, 404, nil)
		var chunks int
		if err := f.db.QueryRow(`SELECT count(*) FROM queue_v1_chunks c JOIN queue_v1_snapshots s ON s.id=c.snapshot_id WHERE s.queue_id=?`, id).Scan(&chunks); err != nil || chunks != 0 {
			t.Fatalf("cleanup kept retained keys for %s: %d %v", id, chunks, err)
		}
	}
}
