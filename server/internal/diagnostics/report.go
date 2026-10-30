// Package diagnostics exports a bounded allowlist of recorded operational facts.
// It never reads source paths, account/session data, raw logs or playback tables.
package diagnostics

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/buildinfo"
	"regexp"
	"runtime"
	"time"
)

const FailureLimit = 20
const MaxReportBytes = 32768

var ErrUnavailable = errors.New("support report unavailable")

type Build struct {
	Version      *string `json:"version"`
	BuildID      *string `json:"buildId"`
	SourceDigest *string `json:"sourceDigest"`
	BuiltAt      *string `json:"builtAt"`
	TargetOS     string  `json:"targetOS"`
	TargetArch   string  `json:"targetArch"`
	GoVersion    *string `json:"goVersion"`
}
type Counts struct {
	Total   int64            `json:"total"`
	ByState map[string]int64 `json:"byState"`
}
type Mounts struct {
	Available bool    `json:"available"`
	Counts    *Counts `json:"counts"`
}
type ScanFailure struct {
	Code       string  `json:"code"`
	CreatedAt  *string `json:"createdAt"`
	UpdatedAt  *string `json:"updatedAt"`
	FinishedAt *string `json:"finishedAt"`
	Processed  *int64  `json:"processed"`
}
type Failures struct {
	Limit   int           `json:"limit"`
	HasMore bool          `json:"hasMore"`
	Items   []ScanFailure `json:"items"`
}
type Report struct {
	SchemaVersion      int      `json:"schemaVersion"`
	ObservedAt         string   `json:"observedAt"`
	Build              Build    `json:"build"`
	Database           string   `json:"database"`
	HostedConfigured   bool     `json:"hostedConfigured"`
	Libraries          Counts   `json:"libraries"`
	Scans              Counts   `json:"scans"`
	Mounts             Mounts   `json:"mounts"`
	RecentScanFailures Failures `json:"recentScanFailures"`
}

var buildToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

func token(s string) *string {
	if !buildToken.MatchString(s) {
		return nil
	}
	return &s
}
func timestamp(s string) *string {
	t, e := time.Parse(time.RFC3339Nano, s)
	if e != nil {
		return nil
	}
	v := t.UTC().Format(time.RFC3339Nano)
	return &v
}
func BuildFacts() Build {
	v := buildinfo.Info()
	return Build{Version: token(v["version"]), BuildID: token(v["buildId"]), SourceDigest: token(v["sourceDigest"]), BuiltAt: timestamp(v["builtAt"]), TargetOS: runtime.GOOS, TargetArch: runtime.GOARCH, GoVersion: token(runtime.Version())}
}
func counts(ctx context.Context, tx *sql.Tx, query string) (Counts, error) {
	out := Counts{ByState: map[string]int64{}}
	rows, e := tx.QueryContext(ctx, query)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var n int64
		if e = rows.Scan(&name, &n); e != nil {
			return out, e
		}
		if n < 0 || n > 9007199254740991 {
			return out, ErrUnavailable
		}
		out.ByState[name] = n
		out.Total += n
		if out.Total > 9007199254740991 {
			return out, ErrUnavailable
		}
	}
	return out, rows.Err()
}
func Read(ctx context.Context, db *sql.DB, hosted bool) (Report, error) {
	out := Report{SchemaVersion: 1, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Build: BuildFacts(), Database: "readable", HostedConfigured: hosted, RecentScanFailures: Failures{Limit: FailureLimit, Items: []ScanFailure{}}}
	if db == nil {
		return out, ErrUnavailable
	}
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, ErrUnavailable
	}
	defer tx.Rollback()
	out.Libraries, e = counts(ctx, tx, `SELECT CASE WHEN kind IN('movie','tv','anime','music','audiobook') THEN kind ELSE 'unknown' END,count(*) FROM libraries GROUP BY 1`)
	if e != nil {
		return out, ErrUnavailable
	}
	out.Scans, e = counts(ctx, tx, `SELECT CASE WHEN status IN('queued','running','complete','failed','cancelled') THEN status ELSE 'unknown' END,count(*) FROM jobs GROUP BY 1`)
	if e != nil {
		return out, ErrUnavailable
	}
	var mountsExist bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='managed_mounts')`).Scan(&mountsExist); e != nil {
		return out, ErrUnavailable
	}
	if mountsExist {
		value, err := counts(ctx, tx, `SELECT CASE WHEN observed IN('stopped','starting','mounted','stopping','failed','unavailable','quarantined') THEN observed ELSE 'unknown' END,count(*) FROM managed_mounts GROUP BY 1`)
		if err != nil {
			return out, ErrUnavailable
		}
		out.Mounts = Mounts{Available: true, Counts: &value}
	}
	rows, e := tx.QueryContext(ctx, `SELECT CASE WHEN j.error='scan_source_unavailable' THEN 'source_unavailable' ELSE 'scan_failed' END,j.created_at,o.updated_at,o.finished_at,CASE WHEN j.processed>=0 AND j.processed<=9007199254740991 THEN j.processed ELSE NULL END FROM jobs j LEFT JOIN job_observations o ON o.job_id=j.id WHERE j.status='failed' ORDER BY j.created_at DESC,j.id DESC LIMIT 21`)
	if e != nil {
		return out, ErrUnavailable
	}
	for rows.Next() {
		if len(out.RecentScanFailures.Items) == FailureLimit {
			out.RecentScanFailures.HasMore = true
			break
		}
		var f ScanFailure
		var created string
		var updated, finished sql.NullString
		if e = rows.Scan(&f.Code, &created, &updated, &finished, &f.Processed); e != nil {
			rows.Close()
			return out, ErrUnavailable
		}
		f.CreatedAt = timestamp(created)
		f.UpdatedAt = timestamp(updated.String)
		f.FinishedAt = timestamp(finished.String)
		out.RecentScanFailures.Items = append(out.RecentScanFailures.Items, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, ErrUnavailable
	}
	if e = tx.Commit(); e != nil {
		return out, ErrUnavailable
	}
	return out, nil
}
