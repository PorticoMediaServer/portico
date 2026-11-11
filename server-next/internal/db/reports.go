package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Reports are "Report a problem" and the reports an app offers to send after a crash. The
// owner reads them, sets their status and replies; the reply also arrives as a notice.

// Report is one report.
type Report struct {
	ID           int64
	PublicID     string
	ReporterName string
	Kind         string // problem or crash
	Status       string // open, in_progress, resolved or closed
	Message      string
	// Context is what was playing and on what device, as the app sent it (JSON).
	Context string
	// LogHash names the app's log the person agreed to attach, "" for none.
	LogHash   string
	Reply     string
	RepliedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewReport is a report a profile sends.
type NewReport struct {
	Viewer                      Viewer
	Kind, Message, Context      string
	LogHash                     string
	IdempotencyKey, RequestHash string
	Now                         time.Time
}

const reportColumns = `id, public_id, reporter_name, kind, status, ifnull(message, ''), ifnull(context, ''), ifnull(log_hash, ''),
	ifnull(reply, ''), replied_at, created_at, updated_at`

func scanReport(row interface{ Scan(...any) error }) (Report, error) {
	var r Report
	var replied sql.NullInt64
	var created, updated int64
	err := row.Scan(&r.ID, &r.PublicID, &r.ReporterName, &r.Kind, &r.Status, &r.Message, &r.Context, &r.LogHash, &r.Reply, &replied, &created, &updated)
	r.RepliedAt, r.CreatedAt, r.UpdatedAt = optionalTime(replied), time.UnixMilli(created), time.UnixMilli(updated)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}

// ReportsPerDay is how many reports one profile may send in a day: far above honest use, and
// low enough that an app crashing in a loop cannot fill the disk with logs.
const ReportsPerDay = 20

// ErrTooManyReports: the profile has sent ReportsPerDay reports in the last day.
var ErrTooManyReports = errors.New("too many reports today")

// ReportsToday counts the reports a profile sent in the day before now.
func (d *DB) ReportsToday(ctx context.Context, v Viewer, now time.Time) (int, error) {
	var n int
	err := d.read.QueryRowContext(ctx, `SELECT count(*) FROM reports WHERE profile_id = ? AND created_at > ?`,
		v.ProfileID, now.Add(-24*time.Hour).UnixMilli()).Scan(&n)
	return n, err
}

// ReportLogs are the app logs the reports name, by hash: what the tidy-up keeps.
func (d *DB) ReportLogs(ctx context.Context) (map[string]bool, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT DISTINCT log_hash FROM reports WHERE log_hash IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		out[hash] = true
	}
	return out, rows.Err()
}

// CreateReport keeps a report. The reporter is named by username, then the profile when its
// name differs, copied so the report outlives the account. A repeat of the same
// Idempotency-Key answers the report made the first time.
func (d *DB) CreateReport(ctx context.Context, n NewReport) (Report, error) {
	var out Report
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		ref, err := createdBefore(tx, n.Viewer.UserID, n.IdempotencyKey, n.RequestHash)
		if err != nil {
			return err
		}
		if ref == "" {
			var today int
			if err := tx.QueryRow(`SELECT count(*) FROM reports WHERE profile_id = ? AND created_at > ?`,
				n.Viewer.ProfileID, n.Now.Add(-24*time.Hour).UnixMilli()).Scan(&today); err != nil {
				return err
			}
			if today >= ReportsPerDay {
				return ErrTooManyReports
			}
			ref = NewID("rpt")
			if _, err := tx.Exec(`INSERT INTO reports (public_id, user_id, profile_id, reporter_name, kind, message, context, log_hash, created_at, updated_at)
				SELECT ?, u.id, p.id, CASE WHEN lower(p.name) = lower(u.username) THEN u.username ELSE u.username || ' · ' || p.name END,
					?, nullif(?, ''), nullif(?, ''), nullif(?, ''), ?, ?
				FROM users u JOIN profiles p ON p.user_id = u.id WHERE u.id = ? AND p.id = ?`,
				ref, n.Kind, n.Message, n.Context, n.LogHash, n.Now.UnixMilli(), n.Now.UnixMilli(), n.Viewer.UserID, n.Viewer.ProfileID); err != nil {
				return err
			}
			if err := rememberCreated(tx, n.Viewer.UserID, n.IdempotencyKey, n.RequestHash, ref, n.Now); err != nil {
				return err
			}
		}
		out, err = scanReport(tx.QueryRow(`SELECT `+reportColumns+` FROM reports WHERE public_id = ?`, ref))
		return err
	})
	return out, err
}

