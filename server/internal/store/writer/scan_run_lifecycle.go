package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These helpers have no birth permit, source authority, lease-exclusion proof,
// clock, grant, transaction constructor or commit. W must supply all of them on
// the SAME turn. Every error requires WHOLE-command rollback, including errors
// after successful native effects. Facts never carry an Applied outcome.
func applyScanRunCreate(ctx context.Context, turn *commandTurn, in scanRunCreateEffects) (value scanRunLifecycleFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if err = scanLifecycleReady(ctx, turn); err != nil {
		return value, err
	}
	if !validScanRunScope(in.scope) || in.appliedMS < 0 || in.identity.Sequence != 1 ||
		in.identity.Incarnation != in.scope.commandIncarnation || in.identity.OperationID != in.scope.createOperation || in.request != in.scope.createDigest ||
		!validScanRunRoot(in.root) || in.root.state != "pending" || in.root.cursor != "" || in.root.pages != 0 || in.root.skipped != 0 {
		return value, invalid("scan_run_create_values")
	}
	createdAt := time.UnixMilli(in.appliedMS).UTC().Format("2006-01-02T15:04:05.000000000Z")
	if len(createdAt) != 30 {
		return value, invalid("scan_run_create_time")
	}
	want := scanRunSnapshot{run: scanRunNative{scope: in.scope, phase: "inventory", revision: 1, lastSequence: 1},
		job: scanRunJob{libraryKey: in.scope.libraryKey, status: "queued", createdAt: createdAt}, root: in.root}
	job, err := scanLifecycleJobQuery(ctx, turn, scanLifecycleInsertJob,
		in.scope.runID, in.scope.libraryKey, "queued", int64(0), createdAt, "")
	if err != nil || job != want.job {
		return value, scanLifecycleMismatch(err)
	}
	run, err := scanLifecycleRunQuery(ctx, turn, scanLifecycleInsertRun, scanLifecycleRunArgs(want.run)...)
	if err != nil || run != want.run {
		return value, scanLifecycleMismatch(err)
	}
	root, err := scanLifecycleRootQuery(ctx, turn, scanLifecycleInsertRoot,
		in.scope.runID, ".", in.root.identity, in.root.revision, "", "pending", int64(0), int64(0))
	if err != nil || root != want.root {
		return value, scanLifecycleMismatch(err)
	}
	rows, err := scanLifecycleFirst(ctx, turn, scanLifecycleInsertActive, in.scope.sourceID, in.scope.runID)
	if err != nil {
		return value, err
	}
	var sourceID, runID string
	err = rows.Scan(&sourceID, &runID)
	err = errors.Join(err, scanLifecycleEnd(rows))
	if err != nil || sourceID != in.scope.sourceID || runID != in.scope.runID {
		return value, scanLifecycleMismatch(err)
	}
	post, err := readScanRunSnapshot(ctx, turn, in.scope.runID)
	if err != nil || post != want {
		return value, scanLifecycleMismatch(err)
	}
	return scanRunLifecycleFacts{scanRunCreated, in.identity, in.request, in.appliedMS, post}, nil
}

