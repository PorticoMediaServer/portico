package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Job states.
const (
	JobWaiting   = "waiting"
	JobRunning   = "running"
	JobFailed    = "failed"
	JobDone      = "done"
	JobCancelled = "cancelled"
)

// Job kinds this server runs.
const (
	JobScan          = "scan"           // target: the library's public id
	JobDeleteLibrary = "delete_library" // target: the library's public id
)

// Job is one piece of background work.
type Job struct {
	ID       int64
	PublicID string
	Kind     string
	Target   string
	State    string
	Payload  string
	Error    string
	Done     int64
	Total    int64 // 0 when the size of the work is not known
	OwnerID  int64
	// Attempts is how many times the job's work has been started since it was asked for.
	Attempts   int
	CreatedAt  int64
	StartedAt  int64
	FinishedAt int64
}

const jobColumns = `id, public_id, kind, target, state, ifnull(payload, ''), ifnull(last_error, ''), progress_done, ifnull(progress_total, 0),
	ifnull(owner_user_id, 0), attempts, created_at, ifnull(started_at, 0), ifnull(finished_at, 0)`

func (j *Job) dest() []any {
	return []any{&j.ID, &j.PublicID, &j.Kind, &j.Target, &j.State, &j.Payload, &j.Error, &j.Done, &j.Total, &j.OwnerID,
		&j.Attempts, &j.CreatedAt, &j.StartedAt, &j.FinishedAt}
}

// NewJob is a piece of work being asked for.
type NewJob struct {
	Kind    string
	Target  string
	Payload string
	OwnerID int64
	// IdempotencyKey and RequestHash, when a request came with a key: the same request sent
	// again answers with the job the first one started, and starts nothing.
	IdempotencyKey string
	RequestHash    string
}

// StartJob asks for a piece of work. Asking again while it is waiting or running joins the
// one there is; asking after it has finished starts it afresh under the same id.
func (d *DB) StartJob(ctx context.Context, n NewJob) (Job, error) {
	var job Job
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		before, err := createdBefore(tx, n.OwnerID, n.IdempotencyKey, n.RequestHash)
		if err != nil {
			return err
		}
		if before != "" {
			err := tx.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE public_id = ?`, before).Scan(job.dest()...)
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		now := time.Now()
		_, err = tx.Exec(`INSERT INTO jobs (public_id, kind, target, payload, run_after_at, owner_user_id, created_at)
			VALUES (?, ?, ?, nullif(?, ''), ?, nullif(?, 0), ?) ON CONFLICT (kind, target) DO UPDATE SET state = 'waiting', attempts = 0,
			last_error = NULL, payload = excluded.payload, progress_done = 0, progress_total = NULL, owner_user_id = excluded.owner_user_id,
			created_at = excluded.created_at, started_at = NULL, finished_at = NULL WHERE jobs.state NOT IN ('waiting', 'running')`,
			NewID("job"), n.Kind, n.Target, n.Payload, now.UnixMilli(), n.OwnerID, now.UnixMilli())
		if err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind = ? AND target = ?`, n.Kind, n.Target).Scan(job.dest()...); err != nil {
			return err
		}
		if before != "" || n.OwnerID == 0 {
			return nil
		}
		return rememberCreated(tx, n.OwnerID, n.IdempotencyKey, n.RequestHash, job.PublicID, now)
	})
	return job, err
}

// Job returns one job by its public id.
func (d *DB) Job(ctx context.Context, publicID string) (Job, error) {
	return d.job(ctx, `public_id = ?`, publicID)
}

// JobFor returns the job of a kind for a target, running or not.
func (d *DB) JobFor(ctx context.Context, kind, target string) (Job, error) {
	return d.job(ctx, `kind = ? AND target = ?`, kind, target)
}

func (d *DB) job(ctx context.Context, where string, args ...any) (Job, error) {
	var job Job
	err := d.read.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE `+where, args...).Scan(job.dest()...)
	if errors.Is(err, sql.ErrNoRows) {
		return job, ErrNotFound
	}
	return job, err
}

// UnfinishedJobs lists the jobs that are waiting or were running when the server stopped,
// oldest first. A job is safe to run twice, so they are simply run again.
func (d *DB) UnfinishedJobs(ctx context.Context) ([]Job, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE state IN ('waiting', 'running') ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(job.dest()...); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

// RunJob marks a waiting job as running. It reports false for a job that was cancelled or
// finished in the meantime.
func (d *DB) RunJob(ctx context.Context, id int64) (bool, error) {
	ran := false
	err := d.write(ctx, Background, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE jobs SET state = 'running', attempts = attempts + 1, started_at = ? WHERE id = ? AND state IN ('waiting', 'running')`,
			time.Now().UnixMilli(), id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		ran = n == 1
		return nil
	})
	return ran, err
}

// JobProgress records how far a running job has got.
func (d *DB) JobProgress(ctx context.Context, id, done, total int64) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE jobs SET progress_done = ?, progress_total = nullif(?, 0) WHERE id = ? AND state = 'running'`, done, total, id)
		return err
	})
}

// FinishJob records how a job ended: done, failed with a reason, or cancelled. A job that
// was already cancelled stays cancelled.
func (d *DB) FinishJob(ctx context.Context, id int64, state, problem string) error {
	return d.write(context.WithoutCancel(ctx), Background, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE jobs SET state = ?, last_error = nullif(?, ''), finished_at = ? WHERE id = ? AND state IN ('waiting', 'running')`,
			state, problem, time.Now().UnixMilli(), id)
		return err
	})
}

// CancelJob records that a person stopped a job. It is that person's write, not background
// work, so it does not wait behind whatever is holding background writes up.
func (d *DB) CancelJob(ctx context.Context, id int64) error {
	return d.write(context.WithoutCancel(ctx), User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE jobs SET state = 'cancelled', last_error = NULL, finished_at = ? WHERE id = ? AND state IN ('waiting', 'running')`,
			time.Now().UnixMilli(), id)
		return err
	})
}

// PauseJob puts a running job back to waiting when the server stops in good order, so that
// the next start runs it again and does not count this run as one that was cut short.
func (d *DB) PauseJob(ctx context.Context, id int64) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE jobs SET state = 'waiting', attempts = max(attempts - 1, 0) WHERE id = ? AND state = 'running'`, id)
		return err
	})
}

// ProfileOfUser reports whether a profile is still there and still belongs to an account.
func (d *DB) ProfileOfUser(ctx context.Context, profileID, userID int64) (bool, error) {
	var there bool
	err := d.read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM profiles WHERE id = ? AND user_id = ?)`, profileID, userID).Scan(&there)
	return there, err
}
