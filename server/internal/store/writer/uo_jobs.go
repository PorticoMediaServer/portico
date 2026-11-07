package writer

import (
	"context"
	"database/sql"
	"math"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// A closed operational family, never a client-selected SQL table or priority.
// Other domains keep their own source/physical custody and job transitions.
type uoJobFamily uint8

const (
	uoJobPersonal      uoJobFamily = 1
	uoJobWebhook       uoJobFamily = 2
	uoJobNotice        uoJobFamily = 3
	uoJobServiceOutbox uoJobFamily = 4
)

type uoJobLease struct {
	owner, generation, expiresMS, attempts, revision int64
	nextMS                                           sql.NullInt64
}
type uoOperationalJob struct {
	family     uoJobFamily
	key1, key2 string
	lease      uoJobLease
}
type uoLeaseAction uint8

const (
	uoLeaseClaim  uoLeaseAction = 1
	uoLeaseRenew  uoLeaseAction = 2
	uoLeaseFinish uoLeaseAction = 3
)

type uoLeaseChange struct {
	action                               uoLeaseAction
	nowMS, owner, generation, durationMS int64
	nextMS                               sql.NullInt64
}

func (old uoJobLease) transition(q uoLeaseChange) (uoJobLease, error) {
	if old.owner < 0 || old.generation < 0 || old.expiresMS < 0 || old.attempts < 0 || old.attempts > math.MaxUint32 || old.revision < 0 ||
		old.nextMS.Valid && old.nextMS.Int64 < 0 || !old.nextMS.Valid && old.nextMS.Int64 != 0 || q.nowMS < 0 || q.owner <= 0 ||
		old.revision == math.MaxInt64 || !q.nextMS.Valid && q.nextMS.Int64 != 0 || q.nextMS.Valid && q.nextMS.Int64 < q.nowMS {
		return uoJobLease{}, ErrInvalid
	}
	after := old
	switch q.action {
	case uoLeaseClaim:
		if !old.nextMS.Valid || q.nowMS < old.nextMS.Int64 || q.nowMS < old.expiresMS || old.generation == math.MaxInt64 || old.attempts == math.MaxUint32 || q.generation != 0 || q.durationMS <= 0 || q.durationMS > 300_000 || q.durationMS > math.MaxInt64-q.nowMS || q.nextMS.Valid {
			return uoJobLease{}, ErrConflict
		}
		after.owner, after.generation, after.expiresMS, after.attempts = q.owner, old.generation+1, q.nowMS+q.durationMS, old.attempts+1
	case uoLeaseRenew:
		if old.owner != q.owner || old.generation != q.generation || q.generation <= 0 || q.nowMS >= old.expiresMS || q.durationMS <= 0 || q.durationMS > 300_000 || q.durationMS > math.MaxInt64-q.nowMS || q.nextMS.Valid {
			return uoJobLease{}, ErrConflict
		}
		after.expiresMS = max(old.expiresMS, q.nowMS+q.durationMS)
		if after.expiresMS == old.expiresMS {
			return old, nil
		}
	case uoLeaseFinish:
		if old.owner != q.owner || old.generation != q.generation || q.generation <= 0 || q.nowMS >= old.expiresMS || q.durationMS != 0 {
			return uoJobLease{}, ErrConflict
		}
		after.owner, after.expiresMS, after.nextMS = 0, 0, q.nextMS
	default:
		return uoJobLease{}, ErrInvalid
	}
	after.revision++
	return after, nil
}

// Native lease effects only. Parent owns current job/source authority, original
// receipt classification, complete domain result and physical terminal. Finish
// clears runnable intent only in the same turn that saves its domain outcome.
// Cancellation never releases a native helper/IO grant: GV retains it to exit.
func applyUOJobLease(ctx context.Context, turn *commandTurn, job uoOperationalJob, change uoLeaseChange) (out uoJobLease, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	if job.key1 == "" || len(job.key1) > 128 || !utf8.ValidString(job.key1) || len(job.key2) > 128 || !utf8.ValidString(job.key2) || (job.family == uoJobNotice) != (job.key2 != "") {
		return out, ErrInvalid
	}
	_, _, _, _, ok := uoJobShape(job.family)
	if !ok {
		return out, ErrInvalid
	}
	after, err := job.lease.transition(change)
	if err != nil {
		return out, err
	}
	nullable := func(v sql.NullInt64) any {
		if v.Valid {
			return v.Int64
		}
		return nil
	}
	if after == job.lease {
		args := []any{job.key1}
		if job.family == uoJobNotice {
			args = append(args, job.key2)
		}
		old := job.lease
		args = append(args, old.owner, old.generation, old.expiresMS, old.attempts, nullable(old.nextMS), old.revision)
		if _, err := wc008DraftInteger(ctx, turn, uoJobCurrentID(job.family), args...); err != nil {
			return out, err
		}
		return after, turn.checkLifetimes(ctx)
	}
	args := []any{after.owner, after.generation, after.expiresMS, after.attempts, nullable(after.nextMS), after.revision, job.key1}
	if job.family == uoJobNotice {
		args = append(args, job.key2)
	}
	old := job.lease
	args = append(args, old.owner, old.generation, old.expiresMS, old.attempts, nullable(old.nextMS), old.revision)
	row, err := wc008DraftQuery(ctx, turn, uoJobLeaseID(job.family), args...)
	if err != nil {
		return out, err
	}
	var revision int64
	if err = row.Scan(&revision); err != nil {
		return out, err
	}
	if revision != after.revision {
		return out, ErrResolveRequired
	}
	return after, turn.checkLifetimes(ctx)
}

func uoJobShape(family uoJobFamily) (table, key1, key2, revision string, ok bool) {
	switch family {
	case uoJobPersonal:
		return "personal_jobs", "id", "", "revision", true
	case uoJobWebhook:
		return "webhook_deliveries", "id", "", "job_revision", true
	case uoJobNotice:
		return "notice_broadcast_work", "scope", "operation_id", "revision", true
	case uoJobServiceOutbox:
		return "identity_service_outbox", "notice_id", "", "job_revision", true
	default:
		return "", "", "", "", false
	}
}
func uoJobLeaseID(f uoJobFamily) stmt.ID {
	switch f {
	case uoJobPersonal:
		return "uo.job.personal.lease"
	case uoJobWebhook:
		return "uo.job.webhook.lease"
	case uoJobNotice:
		return "uo.job.notice.lease"
	case uoJobServiceOutbox:
		return "uo.job.service_outbox.lease"
	default:
		return ""
	}
}

func uoJobCurrentID(f uoJobFamily) stmt.ID { return stmt.ID(string(uoJobLeaseID(f)) + ".current") }

func uoJobDefinitions() []stmt.Definition {
	var out []stmt.Definition
	for _, f := range []uoJobFamily{uoJobPersonal, uoJobWebhook, uoJobNotice, uoJobServiceOutbox} {
		table, key1, key2, rev, _ := uoJobShape(f)
		// Identifiers come only from the closed compiled family above.
		query := "UPDATE " + table + " SET job_lease_owner=?,job_lease_generation=?,job_lease_expires_ms=?,job_attempts=?,job_next_attempt_ms=?," + rev + "=? WHERE " + key1 + "=?"
		params := []stmt.Param{{Name: "next_owner", Type: stmt.Integer}, {Name: "next_generation", Type: stmt.Integer}, {Name: "next_expiry", Type: stmt.Integer}, {Name: "next_attempts", Type: stmt.Integer}, {Name: "next_attempt", Type: stmt.Integer, Nullable: true}, {Name: "next_revision", Type: stmt.Integer}, {Name: "key1", Type: stmt.Text}}
		if key2 != "" {
			query += " AND " + key2 + "=?"
			params = append(params, stmt.Param{Name: "key2", Type: stmt.Text})
		}
		query += " AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=? AND job_next_attempt_ms IS ? AND " + rev + "=? RETURNING " + rev
		params = append(params, stmt.Param{Name: "old_owner", Type: stmt.Integer}, stmt.Param{Name: "old_generation", Type: stmt.Integer}, stmt.Param{Name: "old_expiry", Type: stmt.Integer}, stmt.Param{Name: "old_attempts", Type: stmt.Integer}, stmt.Param{Name: "old_attempt", Type: stmt.Integer, Nullable: true}, stmt.Param{Name: "old_revision", Type: stmt.Integer})
		out = append(out, stmt.Definition{ID: uoJobLeaseID(f), Owner: "store/writer UO operations", Kind: stmt.Write, SQL: query, Params: params, CallSites: []string{"store/writer/uo_jobs.go:applyUOJobLease"}, Plan: stmt.Plan{Hot: true}})
		point := "SELECT " + rev + " FROM " + table + " WHERE " + key1 + "=?"
		pointParams := []stmt.Param{{Name: "key1", Type: stmt.Text}}
		if key2 != "" {
			point += " AND " + key2 + "=?"
			pointParams = append(pointParams, stmt.Param{Name: "key2", Type: stmt.Text})
		}
		point += " AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=? AND job_next_attempt_ms IS ? AND " + rev + "=?"
		pointParams = append(pointParams, params[len(params)-6:]...)
		out = append(out, stmt.Definition{ID: uoJobCurrentID(f), Owner: "store/writer UO operations", Kind: stmt.Read, SQL: point, Params: pointParams, CallSites: []string{"store/writer/uo_jobs.go:applyUOJobLease"}, Plan: stmt.Plan{Hot: true}})
	}
	return out
}
