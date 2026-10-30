package dvr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

type Store struct {
	db               *sql.DB
	live             *livechannels.Store
	durable          DurableAuthority
	retainedCapture  DurableAuthority
	now              func() time.Time
	captureAvailable bool
	driver           CaptureDriver
	storage          StorageDriver
	policy           atomic.Value
	locks            *livechannels.PhysicalLocks
	instance         string
	mu               sync.Mutex
	active           map[string]map[string]context.CancelFunc
	wg               sync.WaitGroup
}

func New(db *sql.DB, live *livechannels.Store, durable DurableAuthority) (*Store, error) {
	if db == nil || live == nil || durable == nil {
		return nil, ErrInvalid
	}
	// The numbered schema migration owns the table. First-use initialization
	// only supplies its default row, preserving settings imported earlier.
	if _, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive,
		`INSERT OR IGNORE INTO dvr_defaults(singleton,revision,before_seconds,after_seconds,retention_days,episode_limit) VALUES(1,1,60,180,0,0)`); err != nil {
		return nil, ErrUnavailable
	}
	return &Store{db: db, live: live, durable: durable, now: time.Now, active: map[string]map[string]context.CancelFunc{}}, nil
}
func (s *Store) transaction(ctx context.Context, a livechannels.Authority, o livechannels.Owner, work func(*sql.Tx, func(string, string) bool) error) error {
	if dbwork.InWriteTx(ctx) {
		return runTransaction(ctx, dbwork.Snapshot(ctx), a, o, work)
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if e != nil {
		return ErrUnavailable
	}
	defer gated.Rollback()
	if e = runTransaction(ctx, gated.Tx(), a, o, work); e != nil {
		return e
	}
	return gated.Commit()
}
func (s *Store) snapshot(ctx context.Context, a livechannels.Authority, o livechannels.Owner, work func(*sql.Tx, func(string, string) bool) error) error {
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return ErrUnavailable
	}
	defer gated.Rollback()
	if e = runTransaction(ctx, gated.Tx(), a, o, work); e != nil {
		return e
	}
	return gated.Commit()
}
func runTransaction(ctx context.Context, tx *sql.Tx, a livechannels.Authority, o livechannels.Owner, work func(*sql.Tx, func(string, string) bool) error) error {
	if !o.Valid() || a == nil {
		return ErrDenied
	}
	fence, allowed, e := a(ctx, tx, false)
	if e != nil {
		return e
	}
	if fence == "" || allowed == nil {
		return ErrDenied
	}
	if e = work(tx, allowed); e != nil {
		return e
	}
	return ctx.Err()
}
func receiptTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, request string, input any, out any) (bool, error) {
	if !hexID.MatchString(request) {
		return false, ErrInvalid
	}
	var old, data string
	e := tx.QueryRowContext(ctx, `SELECT digest,result_json FROM dvr_receipts WHERE owner_key=? AND request_id=?`, o.Key(), request).Scan(&old, &data)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, ErrUnavailable
	}
	if old != digest(input) {
		return false, ErrConflict
	}
	if json.Unmarshal([]byte(data), out) != nil {
		return false, ErrUnavailable
	}
	return true, nil
}
func saveReceiptTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, request string, input, out any, now time.Time) error {
	b, e := json.Marshal(out)
	if e != nil {
		return ErrUnavailable
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO dvr_receipts VALUES(?,?,?,?,?)`, o.Key(), request, digest(input), string(b), now.UnixMilli())
	return e
}
func touchTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO dvr_owner_revisions VALUES(?,1) ON CONFLICT(owner_key) DO UPDATE SET revision=revision+1`, o.Key())
	return e
}

const recordingColumns = `id,revision,source_id,channel_id,guide_generation,programme_id,programme_json,rule_id,options_json,start_ms,end_ms,state,reason,keep,COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=dvr_recordings.item_id),''),captured_start_ms,captured_end_ms,bytes`

type scanner interface{ Scan(...any) error }