// ReportQuery picks reports. Viewer, when set, is one profile's own; Status narrows by status.
type ReportQuery struct {
	Viewer *Viewer
	Status string
	After  int64 // the id of the last one read, 0 for the first page
	Limit  int
}

// Reports lists reports newest first.
func (d *DB) Reports(ctx context.Context, q ReportQuery) ([]Report, error) {
	where, args := `1 = 1`, []any{}
	if q.Viewer != nil {
		where, args = `user_id = ? AND profile_id = ?`, append(args, q.Viewer.UserID, q.Viewer.ProfileID)
	}
	if q.Status != "" {
		where, args = where+` AND status = ?`, append(args, q.Status)
	}
	if q.After > 0 {
		where, args = where+` AND id < ?`, append(args, q.After)
	}
	rows, err := d.read.QueryContext(ctx, `SELECT `+reportColumns+` FROM reports WHERE `+where+` ORDER BY id DESC LIMIT ?`, append(args, q.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReportByID returns one report by its public id.
func (d *DB) ReportByID(ctx context.Context, publicID string) (Report, error) {
	return scanReport(d.read.QueryRowContext(ctx, `SELECT `+reportColumns+` FROM reports WHERE public_id = ?`, publicID))
}

// AnswerReport sets a report's status, its reply, or both; nil leaves one as it is. A new
// reply reaches the reporter's profile as a notice in the same commit, or the account when the
// profile is gone, or nobody when the account is gone too.
func (d *DB) AnswerReport(ctx context.Context, publicID string, status, reply *string, now time.Time) (Report, error) {
	var out Report
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		r, err := scanReport(tx.QueryRow(`SELECT `+reportColumns+` FROM reports WHERE public_id = ?`, publicID))
		if err != nil {
			return err
		}
		if status != nil {
			if _, err := tx.Exec(`UPDATE reports SET status = ?, updated_at = ? WHERE id = ?`, *status, now.UnixMilli(), r.ID); err != nil {
				return err
			}
		}
		if reply != nil && *reply != r.Reply {
			if _, err := tx.Exec(`UPDATE reports SET reply = nullif(?, ''), replied_at = ?, updated_at = ? WHERE id = ?`,
				*reply, now.UnixMilli(), now.UnixMilli(), r.ID); err != nil {
				return err
			}
			var userID, profileID sql.NullInt64
			if err := tx.QueryRow(`SELECT user_id, profile_id FROM reports WHERE id = ?`, r.ID).Scan(&userID, &profileID); err != nil {
				return err
			}
			if *reply != "" && userID.Valid {
				n := NewNotice{To: ToAccount, UserID: userID.Int64, Kind: "report_reply", Ref: publicID, Body: *reply, Now: now}
				if profileID.Valid {
					n.To, n.ProfileID = ToProfile, profileID.Int64
				}
				if _, _, err := sendNotice(tx, n); err != nil {
					return err
				}
			}
		}
		out, err = scanReport(tx.QueryRow(`SELECT `+reportColumns+` FROM reports WHERE id = ?`, r.ID))
		return err
	})
	return out, err
}

// DeleteReports removes a report by its public id, or every crash report made before a time
// when publicID is "", and returns the app logs no report names any more, for their files to
// be removed. Deleting one that is not there is fine.
func (d *DB) DeleteReports(ctx context.Context, publicID string, crashesBefore time.Time) (unusedLogs []string, err error) {
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		where, arg := `public_id = ?`, any(publicID)
		if publicID == "" {
			where, arg = `kind = 'crash' AND created_at < ?`, crashesBefore.UnixMilli()
		}
		rows, err := tx.Query(`DELETE FROM reports WHERE `+where+` RETURNING ifnull(log_hash, '')`, arg)
		if err != nil {
			return err
		}
		var logs []string
		for rows.Next() {
			var hash string
			if err := rows.Scan(&hash); err != nil {
				rows.Close()
				return err
			}
			if hash != "" {
				logs = append(logs, hash)
			}
		}
		rows.Close()
		for _, hash := range logs {
			var used bool
			if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM reports WHERE log_hash = ?)`, hash).Scan(&used); err != nil {
				return err
			}
			if !used {
				unusedLogs = append(unusedLogs, hash)
			}
		}
		return rows.Err()
	})
	return unusedLogs, err
}
