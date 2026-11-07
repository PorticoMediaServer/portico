package writer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// These are the existing video_nfo_evidence native values, not a replacement
// catalogue document. Parsing and metadata projection happen in preparation.
// Before is a complete owned snapshot; After is a complete observed set. An
// unavailable/partial observation must never be passed as known absence.
type basicNFORow struct {
	locator, kind, coordinate, digest, payload string
	size, modifiedNS                           int64
	observedAt                                 string
}
type basicNFOSet struct {
	source        basicStreamFactStage
	library       string
	before, after []basicNFORow
	beforeStatus  *basicNFOStatus
	afterStatus   basicNFOStatus
	valid         bool
}
type basicNFOStatus struct{ status, issue string }
type basicNFOProgress struct{ removed, inserted int64 }

func basicNFOTexts(r basicNFORow) [6]string {
	return [6]string{r.locator, r.kind, r.coordinate, r.digest, r.payload, r.observedAt}
}
func prepareBasicNFOSet(source basicStreamFactStage, library string, before, after []basicNFORow, oldStatus *basicNFOStatus, status basicNFOStatus, complete bool) (*basicNFOSet, error) {
	if !complete || library == "" || status.status != "observed" || (status.issue != "" && status.issue != "video_nfo_fields_skipped") {
		return nil, ErrInvalid
	}
	// Includes both simultaneous snapshots and per-row storage before any copy,
	// JSON decode, map, or argument allocation. This is the original preparation
	// envelope, not a numerical W memory/admission profile.
	remaining := int64(33554432)
	for _, s := range [...]string{library, source.assetToken, source.runID, source.sourceID, source.incarnation, source.objectID, source.objectRevision, source.fingerprint, status.status, status.issue} {
		if int64(len(s)) > remaining {
			return nil, ErrCap
		}
		if !utf8.ValidString(s) {
			return nil, ErrInvalid
		}
		remaining -= int64(len(s))
	}
	if oldStatus != nil {
		for _, s := range [...]string{oldStatus.status, oldStatus.issue} {
			if !utf8.ValidString(s) {
				return nil, ErrInvalid
			}
			if int64(len(s)) > remaining {
				return nil, ErrCap
			}
			remaining -= int64(len(s))
		}
	}
	for setIndex, set := range [2][]basicNFORow{before, after} {
		for _, r := range set {
			if setIndex == 1 {
				if remaining < 64 {
					return nil, ErrCap
				}
				remaining -= 64
			}
			if remaining < 192 {
				return nil, ErrCap
			}
			remaining -= 192
			for _, s := range basicNFOTexts(r) {
				if int64(len(s)) > remaining {
					return nil, ErrCap
				}
				remaining -= int64(len(s))
			}
		}
	}
	for setIndex, set := range [2][]basicNFORow{before, after} {
		var previous [3]string
		for n, r := range set {
			key := [3]string{r.locator, r.kind, r.coordinate}
			if n > 0 && !basicNFOKeyLess(previous, key) {
				return nil, ErrInvalid
			}
			previous = key
			if r.locator == "" || r.digest == "" || (setIndex == 0 && r.observedAt == "") || (setIndex == 1 && r.observedAt != "") || r.size < 0 || (r.kind != "movie" && r.kind != "show" && r.kind != "episode") {
				return nil, ErrInvalid
			}
			// The native sidecar locator is carried exactly. TEXT cannot store a
			// non-UTF-8 filename; keep that typed refusal distinct from NUL.
			if strings.IndexByte(r.locator, 0) >= 0 {
				return nil, ErrInvalid
			}
			if !utf8.ValidString(r.locator) {
				return nil, ErrFilenameUnrepresentable
			}
			for _, s := range basicNFOTexts(r) {
				if !utf8.ValidString(s) {
					return nil, ErrInvalid
				}
			}
			// Coordinate and payload are exactly the bounded parsed evidence JSON
			// already used by this table. Neither is a source revision or identity.
			var coordinate []*int
			if !json.Valid([]byte(r.payload)) || json.Unmarshal([]byte(r.coordinate), &coordinate) != nil || len(coordinate) != 3 {
				return nil, ErrInvalid
			}
		}
	}
	cloneRows := func(rows []basicNFORow) []basicNFORow {
		out := make([]basicNFORow, len(rows))
		for n, r := range rows {
			r.locator = strings.Clone(r.locator)
			r.kind = strings.Clone(r.kind)
			r.coordinate = strings.Clone(r.coordinate)
			r.digest = strings.Clone(r.digest)
			r.payload = strings.Clone(r.payload)
			r.observedAt = strings.Clone(r.observedAt)
			out[n] = r
		}
		return out
	}
	out := &basicNFOSet{source: cloneBasicSource(source), library: strings.Clone(library), before: cloneRows(before), after: cloneRows(after), afterStatus: basicNFOStatus{strings.Clone(status.status), strings.Clone(status.issue)}, valid: true}
	if oldStatus != nil {
		out.beforeStatus = &basicNFOStatus{strings.Clone(oldStatus.status), strings.Clone(oldStatus.issue)}
	}
	return out, nil
}
func basicNFOKeyLess(a, b [3]string) bool {
	for n := range a {
		if a[n] != b[n] {
			return a[n] < b[n]
		}
	}
	return false
}
func cloneBasicSource(s basicStreamFactStage) basicStreamFactStage {
	s.assetToken = strings.Clone(s.assetToken)
	s.runID = strings.Clone(s.runID)
	s.sourceID = strings.Clone(s.sourceID)
	s.incarnation = strings.Clone(s.incarnation)
	s.objectID = strings.Clone(s.objectID)
	s.objectRevision = strings.Clone(s.objectRevision)
	s.fingerprint = strings.Clone(s.fingerprint)
	return s
}
func basicNFOValues(r basicNFORow) []any {
	return []any{r.locator, r.kind, r.coordinate, r.digest, r.payload, r.size, r.modifiedNS, r.observedAt}
}

