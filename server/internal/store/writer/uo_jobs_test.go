package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func uoJobExecutor(t *testing.T) *executor {
	t.Helper()
	return wc008NativeExecutor(t)
}

func uoJobSeed(t *testing.T, w *executor) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO webhooks(id,name,url,secret,events,created_ms,updated_ms) VALUES('hook-one','hook','https://example.invalid/hook','secret','[]',1,1)`)
	wc008NativeExec(t, w, `INSERT INTO personal_jobs(id,profile_id,operation_id,request_hash,principal,selector,args,state,revision,created_ms,updated_ms,job_next_attempt_ms)
VALUES('job-personal','profile-one','op-personal','hash-personal','profile-one','{}','{"query":"one"}','queued',1,1,1,100)`)
	wc008NativeExec(t, w, `INSERT INTO webhook_deliveries(id,webhook_id,type,subject,data,state,created_ms,job_next_attempt_ms)
VALUES('job-webhook','hook-one','title.added','subject-one','{"n":1}','pending',1,100)`)
	wc008NativeExec(t, w, `INSERT INTO notice_broadcast_work(scope,operation_id,digest,upper_account,upper_profile,expires_ms,job_next_attempt_ms)
VALUES('scope-one','op-notice','digest-one','upper-account','upper-profile',9999,100)`)
	wc008NativeExec(t, w, `INSERT INTO identity_service_outbox(notice_id,operation_id,server_id,claim_generation,credential_generation,payload,signature,created_ms,job_next_attempt_ms)
