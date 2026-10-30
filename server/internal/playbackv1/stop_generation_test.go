package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func stopGenerationFixture(t *testing.T, audio ...bool) (*carryFixture, SessionView) {
	t.Helper()
	f := newCarryFixture(t, len(audio) > 0 && audio[0])
	f.service.StopAuthorityTx = func(ctx context.Context, tx *sql.Tx, c Caller) error {
		if dbwork.Snapshot(ctx) != tx {
			return identity.ErrForbidden
		}
		family, err := f.ident.SessionFamilyTx(ctx, tx, c.Principal)
		if err != nil {
			return err
		}
		if family.DeviceID != c.DeviceID {
			return identity.ErrForbidden
		}
		return nil
	}
	f.caller.AuthorizeItem = func(ctx context.Context, item string) error {
		if dbwork.Snapshot(ctx) == nil {
			return errors.New("item check escaped original snapshot")
		}
		var id int64
		return dbwork.QueryRow(ctx, f.catalog.DB, `SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&id)
	}
	// Ordinary starts do not need the conditional writer-only item callback.
	startCaller := f.caller
	startCaller.AuthorizeItem = nil
	v, preparing, err := f.service.Start(context.Background(), startCaller, "conditional-start-original-0001", StartRequest{ItemID: f.items[0].Public, State: "playing"})
	if err != nil || preparing {
		t.Fatalf("start: %v preparing=%v", err, preparing)
	}
	return f, v
}

func stopGenerationSnapshot(t *testing.T, f *carryFixture, id string) string {
	t.Helper()
	var snapshot string
	err := f.catalog.DB.QueryRow(`SELECT json_array(s.revision,s.generation,s.ended_ms,s.position_ms,s.last_seq,s.media_session_id,s.media_generation,p.state,p.generation,p.grant_hash,(SELECT count(*) FROM playback_v1_prepared WHERE session_id=s.id),(SELECT count(*) FROM api_events)) FROM playback_v1_sessions s JOIN playback_sessions p ON p.id=s.media_session_id WHERE s.id=?`, id).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestStopGenerationOriginalAndEndedReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, v := stopGenerationFixture(t)
	ctx := context.Background()
	if err := f.service.StopGeneration(ctx, f.caller, v.ID, v.Presentation.Generation); err != nil {
		t.Fatal(err)
	}
	r, err := loadRow(ctx, f.catalog.DB, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.ended == 0 || r.endReason != "stopped" {
		t.Fatal("original was not ended")
	}
	var state string
	if err = f.catalog.DB.QueryRow(`SELECT state FROM playback_sessions WHERE id=?`, r.media).Scan(&state); err != nil || state != "stopped" {
		t.Fatalf("grant fence: %s %v", state, err)
	}
	before := stopGenerationSnapshot(t, f, v.ID)
	if err = f.service.StopGeneration(ctx, f.caller, v.ID, v.Presentation.Generation); err != nil {
		t.Fatal(err)
	}
	if after := stopGenerationSnapshot(t, f, v.ID); after != before {
		t.Fatal("ended replay changed durable facts")
	}
}

func TestStopGenerationRefusalsHaveNoOccurrenceEffects(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"generation", "device", "owner", "missing_callback", "refusing_callback", "item_refusal", "same_writer_revoke", "same_writer_physical_drift", "same_writer_successor", "same_writer_rollback"} {
		t.Run(kind, func(t *testing.T) {
			f, v := stopGenerationFixture(t)
			ctx := context.Background()
			c := f.caller
			generation := v.Presentation.Generation
			switch kind {
			case "generation":
				generation++
			case "device":
				c.DeviceID = "other-device"
			case "owner":
				c.Principal.AccountID = "foreign"
			case "missing_callback":
				f.service.StopAuthorityTx = nil
			case "refusing_callback":
				f.service.StopAuthorityTx = func(context.Context, *sql.Tx, Caller) error { return identity.ErrForbidden }
			case "item_refusal":
				c.AuthorizeItem = func(context.Context, string) error { return identity.ErrForbidden }
			case "same_writer_revoke":
				original := c.AuthorizeItem
				changed := false
				c.AuthorizeItem = func(ctx context.Context, item string) error {
					if dbwork.InWriteTx(ctx) && !changed {
						changed = true
						if _, err := dbwork.Snapshot(ctx).ExecContext(ctx, `UPDATE authorization_session_families SET revoked=1 WHERE id IN (SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, c.Principal.Hash); err != nil {
							return err
						}
					}
					return original(ctx, item)
				}
			case "same_writer_physical_drift", "same_writer_successor":
				// The first check is before FenceTx. The second is its mandatory original
				// callback, after the claim froze the immutable physical/source tuple.
				original := c.AuthorizeItem
				calls := 0
				c.AuthorizeItem = func(ctx context.Context, item string) error {
					if dbwork.InWriteTx(ctx) {
						calls++
						if calls == 2 {
							query := `UPDATE playback_source_pins SET modified_ns=modified_ns+1 WHERE session_id=(SELECT media_session_id FROM playback_v1_sessions WHERE id=?)`
							if kind == "same_writer_successor" {
								query = `UPDATE playback_sessions SET generation=generation+1,grant_hash='successor-grant' WHERE id=(SELECT media_session_id FROM playback_v1_sessions WHERE id=?)`
							}
							if _, err := dbwork.Snapshot(ctx).ExecContext(ctx, query, v.ID); err != nil {
								return err
							}
						}
					}
					return original(ctx, item)
				}
			case "same_writer_rollback":
				original := c.AuthorizeItem
				calls := 0
				c.AuthorizeItem = func(ctx context.Context, item string) error {
					if dbwork.InWriteTx(ctx) {
						calls++
						if calls == 2 {
							return errors.New("accepting callback refused")
						}
					}
					return original(ctx, item)
				}
			}
			r, err := loadRow(ctx, f.catalog.DB, v.ID)
			if err != nil {
				t.Fatal(err)
			}
			owned, err := f.service.Playback.RetainPreparation(ctx, r.media, r.mediaGeneration)
			if err != nil {
				t.Fatal(err)
			}
			defer owned.Close()
			if !f.service.Playback.PrefetchCharge(r.media, 37, 100) {
				t.Fatal("budget fixture")
			}
			queueBefore := stopQueueSnapshot(t, f)
			before := stopGenerationSnapshot(t, f, v.ID)
			if err := f.service.StopGeneration(ctx, c, v.ID, generation); err == nil {
				t.Fatal("invalid cleanup accepted")
			}
			if after := stopGenerationSnapshot(t, f, v.ID); after != before {
				t.Fatalf("refusal changed original: %s -> %s", before, after)
			}
			if owned.Context().Err() != nil {
				t.Fatal("refusing held claim cancelled original work")
			}
			if stopQueueSnapshot(t, f) != queueBefore {
				t.Fatal("refusal changed queue/event/process/artifact facts")
			}
			if n := f.service.Playback.PrefetchTake(r.media, 100, 100); n != 63 {
				t.Fatalf("refusal changed original retained budget: %d", n)
			}
			owned.Close() // a later valid Stop may now join this original owner
			// Aborted process-side reservations do not poison an independently valid
			// retry; restore only this fixture's callback, not another writer's state.
			if kind == "same_writer_revoke" || kind == "same_writer_rollback" || kind == "same_writer_physical_drift" || kind == "same_writer_successor" {
				if err := f.service.StopGeneration(ctx, f.caller, v.ID, v.Presentation.Generation); err != nil {
					t.Fatalf("rollback left closed admission: %v", err)
				}
			}
		})
	}
}