// applyBasicNFO replaces only the exact scoped complete old cohort. The caller
// owns ONE source/receipt transaction; a later failure rolls all changes back.
// This evidence write does not publish effective metadata or Basic readiness.
func applyBasicNFO(ctx context.Context, turn *commandTurn, in *basicNFOSet, appliedMS int64) (value basicNFOProgress, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || in == nil || !in.valid || appliedMS < 0 {
		return value, ErrInvalid
	}
	observedAt := time.UnixMilli(appliedMS).UTC().Format(time.RFC3339Nano)
	if len(observedAt) > 64 {
		return value, ErrInvalid
	}
	if err = checkBasicStreamInput(ctx, turn, in.source); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicNFOLibrary, in.source.libraryID)
	if err != nil {
		return value, err
	}
	var library string
	if err = row.Scan(&library); err != nil {
		return value, basicStageError(err)
	}
	if library != in.library {
		return value, ErrConflict
	}
	rows, err := basicRows(ctx, turn, basicNFORows, in.library, in.source.assetToken, int64(len(in.before))+1)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	n := 0
	for rows.Next() {
		if n >= len(in.before) {
			return value, ErrConflict
		}
		if err = basicMediaScan(rows, basicNFOValues(in.before[n])); err != nil {
			return value, err
		}
		n++
	}
	if err = rows.Err(); err != nil {
		return value, err
	}
	if n != len(in.before) {
		return value, ErrConflict
	}
	if err = rows.Close(); err != nil {
		return value, err
	}
	statusRows, err := basicRows(ctx, turn, basicNFOStatusRead, in.library, in.source.assetToken)
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, statusRows.Close()) }()
	if in.beforeStatus == nil {
		if statusRows.Next() {
			return value, ErrConflict
		}
		if err = statusRows.Err(); err != nil {
			return value, err
		}
	} else if err = basicMediaExactRow(statusRows, []any{in.beforeStatus.status, in.beforeStatus.issue}); err != nil {
		return value, err
	}
	if err = statusRows.Close(); err != nil {
		return value, err
	}
	for _, r := range in.before {
		step := turn.steps[basicNFODelete]
		result, e := turn.exec(ctx, step.statementStep, in.library, in.source.assetToken, r.locator, r.kind, r.coordinate, r.digest, r.payload, r.size, r.modifiedNS, r.observedAt)
		if e != nil {
			return value, e
		}
		count, e := result.RowsAffected()
		if e != nil {
			return value, e
		}
		if count != 1 {
			return value, ErrConflict
		}
		value.removed++
	}
	for _, r := range in.after {
		r.observedAt = observedAt
		args := append([]any{in.library, in.source.assetToken}, basicNFOValues(r)...)
		rows, e := basicRows(ctx, turn, basicNFOInsert, args...)
		if e != nil {
			return value, e
		}
		if e = basicMediaExactRow(rows, args); e != nil {
			return value, e
		}
		value.inserted++
	}
	var oldStatus, oldIssue any
	id := basicNFOStatusInsert
	if in.beforeStatus != nil {
		id = basicNFOStatusCAS
		oldStatus = in.beforeStatus.status
		oldIssue = in.beforeStatus.issue
	}
	args := []any{in.library, in.source.assetToken, in.afterStatus.status, in.afterStatus.issue}
	if in.beforeStatus != nil {
		args = []any{in.afterStatus.status, in.afterStatus.issue, in.library, in.source.assetToken, oldStatus, oldIssue}
	}
	terminalRows, err := basicRows(ctx, turn, id, args...)
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(terminalRows, []any{in.library, in.source.assetToken, in.afterStatus.status, in.afterStatus.issue}); err != nil {
		return value, err
	}
	return value, nil
}
