package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

var ErrJobRequest = requestinput.New("invalid job request")

func jobInvalid(message string) error { return fmt.Errorf("%w: %s", ErrJobRequest, message) }

// BulkAccess resolves live session, library and content authority in the same
// transaction that applies a batch. Stored acceptance scope is never a grant.
type BulkAccess func(context.Context, *sql.Tx, identity.Principal, string) (string, []any, error)
type JobContainer struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type JobItems struct {
	IDs []string `json:"ids"`
}
type JobQuery struct {
	SelectionBasis string                `json:"selectionBasis,omitempty"`
	LibraryID      string                `json:"libraryId"`
	Pivot          string                `json:"pivot"`
	Filter         json.RawMessage       `json:"filter,omitempty"`
	Sort           []BrowseSortSelection `json:"sort,omitempty"`
}
type JobSelector struct {
	Container *JobContainer `json:"container,omitempty"`
	Query     *JobQuery     `json:"query,omitempty"`
	Items     *JobItems     `json:"items,omitempty"`
}
type JobPersonalArgs = JobArguments

type JobArguments struct {
	Watched          *bool                   `json:"watched,omitempty"`
	Favorite         *bool                   `json:"favorite,omitempty"`
	Watchlist        *bool                   `json:"watchlist,omitempty"`
	Rating           json.RawMessage         `json:"rating,omitempty"`
	PlaylistID       string                  `json:"playlistId,omitempty"`
	CollectionID     string                  `json:"collectionId,omitempty"`
	ExpectedRevision *int64                  `json:"expectedRevision,omitempty"`
	Placement        json.RawMessage         `json:"placement,omitempty"`
	Fields           map[string]JobFieldEdit `json:"fields,omitempty"`
	Lists            map[string]JobListEdit  `json:"lists,omitempty"`
	Genres           *JobListEdit            `json:"genres,omitempty"`
	LockEdited       *bool                   `json:"lockEdited,omitempty"`
}
type JobExpected struct {
	CatalogRevision string `json:"catalogRevision,omitempty"`
}
type JobRequest struct {
	OperationID string          `json:"operationId"`
	Command     string          `json:"command"`
	Selector    JobSelector     `json:"selector"`
	Args        JobPersonalArgs `json:"args"`
	Expected    *JobExpected    `json:"expected,omitempty"`
}
type BulkJob struct {
	JobID          string `json:"jobId"`
	Command        string `json:"command"`
	State          string `json:"state"`
	Total          int64  `json:"total"`
	Done           int64  `json:"done"`
	Failed         int64  `json:"failed"`
	FailuresCursor string `json:"failuresCursor,omitempty"`
	TotalKnown     bool   `json:"totalKnown"`
	ErrorCode      string `json:"errorCode,omitempty"`
}
type JobFailure struct {
	ItemID string `json:"itemId"`
	Code   string `json:"code"`
}
type JobFailures struct {
	Items      []JobFailure `json:"items"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

// bulkBatchSize is how many selected keys one capture transaction records.
const bulkBatchSize = 20

// bulkApplyBatch is how many members one apply transaction changes. Applying
// one member's personal state changes about fifteen rows once its triggers
// run, so eight keep a batch inside a background write plan (160 rows). An
// owner metadata command changes about sixty rows a member, so it takes two.
const (
	bulkApplyBatch      = 8
	bulkOwnerApplyBatch = 2
)

// MaxActiveBulkJobs is how many bulk jobs one profile may have capturing,
// queued or running at once (PERF-S22). It bounds work in flight, never work
// over time: a finished job frees its slot at once.
const MaxActiveBulkJobs = 32

// bulkPurgeBatch is how many captured-membership (or failure) rows one short
// write removes: within a maintenance write plan (160 rows).
const bulkPurgeBatch = 128

// bulkPurgeGrace is how far past a worker slice a terminal job's purge may run
// before the operation yields and comes back (a variable only for tests).
var bulkPurgeGrace = 250 * time.Millisecond

// bulkRetention is how long a finished job stays readable (its progress and
// failures page) and its operationId replays it: the same 30 days as every
// other personal receipt.
const bulkRetention = 30 * 24 * time.Hour

func bulkTerminal(state string) bool {
	return state == "complete" || state == "partial" || state == "failed"
}

// deleteBulkRows removes one batch of a job's rows from table (captured
// membership or failures) and says whether rows remain.
func (s *Service) deleteBulkRows(ctx context.Context, table, id string) (bool, error) {
	res, e := dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance, `DELETE FROM `+table+` WHERE rowid IN(SELECT rowid FROM `+table+` WHERE job_id=? LIMIT ?)`, id, bulkPurgeBatch)
	if e != nil {
		return false, e
	}
	n, e := res.RowsAffected()
	return n == bulkPurgeBatch, e
}

// purgeBulkJobItems removes a terminal job's captured membership in batches
// until none is left or the deadline passes; remaining says there is more.
func (s *Service) purgeBulkJobItems(ctx context.Context, id string, deadline time.Time) (remaining bool, err error) {
	for {
		more, e := s.deleteBulkRows(ctx, "personal_job_items", id)
		if e != nil || !more {
			return false, e
		}
		if !dbwork.Yield(ctx) || time.Now().After(deadline) {
			return true, ctx.Err()
		}
	}
}

// CleanupBulkJobs forgets finished jobs older than bulkRetention: their
// captured membership (a job finished before the purge existed may still hold
// it), their failures, their destination and the job row, a batch per write so
// no statement is library-sized. It stops at budget and continues next time.
func (s *Service) CleanupBulkJobs(ctx context.Context, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	cutoff := time.Now().Add(-bulkRetention).UnixMilli()
	for time.Now().Before(deadline) && dbwork.Yield(ctx) {
		var id string
		e := s.WithContext(ctx).read().QueryRow(`SELECT id FROM personal_jobs WHERE state IN('complete','partial','failed') AND updated_ms<? LIMIT 1`, cutoff).Scan(&id)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		more := false
		for _, table := range []string{"personal_job_items", "personal_job_failures", "personal_job_membership_outcomes"} {
			if more, e = s.deleteBulkRows(ctx, table, id); e != nil {
				return e
			}
			if more {
				break
			}
		}
		if more {
			continue
		}
		// Anything left is at most one batch, gone with the row.
		if e = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			for _, q := range []string{`DELETE FROM personal_job_items WHERE job_id=?`, `DELETE FROM personal_job_failures WHERE job_id=?`, `DELETE FROM personal_job_destinations WHERE job_id=?`, `DELETE FROM personal_jobs WHERE id=? AND state IN('complete','partial','failed')`} {
				if _, err := tx.ExecContext(ctx, q, id); err != nil {
					return err
				}
			}
			return nil
		}); e != nil {
			return e
		}
	}
	return ctx.Err()
}

type bulkBatchObserverKey struct{}
type bulkTimingObserverKey struct{}

func observeBulkTiming(ctx context.Context, phase string) func() {
	started := time.Now()
	return func() {
		if observe, ok := ctx.Value(bulkTimingObserverKey{}).(func(string, time.Duration)); ok {
			observe(phase, time.Since(started))
		}
	}
}

// The observer records the number of member keys handled by this transaction,
// including denied members, so the acceptance test covers every write phase.
func observeBulkBatch(ctx context.Context, phase string, count int) {
	if observe, ok := ctx.Value(bulkBatchObserverKey{}).(func(string, int)); ok {
		observe(phase, count)
	}
}

const bulkColumns = `id,state,total,done,failed,error_code,selection_complete,command`

func scanBulk(row interface{ Scan(...any) error }) (BulkJob, error) {
	j := BulkJob{}
	err := row.Scan(&j.JobID, &j.State, &j.Total, &j.Done, &j.Failed, &j.ErrorCode, &j.TotalKnown, &j.Command)
	if j.Failed > 0 {
		j.FailuresCursor = "0"
	}
	return j, err
}
func (s *Service) BulkJob(profile, id string) (BulkJob, error) {
	j, err := scanBulk(s.read().QueryRow(`SELECT `+bulkColumns+` FROM personal_jobs WHERE id=? AND profile_id=?`, id, profile))
	if j.State == "building" {
		j.State = "queued"
	}
	return j, err
}
func validJobArgs(a JobPersonalArgs) error {
	if a.Watched == nil && a.Favorite == nil && a.Watchlist == nil && len(a.Rating) == 0 {
		return jobInvalid("set at least one personal field")
	}
	if len(a.Rating) > 0 {
		m := PersonalMutation{Rating: a.Rating}
		_, _, err := mutationField(&m)
		if err != nil {
			return jobInvalid(err.Error())
		}
		return nil
	}
	return nil
}
func bulkEvent(tx *sql.Tx, p identity.Principal, id string) error {
	j, err := scanBulk(tx.QueryRow(`SELECT `+bulkColumns+` FROM personal_jobs WHERE id=?`, id))
	if err != nil {
		return err
	}
	if j.State == "building" {
		j.State = "queued"
	}
	var revision int64
	if err = tx.QueryRow(`SELECT revision FROM personal_jobs WHERE id=?`, id).Scan(&revision); err != nil {
		return err
	}
	if err = apievents.Append(tx, apievents.ProfileAudience(p.Authority, p.AccountID, p.ProfileID), "job.updated", "job", id, strconv.FormatInt(revision, 10), j); err != nil {
		return err
	}
	if jobOwnerCommand(j.Command) {
		return apievents.Append(tx, "admin", "job.updated", "job", id, strconv.FormatInt(revision, 10), j)
	}
	return operations.AppendActivityProgressTx(tx, "job.updated", "job", id, strconv.FormatInt(revision, 10), j.State != "running" && j.State != "queued")
}

func (s *Service) CreateBulkJob(ctx context.Context, p identity.Principal, v Viewer, in JobRequest, scheduler *operations.Scheduler, access BulkAccess) (out BulkJob, err error) {
	if scheduler == nil || access == nil {
		return out, errors.New("job service unavailable")
	}
	if !personalOperation.MatchString(in.OperationID) {
		return out, jobInvalid("invalid command or operationId")
	}
	if jobOwnerCommand(in.Command) && !identity.Administrative(p.Role) {
		return out, identity.ErrUnauthorized
	}
	if err = s.validateBulkCommand(in.Command, in.Args); err != nil {
		return out, err
	}
	rawHash, _ := json.Marshal(in)
	var canonical any
	if err = json.Unmarshal(rawHash, &canonical); err != nil {
		return out, jobInvalid("invalid request JSON")
	}
	hash := operationHash(canonical)
	profile := identity.PersonalKey(p.Viewer)
	// Replay does not recompile a filter against a catalogue the original job changed.
	var id, old string
	e := s.WithContext(ctx).read().QueryRow(`SELECT id,request_hash FROM personal_jobs WHERE profile_id=? AND operation_id=?`, profile, in.OperationID).Scan(&id, &old)
	if e == nil {
		if old != hash {
			return out, ErrOperationConflict
		}
		err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			_, _, e := access(ctx, tx, p, "i.id")
			if e != nil {
				return e
			}
			return s.authorizeBulkCommand(ctx, tx, p, in.Command, in.Args, false)
		})
		if err != nil {
			return out, err
		}
		return s.WithContext(ctx).BulkJob(profile, id)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	snap, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	query, args, e := s.WithContext(snap.Context()).bulkSelection(p, v, in)
	if e != nil {
		snap.Rollback()
		return out, e
	}
	fence, e := s.WithContext(snap.Context()).bulkSelectionFence(snap.Tx(), v, in.Selector)
	snap.Rollback()
	if e != nil {
		return out, e
	}
	rawP, _ := json.Marshal(p)
	rawS, _ := json.Marshal(in.Selector)
	rawA, _ := json.Marshal(in.Args)
	rawArgs, _ := json.Marshal(args)
	rawScope, _ := json.Marshal(v)
	id = identity.Token()
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if _, _, e := access(ctx, tx, p, "i.id"); e != nil {
			return e
		}
		if e := s.authorizeBulkCommand(ctx, tx, p, in.Command, in.Args, false); e != nil {
			return e
		}
		var prior string
		e := tx.QueryRowContext(ctx, `SELECT id,request_hash FROM personal_jobs WHERE profile_id=? AND operation_id=?`, profile, in.OperationID).Scan(&id, &prior)
		if e == nil {
			if prior != hash {
				return ErrOperationConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		current, e := s.WithContext(ctx).bulkSelectionFence(tx, v, in.Selector)
		if e != nil {
			return e
		}
		if current != fence {
			return ErrStaleContinuation
		}
		// PERF-S22: admission bounds what a profile has in flight, not what
		// it did this month. A finished job frees its slot; the route's rate
		// limiter bounds how fast jobs arrive. One partial-index seek (0221).
		var active int
		if e = tx.QueryRow(`SELECT count(*) FROM personal_jobs WHERE profile_id=? AND state IN('building','queued','running')`, profile).Scan(&active); e != nil {
			return e
		}
		if active >= MaxActiveBulkJobs {
			return ErrPersonalCapacity
		}
		if e := s.authorizeBulkCommand(ctx, tx, p, in.Command, in.Args, true); e != nil {
			return e
		}
		now := time.Now().UnixMilli()
		if _, e = tx.ExecContext(ctx, `INSERT INTO personal_jobs(id,profile_id,operation_id,request_hash,principal,selector,args,state,created_ms,updated_ms,selection_sql,selection_args,selection_scope,selection_fence,command) VALUES(?,?,?,?,?,?,?,'building',?,?,?,?,?,?,?)`, id, profile, in.OperationID, hash, string(rawP), string(rawS), string(rawA), now, now, query, string(rawArgs), string(rawScope), fence, in.Command); e != nil {
			return e
		}
		if e = s.createBulkDestination(tx, id, p, in.Command, in.Args); e != nil {
			return e
		}
		if _, e = scheduler.EnqueueDomainTx(ctx, tx, "personal-state", id, profile); e != nil {
			return e
		}
		return bulkEvent(tx, p, id)
	})
	if err != nil {
		return out, err
	}
	return s.WithContext(ctx).BulkJob(profile, id)
}

func (s *Service) BulkAdapter(access BulkAccess) operations.Adapter {
	return operations.Adapter{Kind: "personal-state", Lane: "background-media", Resource: operations.LaneWriteHeavy,
		ValidateTx: func(ctx context.Context, tx *sql.Tx, id string) error {
			var n int
			return tx.QueryRowContext(ctx, `SELECT 1 FROM personal_jobs WHERE id=? AND state IN('building','queued','running')`, id).Scan(&n)
		},
		Step: func(ctx context.Context, id string) (operations.JobObservation, error) {
			j := BulkJob{State: "running"}
			var err error
			sliceEnd := time.Now().Add(750 * time.Millisecond)
			for ctx.Err() == nil && time.Now().Before(sliceEnd) {
				batchStarted := time.Now()
				next, stepErr := s.AdvanceBulkJob(ctx, id, access)
				err = stepErr
				if err != nil {
					if ctx.Err() != nil {
						break
					}
					if dbwork.Retryable(err) {
						return operations.JobObservation{}, err
					}
					if errors.Is(err, sql.ErrNoRows) {
						var exists bool
						if e := s.WithContext(ctx).read().QueryRow(`SELECT EXISTS(SELECT 1 FROM personal_jobs WHERE id=?)`, id).Scan(&exists); e != nil {
							return operations.JobObservation{}, e
						}
						if !exists {
							return operations.JobObservation{State: "failed", Phase: "removed"}, nil
						}
					}
					next, err = s.failBulkJob(ctx, id, "execution_failed")
					if err != nil {
						return operations.JobObservation{}, err
					}
				}
				preparing := next.State == "building" && next.Total == j.Total
				j = next
				if preparing {
					break
				}
				if j.State != "running" && j.State != "queued" && j.State != "building" {
					break
				}
				if !dbwork.PaceBackground(ctx, batchStarted) {
					break
				}
			}
			state := j.State
			if state == "building" {
				state = "running"
			}
			n := j.Done + j.Failed
			if bulkTerminal(j.State) && ctx.Err() == nil {
				// The captured membership exists only to run the job: once it is
				// terminal its rows go, in short batches, before the operation
				// ends. A library-sized job never leaves library-sized storage.
				remaining, purgeErr := s.purgeBulkJobItems(ctx, id, sliceEnd.Add(bulkPurgeGrace))
				if purgeErr != nil {
					return operations.JobObservation{}, purgeErr
				}
				if remaining {
					return operations.JobObservation{State: "running", Phase: "cleanup", Processed: &n}, nil
				}
			}
			if state == "complete" || state == "partial" {
				state = "succeeded"
			}
			return operations.JobObservation{State: state, Phase: j.State, Processed: &n, Total: func() *int64 {
				if j.TotalKnown {
					n := j.Total
					return &n
				}
				return nil
			}(), ErrorCode: j.ErrorCode}, nil
		}}
}

// AdvanceBulkJob performs one restartable, bounded worker transaction.
func (s *Service) AdvanceBulkJob(ctx context.Context, id string, access BulkAccess) (out BulkJob, err error) {
	var state, command string
	if err = s.WithContext(ctx).read().QueryRow(`SELECT state,command FROM personal_jobs WHERE id=?`, id).Scan(&state, &command); err != nil {
		return out, err
	}
	if state == "building" {
		return s.bulkCaptureStep(ctx, id, access)
	}

	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		defer observeBulkTiming(ctx, "apply")()
		var rawP, rawA string
		var cursor int64
		if e := tx.QueryRowContext(ctx, `SELECT principal,args,cursor FROM personal_jobs WHERE id=?`, id).Scan(&rawP, &rawA, &cursor); e != nil {
			return e
		}
		var p identity.Principal
		var a JobPersonalArgs
		if e := json.Unmarshal([]byte(rawP), &p); e != nil {
			return e
		}
		if e := json.Unmarshal([]byte(rawA), &a); e != nil {
			return e
		}
		j, e := scanBulk(tx.QueryRowContext(ctx, `SELECT `+bulkColumns+` FROM personal_jobs WHERE id=?`, id))
		if e != nil {
			return e
		}
		if j.State != "queued" && j.State != "running" {
			out = j
			return nil
		}
		retired, fenceErr := identity.ProfileRetiredTx(ctx, tx, p.Viewer)
		if fenceErr != nil {
			return fenceErr
		}
		if retired {
			// The purge owns this job and all of its children. A captured worker
			// must not publish an event or recreate state for its retired actor.
			out = j
			return nil
		}
		var commandErr error
		if jobOwnerCommand(j.Command) {
			commandErr = s.authorizeBulkCommand(ctx, tx, p, j.Command, a, false)
		}
		visible, bound, e := access(ctx, tx, p, "i.id")
		if e == nil {
			e = commandErr
		}
		if e != nil {
			if !errors.Is(e, identity.ErrUnauthorized) && !errors.Is(e, identity.ErrForbidden) && !errors.Is(e, identity.ErrContentRestricted) {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE personal_jobs SET revision=revision+1,state='failed',error_code='authority_revoked',updated_ms=? WHERE id=?`, time.Now().UnixMilli(), id); e != nil {
				return e
			}
			out, e = scanBulk(tx.QueryRowContext(ctx, `SELECT `+bulkColumns+` FROM personal_jobs WHERE id=?`, id))
			if e != nil {
				return e
			}
			return bulkEvent(tx, p, id)
		}
		if e = s.checkBulkDestination(tx, id, p, j.Command); e != nil {
			code := ""
			switch {
			case errors.Is(e, ErrPersonalConflict):
				code = "revision_mismatch"
			case errors.Is(e, sql.ErrNoRows):
				code = "destination_not_found"
			case errors.Is(e, identity.ErrUnauthorized):
				code = "authority_revoked"
			default:
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE personal_jobs SET state='failed',error_code=?,revision=revision+1,updated_ms=? WHERE id=?`, code, time.Now().UnixMilli(), id); e != nil {
				return e
			}
			out, e = scanBulk(tx.QueryRow(`SELECT `+bulkColumns+` FROM personal_jobs WHERE id=?`, id))
			if e != nil {
				return e
			}
			return bulkEvent(tx, p, id)
		}
		batch := bulkApplyBatch
		if jobOwnerCommand(j.Command) {
			batch = bulkOwnerApplyBatch
		}
		args := append(append([]any{}, bound...), id, cursor, batch)
		// Limit membership first, then check live visibility. Revoked members cannot
		// disappear from the cursor and make the job loop indefinitely.
		memberSQL := `SELECT m.ordinal,m.item_id,i.id IS NOT NULL AND i.retired=0 AND (` + visible + `),COALESCE(pid(i.public_id),'') FROM (SELECT ordinal,item_id FROM personal_job_items WHERE job_id=? AND ordinal>? ORDER BY ordinal LIMIT ?) m LEFT JOIN catalog_entities i ON i.id=m.item_id`
		membership := j.Command == "playlist-add" || j.Command == "collection-add"
		if membership {
			memberSQL = `SELECT m.ordinal,m.item_id,i.id IS NOT NULL AND i.retired=0 AND i.public_id=outcome.item_public_id AND (` + visible + `),pid(outcome.item_public_id) FROM (SELECT ordinal,item_id,job_id FROM personal_job_items WHERE job_id=? AND ordinal>? ORDER BY ordinal LIMIT ?) m JOIN personal_job_details detail ON detail.job_id=m.job_id AND detail.item_id=m.item_id JOIN personal_job_membership_outcomes outcome ON outcome.job_id=m.job_id AND outcome.item_public_id=pid_blob(detail.expected) LEFT JOIN catalog_entities i ON i.id=m.item_id`
		}
		rows, e := tx.QueryContext(ctx, memberSQL, args...)
		if e != nil {
			return e
		}
		type member struct {
			ordinal  int64
			entityID int64
			visible  bool
			publicID string
		}
		members := []member{}
		for rows.Next() {
			var m member
			if e = rows.Scan(&m.ordinal, &m.entityID, &m.visible, &m.publicID); e != nil {
				rows.Close()
				return e
			}
			members = append(members, m)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		observeBulkBatch(ctx, "apply", len(members))
		for _, m := range members {
			code := ""
			if !m.visible {
				code = "not_found"
			} else {
				if _, e = tx.ExecContext(ctx, `SAVEPOINT personal_job_item`); e != nil {
					return e
				}
				e = s.applyBulkCommand(ctx, tx, p, id, j.Command, m.publicID, a)
				if e != nil {
					if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO personal_job_item`); rollbackErr != nil {
						return rollbackErr
					}
					var itemError *JobItemError
					if errors.As(e, &itemError) {
						code = itemError.Code
					} else if errors.Is(e, ErrPersonalResolution) {
						code = "personal_needs_resolution"
					} else {
						return e
					}
				}
				if _, e = tx.ExecContext(ctx, `RELEASE personal_job_item`); e != nil {
					return e
				}
			}
			if code != "" {
				if _, e = tx.ExecContext(ctx, `INSERT INTO personal_job_failures VALUES(?,?,?,?)`, id, m.ordinal, m.entityID, code); e != nil {
					return e
				}
				j.Failed++
				if membership {
					result, err := tx.ExecContext(ctx, `UPDATE personal_job_membership_outcomes SET outcome='failed',code=? WHERE job_id=? AND item_public_id=pid_blob(?) AND outcome='pending'`, code, id, m.publicID)
					if err != nil {
						return err
					}
					count, err := result.RowsAffected()
					if err != nil {
						return err
					}
					if count != 1 {
						return errors.New("membership failure did not match captured identity")
					}
				}
			} else {
				j.Done++
			}
			cursor = m.ordinal
		}
		if e = s.advanceBulkDestination(tx, id, j.Command); e != nil {
			return e
		}
		j.State = "running"
		if j.Done+j.Failed == j.Total {
			j.State = "complete"
			if j.Failed > 0 {
				j.State = "partial"
				if j.Done == 0 {
					j.State = "failed"
				}
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE personal_jobs SET revision=revision+1,state=?,done=?,failed=?,cursor=?,updated_ms=? WHERE id=?`, j.State, j.Done, j.Failed, cursor, time.Now().UnixMilli(), id); e != nil {
			return e
		}
		out = j
		return bulkEvent(tx, p, id)
	})
	return
}

func (s *Service) applyJobPersonal(tx *sql.Tx, profile, item string, a JobPersonalArgs) error {
	state, e := readPersonal(tx, profile, item)
	if e != nil {
		return e
	}
	for _, c := range state.Conflicts {
		if c.Field == "watched" && a.Watched != nil || c.Field == "favorite" && a.Favorite != nil || c.Field == "watchlisted" && a.Watchlist != nil || c.Field == "rating" && len(a.Rating) > 0 {
			return ErrPersonalResolution
		}
	}
	if e = snapshotPersonal(tx, profile, item, state); e != nil {
		return e
	}
	if a.Watched != nil {
		state.Watched = *a.Watched
		entity, e := personalID(tx, item)
		if e != nil {
			return e
		}
		if e = personalstate.Write(tx, profile, entity, state.Watched); e != nil {
			return e
		}
		state.ProgressSeconds = 0
		if e = explicitResume(tx, profile, item, 0); e != nil {
			return e
		}
	}
	if a.Favorite != nil {
		state.Favorite = *a.Favorite
	}
	if a.Watchlist != nil {
		state.Watchlisted = *a.Watchlist
	}
	if len(a.Rating) > 0 {
		if e = json.Unmarshal(a.Rating, &state.Rating); e != nil {
			return e
		}
	}
	state.Revision++
	if e = storePersonal(tx, profile, item, state); e != nil {
		return e
	}
	return snapshotPersonal(tx, profile, item, state)
}

// Preserve the last committed cursor when a permanent batch error occurs.
// A rolled-back batch is never reported as successful or retried forever.
func (s *Service) failBulkJob(ctx context.Context, id, code string) (out BulkJob, err error) {
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		var raw string
		if e := tx.QueryRowContext(ctx, `SELECT principal FROM personal_jobs WHERE id=?`, id).Scan(&raw); e != nil {
			return e
		}
		var p identity.Principal
		if e := json.Unmarshal([]byte(raw), &p); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `UPDATE personal_jobs SET revision=revision+1,state='failed',error_code=?,updated_ms=? WHERE id=? AND state IN('building','queued','running')`, code, time.Now().UnixMilli(), id); e != nil {
			return e
		}
		var e error
		out, e = scanBulk(tx.QueryRowContext(ctx, `SELECT `+bulkColumns+` FROM personal_jobs WHERE id=?`, id))
		if e != nil {
			return e
		}
		return bulkEvent(tx, p, id)
	})
	return
}