VALUES('job-outbox','op-outbox','server-one','claim-one','credential-one',?,?,1,100)`, []byte("payload-one"), []byte("signature-one"))
}

func uoJobFamilies() []uoJobFamily {
	return []uoJobFamily{uoJobPersonal, uoJobWebhook, uoJobNotice, uoJobServiceOutbox}
}

func uoJobKey(f uoJobFamily) (string, string) {
	switch f {
	case uoJobPersonal:
		return "job-personal", ""
	case uoJobWebhook:
		return "job-webhook", ""
	case uoJobNotice:
		return "scope-one", "op-notice"
	case uoJobServiceOutbox:
		return "job-outbox", ""
	default:
		return "", ""
	}
}

func uoJobFamilyName(f uoJobFamily) string {
	switch f {
	case uoJobPersonal:
		return "personal"
	case uoJobWebhook:
		return "webhook"
	case uoJobNotice:
		return "notice"
	case uoJobServiceOutbox:
		return "service_outbox"
	default:
		return "unknown"
	}
}

func uoJobLeaseRow(t *testing.T, w *executor, f uoJobFamily, key1, key2 string) uoJobLease {
	t.Helper()
	shape, ok := uoJobShape(f)
	if !ok {
		t.Fatal("unknown family", f)
	}
	query := "SELECT job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,COALESCE(job_next_attempt_ms,-1)," + shape.revision + " FROM " + shape.table + " WHERE " + shape.key1 + "=?"
	args := []any{key1}
	if shape.key2 != "" {
		query += " AND " + shape.key2 + "=?"
		args = append(args, key2)
	}
	var lease uoJobLease
	var next int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&lease.owner, &lease.generation, &lease.expiresMS, &lease.attempts, &next, &lease.revision); err != nil {
		t.Fatal(err)
	}
	if next >= 0 {
		lease.nextMS = sql.NullInt64{Int64: next, Valid: true}
	}
	return lease
}

func uoJobVariableBytes(t *testing.T, w *executor, f uoJobFamily, key1, key2 string) int64 {
	t.Helper()
	shape, ok := uoJobShape(f)
	if !ok {
		t.Fatal("unknown family", f)
	}
	expressions := make([]string, len(shape.byteColumns))
	for i, column := range shape.byteColumns {
		expressions[i] = "length(" + column + ")"
	}
	query := "SELECT " + strings.Join(expressions, ",") + " FROM " + shape.table + " WHERE " + shape.key1 + "=?"
	args := []any{key1}
	if shape.key2 != "" {
		query += " AND " + shape.key2 + "=?"
		args = append(args, key2)
	}
	values := make([]sql.NullInt64, len(expressions))
	dest := make([]any, len(values))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(dest...); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, value := range values {
		if value.Valid {
			total += value.Int64
		}
	}
	return total
}

func uoJobMarkTerminal(t *testing.T, w *executor, f uoJobFamily, key1, key2 string) {
	t.Helper()
	switch f {
	case uoJobPersonal:
		wc008NativeExec(t, w, `UPDATE personal_jobs SET state='complete' WHERE id=?`, key1)
	case uoJobWebhook:
		wc008NativeExec(t, w, `UPDATE webhook_deliveries SET state='delivered' WHERE id=?`, key1)
	case uoJobNotice:
		wc008NativeExec(t, w, `UPDATE notice_broadcast_work SET delivered=1 WHERE scope=? AND operation_id=?`, key1, key2)
	case uoJobServiceOutbox:
		wc008NativeExec(t, w, `UPDATE identity_service_outbox SET delivered_ms=1 WHERE notice_id=?`, key1)
	default:
		t.Fatal("unknown family", f)
	}
}

func uoJobTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	return wc008NativeTurn(t, w, ClaimOperationalJob, uoJobDefinitions())
}

func uoJobCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func TestUOJobLeaseClaimRenewAndStaleWorker(t *testing.T) {
	old := uoJobLease{nextMS: sql.NullInt64{Int64: 100, Valid: true}, revision: 1}
	claimed, err := old.transition(uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 7, durationMS: 1000})
	if err != nil || claimed.owner != 7 || claimed.generation != 1 || claimed.expiresMS != 1100 || claimed.attempts != 1 {
		t.Fatal(claimed, err)
	}
	renewed, err := claimed.transition(uoLeaseChange{action: uoLeaseRenew, nowMS: 200, owner: 7, generation: 1, durationMS: 2000})
	if err != nil || renewed.expiresMS != 2200 {
		t.Fatal(renewed, err)
	}
	if _, err := renewed.transition(uoLeaseChange{action: uoLeaseFinish, nowMS: 300, owner: 8, generation: 1}); err == nil {
		t.Fatal("foreign worker completed lease")
	}
	if _, err := renewed.transition(uoLeaseChange{action: uoLeaseFinish, nowMS: 300, owner: 7, generation: 2}); err == nil {
		t.Fatal("successor worker completed lease")
	}
	finished, err := renewed.transition(uoLeaseChange{action: uoLeaseFinish, nowMS: 300, owner: 7, generation: 1})
	if err != nil || finished.owner != 0 || finished.expiresMS != 0 || finished.nextMS.Valid {
		t.Fatal(finished, err)
	}
	if _, err := finished.transition(uoLeaseChange{action: uoLeaseClaim, nowMS: 400, owner: 9, durationMS: 100}); err == nil {
		t.Fatal("held job became runnable")
	}
}

func TestUOJobLeaseExpiryAndRetry(t *testing.T) {
	old := uoJobLease{owner: 1, generation: 2, expiresMS: 100, attempts: 3, revision: 4, nextMS: sql.NullInt64{Int64: 10, Valid: true}}
	if _, err := old.transition(uoLeaseChange{action: uoLeaseRenew, nowMS: 100, owner: 1, generation: 2, durationMS: 100}); err == nil {
		t.Fatal("expired renewal")
	}
	next, err := old.transition(uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 2, durationMS: 100})
	if err != nil || next.generation != 3 || next.attempts != 4 {
		t.Fatal(next, err)
	}
	retry, err := next.transition(uoLeaseChange{action: uoLeaseFinish, nowMS: 150, owner: 2, generation: 3, nextMS: sql.NullInt64{Int64: 500, Valid: true}})
	if err != nil || retry.nextMS.Int64 != 500 || retry.owner != 0 {
		t.Fatal(retry, err)
	}
	if _, err := retry.transition(uoLeaseChange{action: uoLeaseClaim, nowMS: 499, owner: 3, durationMS: 100}); err == nil {
		t.Fatal("retry became due early")
	}
}

func TestUOJobLeaseNativeClaimRenewFinish(t *testing.T) {
	for _, family := range uoJobFamilies() {
		t.Run(uoJobFamilyName(family), func(t *testing.T) {
			w := uoJobExecutor(t)
			uoJobSeed(t, w)
			key1, key2 := uoJobKey(family)
			initial := uoJobLeaseRow(t, w, family, key1, key2)
			job := uoOperationalJob{family: family, key1: key1, key2: key2, lease: initial}

			turn := uoJobTurn(t, w)
			claimed, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 7, durationMS: 1000})
			if err != nil || claimed.owner != 7 || claimed.generation != 1 || claimed.expiresMS != 1100 || claimed.attempts != 1 || claimed.revision != initial.revision+1 || claimed.nextMS.Valid {
				t.Fatal("claim", claimed, err)
			}
			uoJobCommit(t, turn)
			if got := uoJobLeaseRow(t, w, family, key1, key2); got != claimed {
				t.Fatal("claimed row", got, claimed)
			}

			job.lease = claimed
			turn = uoJobTurn(t, w)
			renewed, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseRenew, nowMS: 200, owner: 7, generation: 1, durationMS: 2000})
			if err != nil || renewed.owner != 7 || renewed.generation != 1 || renewed.expiresMS != 2200 || renewed.attempts != 1 || renewed.revision != initial.revision+2 {
				t.Fatal("renew", renewed, err)
			}
			uoJobCommit(t, turn)
			if got := uoJobLeaseRow(t, w, family, key1, key2); got != renewed {
				t.Fatal("renewed row", got, renewed)
			}

			job.lease = renewed
			turn = uoJobTurn(t, w)
			finished, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseFinish, nowMS: 300, owner: 7, generation: 1})
			if err != nil || finished.owner != 0 || finished.expiresMS != 0 || finished.nextMS.Valid || finished.generation != 1 || finished.attempts != 1 || finished.revision != initial.revision+3 {
				t.Fatal("finish", finished, err)
			}
			uoJobCommit(t, turn)
			if got := uoJobLeaseRow(t, w, family, key1, key2); got != finished {
				t.Fatal("finished row", got, finished)
			}
			var events int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM api_events`).Scan(&events); err != nil || events != 0 {
				t.Fatal("lease emitted a lease-only hint", events, err)
			}
		})
	}
}