func applyScanRunClaim(ctx context.Context, turn *commandTurn, in scanRunClaimEffects) (value scanRunLifecycleFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if err = scanLifecycleReady(ctx, turn); err != nil {
		return value, err
	}
	before := in.before
	r := before.run
	if !validScanRunSnapshot(before) || in.appliedMS < 0 || in.leaseOwner <= 0 || in.expiresMS <= in.appliedMS ||
		r.acknowledgedSequence != r.lastSequence || r.revision == math.MaxInt64 || r.lastSequence == math.MaxInt64 || r.leaseGeneration == math.MaxInt64 ||
		in.identity.Incarnation != r.scope.commandIncarnation || in.identity.Sequence != r.lastSequence+1 ||
		(before.job.status != "queued" && before.job.status != "running") || r.phase != "inventory" || r.authoritative != 0 || r.rootRebound != 0 || r.finishedAt != "" || before.root.state != "pending" {
		return value, ErrConflict
	}
	// Arithmetic/shape checks only. These do NOT establish W's trusted time,
	// owner authority, whole-turn coverage, schedule or physical exclusion.
	switch in.mode {
	case scanRunInitialClaim:
		if r.leaseOwner != 0 || r.leaseGeneration != 0 || r.leaseExpiresMS != 0 {
			return value, ErrConflict
		}
	case scanRunLiveRenew:
		if r.leaseOwner != in.leaseOwner || r.leaseGeneration <= 0 || r.leaseExpiresMS <= in.appliedMS || in.expiresMS <= r.leaseExpiresMS {
			return value, ErrConflict
		}
	case scanRunExpiredReclaim:
		if r.leaseOwner <= 0 || r.leaseGeneration <= 0 || r.leaseExpiresMS <= 0 || r.leaseExpiresMS > in.appliedMS {
			return value, ErrConflict
		}
	default:
		return value, invalid("scan_run_claim_mode")
	}
	actual, err := readScanRunSnapshot(ctx, turn, r.scope.runID)
	if err != nil || actual != before {
		return value, scanLifecycleMismatch(err)
	}
	if err = scanLifecycleCheckActive(ctx, turn, r.scope); err != nil {
		return value, err
	}
	want := before
	want.run.leaseOwner, want.run.leaseGeneration, want.run.leaseExpiresMS = in.leaseOwner, r.leaseGeneration+1, in.expiresMS
	want.run.revision, want.run.lastSequence = r.revision+1, r.lastSequence+1
	want.job.status = "running"
	args := []any{want.run.leaseOwner, want.run.leaseGeneration, want.run.leaseExpiresMS, want.run.revision, want.run.lastSequence}
	old := scanLifecycleRunArgs(r)
	args = append(args, old[:21]...)
	args = append(args, old[22:]...)
	id := scanLifecycleClaimNull
	if r.nextAttemptMS.Valid {
		id = scanLifecycleClaimDue
		args = append(args, r.nextAttemptMS.Int64)
	}
	changed, err := scanLifecycleRunQuery(ctx, turn, id, args...)
	if err != nil || changed != want.run {
		return value, scanLifecycleMismatch(err)
	}
	job, err := scanLifecycleJobQuery(ctx, turn, scanLifecycleClaimJob, "running", r.scope.runID,
		before.job.libraryKey, before.job.status, before.job.processed, before.job.createdAt, before.job.errorText)
	if err != nil || job != want.job {
		return value, scanLifecycleMismatch(err)
	}
	post, err := readScanRunSnapshot(ctx, turn, r.scope.runID)
	if err != nil || post != want {
		return value, scanLifecycleMismatch(err)
	}
	return scanRunLifecycleFacts{scanRunClaimed, in.identity, in.request, in.appliedMS, post}, nil
}

