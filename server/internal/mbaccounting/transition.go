// Package mbaccounting owns the private count facts of one real pin mutation.
// Its callers retain publication authority and the exact pin statement. This
// package never stores a query, selector, response, or enumerated target list.
package mbaccounting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

var (
	ErrBusy      = errors.New("MusicBrainz accounting mutation is in progress")
	ErrStale     = errors.New("MusicBrainz accounting mutation changed")
	ErrInvariant = errors.New("MusicBrainz accounting invariant failed")
)

const FactsPerTurn = 11
const InactivityLease = 90 * time.Second

type timeKey struct{}

// WithTime uses the caller's existing worker clock for inactivity only. It is
// never a domain-authority witness; the final writer still reauthorizes.
func WithTime(ctx context.Context, at time.Time) context.Context {
	return context.WithValue(ctx, timeKey{}, at)
}
func nowMS(ctx context.Context) int64 {
	if at, ok := ctx.Value(timeKey{}).(time.Time); ok {
		return at.UnixMilli()
	}
	return time.Now().UnixMilli()
}
func nextExpiry(ctx context.Context) int64 { return nowMS(ctx) + InactivityLease.Milliseconds() }

type Owner struct {
	Kind, Operation string
	Generation      int64
}
type Change struct {
	Owner                                              Owner
	ID, Link, Digest                                   string
	Entity                                             int64
	OldRelease, NewRelease, OldRecording, NewRecording string
}
type State struct {
	ExpiresMS                                                int64
	Owner                                                    Owner
	Phase, ID, Link, Digest, Source, Cursor                  string
	Entity                                                   int64
	OldRelease, NewRelease                                   string
	OldDirect, NewDirect, Rows, DeltaRecordings, DeltaCharge int64
	Published                                                bool
}

func ReadTx(ctx context.Context, tx *sql.Tx) (s State, err error) {
	err = tx.QueryRowContext(ctx, `SELECT phase,transition_id,published_next,expires_ms,owner_kind,owner_operation,owner_generation,stage_link,COALESCE(stage_entity,0),stage_pin_digest,stage_old_release,stage_new_release,stage_old_direct,stage_new_direct,stage_source,stage_cursor,stage_rows,stage_delta_recordings,stage_delta_charge FROM mb_accounting_state WHERE singleton=1`).Scan(&s.Phase, &s.ID, &s.Published, &s.ExpiresMS, &s.Owner.Kind, &s.Owner.Operation, &s.Owner.Generation, &s.Link, &s.Entity, &s.Digest, &s.OldRelease, &s.NewRelease, &s.OldDirect, &s.NewDirect, &s.Source, &s.Cursor, &s.Rows, &s.DeltaRecordings, &s.DeltaCharge)
	return
}
func IdleTx(ctx context.Context, tx *sql.Tx) error {
	s, e := ReadTx(ctx, tx)
	if e != nil {
		return e
	}
	if s.Phase != "idle" {
		return ErrBusy
	}
	return nil
}
func same(s State, c Change) bool {
	return s.ID == c.ID && s.Owner == c.Owner && s.Link == c.Link && s.Entity == c.Entity && s.Digest == c.Digest && s.OldRelease == c.OldRelease && s.NewRelease == c.NewRelease
}
func releaseRefs(ctx context.Context, tx *sql.Tx, revision string) (n int64, err error) {
	if revision == "" {
		return 0, nil
	}
	err = tx.QueryRowContext(ctx, `SELECT direct_refs FROM mb_release_accounting WHERE revision_id=?`, revision).Scan(&n)
	return
}