func TestUOJobLeaseStaleWorkerCannotMutate(t *testing.T) {
	for _, family := range uoJobFamilies() {
		t.Run(uoJobFamilyName(family), func(t *testing.T) {
			w := uoJobExecutor(t)
			uoJobSeed(t, w)
			key1, key2 := uoJobKey(family)
			job := uoOperationalJob{family: family, key1: key1, key2: key2, lease: uoJobLeaseRow(t, w, family, key1, key2)}

			turn := uoJobTurn(t, w)
			claimed, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 7, durationMS: 1000})
			if err != nil {
				t.Fatal(err)
			}
			uoJobCommit(t, turn)

			for _, change := range []uoLeaseChange{
				{action: uoLeaseFinish, nowMS: 200, owner: 8, generation: 1},
				{action: uoLeaseFinish, nowMS: 200, owner: 7, generation: 2},
				{action: uoLeaseRenew, nowMS: 200, owner: 7, generation: 2, durationMS: 1000},
				{action: uoLeaseClaim, nowMS: 200, owner: 9, durationMS: 1000},
			} {
				job.lease = claimed
				turn = uoJobTurn(t, w)
				if out, err := applyUOJobLease(t.Context(), turn, job, change); !errors.Is(err, ErrConflict) || out != (uoJobLease{}) {
					t.Fatal("stale worker mutated lease", change, out, err)
				}
				if _, err := turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
				if got := uoJobLeaseRow(t, w, family, key1, key2); got != claimed {
					t.Fatal("stale worker changed row", got, claimed)
				}
			}

			job.lease = claimed
			turn = uoJobTurn(t, w)
			finished, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseFinish, nowMS: 300, owner: 7, generation: 1, nextMS: sql.NullInt64{Int64: 400, Valid: true}})
			if err != nil {
				t.Fatal(err)
			}
			uoJobCommit(t, turn)

			job.lease = finished
			turn = uoJobTurn(t, w)
			successor, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseClaim, nowMS: 400, owner: 9, durationMS: 100})
			if err != nil || successor.generation != 2 || successor.owner != 9 {
				t.Fatal("successor claim", successor, err)
			}
			uoJobCommit(t, turn)

			stale := claimed
			turn = uoJobTurn(t, w)
			job.lease = stale
			if _, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseFinish, nowMS: 500, owner: 7, generation: 1}); !errors.Is(err, ErrConflict) {
				t.Fatal("stale generation finished successor", err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if got := uoJobLeaseRow(t, w, family, key1, key2); got != successor {
				t.Fatal("stale generation changed successor", got, successor)
			}
		})
	}
}

