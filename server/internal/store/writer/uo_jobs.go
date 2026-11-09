package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
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

// uoJobFamilyShape is compiled metadata only. It names the one primary key and
// the one governor-due index for a family; it is not a table selector, a
// readiness query or an admission capability. GV owns readiness/custody and
// supplies the single candidate key from its own indexed read.
type uoJobFamilyShape struct {
	table, key1, key2, revision string
	dueIndex, pkIndex           string
	terminal, nonTerminal       string
	byteColumns                 []string
}

// A terminal parent row may only be removed after its owning child families
// have been reclaimed. The probes are closed reads on each child primary key;
// they are not a caller-selected table or a cascade authorization.
type uoJobChildProbe struct {
	id      stmt.ID
	table   string
	pkIndex string
	columns []stmt.IndexColumn
}

var uoJobPersonalChildProbes = []uoJobChildProbe{
	{id: "uo.job.personal.children.items", table: "personal_job_items", pkIndex: "sqlite_autoindex_personal_job_items_1", columns: []stmt.IndexColumn{{Name: "job_id", Collation: "BINARY"}, {Name: "ordinal", Collation: "BINARY"}}},
	{id: "uo.job.personal.children.failures", table: "personal_job_failures", pkIndex: "sqlite_autoindex_personal_job_failures_1", columns: []stmt.IndexColumn{{Name: "job_id", Collation: "BINARY"}, {Name: "ordinal", Collation: "BINARY"}}},
	{id: "uo.job.personal.children.outcomes", table: "personal_job_membership_outcomes", pkIndex: "sqlite_autoindex_personal_job_membership_outcomes_1", columns: []stmt.IndexColumn{{Name: "job_id", Collation: "BINARY"}, {Name: "item_public_id", Collation: "BINARY"}}},
}

func uoJobShape(family uoJobFamily) (uoJobFamilyShape, bool) {
	switch family {
	case uoJobPersonal:
		return uoJobFamilyShape{
			table: "personal_jobs", key1: "id", revision: "revision",
			dueIndex: "personal_jobs_governor_due", pkIndex: "sqlite_autoindex_personal_jobs_1",
			terminal:    "state IN ('complete','partial','failed')",
			nonTerminal: "state NOT IN ('complete','partial','failed')",
			byteColumns: []string{"operation_id", "request_hash", "principal", "selector", "args", "error_code", "selection_scope", "selection_fence", "selection_cursor", "command", "destination_kind", "destination_target_id", "destination_order_prefix"},
		}, true
	case uoJobWebhook:
		return uoJobFamilyShape{
			table: "webhook_deliveries", key1: "id", revision: "job_revision",
			dueIndex: "webhook_deliveries_governor_due", pkIndex: "sqlite_autoindex_webhook_deliveries_1",
			terminal:    "state IN ('delivered','failed')",
			nonTerminal: "state NOT IN ('delivered','failed')",
			byteColumns: []string{"type", "subject", "data", "error"},
		}, true
	case uoJobNotice:
		return uoJobFamilyShape{
			table: "notice_broadcast_work", key1: "scope", key2: "operation_id", revision: "revision",
			dueIndex:    "notice_broadcast_work_governor_due",
			terminal:    "delivered=1",
			nonTerminal: "delivered<>1",
			byteColumns: []string{"scope", "operation_id", "digest", "last_account", "last_profile", "upper_account", "upper_profile", "ids"},
		}, true
	case uoJobServiceOutbox:
		return uoJobFamilyShape{
			table: "identity_service_outbox", key1: "notice_id", revision: "job_revision",
			dueIndex: "identity_service_outbox_governor_due", pkIndex: "sqlite_autoindex_identity_service_outbox_1",
			terminal:    "delivered_ms>0",
			nonTerminal: "delivered_ms<=0",
			byteColumns: []string{"operation_id", "server_id", "claim_generation", "credential_generation", "payload", "signature"},
		}, true
	default:
		return uoJobFamilyShape{}, false
	}
}

