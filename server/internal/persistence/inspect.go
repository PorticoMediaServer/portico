package persistence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Restore-candidate validation errors. The admin API reports them as
// restore_not_portico, restore_integrity_failed and
// restore_pre_release.
var (
	ErrCandidateNotPortico = errors.New("this file is not a Portico database")
	ErrCandidateIntegrity  = errors.New("this database copy failed its integrity check")
	ErrCandidatePreRelease = ErrSchemaResetRequired
)

// sqliteHeader opens every SQLite database file. A candidate without it is
// not a database at all, let alone a Portico one.
const sqliteHeader = "SQLite format 3\x00"

// CandidateReport is what inspecting a restore candidate found, and what was
// done to make it usable. Integrity is "ok" for a healthy file, or
// "recovered" when Recovery says what was repaired or lost.
type CandidateReport struct {
	SchemaVersion int
	Integrity     string
	Recovery      *Recovery
}

// Recovery tells the owner what restore did to a damaged database. A copy of
// a running server's folder is the usual case: its log was mid-write, or the
// database file and its log were copied at different moments.
type Recovery struct {
	ManifestDamage *ManifestRecovery `json:"manifestDamage,omitempty"`
	// IndexesRebuilt: only indexes were damaged, and were rebuilt from the
	// table rows. Nothing was lost.
	IndexesRebuilt bool `json:"indexesRebuilt,omitempty"`
	// Salvaged: the database was rebuilt into a fresh file from every row
	// that could still be read.
	Salvaged bool `json:"salvaged,omitempty"`
	// DamagedTables are tables where some rows could not be read, or broke
	// the schema's own rules, and are missing from the restored database.
	DamagedTables []string `json:"damagedTables,omitempty"`
	// RemovedRows were readable but named something that was lost, and were
	// removed so the restored database is consistent.
	RemovedRows int64 `json:"removedRows,omitempty"`
}

// ManifestRecovery reports partial file recovery without retaining an unbounded
// list of damaged regions in memory or in API responses.
type ManifestRecovery struct {
	Count              int64            `json:"count"`
	Regions            []ManifestDamage `json:"regions"`
	RegionsTruncated   bool             `json:"regionsTruncated,omitempty"`
	MissingFiles       int64            `json:"missingFiles"`
	UnverifiedDatabase bool             `json:"unverifiedDatabase,omitempty"`
}
type ManifestDamage struct {
	FirstLine int64  `json:"firstLine"`
	LastLine  int64  `json:"lastLine"`
	StartByte int64  `json:"startByte"`
	EndByte   int64  `json:"endByte"`
	Reason    string `json:"reason"`
}

// InspectCandidate checks a restore candidate, dir/name, and makes it usable
// if it can (JUSTIN-ANSWERS #21): a readable file is tried whatever its
// source, including a copy taken while a server was running. It opens the
// file privately (dbwork.Candidate), so SQLite replays every complete
// transaction in a -wal beside it and ignores a torn tail, folds the log into
// the file and checks the result. Damaged indexes are rebuilt; anything worse
// is salvaged row by row into a fresh copy of this baseline. Only a file that
// is not a SQLite database, is not Portico's, belongs to a different baseline,
// or cannot have its schema read at all is refused. The candidate must be a
// private copy: recovery rewrites it in place.
func InspectCandidate(ctx context.Context, dir, name string) (CandidateReport, error) {
	report := CandidateReport{SchemaVersion: SchemaVersion(), Integrity: "ok"}
	head, err := os.Open(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		return report, ErrCandidateNotPortico
	}
	prefix := make([]byte, len(sqliteHeader))
	_, err = readFull(head, prefix)
	closeErr := head.Close()
	if err != nil || closeErr != nil || string(prefix) != sqliteHeader {
		return report, ErrCandidateNotPortico
	}
	candidate, err := dbwork.OpenCandidate(dir, name)
	if err != nil {
		return report, ErrCandidateIntegrity
	}
	// Merge the log first so the check sees the whole database. A fold that
	// fails is not the end: salvage reads what it can.
	folded := candidate.Fold(ctx) == nil
	if err = checkPortico(ctx, candidate); err != nil {
		candidate.Close()
		return report, err
	}
	lines, err := candidate.Check(ctx, false, 100)
	if err == nil && folded && dbwork.Healthy(lines) {
		return report, candidate.Close()
	}
	if err == nil && folded && indexDamageOnly(lines) {
		if _, err = candidate.ExecContext(ctx, `REINDEX`); err == nil {
			if lines, err = candidate.Check(ctx, false, 1); err == nil && dbwork.Healthy(lines) {
				if err = candidate.Fold(ctx); err == nil {
					report.Integrity, report.Recovery = "recovered", &Recovery{IndexesRebuilt: true}
					return report, candidate.Close()
				}
			}
		}
	}
	if err = candidate.Close(); err != nil {
		return report, ErrCandidateIntegrity
	}
	recovery, err := salvage(ctx, dir, name)
	if err != nil {
		return report, err
	}
	report.Integrity, report.Recovery = "recovered", recovery
	return report, nil
}

// checkPortico refuses what restore cannot use at all: a schema SQLite cannot
// read, a database without Portico's configuration table, or another baseline.
func checkPortico(ctx context.Context, candidate *dbwork.Candidate) error {
	var configuration int
	if err := candidate.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='configuration'`).Scan(&configuration); err != nil {
		return ErrCandidateIntegrity
	}
	if configuration != 1 {
		return ErrCandidateNotPortico
	}
	return verifyBaseline(ctx, candidate)
}

// indexDamageOnly reports whether every problem integrity_check found is in an
// index, which REINDEX rebuilds from the table rows without losing anything.
func indexDamageOnly(lines []string) bool {
	if len(lines) == 0 || dbwork.Healthy(lines) {
		return false
	}
	for _, line := range lines {
		if !(strings.HasPrefix(line, "row ") && strings.Contains(line, " missing from index ")) &&
			!strings.HasPrefix(line, "wrong # of entries in index ") &&
			!strings.HasPrefix(line, "non-unique entry in index ") {
			return false
		}
	}
	return true
}

func readFull(file *os.File, buf []byte) (int, error) {
	done := 0
	for done < len(buf) {
		n, err := file.Read(buf[done:])
		done += n
		if err != nil {
			return done, err
		}
	}
	return done, nil
}