func TestUOJobLeaseRenewNoopDoesNotRewrite(t *testing.T) {
	for _, family := range uoJobFamilies() {
		t.Run(uoJobFamilyName(family), func(t *testing.T) {
			w := uoJobExecutor(t)
			uoJobSeed(t, w)
			key1, key2 := uoJobKey(family)
			job := uoOperationalJob{family: family, key1: key1, key2: key2, lease: uoJobLeaseRow(t, w, family, key1, key2)}

			turn := uoJobTurn(t, w)
			claimed, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 7, durationMS: 1000})
			if err != nil {
				t.Fatal(err)
			}
			uoJobCommit(t, turn)

			job.lease = claimed
			turn = uoJobTurn(t, w)
			var before, after int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			noop, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseRenew, nowMS: 200, owner: 7, generation: 1, durationMS: 100})
			if err != nil || noop != claimed {
				t.Fatal("shorter renewal", noop, err)
			}
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
				t.Fatal("no-op renewal rewrote the row", before, after, err)
			}
			uoJobCommit(t, turn)
			if got := uoJobLeaseRow(t, w, family, key1, key2); got != claimed {
				t.Fatal("no-op renewal changed row", got, claimed)
			}
		})
	}
}

func TestUOJobLeaseCanceledFinishAndRollback(t *testing.T) {
	for _, family := range uoJobFamilies() {
		t.Run(uoJobFamilyName(family), func(t *testing.T) {
			w := uoJobExecutor(t)
			uoJobSeed(t, w)
			key1, key2 := uoJobKey(family)
			job := uoOperationalJob{family: family, key1: key1, key2: key2, lease: uoJobLeaseRow(t, w, family, key1, key2)}

			turn := uoJobTurn(t, w)
			claimed, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 7, durationMS: 1000})
			if err != nil {
				t.Fatal(err)
			}
			uoJobCommit(t, turn)

			job.lease = claimed
			turn = uoJobTurn(t, w)
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if out, err := applyUOJobLease(canceled, turn, job, uoLeaseChange{action: uoLeaseFinish, nowMS: 200, owner: 7, generation: 1}); err == nil || out != (uoJobLease{}) {
				t.Fatal("canceled finish succeeded", out, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if got := uoJobLeaseRow(t, w, family, key1, key2); got != claimed {
				t.Fatal("canceled finish released custody", got, claimed)
			}

			// A successful finish followed by a failing successor in the same turn
			// must roll back the whole nonempty prefix.
			job.lease = claimed
			turn = uoJobTurn(t, w)
			if _, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseFinish, nowMS: 200, owner: 7, generation: 1}); err != nil {
				t.Fatal(err)
			}
			job.lease = claimed
			if _, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseFinish, nowMS: 200, owner: 8, generation: 1}); !errors.Is(err, ErrConflict) {
				t.Fatal("failing successor did not conflict", err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if got := uoJobLeaseRow(t, w, family, key1, key2); got != claimed {
				t.Fatal("rollback left a half-finished lease", got, claimed)
			}
		})
	}
}