// Receipt output is the actual post-effect run/job/root cohort. The original
// request digest stays allocation-independent; the closed result digest is W's
// responsibility. SQL NULL source_revision has NO N1 observation meaning.
func writeScanLifecycleReceipt(ctx context.Context, turn *commandTurn, in scanLifecycleReceiptEffects) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = scanLifecycleReady(ctx, turn); err != nil {
		return err
	}
	f, r := in.facts, in.facts.post.run
	if !validScanRunSnapshot(f.post) || f.appliedMS < 0 || f.identity.Incarnation != r.scope.commandIncarnation || f.identity.Sequence != r.lastSequence || r.acknowledgedSequence >= r.lastSequence {
		return invalid("scan_lifecycle_receipt_values")
	}
	var kind string
	switch f.kind {
	case scanRunCreated:
		kind = "inventory_create"
		if r.revision != 1 || r.lastSequence != 1 || r.acknowledgedSequence != 0 || r.leaseOwner != 0 || r.leaseGeneration != 0 || r.leaseExpiresMS != 0 ||
			r.discovered != 0 || f.post.job.processed != 0 || f.post.root.cursor != "" || f.post.root.pages != 0 || f.post.root.skipped != 0 ||
			f.identity.OperationID != r.scope.createOperation || f.request != r.scope.createDigest {
			return invalid("scan_create_receipt_values")
		}
	case scanRunClaimed:
		kind = "inventory_claim"
		if r.leaseOwner <= 0 || r.leaseGeneration <= 0 || r.leaseExpiresMS <= f.appliedMS {
			return invalid("scan_claim_receipt_values")
		}
	default:
		return invalid("scan_lifecycle_receipt_kind")
	}
	post, err := readScanRunSnapshot(ctx, turn, r.scope.runID)
	if err != nil || post != f.post {
		return scanLifecycleMismatch(err)
	}
	args := []any{r.scope.runID, f.identity.Incarnation[:], f.identity.Sequence, f.identity.OperationID[:], f.request[:], kind,
		r.revision, f.appliedMS, r.scope.sourceID, r.scope.incarnation, nil, "", int64(0), in.digest[:],
		r.leaseOwner, r.leaseGeneration, r.leaseExpiresMS, r.acknowledgedSequence, r.discovered, f.post.job.processed,
		f.post.root.identity, f.post.root.revision, f.post.root.cursor, f.post.root.pages, f.post.root.skipped}
	rows, err := scanLifecycleFirst(ctx, turn, scanLifecycleReceiptInsert, args...)
	if err != nil {
		return err
	}
	var runID, gotKind, sourceID, incarnation, cursor string
	var nonce, operation, request, digest []byte
	var sequence, revision, appliedMS, count, owner, generation, expiry, ack, discovered, processed int64
	var sourceRevision sql.NullString
	var root scanRunRoot
	err = rows.Scan(&runID, &nonce, &sequence, &operation, &request, &gotKind, &revision, &appliedMS, &sourceID, &incarnation,
		&sourceRevision, &cursor, &count, &digest, &owner, &generation, &expiry, &ack, &discovered, &processed,
		&root.identity, &root.revision, &root.cursor, &root.pages, &root.skipped)
	err = errors.Join(err, scanLifecycleEnd(rows))
	if err != nil {
		return err
	}
	if runID != r.scope.runID || !bytes.Equal(nonce, f.identity.Incarnation[:]) || sequence != f.identity.Sequence || !bytes.Equal(operation, f.identity.OperationID[:]) ||
		!bytes.Equal(request, f.request[:]) || gotKind != kind || revision != r.revision || appliedMS != f.appliedMS || sourceID != r.scope.sourceID || incarnation != r.scope.incarnation ||
		sourceRevision.Valid || cursor != "" || count != 0 || !bytes.Equal(digest, in.digest[:]) || owner != r.leaseOwner || generation != r.leaseGeneration || expiry != r.leaseExpiresMS ||
		ack != r.acknowledgedSequence || discovered != r.discovered || processed != f.post.job.processed || root.identity != f.post.root.identity || root.revision != f.post.root.revision ||
		root.cursor != f.post.root.cursor || root.pages != f.post.root.pages || root.skipped != f.post.root.skipped {
		return invalid("scan_lifecycle_receipt_returning")
	}
	return nil
}

func readScanRunSnapshot(ctx context.Context, turn *commandTurn, runID string) (value scanRunSnapshot, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	value.run, err = scanLifecycleRunQuery(ctx, turn, scanLifecycleRun, runID)
	if err != nil {
		return value, err
	}
	value.job, err = scanLifecycleJobQuery(ctx, turn, scanLifecycleJob, runID)
	if err != nil {
		return value, err
	}
	value.root, err = scanLifecycleRootQuery(ctx, turn, scanLifecycleRoot, runID, ".")
	return value, err
}