func uoJobDueIndex(family uoJobFamily) string {
	shape, ok := uoJobShape(family)
	if !ok {
		return ""
	}
	return shape.dueIndex
}

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
		// nextMS is deliberately left unchanged: job_due_ms stays in the partial
		// due index during the lease, so a crashed worker's row is visible again
		// after expiry. A live lease is refused by the expiry check above.
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
// The caller supplies exactly one candidate from GV's indexed readiness read;
// this component never selects a row, a family priority or a timer.
func applyUOJobLease(ctx context.Context, turn *commandTurn, job uoOperationalJob, change uoLeaseChange) (out uoJobLease, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	shape, ok := uoJobShape(job.family)
	if !ok || job.key1 == "" || len(job.key1) > 128 || !utf8.ValidString(job.key1) ||
		len(job.key2) > 128 || !utf8.ValidString(job.key2) || (job.family == uoJobNotice) != (job.key2 != "") {
		return out, ErrInvalid
	}
	after, err := job.lease.transition(change)
	if err != nil {
		return out, err
	}
	keyArgs := []any{job.key1}
	if shape.key2 != "" {
		keyArgs = append(keyArgs, job.key2)
	}
	if after == job.lease {
		old := job.lease
		args := append(append([]any{}, keyArgs...), old.owner, old.generation, old.expiresMS, old.attempts, leaseAttemptParam(old.nextMS), old.revision)
		if _, err := wc008DraftInteger(ctx, turn, uoJobCurrentID(job.family), args...); err != nil {
			return out, err
		}
		return after, turn.checkLifetimes(ctx)
	}
	old := job.lease
	args := []any{after.owner, after.generation, after.expiresMS, after.attempts, nullableLeaseAttempt(after.nextMS), after.revision}
	args = append(args, keyArgs...)
	args = append(args, old.owner, old.generation, old.expiresMS, old.attempts, leaseAttemptParam(old.nextMS), old.revision)
	statementID := uoJobLeaseID(job.family)
	if change.action == uoLeaseClaim {
		statementID = uoJobClaimID(job.family)
	}
	row, err := wc008DraftQuery(ctx, turn, statementID, args...)
	if err != nil {
		return out, err
	}
	var revision int64
	if err = row.Scan(&revision); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	} else if err != nil {
		return out, err
	}
	if revision != after.revision {
		return out, ErrResolveRequired
	}
	return after, turn.checkLifetimes(ctx)
}

type uoJobReclaimResult struct {
	removed              bool
	removedVariableBytes int64
}

// Terminal physical reclaim removes exactly one already-terminal row under the
// exact observed generation/attempts/revision and no live lease. The returned
// byte count is measured from the row actually deleted by RETURNING; a stale or
// non-terminal observation deletes nothing and refunds zero. Child rows are
// owned and reclaimed by their own domain command before this parent row.
func applyUOJobReclaim(ctx context.Context, turn *commandTurn, job uoOperationalJob) (out uoJobReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	shape, ok := uoJobShape(job.family)
	if !ok || job.key1 == "" || len(job.key1) > 128 || !utf8.ValidString(job.key1) ||
		len(job.key2) > 128 || !utf8.ValidString(job.key2) || (job.family == uoJobNotice) != (job.key2 != "") {
		return out, ErrInvalid
	}
	lease := job.lease
	if lease.owner != 0 || lease.expiresMS != 0 || lease.nextMS.Valid || lease.generation < 0 ||
		lease.attempts < 0 || lease.attempts > math.MaxUint32 || lease.revision <= 0 {
		return out, ErrInvalid
	}
	if job.family == uoJobPersonal {
		for _, probe := range uoJobPersonalChildProbes {
			present, err := uoJobChildPresent(ctx, turn, probe, job.key1)
			if err != nil {
				return out, err
			}
			if present {
				return out, ErrConflict
			}
		}
	}
	args := []any{job.key1}
	if shape.key2 != "" {
		args = append(args, job.key2)
	}
	args = append(args, lease.generation, lease.attempts, lease.revision)
	row, err := wc008DraftQuery(ctx, turn, uoJobReclaimID(job.family), args...)
	if err != nil {
		return out, err
	}
	values := make([]sql.NullString, len(shape.byteColumns))
	dest := make([]any, len(values))
	for i := range values {
		dest[i] = &values[i]
	}
	if err = row.Scan(dest...); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	} else if err != nil {
		return out, err
	}
	var removed int64
	for _, value := range values {
		if value.Valid {
			removed += int64(len(value.String))
		}
	}
	return uoJobReclaimResult{removed: true, removedVariableBytes: removed}, turn.checkLifetimes(ctx)
}

func uoJobChildPresent(ctx context.Context, turn *commandTurn, probe uoJobChildProbe, parent string) (bool, error) {
	row, err := wc008DraftQuery(ctx, turn, probe.id, parent)
	if err != nil {
		return false, err
	}
	var found string
	if err := row.Scan(&found); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, turn.checkLifetimes(ctx)
}

func nullableLeaseAttempt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}