func scanRecording(row scanner) (Recording, error) {
	var r Recording
	var p, options string
	var start, end, ca, cb int64
	e := row.Scan(&r.ID, &r.Revision, &r.Occurrence.SourceID, &r.Occurrence.ChannelID, &r.Occurrence.Generation, &r.Occurrence.ProgrammeID, &p, &r.RuleID, &options, &start, &end, &r.State, &r.Reason, &r.Keep, &r.ItemID, &ca, &cb, &r.Bytes)
	if e != nil {
		return r, e
	}
	if json.Unmarshal([]byte(p), &r.Programme) != nil || json.Unmarshal([]byte(options), &r.Options) != nil {
		return r, ErrUnavailable
	}
	setSeriesGrouping(&r)
	r.Start = time.UnixMilli(start).UTC().Format(time.RFC3339Nano)
	r.End = time.UnixMilli(end).UTC().Format(time.RFC3339Nano)
	if ca != 0 {
		r.CoverageStart = time.UnixMilli(ca).UTC().Format(time.RFC3339Nano)
	}
	if cb != 0 {
		r.CoverageEnd = time.UnixMilli(cb).UTC().Format(time.RFC3339Nano)
	}
	r.Conflicts = []livechannels.LosingInterval{}
	return r, nil
}
func loadTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, id string) (Recording, error) {
	r, e := scanRecording(tx.QueryRowContext(ctx, `SELECT `+recordingColumns+` FROM dvr_recordings WHERE id=? AND owner_key=?`, id, o.Key()))
	if e == nil {
		e = decorateTx(ctx, tx, o, &r)
	}
	if errors.Is(e, sql.ErrNoRows) {
		return r, ErrDenied
	}
	if r.ItemID != "" {
		r.LibraryID = digest([]string{"private-recorded-library-v1", o.Key()})
	}
	return r, e
}
func (s *Store) Schedule(ctx context.Context, a livechannels.Authority, o livechannels.Owner, in ScheduleInput) (Recording, error) {
	var out Recording
	if !hexID.MatchString(in.RequestID) || !in.Occurrence.Valid() || !in.Options.Valid() {
		return out, ErrInvalid
	}
	e := s.transaction(ctx, a, o, func(tx *sql.Tx, allowed func(string, string) bool) error {
		if err := s.durable(ctx, tx, o, in.Occurrence.SourceID, in.Occurrence.ChannelID); err != nil {
			return ErrDenied
		}
		if !allowed(in.Occurrence.SourceID, in.Occurrence.ChannelID) {
			return ErrDenied
		}
		originalInput := in
		replay, e := receiptTx(ctx, tx, o, in.RequestID, in, &out)
		if e != nil {
			return e
		}
		if replay {
			return programmeVisibleTx(ctx, tx, o, out.Programme)
		}
		if in.UseDefaults {
			var err error
			in.Options, err = DefaultOptionsTx(ctx, tx)
			if err != nil {
				return err
			}
		}
		if !s.captureAvailable {
			return ErrCaptureUnavailable
		}
		p, e := s.live.ProgrammeTx(ctx, tx, in.Occurrence.SourceID, in.Occurrence.ChannelID, in.Occurrence.Generation, in.Occurrence.ProgrammeID)
		if e != nil {
			return e
		}
		if e = programmeVisibleTx(ctx, tx, o, p); e != nil {
			return e
		}
		start, end, e := padded(p, in.Options)
		if e != nil || !end.After(s.now()) {
			return ErrInvalid
		}
		out, e = s.insertTx(ctx, tx, o, in.Occurrence, p, in.Options, "", 0, true, start, end)
		if e != nil {
			return e
		}
		return saveReceiptTx(ctx, tx, o, in.RequestID, originalInput, out, s.now())
	})
	return out, e
}
func (s *Store) insertTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, binding Occurrence, p livechannels.Programme, options Options, rule string, ruleRevision int64, manual bool, start, end time.Time) (Recording, error) {
	rid := recordID(o, binding.SourceID, binding.ProgrammeID)
	r, e := loadTx(ctx, tx, o, rid)
	if e == nil { // One canonical occurrence per owner, regardless of which rule found it.
		if r.State == "cancelled" || r.State == "failed" || r.State == "deleted" || r.State == "pending-delete" {
			return r, ErrConflict
		}
		if manual && mutable(r.State) && r.RuleID != "" {
			// Converting rule intent into an explicit recording leaves history intact.
			if _, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET manual=1,rule_id='',rule_revision=0,revision=revision+1,updated_ms=? WHERE id=?`, s.now().UnixMilli(), rid); e != nil {
				return r, e
			}
			r.RuleID = ""
			r.Revision++
			if e = touchTx(ctx, tx, o); e != nil {
				return r, e
			}
		}
		return r, nil
	}
	if !errors.Is(e, ErrDenied) {
		return r, e
	}
	pb, _ := json.Marshal(p)
	ob, _ := json.Marshal(options)
	now := s.now().UnixMilli()
	_, e = tx.ExecContext(ctx, `INSERT INTO dvr_recordings(id,owner_key,authority,account_id,profile_id,source_id,channel_id,programme_id,guide_generation,programme_json,rule_id,rule_revision,manual,options_json,start_ms,end_ms,priority,revision,state,item_id,created_ms,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,'scheduled',0,?,?)`, rid, o.Key(), o.Authority, o.AccountID, o.ProfileID, binding.SourceID, binding.ChannelID, binding.ProgrammeID, binding.Generation, string(pb), rule, ruleRevision, manual, string(ob), start.UnixMilli(), end.UnixMilli(), options.Priority, now, now)
	if e != nil {
		return r, e
	}
	if e = reserveTx(ctx, tx, rid, binding.SourceID, start, end, options.Priority); e != nil {
		return r, e
	}
	if e = touchTx(ctx, tx, o); e != nil {
		return r, e
	}
	return loadTx(ctx, tx, o, rid)
}
func reserveTx(ctx context.Context, tx *sql.Tx, id, source string, start, end time.Time, priority int) error {
	if _, e := tx.ExecContext(ctx, `INSERT INTO live_reservations VALUES(?,?,?,?,?,1) ON CONFLICT(id) DO UPDATE SET start_ms=excluded.start_ms,end_ms=excluded.end_ms,priority=excluded.priority,enabled=1`, id, source, start.UnixMilli(), end.UnixMilli(), priority); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO live_source_dependencies VALUES(?,'recording',?)`, source, id)
	return e
}
func unreserveTx(ctx context.Context, tx *sql.Tx, id string) error {
	if _, e := tx.ExecContext(ctx, `UPDATE live_reservations SET enabled=0 WHERE id=?`, id); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `DELETE FROM live_source_dependencies WHERE kind='recording' AND id=? AND NOT EXISTS(SELECT 1 FROM dvr_recordings r WHERE r.id=live_source_dependencies.id AND (r.artifact_id!='' OR r.item_id!=0 OR r.state IN('scheduled','conflicted','waiting-source','waiting-guide')) AND r.state!='deleted')`, id)
	return e
}
func (s *Store) Update(ctx context.Context, a livechannels.Authority, o livechannels.Owner, id string, in UpdateInput) (Recording, error) {
	return s.mutate(ctx, a, o, id, "update", in.Mutation, in, func(ctx context.Context, tx *sql.Tx, r Recording) (Recording, error) {
		if e := s.durable(ctx, tx, o, r.Occurrence.SourceID, r.Occurrence.ChannelID); e != nil {
			return r, e
		}
		if !mutable(r.State) || !in.Options.Valid() {
			return r, ErrInvalid
		}
		start, end, e := padded(r.Programme, in.Options)
		if e != nil || !end.After(s.now()) {
			return r, ErrInvalid
		}
		b, _ := json.Marshal(in.Options)
		_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET manual=1,rule_id='',rule_revision=0,options_json=?,start_ms=?,end_ms=?,priority=?,state='scheduled',reason='',revision=revision+1,updated_ms=? WHERE id=?`, string(b), start.UnixMilli(), end.UnixMilli(), in.Options.Priority, s.now().UnixMilli(), id)
		if e != nil {
			return r, e
		}
		if e = reserveTx(ctx, tx, id, r.Occurrence.SourceID, start, end, in.Options.Priority); e != nil {
			return r, e
		}
		return loadTx(ctx, tx, o, id)
	})
}
func (s *Store) Cancel(ctx context.Context, a livechannels.Authority, o livechannels.Owner, id string, in Mutation) (Recording, error) {
	return s.mutate(ctx, a, o, id, "cancel", in, in, func(ctx context.Context, tx *sql.Tx, r Recording) (Recording, error) {
		if terminal(r.State) {
			if r.State == "cancelled" {
				return r, nil
			}
			return r, ErrConflict
		}
		_, e := tx.ExecContext(ctx, `UPDATE dvr_recordings SET state='cancelled',reason='owner-cancelled',cancel_generation=cancel_generation+1,revision=revision+1,updated_ms=?,finished_ms=? WHERE id=?`, s.now().UnixMilli(), s.now().UnixMilli(), id)
		if e != nil {
			return r, e
		}
		// The capture worker still owns its physical allocation until it has closed
		// the input and retired children. Logical cancellation never frees a tuner.
		if e = unreserveTx(ctx, tx, id); e != nil {
			return r, e
		}
		return loadTx(ctx, tx, o, id)
	})
}
func (s *Store) SetKeep(ctx context.Context, a livechannels.Authority, o livechannels.Owner, id string, in KeepInput) (Recording, error) {
	return s.mutate(ctx, a, o, id, "keep", in.Mutation, in, func(ctx context.Context, tx *sql.Tx, r Recording) (Recording, error) {
		if r.State == "pending-delete" || r.State == "deleted" {
			return r, ErrConflict
		}
		_, e := tx.ExecContext(ctx, `UPDATE dvr_recordings SET keep=?,revision=revision+1,updated_ms=? WHERE id=?`, in.Keep, s.now().UnixMilli(), id)
		if e != nil {
			return r, e
		}
		return loadTx(ctx, tx, o, id)
	})
}
func (s *Store) mutate(ctx context.Context, a livechannels.Authority, o livechannels.Owner, id string, action string, in Mutation, body any, change func(context.Context, *sql.Tx, Recording) (Recording, error)) (Recording, error) {
	var out Recording
	if !canonicalID.MatchString(id) || !hexID.MatchString(in.RequestID) || in.ExpectedRevision < 1 {
		return out, ErrInvalid
	}
	input := struct {
		Action, ID string
		Body       any
	}{action, id, body}
	e := s.transaction(ctx, a, o, func(tx *sql.Tx, _ func(string, string) bool) error {
		// Owning a recording permits cancellation/deletion after the provider was
		// removed. Playback and new work separately require current rights.
		replay, e := receiptTx(ctx, tx, o, in.RequestID, input, &out)
		if e != nil {
			return e
		}
		if replay {
			return restrictRecordingTx(ctx, tx, o, &out)
		}
		r, e := loadTx(ctx, tx, o, id)
		if e != nil {
			return e
		}
		if r.Revision != in.ExpectedRevision {
			return ErrConflict
		}
		out, e = change(ctx, tx, r)
		if e != nil {
			return e
		}
		if e = touchTx(ctx, tx, o); e != nil {
			return e
		}
		if e = saveReceiptTx(ctx, tx, o, in.RequestID, input, out, s.now()); e != nil {
			return e
		}
		return restrictRecordingTx(ctx, tx, o, &out)
	})
	return out, e
}

// ConfigureRetainedCaptures is startup-only. The callback is never used for
// schedules, first/recovery claims or user grants; it follows exact active-claim
// proof in currentClaimTx for a recording that already accepted a checkpoint.
func (s *Store) ConfigureRetainedCaptures(authorize DurableAuthority) error {
	if s == nil || authorize == nil {
		return ErrInvalid
	}
	s.retainedCapture = authorize
	return nil
}
