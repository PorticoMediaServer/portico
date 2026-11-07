package writer

import (
	"database/sql"
	"testing"
)

func TestUOOutboxNativeClaimCompleteAndReclaim(t *testing.T) {
	w := wc008NativeExecutor(t)
	wc008NativeExec(t, w, `INSERT INTO webhooks(id,name,url,secret,events,created_ms,updated_ms) VALUES('hook','Fixture','https://example.invalid','test-only','["download.completed"]',1,1)`)
	defs := append(uoOutboxDefinitions(), uoJobDefinitions()...)
	turn := wc008NativeTurn(t, w, ProgressAccept, defs)
	id, err := enqueueUOWebhook(t.Context(), turn, uoWebhookIntent{id: "delivery", webhook: "hook", eventType: "download.completed", data: "{}", authority: 1, revision: 1, nowMS: 100})
	if err != nil || id != "delivery" {
		t.Fatal(id, err)
	}
	uoFlagsCommit(t, turn)
	job := uoOperationalJob{family: uoJobWebhook, key1: "delivery", lease: uoJobLease{revision: 1, nextMS: sql.NullInt64{Int64: 100, Valid: true}}}
	turn = wc008NativeTurn(t, w, ProgressAccept, defs)
	claimed, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 7, durationMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	job.lease = claimed
	// A source authority change rejects the stale completion without releasing
	// the leased obligation or writing a false terminal result.
	wc008NativeExec(t, w, `UPDATE webhooks SET enabled=0,authority_generation=2 WHERE id='hook'`)
	turn = wc008NativeTurn(t, w, ProgressAccept, defs)
	outcome := uoWebhookOutcome{job: job, webhook: "hook", authority: 1, webhookRevision: 1, nowMS: 200, status: 204, delivered: true}
	if out, err := completeUOWebhook(t.Context(), turn, outcome); err == nil || out != (uoJobLease{}) {
		t.Fatal("stale authority accepted", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE webhooks SET enabled=1,authority_generation=1 WHERE id='hook'`)
	turn = wc008NativeTurn(t, w, ProgressAccept, defs)
	finished, err := completeUOWebhook(t.Context(), turn, outcome)
	if err != nil || finished.owner != 0 || finished.nextMS.Valid || finished.revision != 3 {
		t.Fatal(finished, err)
	}
	uoFlagsCommit(t, turn)
	var state string
	var status, expiry, owner int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT state,status,job_lease_owner,job_lease_expires_ms FROM webhook_deliveries WHERE id='delivery'`).Scan(&state, &status, &owner, &expiry); err != nil || state != "delivered" || status != 204 || owner != 0 || expiry != 0 {
		t.Fatal(state, status, owner, expiry, err)
	}
	turn = wc008NativeTurn(t, w, ProgressAccept, defs)
	if _, err := reclaimUOWebhook(t.Context(), turn, "delivery", 3, 200, 199); err == nil {
		t.Fatal("premature retention accepted")
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	turn = wc008NativeTurn(t, w, ProgressAccept, defs)
	if _, err := reclaimUOWebhook(t.Context(), turn, "delivery", 3, 200, 300); err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
}

func TestUOOutboxServiceGenerationFence(t *testing.T) {
	w := wc008NativeExecutor(t)
	wc008NativeExec(t, w, `INSERT INTO identity_service_outbox(notice_id,operation_id,server_id,claim_generation,credential_generation,payload,signature,created_ms,job_next_attempt_ms) VALUES('notice','operation','server','claim1','credential1',x'01',zeroblob(64),1,100)`)
	defs := append(uoOutboxDefinitions(), uoJobDefinitions()...)
	job := uoOperationalJob{family: uoJobServiceOutbox, key1: "notice", lease: uoJobLease{revision: 1, nextMS: sql.NullInt64{Int64: 100, Valid: true}}}
	turn := wc008NativeTurn(t, w, ProgressAccept, defs)
	claimed, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 9, durationMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	job.lease = claimed
	q := uoServiceOutboxOutcome{job: job, operation: "operation", server: "server", claimGeneration: "claim1", credentialGeneration: "wrong", nowMS: 200}
	turn = wc008NativeTurn(t, w, ProgressAccept, defs)
	if out, err := completeUOServiceOutbox(t.Context(), turn, q); err == nil || out != (uoJobLease{}) {
		t.Fatal("wrong credential generation accepted", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	q.credentialGeneration = "credential1"
	turn = wc008NativeTurn(t, w, ProgressAccept, defs)
	finished, err := completeUOServiceOutbox(t.Context(), turn, q)
	if err != nil || finished.nextMS.Valid || finished.owner != 0 {
		t.Fatal(finished, err)
	}
	uoFlagsCommit(t, turn)
}

func TestUOOutboxFailedAttemptKeepsRetryAndStaleNoopRenewalFails(t *testing.T) {
	w := wc008NativeExecutor(t)
	wc008NativeExec(t, w, `INSERT INTO webhooks(id,name,url,secret,events,created_ms,updated_ms) VALUES('hook','Fixture','https://example.invalid','test-only','[]',1,1)`)
	wc008NativeExec(t, w, `INSERT INTO webhook_deliveries(id,webhook_id,type,created_ms,job_next_attempt_ms,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_revision) VALUES('delivery','hook','download.completed',1,100,7,1,1000,1,2)`)
	defs := append(uoOutboxDefinitions(), uoJobDefinitions()...)
	job := uoOperationalJob{family: uoJobWebhook, key1: "delivery", lease: uoJobLease{owner: 7, generation: 1, expiresMS: 1000, attempts: 1, revision: 2, nextMS: sql.NullInt64{Int64: 100, Valid: true}}}
	turn := wc008NativeTurn(t, w, ProgressAccept, defs)
	finished, err := completeUOWebhook(t.Context(), turn, uoWebhookOutcome{job: job, webhook: "hook", authority: 1, webhookRevision: 1, nowMS: 200, status: 503, errorCode: "status_503"})
	if err != nil || !finished.nextMS.Valid || finished.nextMS.Int64 != 30200 || finished.owner != 0 {
		t.Fatal(finished, err)
	}
	uoFlagsCommit(t, turn)
	var state string
	var finishedMS, failures int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT state,finished_ms,(SELECT failures FROM webhooks WHERE id='hook') FROM webhook_deliveries WHERE id='delivery'`).Scan(&state, &finishedMS, &failures); err != nil || state != "pending" || finishedMS != 0 || failures != 1 {
		t.Fatal(state, finishedMS, failures, err)
	}
	// This renewal's pure fold is a no-op, but its old native claim is gone.
	turn = wc008NativeTurn(t, w, ProgressAccept, defs)
	if out, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseRenew, nowMS: 300, owner: 7, generation: 1, durationMS: 100}); err == nil || out != (uoJobLease{}) {
		t.Fatal("stale no-op renewal accepted", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}