func TestUOJobLeaseReclaimTerminalRowAndBytes(t *testing.T) {
	for _, family := range uoJobFamilies() {
		t.Run(uoJobFamilyName(family), func(t *testing.T) {
			w := uoJobExecutor(t)
			uoJobSeed(t, w)
			key1, key2 := uoJobKey(family)
			job := uoOperationalJob{family: family, key1: key1, key2: key2, lease: uoJobLeaseRow(t, w, family, key1, key2)}

			turn := uoJobTurn(t, w)
			claimed, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 7, durationMS: 1000})
			if err != nil {
				t.Fatal(err)
			}
			uoJobCommit(t, turn)
			job.lease = claimed
			turn = uoJobTurn(t, w)
			finished, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseFinish, nowMS: 200, owner: 7, generation: 1})
			if err != nil {
				t.Fatal(err)
			}
			uoJobCommit(t, turn)
			uoJobMarkTerminal(t, w, family, key1, key2)
			expectedBytes := uoJobVariableBytes(t, w, family, key1, key2)

			job.lease = finished
			turn = uoJobTurn(t, w)
			reclaimed, err := applyUOJobReclaim(t.Context(), turn, job)
			if err != nil || !reclaimed.removed || reclaimed.removedVariableBytes != expectedBytes || expectedBytes <= 0 {
				t.Fatal("terminal reclaim", reclaimed, expectedBytes, err)
			}
			uoJobCommit(t, turn)

			shape, _ := uoJobShape(family)
			query := "SELECT count(*) FROM " + shape.table + " WHERE " + shape.key1 + "=?"
			args := []any{key1}
			if shape.key2 != "" {
				query += " AND " + shape.key2 + "=?"
				args = append(args, key2)
			}
			var rows int64
			if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&rows); err != nil || rows != 0 {
				t.Fatal("terminal row remained", rows, err)
			}
			turn = uoJobTurn(t, w)
			if second, err := applyUOJobReclaim(t.Context(), turn, job); !errors.Is(err, ErrConflict) || second != (uoJobReclaimResult{}) {
				t.Fatal("second reclaim refunded absent row", second, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var events int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM api_events`).Scan(&events); err != nil || events != 0 {
				t.Fatal("reclaim emitted a lease-only hint", events, err)
			}
		})
	}
}

func TestUOJobLeaseReclaimRefusesLivePendingAndStale(t *testing.T) {
	for _, family := range uoJobFamilies() {
		t.Run(uoJobFamilyName(family), func(t *testing.T) {
			w := uoJobExecutor(t)
			uoJobSeed(t, w)
			key1, key2 := uoJobKey(family)
			job := uoOperationalJob{family: family, key1: key1, key2: key2, lease: uoJobLeaseRow(t, w, family, key1, key2)}

			turn := uoJobTurn(t, w)
			claimed, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseClaim, nowMS: 100, owner: 7, durationMS: 1000})
			if err != nil {
				t.Fatal(err)
			}
			uoJobCommit(t, turn)

			// A live lease is never a reclaim candidate.
			job.lease = claimed
			turn = uoJobTurn(t, w)
			if out, err := applyUOJobReclaim(t.Context(), turn, job); !errors.Is(err, ErrInvalid) || out != (uoJobReclaimResult{}) {
				t.Fatal("live lease reclaimed", out, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			// A retry is still runnable intent even when the domain outcome is terminal.
			job.lease = claimed
			turn = uoJobTurn(t, w)
			retry, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseFinish, nowMS: 200, owner: 7, generation: 1, nextMS: sql.NullInt64{Int64: 500, Valid: true}})
			if err != nil {
				t.Fatal(err)
			}
			uoJobCommit(t, turn)
			uoJobMarkTerminal(t, w, family, key1, key2)
			job.lease = retry
			turn = uoJobTurn(t, w)
			if out, err := applyUOJobReclaim(t.Context(), turn, job); !errors.Is(err, ErrInvalid) || out != (uoJobReclaimResult{}) {
				t.Fatal("pending retry reclaimed", out, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			// Even a caller that hides the pending retry deletes nothing.
			job.lease = uoJobLease{generation: retry.generation, attempts: retry.attempts, revision: retry.revision}
			turn = uoJobTurn(t, w)
			if out, err := applyUOJobReclaim(t.Context(), turn, job); !errors.Is(err, ErrConflict) || out != (uoJobReclaimResult{}) {
				t.Fatal("hidden pending retry reclaimed", out, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			// A stale generation/attempt/revision never refunds bytes.
			job.lease = retry
			turn = uoJobTurn(t, w)
			reclaimedAgain, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseClaim, nowMS: 600, owner: 9, durationMS: 100})
			if err != nil {
				t.Fatal(err)
			}
			uoJobCommit(t, turn)
			job.lease = reclaimedAgain
			turn = uoJobTurn(t, w)
			terminal, err := applyUOJobLease(t.Context(), turn, job, uoLeaseChange{action: uoLeaseFinish, nowMS: 650, owner: 9, generation: 2})
			if err != nil {
				t.Fatal(err)
			}
			uoJobCommit(t, turn)
			uoJobMarkTerminal(t, w, family, key1, key2)
			for _, stale := range []uoJobLease{
				{generation: terminal.generation - 1, attempts: terminal.attempts, revision: terminal.revision},
				{generation: terminal.generation, attempts: terminal.attempts - 1, revision: terminal.revision},
				{generation: terminal.generation, attempts: terminal.attempts, revision: terminal.revision - 1},
			} {
				job.lease = stale
				turn = uoJobTurn(t, w)
				if out, err := applyUOJobReclaim(t.Context(), turn, job); !errors.Is(err, ErrConflict) || out != (uoJobReclaimResult{}) {
					t.Fatal("stale reclaim refunded", stale, out, err)
				}
				if _, err := turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
			}

			if family == uoJobPersonal {
				// A terminal parent with owned child rows is not one-row
				// reclaimable; the child family must refund those bytes first.
				wc008NativeExec(t, w, `INSERT INTO personal_job_items(job_id,ordinal,item_id) VALUES('job-personal',0,1)`)
				job.lease = terminal
				turn = uoJobTurn(t, w)
				if out, err := applyUOJobReclaim(t.Context(), turn, job); !errors.Is(err, ErrConflict) || out != (uoJobReclaimResult{}) {
					t.Fatal("parent with child rows reclaimed", out, err)
				}
				if _, err := turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
				var children int64
				if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM personal_job_items WHERE job_id='job-personal'`).Scan(&children); err != nil || children != 1 {
					t.Fatal("child row deleted", children, err)
				}
				wc008NativeExec(t, w, `DELETE FROM personal_job_items WHERE job_id='job-personal'`)
				turn = uoJobTurn(t, w)
				reclaimed, err := applyUOJobReclaim(t.Context(), turn, job)
				if err != nil || !reclaimed.removed {
					t.Fatal("parent reclaim after child removal", reclaimed, err)
				}
				uoJobCommit(t, turn)
			}
		})
	}
}