// leaseAttemptParam maps NULL to a non-NULL sentinel so the closed SQL grammar
// can compare the nullable attempt column without an IS ? extension.
func leaseAttemptParam(v sql.NullInt64) int64 {
	if v.Valid {
		return v.Int64
	}
	return -1
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

func uoJobCurrentID(f uoJobFamily) stmt.ID {
	id := uoJobLeaseID(f)
	if id == "" {
		return ""
	}
	return stmt.ID(string(id) + ".current")
}

func uoJobClaimID(f uoJobFamily) stmt.ID {
	id := uoJobLeaseID(f)
	if id == "" {
		return ""
	}
	return stmt.ID(string(id) + ".claim")
}

func uoJobReclaimID(f uoJobFamily) stmt.ID {
	id := uoJobLeaseID(f)
	if id == "" {
		return ""
	}
	return stmt.ID(string(id) + ".reclaim")
}

func uoJobAccessPlan(shape uoJobFamilyShape) stmt.Plan {
	if shape.pkIndex == "" {
		return stmt.Plan{Hot: true, PrimaryKeys: []string{shape.table}, Seeks: []string{shape.key1 + "=? AND " + shape.key2 + "=?"}}
	}
	return stmt.Plan{Hot: true, Indexes: []string{shape.pkIndex}, AutoIndexes: []stmt.AutoIndexBinding{{
		Table: shape.table, Name: shape.pkIndex, Origin: "pk",
		Columns: []stmt.IndexColumn{{Name: shape.key1, Collation: "BINARY"}},
	}}}
}

func uoJobDefinitions() []stmt.Definition {
	var out []stmt.Definition
	for _, f := range []uoJobFamily{uoJobPersonal, uoJobWebhook, uoJobNotice, uoJobServiceOutbox} {
		shape, _ := uoJobShape(f)
		plan := uoJobAccessPlan(shape)
		keyWhere := shape.key1 + "=?"
		if shape.key2 != "" {
			keyWhere += " AND " + shape.key2 + "=?"
		}
		from := shape.table
		if shape.pkIndex != "" {
			from += " INDEXED BY " + shape.pkIndex
		}
		rev := shape.revision
		leaseBase := "UPDATE " + from + " SET job_lease_owner=?,job_lease_generation=?,job_lease_expires_ms=?,job_attempts=?,job_next_attempt_ms=?," + rev + "=? WHERE " + keyWhere +
			" AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=? AND COALESCE(job_next_attempt_ms,-1)=? AND " + rev + "=?"
		leaseSQL := leaseBase + " RETURNING " + rev
		claimSQL := leaseBase + " AND " + shape.nonTerminal + " RETURNING " + rev
		leaseParams := []stmt.Param{
			{Name: "next_owner", Type: stmt.Integer}, {Name: "next_generation", Type: stmt.Integer},
			{Name: "next_expiry", Type: stmt.Integer}, {Name: "next_attempts", Type: stmt.Integer},
			{Name: "next_attempt", Type: stmt.Integer, Nullable: true}, {Name: "next_revision", Type: stmt.Integer},
			{Name: "key1", Type: stmt.Text},
		}
		leaseArgs := []any{int64(7), int64(1), int64(1100), int64(1), int64(100), int64(2), "job-one"}
		claimArgs := []any{int64(7), int64(1), int64(1100), int64(1), int64(100), int64(2), "job-one"}
		if shape.key2 != "" {
			leaseParams = append(leaseParams, stmt.Param{Name: "key2", Type: stmt.Text})
			leaseArgs = append(leaseArgs, "operation-one")
			claimArgs = append(claimArgs, "operation-one")
		}
		leaseParams = append(leaseParams,
			stmt.Param{Name: "old_owner", Type: stmt.Integer}, stmt.Param{Name: "old_generation", Type: stmt.Integer},
			stmt.Param{Name: "old_expiry", Type: stmt.Integer}, stmt.Param{Name: "old_attempts", Type: stmt.Integer},
			stmt.Param{Name: "old_attempt", Type: stmt.Integer}, stmt.Param{Name: "old_revision", Type: stmt.Integer})
		leaseArgs = append(leaseArgs, int64(7), int64(1), int64(100), int64(1), int64(100), int64(1))
		claimArgs = append(claimArgs, int64(0), int64(0), int64(0), int64(0), int64(100), int64(1))
		out = append(out, stmt.Definition{
			ID: uoJobLeaseID(f), Owner: "store/writer UO operations", Kind: stmt.Write,
			SQL: leaseSQL, Params: leaseParams,
			Cohorts:   []stmt.Cohort{{Name: "renew_or_finish", Bindings: leaseArgs}},
			CallSites: []string{"store/writer/uo_jobs.go:applyUOJobLease"}, Plan: plan,
		})
		out = append(out, stmt.Definition{
			ID: uoJobClaimID(f), Owner: "store/writer UO operations", Kind: stmt.Write,
			SQL: claimSQL, Params: leaseParams,
			Cohorts:   []stmt.Cohort{{Name: "due_nonterminal", Bindings: claimArgs}},
			CallSites: []string{"store/writer/uo_jobs.go:applyUOJobLease"}, Plan: plan,
		})

		currentSQL := "SELECT " + rev + " FROM " + from + " WHERE " + keyWhere +
			" AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=? AND COALESCE(job_next_attempt_ms,-1)=? AND " + rev + "=?"
		currentParams := []stmt.Param{{Name: "key1", Type: stmt.Text}}
		currentArgs := []any{"job-one"}
		if shape.key2 != "" {
			currentParams = append(currentParams, stmt.Param{Name: "key2", Type: stmt.Text})
			currentArgs = append(currentArgs, "operation-one")
		}
		currentParams = append(currentParams,
			stmt.Param{Name: "owner", Type: stmt.Integer}, stmt.Param{Name: "generation", Type: stmt.Integer},
			stmt.Param{Name: "expiry", Type: stmt.Integer}, stmt.Param{Name: "attempts", Type: stmt.Integer},
			stmt.Param{Name: "attempt", Type: stmt.Integer}, stmt.Param{Name: "revision", Type: stmt.Integer})
		currentArgs = append(currentArgs, int64(7), int64(1), int64(1100), int64(1), int64(-1), int64(2))
		out = append(out, stmt.Definition{
			ID: uoJobCurrentID(f), Owner: "store/writer UO operations", Kind: stmt.Read,
			SQL: currentSQL, Params: currentParams,
			Cohorts:   []stmt.Cohort{{Name: "held_lease", Bindings: currentArgs}},
			CallSites: []string{"store/writer/uo_jobs.go:applyUOJobLease"}, Plan: plan,
		})

		reclaimSQL := "DELETE FROM " + from + " WHERE " + keyWhere +
			" AND job_lease_owner=0 AND job_lease_generation=? AND job_lease_expires_ms=0 AND job_attempts=? AND job_next_attempt_ms IS NULL AND " + rev + "=? AND " + shape.terminal +
			" RETURNING " + strings.Join(shape.byteColumns, ",")
		reclaimParams := []stmt.Param{{Name: "key1", Type: stmt.Text}}
		reclaimArgs := []any{"job-one"}
		if shape.key2 != "" {
			reclaimParams = append(reclaimParams, stmt.Param{Name: "key2", Type: stmt.Text})
			reclaimArgs = append(reclaimArgs, "operation-one")
		}
		reclaimParams = append(reclaimParams,
			stmt.Param{Name: "generation", Type: stmt.Integer}, stmt.Param{Name: "attempts", Type: stmt.Integer},
			stmt.Param{Name: "revision", Type: stmt.Integer})
		reclaimArgs = append(reclaimArgs, int64(1), int64(1), int64(2))
		out = append(out, stmt.Definition{
			ID: uoJobReclaimID(f), Owner: "store/writer UO operations", Kind: stmt.Write,
			SQL: reclaimSQL, Params: reclaimParams,
			Cohorts:   []stmt.Cohort{{Name: "terminal_row", Bindings: reclaimArgs}},
			CallSites: []string{"store/writer/uo_jobs.go:applyUOJobReclaim"}, Plan: plan,
		})
	}
	for _, probe := range uoJobPersonalChildProbes {
		out = append(out, stmt.Definition{
			ID: probe.id, Owner: "store/writer UO operations", Kind: stmt.Read,
			SQL:       "SELECT job_id FROM " + probe.table + " INDEXED BY " + probe.pkIndex + " WHERE job_id=? ORDER BY " + probe.columns[1].Name + " LIMIT 1",
			Params:    []stmt.Param{{Name: "job_id", Type: stmt.Text}},
			Cohorts:   []stmt.Cohort{{Name: "parent", Bindings: []any{"job-personal"}}},
			CallSites: []string{"store/writer/uo_jobs.go:applyUOJobReclaim"},
			Plan: stmt.Plan{Hot: true, Indexes: []string{probe.pkIndex}, AutoIndexes: []stmt.AutoIndexBinding{{
				Table: probe.table, Name: probe.pkIndex, Origin: "pk", Columns: probe.columns,
			}}},
		})
	}
	return out
}