// BeginTx takes the singleton only after the caller proves its live domain
// authority and captures the actual old pin. No track set is read here.
func BeginTx(ctx context.Context, tx *sql.Tx, c Change) (State, error) {
	s, e := ReadTx(ctx, tx)
	if e != nil {
		return s, e
	}
	if s.Phase != "idle" {
		if !same(s, c) {
			return s, ErrBusy
		}
		return s, nil
	}
	if c.ID == "" || c.Digest == "" || c.Entity <= 0 || c.Owner.Operation == "" || c.Owner.Generation <= 0 || (c.Owner.Kind != "publication" && c.Owner.Kind != "entity-mutation") || (c.Link != "album" && c.Link != "song") {
		return s, ErrInvariant
	}
	old, e := releaseRefs(ctx, tx, c.OldRelease)
	if e != nil {
		return s, e
	}
	next, e := releaseRefs(ctx, tx, c.NewRelease)
	if e != nil {
		return s, e
	}
	if c.OldRelease != "" && old <= 0 {
		return s, ErrInvariant
	}
	_, e = tx.ExecContext(ctx, `UPDATE mb_accounting_state SET phase='prepare',transition_id=?,published_next=0,expires_ms=?,owner_kind=?,owner_operation=?,owner_generation=?,stage_kind=?,stage_link=?,stage_entity=?,stage_old_release=?,stage_new_release=?,stage_old_direct=?,stage_new_direct=?,stage_pin_digest=?,stage_cursor='',stage_source='old-tracks',stage_rows=0,stage_delta_recordings=0,stage_delta_charge=0 WHERE singleton=1 AND phase='idle'`, c.ID, nextExpiry(ctx), c.Owner.Kind, c.Owner.Operation, c.Owner.Generation, changeKind(c), c.Link, c.Entity, c.OldRelease, c.NewRelease, old, next, c.Digest)
	if e != nil {
		return s, e
	}
	return ReadTx(ctx, tx)
}
func changeKind(c Change) string {
	if c.NewRelease == "" && c.NewRecording == "" {
		return "unpin"
	}
	if c.OldRelease == "" && c.OldRecording == "" {
		return "pin"
	}
	return "move"
}

type fact struct {
	revision                string
	direct, release, charge int64
	existed                 bool
}

func readFact(ctx context.Context, tx *sql.Tx, id, revision string) (f fact, e error) {
	f.revision = revision
	e = tx.QueryRowContext(ctx, `SELECT charge,direct_refs,release_refs FROM mb_recording_accounting_base WHERE revision_id=?`, revision).Scan(&f.charge, &f.direct, &f.release)
	if e != nil {
		return f, e
	}
	var direct, release int64
	e = tx.QueryRowContext(ctx, `SELECT direct_refs,release_refs FROM mb_recording_accounting_next WHERE transition_id=? AND revision_id=?`, id, revision).Scan(&direct, &release)
	if errors.Is(e, sql.ErrNoRows) {
		return f, nil
	}
	if e != nil {
		return f, e
	}
	f.direct, f.release, f.existed = direct, release, true
	return f, nil
}
func unaccepted(direct, release int64) int64 {
	if direct == 0 && release == 0 {
		return 1
	}
	return 0
}
func addFact(ctx context.Context, tx *sql.Tx, id, revision string, direct, release int64) (added, records, charge int64, err error) {
	f, e := readFact(ctx, tx, id, revision)
	if e != nil {
		return 0, 0, 0, e
	}
	nd, nr := f.direct+direct, f.release+release
	if nd < 0 || nr < 0 {
		return 0, 0, 0, ErrInvariant
	}
	delta := unaccepted(nd, nr) - unaccepted(f.direct, f.release)
	_, e = tx.ExecContext(ctx, `INSERT INTO mb_recording_accounting_next(transition_id,revision_id,direct_refs,release_refs) VALUES(?,?,?,?) ON CONFLICT(transition_id,revision_id) DO UPDATE SET direct_refs=excluded.direct_refs,release_refs=excluded.release_refs`, id, revision, nd, nr)
	if !f.existed {
		added = 1
	}
	return added, delta, delta * f.charge, e
}
func advance(ctx context.Context, tx *sql.Tx, s State, source, cursor string, added, records, charge int64) error {
	if s.Rows+added > 20002 {
		return ErrInvariant
	}
	r, e := tx.ExecContext(ctx, `UPDATE mb_accounting_state SET stage_source=?,stage_cursor=?,expires_ms=?,stage_rows=stage_rows+?,stage_delta_recordings=stage_delta_recordings+?,stage_delta_charge=stage_delta_charge+? WHERE singleton=1 AND phase='prepare' AND transition_id=? AND owner_generation=? AND stage_source=? AND stage_cursor=?`, source, cursor, nextExpiry(ctx), added, records, charge, s.ID, s.Owner.Generation, s.Source, s.Cursor)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrStale
	}
	return nil
}