func TestUOJobLeaseClosedFamilyCandidates(t *testing.T) {
	want := map[uoJobFamily]struct{ table, due, revision, key1 string }{
		uoJobPersonal:      {"personal_jobs", "personal_jobs_governor_due", "revision", "id"},
		uoJobWebhook:       {"webhook_deliveries", "webhook_deliveries_governor_due", "job_revision", "id"},
		uoJobNotice:        {"notice_broadcast_work", "notice_broadcast_work_governor_due", "revision", "scope"},
		uoJobServiceOutbox: {"identity_service_outbox", "identity_service_outbox_governor_due", "job_revision", "notice_id"},
	}
	for family, expected := range want {
		shape, ok := uoJobShape(family)
		if !ok || shape.table != expected.table || shape.key1 != expected.key1 || shape.revision != expected.revision {
			t.Fatal("family shape", family, shape, ok)
		}
		if uoJobDueIndex(family) != expected.due {
			t.Fatal("family due index", family, uoJobDueIndex(family), expected.due)
		}
		if shape.pkIndex == "" && shape.key2 == "" || shape.pkIndex != "" && shape.key2 != "" {
			t.Fatal("family must name exactly one primary key shape", family, shape)
		}
	}
	for _, family := range []uoJobFamily{0, 5, 255} {
		if _, ok := uoJobShape(family); ok || uoJobDueIndex(family) != "" || uoJobLeaseID(family) != "" || uoJobReclaimID(family) != "" {
			t.Fatal("unknown family was selectable", family)
		}
	}

	definitions := uoJobDefinitions()
	if len(definitions) != 3*len(uoJobFamilies())+len(uoJobPersonalChildProbes) {
		t.Fatal("unexpected statement count", len(definitions))
	}
	familyIDs := map[string]bool{}
	for _, family := range uoJobFamilies() {
		familyIDs[string(uoJobLeaseID(family))] = true
		familyIDs[string(uoJobCurrentID(family))] = true
		familyIDs[string(uoJobReclaimID(family))] = true
	}
	seen := map[string]bool{}
	for _, definition := range definitions {
		if !definition.Plan.Hot || len(definition.Plan.PrimaryKeys)+len(definition.Plan.Indexes) != 1 {
			t.Fatal("statement is not one indexed hot path", definition.ID)
		}
		if familyIDs[string(definition.ID)] {
			upper := strings.ToUpper(definition.SQL)
			if strings.Contains(upper, " ORDER BY ") || strings.Contains(upper, " LIMIT ") || strings.Contains(upper, " OFFSET ") {
				t.Fatal("writer selected a due candidate or priority", definition.ID)
			}
		}
		if seen[string(definition.ID)] {
			t.Fatal("duplicate statement", definition.ID)
		}
		seen[string(definition.ID)] = true
	}
	for _, family := range uoJobFamilies() {
		for _, id := range []stmt.ID{uoJobLeaseID(family), uoJobCurrentID(family), uoJobReclaimID(family)} {
			if !seen[string(id)] {
				t.Fatal("missing closed family statement", id)
			}
		}
	}
	for _, probe := range uoJobPersonalChildProbes {
		if !seen[string(probe.id)] {
			t.Fatal("missing closed child probe", probe.id)
		}
	}
}
