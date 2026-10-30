package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func receiptStopContext(t *testing.T, f *carryFixture, nonce int, operation string) (context.Context, usermutation.Binding, string) {
	t.Helper()
	if f.caller.AuthorizeItem == nil {
		f.caller.AuthorizeItem = func(context.Context, string) error { return nil }
	}
	var family identity.FamilyState
	err := dbwork.WithReadSnapshot(context.Background(), f.catalog.DB, func(ctx context.Context) error {
		var err error
		family, err = f.ident.SessionFamilyTx(ctx, dbwork.Snapshot(ctx), f.caller.Principal)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	p := f.caller.Principal
	binding := usermutation.Binding{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, FamilyID: family.ID}
	key := fmt.Sprintf("p251_%013d_%032x", f.service.now().UnixMilli(), nonce)
	ctx := usermutation.WithRequest(dbwork.WithDurableUser(context.Background()), binding, usermutation.Envelope{Key: key, Operation: operation, Digest: fmt.Sprintf("%064x", nonce), Stop: true})
	ctx = usermutation.WithAuthority(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var mode int
		if err := tx.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&mode); err != nil {
			return err
		}
		if mode != 2 {
			return fmt.Errorf("Stop receipt writer synchronous=%d, want FULL", mode)
		}
		_, err := f.ident.SessionFamilyTx(ctx, tx, p)
		return err
	})
	return ctx, binding, key
}
func stopReceipt(t *testing.T, f *carryFixture, binding usermutation.Binding, key string) *usermutation.Receipt {
	t.Helper()
	r, err := usermutation.Lookup(context.Background(), f.catalog.DB, binding, key, f.service.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStopReceiptLostResponseReplayAndSuccessor(t *testing.T) {
	f := newCarryFixture(t, false)
	v := *f.queue().Session
	ctx, binding, key := receiptStopContext(t, f, 1, "stop_playback_session")
	position := int64(42_000)
	generation := v.Presentation.Generation
	if err := f.service.Stop(ctx, f.caller, v.ID, &position, &generation); err != nil {
		t.Fatal(err)
	}
	r := stopReceipt(t, f, binding, key)
	if r == nil || r.Status != "applied" || r.PositionStatus != "applied" || r.PositionMs != position || r.Generation != generation {
		t.Fatalf("receipt: %+v", r)
	}
	before := readStoppedRow(t, f.catalog.DB, v.ID)
	// A lost response is recovered with the same key, without a second sequence,
	// end event, or evidence write. The old key also precedes successor fences.
	if err := f.service.Stop(ctx, f.caller, v.ID, &position, &generation); err != nil {
		t.Fatal(err)
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != before {
		t.Fatalf("duplicate moved row: %+v", after)
	}
	f.catalog.Exec(`UPDATE playback_v1_sessions SET generation=generation+1,ended_ms=0,end_reason='',position_ms=123 WHERE id=?`, v.ID)
	successor := readStoppedRow(t, f.catalog.DB, v.ID)
	if err := f.service.Stop(ctx, f.caller, v.ID, &position, &generation); err != nil {
		t.Fatal(err)
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != successor {
		t.Fatalf("replay touched successor: %+v", after)
	}
	fresh, _, _ := receiptStopContext(t, f, 2, "stop_playback_session")
	if err := f.service.Stop(fresh, f.caller, v.ID, &position, &generation); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale fresh stop: %v", err)
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != successor {
		t.Fatal("stale stop touched successor")
	}
}

func TestStopReceiptStorageFailureRollsBackEverything(t *testing.T) {
	f := newCarryFixture(t, false)
	v := *f.queue().Session
	ctx, binding, key := receiptStopContext(t, f, 3, "stop_playback_session")
	before := readStoppedRow(t, f.catalog.DB, v.ID)
	var legacyBefore string
	if err := f.catalog.DB.QueryRow(`SELECT json_array(sequence,state,(SELECT position FROM playback_observations WHERE session_id=playback_sessions.id)) FROM playback_sessions WHERE id=?`, before.media).Scan(&legacyBefore); err != nil {
		t.Fatal(err)
	}
	f.catalog.Exec(`CREATE TRIGGER stop_receipt_storage_failure BEFORE UPDATE OF position_ms ON playback_v1_sessions BEGIN SELECT RAISE(ABORT,'storage failed'); END`)
	position := int64(12345)
	if err := f.service.Stop(ctx, f.caller, v.ID, &position); err == nil {
		t.Fatal("storage error succeeded")
	}
	if r := stopReceipt(t, f, binding, key); r != nil {
		t.Fatalf("failed stop saved receipt: %+v", r)
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != before {
		t.Fatal("storage failure changed session")
	}
	var legacyAfter string
	if err := f.catalog.DB.QueryRow(`SELECT json_array(sequence,state,(SELECT position FROM playback_observations WHERE session_id=playback_sessions.id)) FROM playback_sessions WHERE id=?`, before.media).Scan(&legacyAfter); err != nil {
		t.Fatal(err)
	}
	if legacyAfter != legacyBefore {
		t.Fatalf("evidence escaped rollback: %s -> %s", legacyBefore, legacyAfter)
	}
	f.catalog.Exec(`DROP TRIGGER stop_receipt_storage_failure`)
	if err := f.service.Stop(ctx, f.caller, v.ID, &position); err != nil {
		t.Fatal(err)
	}
	if r := stopReceipt(t, f, binding, key); r == nil || r.PositionStatus != "applied" {
		t.Fatalf("retry: %+v", r)
	}
}

func TestStopReceiptExplicitPositionDispositions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, disposition := range []string{"policy_rejected", "not_renderer", "unchanged", "already_stopped"} {
		t.Run(disposition, func(t *testing.T) {
			f := newCarryFixture(t, false)
			v := *f.queue().Session
			ctx, binding, key := receiptStopContext(t, f, 4, "stop_playback_session")
			c := f.caller
			position := int64(12345)
			final := &position
			switch disposition {
			case "policy_rejected":
				f.service.Playback.AuthorityTx = func(context.Context, *sql.Tx, identity.Principal, string) (identity.Principal, error) {
					return identity.Principal{}, identity.ErrForbidden
				}
			case "not_renderer":
				c.DeviceID = "controller"
			case "unchanged":
				final = nil
			case "already_stopped":
				if err := f.service.Stop(context.Background(), c, v.ID, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.Stop(ctx, c, v.ID, final); err != nil {
				t.Fatal(err)
			}
			r := stopReceipt(t, f, binding, key)
			if r == nil || r.PositionStatus != disposition {
				t.Fatalf("receipt %+v", r)
			}
			if disposition == "policy_rejected" && r.PositionMs == position {
				t.Fatal("rejected evidence acknowledged as applied")
			}
			if after := readStoppedRow(t, f.catalog.DB, v.ID); after.ended == 0 {
				t.Fatal("stop did not commit")
			}
		})
	}
}

func TestStopReceiptConcurrentDuplicate(t *testing.T) {
	f := newCarryFixture(t, false)
	v := *f.queue().Session
	ctx, binding, key := receiptStopContext(t, f, 5, "stop_playback_session")
	before := readStoppedRow(t, f.catalog.DB, v.ID)
	position := int64(12000)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- f.service.Stop(ctx, f.caller, v.ID, &position) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	after := readStoppedRow(t, f.catalog.DB, v.ID)
	if after.revision != before.revision+1 || after.lastSeq != before.lastSeq+1 {
		t.Fatalf("duplicate applied twice: %+v", after)
	}
	if r := stopReceipt(t, f, binding, key); r == nil || r.PositionStatus != "applied" {
		t.Fatalf("receipt %+v", r)
	}
}

func TestStopGenerationReceiptDrainAndReplay(t *testing.T) {
	f := newObservationFixture(t)
	f.service.StopAuthorityTx = func(context.Context, *sql.Tx, Caller) error { return nil }
	f.caller.AuthorizeItem = func(context.Context, string) error { return nil }
	id := f.session.ID
	epoch := f.claim(id, "")
	f.clock.add(1_000_000_000)
	if _, err := f.tick(id, epoch, 1, "paused"); err != nil {
		t.Fatal(err)
	}
	ctx, binding, key := receiptStopContext(t, f.carryFixture, 6, "stop_playback_session")
	if err := f.service.StopGeneration(ctx, f.caller, id, f.generation); err != nil {
		t.Fatal(err)
	}
	r := stopReceipt(t, f.carryFixture, binding, key)
	if r == nil || r.Status != "applied" || r.PositionMs != 61000 || r.PositionStatus != "unchanged" {
		t.Fatalf("receipt %+v", r)
	}
	before := stopGenerationSnapshot(t, f.carryFixture, id)
	if err := f.service.StopGeneration(ctx, f.caller, id, f.generation); err != nil {
		t.Fatal(err)
	}
	if after := stopGenerationSnapshot(t, f.carryFixture, id); after != before {
		t.Fatal("replay changed state")
	}
}

func TestStopReceiptQuotaPrecedesObservationDrain(t *testing.T) {
	for _, conditional := range []bool{false, true} {
		t.Run(fmt.Sprint(conditional), func(t *testing.T) {
			f := newObservationFixture(t)
			f.service.StopAuthorityTx = func(context.Context, *sql.Tx, Caller) error { return nil }
			f.caller.AuthorizeItem = func(context.Context, string) error { return nil }
			id := f.session.ID
			epoch := f.claim(id, "")
			f.clock.add(1_000_000_000)
			if _, err := f.tick(id, epoch, 1, "playing"); err != nil {
				t.Fatal(err)
			}
			ctx, _, _ := receiptStopContext(t, f.carryFixture, 7, "stop_playback_session")
			f.catalog.Exec(`UPDATE user_mutation_global SET stop_bytes=33554432-ordinary_bytes WHERE singleton=1`)
			before := readStoppedRow(t, f.catalog.DB, id)
			var err error
			if conditional {
				err = f.service.StopGeneration(ctx, f.caller, id, f.generation)
			} else {
				err = f.service.Stop(ctx, f.caller, id, nil)
			}
			if !errors.Is(err, usermutation.ErrCapacity) {
				t.Fatalf("quota: %v", err)
			}
			if after := readStoppedRow(t, f.catalog.DB, id); after != before {
				t.Fatal("quota refusal drained/stopped session")
			}
			if f.proved.total() != 0 {
				t.Fatal("quota refusal applied pending evidence")
			}
		})
	}
}

func TestStopReceiptLookupFinishesBeforeAcknowledgement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newCarryFixture(t, false)
	v := *f.queue().Session
	ctx, binding, key := receiptStopContext(t, f, 8, "stop_playback_session")
	r, err := loadRow(context.Background(), f.catalog.DB, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A real registered preparation ignores cancellation until its owner
	// joins. The Stop commits its receipt/fences but must report unfinished.
	owner, err := f.service.Playback.RetainPreparation(context.Background(), r.media, r.mediaGeneration)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err = f.service.Stop(ctx, f.caller, v.ID, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfinished Stop acknowledged: %v", err)
	}
	if owner.Context().Err() != context.Canceled {
		t.Fatal("original process was not canceled")
	}
	receipt := stopReceipt(t, f, binding, key)
	if receipt == nil || receipt.Status != "applied" {
		t.Fatalf("missing committed receipt: %+v", receipt)
	}
	if err = dbwork.WithReadSnapshot(context.Background(), f.catalog.DB, func(ctx context.Context) error {
		if err := f.service.CompleteStopReceipt(ctx, *receipt); err == nil {
			t.Fatal("completion accepted a live read snapshot")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The core's GET-only recovery cannot acknowledge applied until the exact
	// pending join completes; no mutation replay is needed to finish it.
	if err = f.service.CompleteStopReceipt(context.Background(), *receipt); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lookup acknowledged unfinished join: %v", err)
	}
	if _, ok := f.service.stopCompletions.Load(key); !ok {
		t.Fatal("failed join was discarded")
	}
	owner.Close()
	if err = f.service.CompleteStopReceipt(context.Background(), *receipt); err != nil {
		t.Fatal(err)
	}
	if err = f.service.CompleteStopReceipt(context.Background(), *receipt); err != nil {
		t.Fatal(err)
	}
	if f.service.stopCompletionCount != 0 {
		t.Fatal("completed join retained capacity")
	}
}

func TestStopReceiptCompletionCapacityAndRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newCarryFixture(t, false)
	v := *f.queue().Session
	ctx, binding, key := receiptStopContext(t, f, 9, "stop_playback_session")
	f.service.stopCompletionCount = maxStopCompletions
	before := readStoppedRow(t, f.catalog.DB, v.ID)
	if err := f.service.Stop(ctx, f.caller, v.ID, nil); !errors.Is(err, usermutation.ErrCapacity) {
		t.Fatalf("pending join cap: %v", err)
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != before {
		t.Fatal("pending capacity refusal changed session")
	}
	if r := stopReceipt(t, f, binding, key); r != nil {
		t.Fatalf("pending capacity saved receipt: %+v", r)
	}
	f.service.stopCompletionCount = 0
	f.catalog.Exec(`CREATE TRIGGER stop_receipt_save_failure BEFORE UPDATE OF status ON user_mutation_receipts WHEN NEW.status='applied' BEGIN SELECT RAISE(ABORT,'receipt save failed'); END`)
	if err := f.service.Stop(ctx, f.caller, v.ID, nil); err == nil {
		t.Fatal("receipt failure reported success")
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != before {
		t.Fatal("receipt failure changed session")
	}
	if f.service.stopCompletionCount != 0 {
		t.Fatal("rollback leaked completion capacity")
	}
	f.catalog.Exec(`DROP TRIGGER stop_receipt_save_failure`)
	if err := f.service.Stop(ctx, f.caller, v.ID, nil); err != nil {
		t.Fatal(err)
	}
}

func TestStopAccountReceiptPrecedesMutableSession(t *testing.T) {
	f := newCarryFixture(t, false)
	v := *f.queue().Session
	ctx, binding, key := receiptStopContext(t, f, 10, "stop_account_playback")
	if err := f.service.StopAccountSession(ctx, f.caller, v.ID, &v.Presentation.Generation); err != nil {
		t.Fatal(err)
	}
	f.catalog.Exec(`DELETE FROM playback_v1_sessions WHERE id=?`, v.ID)
	if err := f.service.StopAccountSession(ctx, f.caller, v.ID, &v.Presentation.Generation); err != nil {
		t.Fatalf("account replay looked up deleted target: %v", err)
	}
	if r := stopReceipt(t, f, binding, key); r == nil || r.Status != "applied" {
		t.Fatalf("receipt %+v", r)
	}
}

func TestStopGenerationReceiptConcurrentDuplicate(t *testing.T) {
	f, v := stopGenerationFixture(t)
	ctx, binding, key := receiptStopContext(t, f, 11, "stop_playback_session")
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- f.service.StopGeneration(ctx, f.caller, v.ID, v.Presentation.Generation)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if r := stopReceipt(t, f, binding, key); r == nil || r.Status != "applied" {
		t.Fatalf("receipt %+v", r)
	}
}

func TestStopReceiptRacingBatchDrainsCreditOnce(t *testing.T) {
	f := newObservationFixture(t)
	id := f.session.ID
	epoch := f.claim(id, "")
	for seq := int64(1); seq <= 12; seq++ {
		f.clock.add(1_000_000_000)
		if _, err := f.tick(id, epoch, seq, "playing"); err != nil {
			t.Fatal(err)
		}
	}
	ctx, binding, key := receiptStopContext(t, f.carryFixture, 12, "stop_playback_session")
	f.clock.add(observationFlush)
	stopped := make(chan error, 1)
	go func() { stopped <- f.service.Stop(ctx, f.caller, id, nil) }()
	o := f.service.observations()
	o.mu.Lock()
	o.wakeLocked()
	o.mu.Unlock()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if f.proved.total() != 12000 {
		t.Fatalf("watched time %d, want exactly 12000", f.proved.total())
	}
	r := stopReceipt(t, f.carryFixture, binding, key)
	if r == nil || r.PositionMs != 72000 {
		t.Fatalf("receipt lost renderer sample: %+v", r)
	}
	if err := f.service.CompleteStopReceipt(context.Background(), *r); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Stop(ctx, f.caller, id, nil); err != nil {
		t.Fatal(err)
	}
	if f.proved.total() != 12000 {
		t.Fatal("recovery credited twice")
	}
}

func TestStopReceiptAuthorizationCannotAdoptSuccessor(t *testing.T) {
	f := newCarryFixture(t, false)
	v := *f.queue().Session
	ctx, binding, key := receiptStopContext(t, f, 13, "stop_playback_session")
	before := readStoppedRow(t, f.catalog.DB, v.ID)
	c := f.caller
	c.AuthorizeItem = func(ctx context.Context, _ string) error {
		_, err := dbwork.Snapshot(ctx).ExecContext(ctx, `UPDATE playback_v1_sessions SET generation=generation+1 WHERE id=?`, v.ID)
		return err
	}
	position := int64(12000)
	generation := v.Presentation.Generation
	if err := f.service.Stop(ctx, c, v.ID, &position, &generation); !errors.Is(err, ErrRevision) {
		t.Fatalf("successor adoption: %v", err)
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != before {
		t.Fatal("same-writer successor escaped rollback")
	}
	if r := stopReceipt(t, f, binding, key); r != nil {
		t.Fatalf("saved successor receipt: %+v", r)
	}
}

func TestStopAccountReceiptHiddenProfileOriginalFamilyAndQuota(t *testing.T) {
	f := newCarryFixture(t, false)
	v := *f.queue().Session
	// Account controls may stop another profile's original session without
	// proving or exposing its hidden item. The receipt belongs to the caller's
	// original profile/family, and the server captures the actual generation.
	f.catalog.Exec(`UPDATE playback_v1_sessions SET profile_id='hidden-profile' WHERE id=?`, v.ID)
	ctx, binding, key := receiptStopContext(t, f, 14, "stop_account_playback")
	c := f.caller
	c.AuthorizeItem = func(context.Context, string) error {
		t.Fatal("account Stop requested a hidden-title proof")
		return errors.New("title proof")
	}
	before := readStoppedRow(t, f.catalog.DB, v.ID)
	f.catalog.Exec(`UPDATE user_mutation_global SET stop_bytes=33554432-ordinary_bytes WHERE singleton=1`)
	if err := f.service.StopAccountSession(ctx, c, v.ID, &v.Presentation.Generation); !errors.Is(err, usermutation.ErrCapacity) {
		t.Fatalf("account quota: %v", err)
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != before {
		t.Fatal("account quota refusal changed original")
	}
	if r := stopReceipt(t, f, binding, key); r != nil {
		t.Fatalf("quota saved receipt: %+v", r)
	}
	f.catalog.Exec(`UPDATE user_mutation_global SET stop_bytes=0 WHERE singleton=1`)
	if err := f.service.StopAccountSession(ctx, c, v.ID, &v.Presentation.Generation); err != nil {
		t.Fatal(err)
	}
	receipt := stopReceipt(t, f, binding, key)
	if receipt == nil || receipt.Operation != "stop_account_playback" || receipt.Generation != int(before.generation) || receipt.TargetID != v.ID {
		t.Fatalf("account receipt %+v", receipt)
	}
	stopped := readStoppedRow(t, f.catalog.DB, v.ID)
	if err := f.service.StopAccountSession(ctx, c, v.ID, &v.Presentation.Generation); err != nil {
		t.Fatal(err)
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != stopped {
		t.Fatal("account duplicate changed original")
	}
	other := binding
	other.FamilyID = "other-family"
	_, envelope, _ := usermutation.RequestFrom(ctx)
	if err := f.service.StopAccountSession(usermutation.WithRequest(ctx, other, envelope), c, v.ID, &v.Presentation.Generation); !errors.Is(err, usermutation.ErrConflict) {
		t.Fatalf("different family disclosed receipt: %v", err)
	}
	// Current authority is checked even for an already-applied receipt.
	f.catalog.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, binding.FamilyID)
	if err := f.service.StopAccountSession(ctx, c, v.ID, &v.Presentation.Generation); err == nil {
		t.Fatal("revoked family replay succeeded")
	}
}

func TestStopReceiptSilentEvidenceNoopIsPolicyRejection(t *testing.T) {
	f := newCarryFixture(t, false)
	v := *f.queue().Session
	ctx, binding, key := receiptStopContext(t, f, 15, "stop_playback_session")
	before := readStoppedRow(t, f.catalog.DB, v.ID)
	// The evidence port no-ops this foreign family. Even a coincidentally equal
	// legacy sequence/paused state cannot be mistaken for accepted evidence.
	f.catalog.Exec(`UPDATE playback_sessions SET session_hash='other-family',sequence=?,state='paused' WHERE id=?`, before.lastSeq+1, before.media)
	position := int64(43000)
	if err := f.service.Stop(ctx, f.caller, v.ID, &position); err != nil {
		t.Fatal(err)
	}
	r := stopReceipt(t, f, binding, key)
	if r == nil || r.PositionStatus != "policy_rejected" || r.PositionMs != before.position {
		t.Fatalf("no-op falsely acknowledged: %+v", r)
	}
}

func TestStopReceiptFencesOriginalPreparedChild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newCarryFixture(t, true)
	q := f.queue()
	prepared, err := f.service.PrepareNext(context.Background(), f.caller, q.Queue.ID, q.Queue.Revision, "receipt-prepare-next-0001", PrepareNextRequest{SessionID: q.Session.ID, SessionGeneration: q.Session.Presentation.Generation})
	if err != nil {
		t.Fatal(err)
	}
	ctx, binding, key := receiptStopContext(t, f, 16, "stop_playback_session")
	position := int64(42000)
	generation := q.Session.Presentation.Generation
	if err = f.service.Stop(ctx, f.caller, q.Session.ID, &position, &generation); err != nil {
		t.Fatal(err)
	}
	var childState, preparedState string
	if err = f.catalog.DB.QueryRow(`SELECT p.state,n.state FROM playback_sessions p JOIN playback_v1_prepared n ON n.media_session_id=p.id WHERE n.token=?`, prepared.Token).Scan(&childState, &preparedState); err != nil {
		t.Fatal(err)
	}
	if childState != "stopped" || preparedState != "canceled" {
		t.Fatalf("child fence %s prepared %s", childState, preparedState)
	}
	if r := stopReceipt(t, f, binding, key); r == nil || r.PositionStatus != "applied" {
		t.Fatalf("receipt %+v", r)
	}
}

func TestStopAccountReceiptQuotaRetryCannotStopSuccessor(t *testing.T) {
	f := newCarryFixture(t, false)
	v := *f.queue().Session
	ctx, binding, key := receiptStopContext(t, f, 17, "stop_account_playback")
	generation := v.Presentation.Generation
	f.catalog.Exec(`UPDATE user_mutation_global SET stop_bytes=33554432-ordinary_bytes WHERE singleton=1`)
	if err := f.service.StopAccountSession(ctx, f.caller, v.ID, &generation); !errors.Is(err, usermutation.ErrCapacity) {
		t.Fatalf("quota: %v", err)
	}
	f.catalog.Exec(`UPDATE user_mutation_global SET stop_bytes=0 WHERE singleton=1`)
	f.catalog.Exec(`UPDATE playback_v1_sessions SET generation=generation+1 WHERE id=?`, v.ID)
	successor := readStoppedRow(t, f.catalog.DB, v.ID)
	if err := f.service.StopAccountSession(ctx, f.caller, v.ID, &generation); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale retry: %v", err)
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != successor {
		t.Fatal("quota retry stopped successor")
	}
	if receipt := stopReceipt(t, f, binding, key); receipt != nil {
		t.Fatalf("stale retry saved receipt: %+v", receipt)
	}
}

func TestStopReceiptReplayReprovesOriginalItemAuthority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, conditional := range []bool{false, true} {
		t.Run(fmt.Sprint(conditional), func(t *testing.T) {
			f, v := stopGenerationFixture(t)
			ctx, binding, key := receiptStopContext(t, f, 18, "stop_playback_session")
			generation := v.Presentation.Generation
			stop := func(c Caller) error {
				if conditional {
					return f.service.StopGeneration(ctx, c, v.ID, generation)
				}
				return f.service.Stop(ctx, c, v.ID, nil, &generation)
			}
			if err := stop(f.caller); err != nil {
				t.Fatal(err)
			}
			receipt := stopReceipt(t, f, binding, key)
			// A successor's visible item cannot authorize disclosure of the old position.
			f.catalog.Exec(`UPDATE playback_v1_sessions SET item_id=?,generation=generation+1,ended_ms=0 WHERE id=?`, f.items[1].ID, v.ID)
			successor := readStoppedRow(t, f.catalog.DB, v.ID)
			c := f.caller
			c.AuthorizeItem = func(ctx context.Context, item string) error {
				if dbwork.Snapshot(ctx) == nil {
					t.Fatal("proof escaped snapshot")
				}
				if item != f.items[0].Public {
					t.Fatalf("proved successor item %s", item)
				}
				return ErrNotFound
			}
			if err := stop(c); !errors.Is(err, ErrNotFound) {
				t.Fatalf("hidden original replay: %v", err)
			}
			if err := dbwork.WithReadSnapshot(ctx, f.catalog.DB, func(ctx context.Context) error { return f.service.AuthorizeStopReceipt(ctx, c, *receipt) }); !errors.Is(err, ErrNotFound) {
				t.Fatalf("hidden original lookup: %v", err)
			}
			if after := readStoppedRow(t, f.catalog.DB, v.ID); after != successor {
				t.Fatal("proof touched successor")
			}
			// Deleting only the playback row does not delete the original content proof.
			f.catalog.Exec(`DELETE FROM playback_v1_sessions WHERE id=?`, v.ID)
			if err := stop(c); !errors.Is(err, ErrNotFound) {
				t.Fatalf("deleted session bypassed original content: %v", err)
			}
			c.AuthorizeItem = func(_ context.Context, item string) error {
				if item != f.items[0].Public {
					t.Fatal(item)
				}
				return nil
			}
			if err := stop(c); err != nil {
				t.Fatalf("genuine deleted session acknowledgement: %v", err)
			}
		})
	}
}

func TestStopReceiptItemEvidenceFailureRollsBackStopAndReceipt(t *testing.T) {
	f := newCarryFixture(t, false)
	v := *f.queue().Session
	ctx, binding, key := receiptStopContext(t, f, 19, "stop_playback_session")
	before := readStoppedRow(t, f.catalog.DB, v.ID)
	f.catalog.Exec(`CREATE TRIGGER stop_item_evidence_failure BEFORE UPDATE OF stop_item_id ON user_mutation_receipts BEGIN SELECT RAISE(ABORT,'item evidence failed'); END`)
	if err := f.service.Stop(ctx, f.caller, v.ID, nil); err == nil {
		t.Fatal("evidence storage failure acknowledged")
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != before {
		t.Fatal("Stop escaped evidence rollback")
	}
	if receipt := stopReceipt(t, f, binding, key); receipt != nil {
		t.Fatal("receipt escaped evidence rollback")
	}
	if f.service.stopCompletionCount != 0 {
		t.Fatal("evidence rollback leaked completion")
	}
	f.catalog.Exec(`DROP TRIGGER stop_item_evidence_failure`)
	if err := f.service.Stop(ctx, f.caller, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	receipt := stopReceipt(t, f, binding, key)
	if err := f.service.AuthorizeStopReceipt(ctx, f.caller, *receipt); err == nil {
		t.Fatal("proof accepted without snapshot")
	}
	f.catalog.Exec(`UPDATE user_mutation_receipts SET stop_item_id='' WHERE key=?`, key)
	f.catalog.Exec(`UPDATE playback_v1_sessions SET generation=generation+1,ended_ms=0 WHERE id=?`, v.ID)
	successor := readStoppedRow(t, f.catalog.DB, v.ID)
	if err := f.service.Stop(ctx, f.caller, v.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("older receipt borrowed successor: %v", err)
	}
	if after := readStoppedRow(t, f.catalog.DB, v.ID); after != successor {
		t.Fatal("older receipt touched successor")
	}
}