// PrepareTx reads a raw primary-key track window before any acceptance filter.
// Repeated recordings are genuine repeated release memberships. Each raw track
// updates its prospective count once, together with cursor and delta progress.
func PrepareTx(ctx context.Context, tx *sql.Tx, c Change) (ready bool, err error) {
	s, e := ReadTx(ctx, tx)
	if e != nil {
		return false, e
	}
	if !same(s, c) {
		return false, ErrStale
	}
	if s.ExpiresMS <= nowMS(ctx) {
		return false, ErrStale
	}
	if s.Phase == "ready" {
		return true, nil
	}
	if s.Phase != "prepare" {
		return false, ErrStale
	}
	source, nextSource, revision, delta := s.Source, "", "", int64(0)
	switch source {
	case "old-tracks":
		nextSource = "new-tracks"
		revision = s.OldRelease
		if s.OldDirect == 1 && s.OldRelease != s.NewRelease {
			delta = -1
		}
	case "new-tracks":
		nextSource = "old-direct"
		revision = s.NewRelease
		if s.NewDirect == 0 && s.OldRelease != s.NewRelease {
			delta = 1
		}
	case "old-direct", "new-direct":
		rec, d := c.OldRecording, int64(-1)
		nextSource = "new-direct"
		if source == "new-direct" {
			rec, d, nextSource = c.NewRecording, 1, "done"
		}
		var added, records, charge int64
		if rec != "" {
			added, records, charge, e = addFact(ctx, tx, s.ID, rec, d, 0)
			if e != nil {
				return false, e
			}
		}
		return false, advance(ctx, tx, s, nextSource, "", added, records, charge)
	case "done":
		old, e := releaseRefs(ctx, tx, s.OldRelease)
		if e != nil {
			return false, e
		}
		next, e := releaseRefs(ctx, tx, s.NewRelease)
		if e != nil {
			return false, e
		}
		if old != s.OldDirect || next != s.NewDirect {
			return false, ErrStale
		}
		_, e = tx.ExecContext(ctx, `UPDATE mb_accounting_state SET phase='ready',expires_ms=? WHERE singleton=1 AND phase='prepare' AND transition_id=? AND stage_source='done'`, nextExpiry(ctx), s.ID)
		return e == nil, e
	default:
		return false, ErrInvariant
	}
	if revision == "" || delta == 0 {
		return false, advance(ctx, tx, s, nextSource, "", 0, 0, 0)
	}
	rows, e := tx.QueryContext(ctx, `SELECT track_id,recording_revision FROM mb_release_tracks WHERE release_revision=? AND track_id>? ORDER BY track_id LIMIT 11`, revision, s.Cursor)
	if e != nil {
		return false, e
	}
	type rawTrack struct {
		id        string
		recording sql.NullString
	}
	var raw []rawTrack
	for rows.Next() {
		var r rawTrack
		if e = rows.Scan(&r.id, &r.recording); e != nil {
			rows.Close()
			return false, e
		}
		raw = append(raw, r)
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil {
		return false, e
	}
	if len(raw) == 0 {
		return false, advance(ctx, tx, s, nextSource, "", 0, 0, 0)
	}
	var added, records, charge int64
	for _, r := range raw {
		if !r.recording.Valid {
			continue
		}
		a, n, b, e := addFact(ctx, tx, s.ID, r.recording.String, 0, delta)
		if e != nil {
			return false, e
		}
		added += a
		records += n
		charge += b
	}
	return false, advance(ctx, tx, s, source, raw[len(raw)-1].id, added, records, charge)
}

// FlipTx runs within the caller's existing authorized final transaction. The
// callback must perform the captured pin statement and the unchanged exact cap
// check after this package applies its delta. Any error rolls back the caller's
// entire writer, including pointer, pin, totals and domain publication.
func FlipTx(ctx context.Context, tx *sql.Tx, c Change, apply func() error, checkCaps func() error) error {
	s, e := ReadTx(ctx, tx)
	if e != nil {
		return e
	}
	if !same(s, c) || s.Phase != "ready" || s.ExpiresMS <= nowMS(ctx) || apply == nil || checkCaps == nil {
		return ErrStale
	}
	dbwork.ObserveAccountingWAL(tx, dbwork.AccountingFlip)
	if _, e = tx.ExecContext(ctx, `UPDATE mb_accounting_state SET phase='flip' WHERE singleton=1 AND phase='ready' AND transition_id=?`, s.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mb_accounting_state SET published_next=1 WHERE singleton=1 AND phase='flip' AND transition_id=?`, s.ID); e != nil {
		return e
	}
	if e = apply(); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mb_evidence_totals SET recordings=recordings+?,charge=charge+? WHERE singleton=1`, s.DeltaRecordings, s.DeltaCharge); e != nil {
		return e
	}
	if e = checkCaps(); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE mb_accounting_state SET phase='fold',last_transition=?,last_outcome='applied' WHERE singleton=1 AND phase='flip' AND transition_id=?`, s.ID, s.ID)
	return e
}
func AbortTx(ctx context.Context, tx *sql.Tx, owner Owner) error {
	s, e := ReadTx(ctx, tx)
	if e != nil {
		return e
	}
	if s.Phase == "idle" {
		return nil
	}
	if s.Owner != owner {
		return ErrBusy
	}
	if s.Published {
		return ErrStale
	}
	_, e = tx.ExecContext(ctx, `UPDATE mb_accounting_state SET phase='fold',last_outcome='aborted' WHERE singleton=1 AND phase IN('prepare','ready') AND transition_id=?`, s.ID)
	return e
}

// FoldTx performs at most22 child effects plus one state update. After a flip
// it needs no expired operation lease: the committed acceptance owns completion.
func FoldTx(ctx context.Context, tx *sql.Tx) (done bool, err error) {
	s, e := ReadTx(ctx, tx)
	if e != nil {
		return false, e
	}
	if s.Phase == "idle" {
		return true, nil
	}
	if s.Phase != "fold" {
		return false, ErrBusy
	}
	dbwork.ObserveAccountingWAL(tx, dbwork.AccountingFold)
	rows, e := tx.QueryContext(ctx, `SELECT revision_id,direct_refs,release_refs FROM mb_recording_accounting_next WHERE transition_id=? ORDER BY revision_id LIMIT 11`, s.ID)
	if e != nil {
		return false, e
	}
	var facts []fact
	for rows.Next() {
		var f fact
		if e = rows.Scan(&f.revision, &f.direct, &f.release); e != nil {
			rows.Close()
			return false, e
		}
		facts = append(facts, f)
	}
	e = errors.Join(rows.Err(), rows.Close())
	if e != nil {
		return false, e
	}
	for _, f := range facts {
		if s.Published {
			r, e := tx.ExecContext(ctx, `UPDATE mb_recording_accounting_base SET direct_refs=?,release_refs=? WHERE revision_id=?`, f.direct, f.release, f.revision)
			if e != nil {
				return false, e
			}
			n, e := r.RowsAffected()
			if e != nil {
				return false, e
			}
			if n != 1 {
				return false, fmt.Errorf("%w: missing base fact", ErrInvariant)
			}
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM mb_recording_accounting_next WHERE transition_id=? AND revision_id=?`, s.ID, f.revision); e != nil {
			return false, e
		}
	}
	var remains bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mb_recording_accounting_next WHERE transition_id=?)`, s.ID).Scan(&remains); e != nil {
		return false, e
	}
	if remains {
		return false, nil
	}
	_, e = tx.ExecContext(ctx, `UPDATE mb_accounting_state SET phase='idle',transition_id='',published_next=0,expires_ms=0,owner_kind='',owner_operation='',owner_generation=0,stage_kind='',stage_link='',stage_entity=0,stage_old_release='',stage_new_release='',stage_old_direct=0,stage_new_direct=0,stage_pin_digest='',stage_cursor='',stage_source='',stage_rows=0,stage_delta_recordings=0,stage_delta_charge=0 WHERE singleton=1 AND phase='fold' AND transition_id=?`, s.ID)
	return e == nil, e
}

// MaintainTx releases an abandoned private owner, or advances an accepted fold.
// live checks the existing domain's point-bound owner/lease; expiry is only a
// cleanup deadline. A published transition is always folded, never undone.
func MaintainTx(ctx context.Context, tx *sql.Tx, live func(Owner) (bool, error)) (worked bool, err error) {
	s, e := ReadTx(ctx, tx)
	if e != nil {
		return false, e
	}
	if s.Phase == "idle" {
		return false, nil
	}
	if s.Phase == "fold" {
		_, e = FoldTx(ctx, tx)
		return e == nil, e
	}
	if s.Phase != "prepare" && s.Phase != "ready" {
		return false, ErrInvariant
	}
	alive := true
	if s.ExpiresMS > nowMS(ctx) && live != nil {
		alive, e = live(s.Owner)
		if e != nil {
			return false, e
		}
	}
	if s.ExpiresMS <= nowMS(ctx) || !alive {
		e = AbortTx(ctx, tx, s.Owner)
		return e == nil, e
	}
	return false, nil
}