func scanLifecycleRunQuery(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (value scanRunNative, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	rows, err := scanLifecycleFirst(ctx, turn, id, args...)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	s := &value.scope
	var operation, digest, nonce []byte
	if err = rows.Scan(&s.runID, &s.libraryID, &s.libraryKey, &s.resolvedRoot, &s.followSymlinks, &operation, &digest,
		&s.sourceID, &s.sourceGeneration, &s.incarnation, &s.rootIdentity, &s.policyRevision, &value.phase, &value.authoritative, &value.rootRebound, &value.finishedAt,
		&s.scanMode, &value.leaseOwner, &value.leaseGeneration, &value.leaseExpiresMS, &value.attempts, &value.nextAttemptMS,
		&value.revision, &nonce, &value.lastSequence, &value.acknowledgedSequence, &value.discovered); err != nil {
		return value, err
	}
	if len(operation) != 16 || len(digest) != 32 || len(nonce) != 16 {
		return value, invalid("scan_lifecycle_native_blob")
	}
	copy(s.createOperation[:], operation)
	copy(s.createDigest[:], digest)
	copy(s.commandIncarnation[:], nonce)
	return value, scanLifecycleEnd(rows)
}

func scanLifecycleJobQuery(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (value scanRunJob, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	rows, err := scanLifecycleFirst(ctx, turn, id, args...)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if err = rows.Scan(&value.libraryKey, &value.status, &value.processed, &value.createdAt, &value.errorText); err != nil {
		return value, err
	}
	return value, scanLifecycleEnd(rows)
}

func scanLifecycleRootQuery(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (value scanRunRoot, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	rows, err := scanLifecycleFirst(ctx, turn, id, args...)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if err = rows.Scan(&value.identity, &value.revision, &value.cursor, &value.state, &value.pages, &value.skipped); err != nil {
		return value, err
	}
	return value, scanLifecycleEnd(rows)
}

func scanLifecycleCheckActive(ctx context.Context, turn *commandTurn, scope scanRunScope) error {
	rows, err := scanLifecycleFirst(ctx, turn, scanLifecycleActive, scope.sourceID)
	if err != nil {
		return err
	}
	var runID string
	err = rows.Scan(&runID)
	err = errors.Join(err, scanLifecycleEnd(rows))
	if err != nil || runID != scope.runID {
		return scanLifecycleMismatch(err)
	}
	return nil
}

func scanLifecycleReady(ctx context.Context, turn *commandTurn) error {
	if ctx == nil || turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	return turn.checkLifetimes(ctx)
}

func scanLifecycleFirst(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (*commandRows, error) {
	if err := scanLifecycleReady(ctx, turn); err != nil {
		return nil, err
	}
	step, ok := turn.steps[id]
	if !ok {
		return nil, invalid("scan_lifecycle_unregistered_step")
	}
	rows, err := turn.query(ctx, step.statementStep, args...)
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		err = errors.Join(ErrConflict, rows.Err(), rows.Close())
		return nil, err
	}
	return rows, nil
}

func scanLifecycleEnd(rows *commandRows) error {
	var err error
	if rows.Next() {
		err = invalid("scan_lifecycle_native_row_count")
	}
	return errors.Join(err, rows.Err(), rows.Close())
}

func scanLifecycleMismatch(err error) error {
	if err != nil {
		return err
	}
	return ErrConflict
}

func scanLifecycleRunArgs(r scanRunNative) []any {
	s := r.scope
	var next any
	if r.nextAttemptMS.Valid {
		next = r.nextAttemptMS.Int64
	}
	return []any{s.runID, s.libraryID, s.libraryKey, s.resolvedRoot, s.followSymlinks, s.createOperation[:], s.createDigest[:],
		s.sourceID, s.sourceGeneration, s.incarnation, s.rootIdentity, s.policyRevision, r.phase, r.authoritative, r.rootRebound, r.finishedAt,
		s.scanMode, r.leaseOwner, r.leaseGeneration, r.leaseExpiresMS, r.attempts, next, r.revision, s.commandIncarnation[:], r.lastSequence, r.acknowledgedSequence, r.discovered}
}

func validScanRunScope(s scanRunScope) bool {
	if s.libraryID <= 0 || s.sourceGeneration <= 0 || s.policyRevision <= 0 || (s.followSymlinks != 0 && s.followSymlinks != 1) ||
		s.runID == "" || s.libraryKey == "" || s.sourceID == "" || s.incarnation == "" || s.rootIdentity == "" || s.resolvedRoot == "" || strings.IndexByte(s.resolvedRoot, 0) >= 0 {
		return false
	}
	for _, text := range [...]string{s.runID, s.libraryKey, s.resolvedRoot, s.sourceID, s.incarnation, s.rootIdentity} {
		if !utf8.ValidString(text) {
			return false
		}
	}
	switch s.scanMode {
	case "first-import", "incremental", "integrity", "full":
		return true
	default:
		return false
	}
}

func validScanRunRoot(r scanRunRoot) bool {
	return r.identity != "" && r.revision != "" && utf8.ValidString(r.identity) && utf8.ValidString(r.revision) &&
		utf8.ValidString(r.cursor) && r.pages >= 0 && r.skipped >= 0 && r.skipped <= r.pages
}

func validScanRunSnapshot(s scanRunSnapshot) bool {
	r := s.run
	return validScanRunScope(r.scope) && validScanRunRoot(s.root) && s.job.libraryKey == r.scope.libraryKey && s.job.processed >= 0 &&
		r.revision > 0 && r.lastSequence > 0 && r.acknowledgedSequence >= 0 && r.acknowledgedSequence <= r.lastSequence && r.discovered >= 0 &&
		r.attempts >= 0 && r.attempts <= math.MaxUint32 && (!r.nextAttemptMS.Valid || r.nextAttemptMS.Int64 >= 0)
}