func TestStopGenerationReplanKeepsSuccessorUnchanged(t *testing.T) {
	f, v := stopGenerationFixture(t)
	ctx := context.Background()
	c := f.caller
	c.AuthorizeItem = nil
	next, err := f.service.Patch(ctx, c, v.ID, v.Revision, Change{Replan: true, Seek: &SeekRequest{PositionMs: 12000, ID: "seek-successor"}})
	if err != nil {
		t.Fatal(err)
	}
	if next.Presentation.Generation <= v.Presentation.Generation {
		t.Fatal("fixture did not replace generation")
	}
	before := stopGenerationSnapshot(t, f, v.ID)
	if err = f.service.StopGeneration(ctx, f.caller, v.ID, v.Presentation.Generation); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale cleanup: %v", err)
	}
	if after := stopGenerationSnapshot(t, f, v.ID); after != before {
		t.Fatal("stale cleanup changed successor")
	}
	if err = f.service.StopGeneration(ctx, f.caller, v.ID, next.Presentation.Generation); err != nil {
		t.Fatal(err)
	}
}

func TestStopGenerationCapturesOnlyOriginalPreparedChildSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, drift := range []bool{false, true} {
		t.Run(map[bool]string{false: "original", true: "child-drift-rollback"}[drift], func(t *testing.T) {
			f, unrelated := stopGenerationFixture(t, true)
			ctx := context.Background()
			originalCaller := f.caller
			f.caller.AuthorizeItem = nil
			q := f.queue()
			f.caller = originalCaller
			ordinary := f.caller
			ordinary.AuthorizeItem = nil
			prepared, err := f.service.PrepareNext(ctx, ordinary, q.Queue.ID, q.Queue.Revision, "conditional-prepare-next-0001", PrepareNextRequest{SessionID: q.Session.ID, SessionGeneration: q.Session.Presentation.Generation})
			if err != nil {
				t.Fatal(err)
			}
			var childID, childState, preparedState string
			if err = f.catalog.DB.QueryRow(`SELECT media_session_id,state FROM playback_v1_prepared WHERE token=?`, prepared.Token).Scan(&childID, &preparedState); err != nil {
				t.Fatal(err)
			}
			if preparedState != "prepared" {
				t.Fatal("fixture did not retain private child")
			}
			before := stopGenerationSnapshot(t, f, q.Session.ID)
			unrelatedBefore, err := loadRow(ctx, f.catalog.DB, unrelated.ID)
			if err != nil {
				t.Fatal(err)
			}
			childBefore := func() string {
				var value string
				if err := f.catalog.DB.QueryRow(`SELECT json_array(p.state,p.generation,p.grant_hash,n.state,n.queue_revision,n.media_session_id,pp.owner_session_id) FROM playback_sessions p JOIN playback_v1_prepared n ON n.media_session_id=p.id LEFT JOIN playback_private_presentations pp ON pp.session_id=p.id WHERE n.token=?`, prepared.Token).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}()
			c := f.caller
			if drift {
				calls := 0
				original := c.AuthorizeItem
				c.AuthorizeItem = func(ctx context.Context, item string) error {
					if dbwork.InWriteTx(ctx) {
						calls++
						if calls == 2 {
							if _, err := dbwork.Snapshot(ctx).ExecContext(ctx, `UPDATE playback_v1_prepared SET queue_revision=queue_revision+1 WHERE token=?`, prepared.Token); err != nil {
								return err
							}
						}
					}
					return original(ctx, item)
				}
			}
			err = f.service.StopGeneration(ctx, c, q.Session.ID, q.Session.Presentation.Generation)
			if drift {
				if err == nil {
					t.Fatal("changed child set accepted")
				}
				if after := stopGenerationSnapshot(t, f, q.Session.ID); after != before {
					t.Fatal("child drift changed parent")
				}
				var after string
				if err := f.catalog.DB.QueryRow(`SELECT json_array(p.state,p.generation,p.grant_hash,n.state,n.queue_revision,n.media_session_id,pp.owner_session_id) FROM playback_sessions p JOIN playback_v1_prepared n ON n.media_session_id=p.id LEFT JOIN playback_private_presentations pp ON pp.session_id=p.id WHERE n.token=?`, prepared.Token).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if after != childBefore {
					t.Fatal("child drift escaped rollback")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err = f.catalog.DB.QueryRow(`SELECT p.state,n.state FROM playback_sessions p JOIN playback_v1_prepared n ON n.media_session_id=p.id WHERE n.token=?`, prepared.Token).Scan(&childState, &preparedState); err != nil {
					t.Fatal(err)
				}
				if childState != "stopped" || preparedState != "canceled" {
					t.Fatalf("original child not fenced: %s %s", childState, preparedState)
				}
				var refs int
				if err = f.catalog.DB.QueryRow(`SELECT count(*) FROM playback_private_presentations WHERE session_id=?`, childID).Scan(&refs); err != nil || refs != 0 {
					t.Fatalf("original private ref retained: %d %v", refs, err)
				}
			}
			if after, e := loadRow(ctx, f.catalog.DB, unrelated.ID); e != nil || after != unrelatedBefore {
				t.Fatal("cleanup touched unrelated occurrence")
			}
		})
	}
}

func stopQueueSnapshot(t *testing.T, f *carryFixture) string {
	t.Helper()
	var value string
	if err := f.catalog.DB.QueryRow(`SELECT json_array((SELECT json_group_array(json_array(id,revision,current_entry_id,current_position,session_id,custody_generation)) FROM queues_v1),(SELECT count(*) FROM api_events),(SELECT count(*) FROM playback_artifacts),(SELECT count(*) FROM playback_vod_intervals),(SELECT count(*) FROM playback_v1_prepared))`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
