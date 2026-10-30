package librarychannels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"time"
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, ErrUnavailable
	}
	return &Store{db: db, now: time.Now}, nil
}
func auth(ctx context.Context, tx *sql.Tx, a Authority, owner bool) (Scope, error) {
	if a == nil {
		return Scope{}, ErrDenied
	}
	s, e := a(ctx, tx, owner)
	if e != nil {
		return s, e
	}
	if s.Fence == "" || owner && !s.Owner {
		return s, ErrDenied
	}
	return s, nil
}

// transaction runs builder work as foreground (interactive) writes: the write
// gate orders them ahead of background scans and every transaction takes its
// lock at BEGIN, so a snapshot conflict can't happen. One admission is one
// attempt: a lock met after admission is a defect or an outside opener, and
// answers as the retryable writer_capacity for the client to send again.
func (s *Store) transaction(ctx context.Context, a Authority, owner bool, work func(*sql.Tx, Scope) error) error {
	return s.transactionOnce(ctx, a, owner, work)
}

func (s *Store) transactionOnce(ctx context.Context, a Authority, owner bool, work func(*sql.Tx, Scope) error) error {
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return unavailable(e)
	}
	tx := gated.Tx()
	defer gated.Rollback()
	scope, e := auth(ctx, tx, a, owner)
	if e != nil {
		return e
	}
	if e = work(tx, scope); e != nil {
		return e
	}
	current, e := auth(ctx, tx, a, owner)
	if e != nil {
		return e
	}
	if current.Fence != scope.Fence {
		return ErrDenied
	}
	if cause := gated.Commit(); cause != nil {
		return unavailable(cause)
	}
	return nil
}
func (s *Store) snapshot(ctx context.Context, a Authority, owner bool, work func(*sql.Tx, Scope) error) error {
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return unavailable(e)
	}
	tx := gated.Tx()
	defer gated.Rollback()
	scope, e := auth(ctx, tx, a, owner)
	if e != nil {
		return e
	}
	if e = work(tx, scope); e != nil {
		return e
	}
	current, e := auth(ctx, tx, a, owner)
	if e != nil {
		return e
	}
	if current.Fence != scope.Fence {
		return ErrDenied
	}
	if cause := gated.Commit(); cause != nil {
		return unavailable(cause)
	}
	return nil
}
func catalogFence(ctx context.Context, tx *sql.Tx, c Config) (string, error) {
	parts := []string{}
	for _, id := range libraries(c) {
		var revision int64
		if tx.QueryRowContext(ctx, `SELECT revision FROM library_revisions WHERE library_id=?`, id).Scan(&revision) != nil {
			return "", ErrConflict
		}
		parts = append(parts, fmt.Sprintf("%s:%d", id, revision))
	}
	return strings.Join(parts, ","), nil
}
func readChannel(ctx context.Context, tx *sql.Tx, id string) (Channel, error) {
	var c Channel
	var raw string
	var through int64
	e := tx.QueryRowContext(ctx, `SELECT config_json,revision,state,health_code,active_generation,generated_through_ms FROM lc_channels WHERE id=? AND removed=0`, id).Scan(&raw, &c.Revision, &c.State, &c.HealthCode, &c.Generation, &through)
	if errors.Is(e, sql.ErrNoRows) {
		return c, ErrConflict
	}
	if e != nil || json.Unmarshal([]byte(raw), &c.Config) != nil {
		return c, unavailable(e)
	}
	// Readers (the builder) see the current selection shape.
	if c.Config.ViewerAccess != "server-members" {
		// A channel stored before Library Channels became shared reads as shared.
		c.Config.ViewerAccess = "server-members"
	}
	if normalized, e := Normalize(c.Config); e == nil {
		c.Config = normalized
	}
	if through > 0 {
		c.GeneratedThrough = time.UnixMilli(through).UTC().Format(time.RFC3339)
	}
	if c.Generation != "" {
		if e = tx.QueryRowContext(ctx, `SELECT candidate_count,unresolved_count FROM lc_generations WHERE id=?`, c.Generation).Scan(&c.CandidateCount, &c.UnresolvedDurationCount); e != nil {
			return c, unavailable(e)
		}
	}
	return c, nil
}
func replacementBoundary(ctx context.Context, tx *sql.Tx, id, gen string, now time.Time) (int64, error) {
	boundary := now.UnixMilli()
	var end sql.NullInt64
	if e := tx.QueryRowContext(ctx, `SELECT end_ms FROM lc_entries WHERE generation_id=? AND start_ms<=? AND end_ms>? ORDER BY start_ms DESC LIMIT 1`, gen, boundary, boundary).Scan(&end); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return 0, unavailable(e)
	}
	if end.Valid {
		boundary = end.Int64
	}
	if e := tx.QueryRowContext(ctx, `SELECT MAX(end_ms) FROM lc_playback_refs WHERE channel_id=? AND lease_until_ms>?`, id, now.UnixMilli()).Scan(&end); e != nil {
		return 0, unavailable(e)
	}
	if end.Valid && end.Int64 > boundary {
		boundary = end.Int64
	}
	return boundary, nil
}
func (s *Store) List(ctx context.Context, a Authority) ([]Channel, error) {
	out := []Channel{}
	e := s.snapshot(ctx, a, true, func(tx *sql.Tx, _ Scope) error {
		rows, e := tx.QueryContext(ctx, `SELECT id FROM lc_channels WHERE removed=0 ORDER BY position,id`)
		if e != nil {
			return unavailable(e)
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if cause := rows.Scan(&id); cause != nil {
				rows.Close()
				return unavailable(cause)
			}
			ids = append(ids, id)
			if len(ids) > MaxChannels {
				rows.Close()
				return ErrUnavailable
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return unavailable(e)
		}
		for _, id := range ids {
			c, e := readChannel(ctx, tx, id)
			if e != nil {
				return e
			}
			boundary, e := replacementBoundary(ctx, tx, id, c.Generation, s.now())
			if e != nil {
				return e
			}
			c.ReplacementBoundary = time.UnixMilli(boundary).UTC().Format(time.RFC3339)
			out = append(out, c)
		}
		return nil
	})
	return out, e
}
func (s *Store) Save(ctx context.Context, a Authority, in SaveInput) (Channel, error) {
	var out Channel
	if !validID(in.RequestID) || in.ExpectedRevision < 0 {
		return out, ErrInvalid
	}
	// First-version selection fields migrate forward on every save (deterministic,
	// so a retried request still matches its receipt).
	normalized, e := Normalize(in.Config)
	if e != nil {
		return out, e
	}
	in.Config = normalized
	if e := Validate(in.Config); e != nil {
		return out, e
	}
	hash := digest("save", encode(in))
	e = s.transaction(ctx, a, true, func(tx *sql.Tx, scope Scope) error {
		if !scope.permits(in.Config) {
			return ErrDenied
		}
		var old, raw string
		e := tx.QueryRowContext(ctx, `SELECT digest,result_json FROM lc_receipts WHERE request_id=?`, in.RequestID).Scan(&old, &raw)
		if e == nil {
			if old != hash {
				return ErrConflict
			}
			if cause := json.Unmarshal([]byte(raw), &out); cause != nil {
				return unavailable(cause)
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return unavailable(e)
		}
		var rev int64
		var gen string
		var removed bool
		e = tx.QueryRowContext(ctx, `SELECT revision,active_generation,removed FROM lc_channels WHERE id=?`, in.Config.ID).Scan(&rev, &gen, &removed)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return unavailable(e)
		}
		if removed || rev != in.ExpectedRevision {
			return ErrConflict
		}
		if rev == 0 {
			var count int
			if cause := tx.QueryRowContext(ctx, `SELECT count(*) FROM lc_channels WHERE removed=0`).Scan(&count); cause != nil {
				return unavailable(cause)
			}
			if count >= MaxChannels {
				return ErrInvalid
			}
		}
		if _, e = catalogFence(ctx, tx, in.Config); e != nil {
			return e
		}
		state := "disabled"
		if in.Config.Enabled {
			state = "generating"
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,config_json=excluded.config_json,enabled=excluded.enabled,position=excluded.position,name=excluded.name,state=excluded.state,health_code=''`, in.Config.ID, rev+1, encode(in.Config), in.Config.Enabled, in.Config.Position, in.Config.Name, state)
		if e != nil {
			return unavailable(e)
		}
		if _, e = tx.ExecContext(ctx, `UPDATE lc_generations SET status='superseded',error_code='configuration-changed' WHERE channel_id=? AND status='pending'`, in.Config.ID); e != nil {
			return unavailable(e)
		}
		if in.Config.Enabled {
			boundary, e := replacementBoundary(ctx, tx, in.Config.ID, gen, s.now())
			if e != nil {
				return e
			}
			if e = s.enqueueTx(ctx, tx, in.Config, rev+1, gen, boundary, false); e != nil {
				return e
			}
		}
		out, e = readChannel(ctx, tx, in.Config.ID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO lc_receipts VALUES(?,?,?)`, in.RequestID, hash, encode(out))
		if e != nil {
			return unavailable(e)
		}
		return nil
	})
	return out, e
}
func (s *Store) enqueueTx(ctx context.Context, tx *sql.Tx, c Config, revision int64, base string, boundary int64, tail bool) error {
	loc, e := time.LoadLocation(c.Timezone)
	if e != nil {
		return ErrInvalid
	}
	window, e := SevenDayWindow(s.now().In(loc).Format("2006-01-02"), c.Timezone)
	if e != nil {
		return e
	}
	start, end := window.Start.UTC.UnixMilli(), window.End.UTC.UnixMilli()
	if tail {
		var previousEnd int64
		if tx.QueryRowContext(ctx, `SELECT end_ms FROM lc_generations WHERE id=? AND status='published'`, base).Scan(&previousEnd) != nil {
			return ErrConflict
		}
		boundary = previousEnd
	}
	if boundary < start {
		boundary = start
	}
	if boundary >= end {
		return nil
	}
	fence, e := catalogFence(ctx, tx, c)
	if e != nil {
		return e
	}
	var historyFence int64
	id := digest(c.ID, fmt.Sprint(revision), fence, fmt.Sprint(boundary), fmt.Sprint(end), base, c.Seed)[:48]
	if hasLeastPlayed(c) {
		historyFence, e = playHistoryFence(ctx, tx)
		if e != nil {
			return unavailable(e)
		}
		id = digest(id, fmt.Sprint(historyFence))[:48]
	}
	phase := "candidates"
	if base != "" {
		phase = "copying"
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms,play_history_fence) VALUES(?,?,?,?,?,?,'pending',?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=CASE WHEN lc_generations.status IN('failed','superseded') THEN 'pending' ELSE lc_generations.status END,error_code=''`, id, c.ID, revision, encode(c), fence, c.Seed, phase, base, start, end, boundary, boundary, s.now().UnixMilli(), historyFence)
	if e != nil {
		return unavailable(e)
	}
	return nil
}
func (s *Store) Regenerate(ctx context.Context, a Authority, id string, in Mutation, boundary string) (Channel, error) {
	var out Channel
	if !validID(id) || !validID(in.RequestID) || in.ExpectedRevision < 1 {
		return out, ErrInvalid
	}
	want, e := time.Parse(time.RFC3339, boundary)
	if e != nil {
		return out, ErrInvalid
	}
	hash := digest("regenerate", id, encode(in), boundary)
	e = s.transaction(ctx, a, true, func(tx *sql.Tx, _ Scope) error {
		var old, raw string
		e := tx.QueryRowContext(ctx, `SELECT digest,result_json FROM lc_receipts WHERE request_id=?`, in.RequestID).Scan(&old, &raw)
		if e == nil {
			if old != hash {
				return ErrConflict
			}
			if cause := json.Unmarshal([]byte(raw), &out); cause != nil {
				return unavailable(cause)
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return unavailable(e)
		}
		c, e := readChannel(ctx, tx, id)
		if e != nil {
			return e
		}
		if c.Revision != in.ExpectedRevision || !c.Config.Enabled {
			return ErrConflict
		}
		at, e := replacementBoundary(ctx, tx, id, c.Generation, s.now())
		if e != nil {
			return e
		}
		if at/1000 != want.Unix() {
			return ErrConflict
		}
		if _, e = tx.ExecContext(ctx, `UPDATE lc_generations SET status='superseded',error_code='explicit-regeneration' WHERE channel_id=? AND status='pending'`, id); e != nil {
			return unavailable(e)
		}
		if e = s.enqueueTx(ctx, tx, c.Config, c.Revision, c.Generation, at, false); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE lc_channels SET state=CASE WHEN EXISTS(SELECT 1 FROM lc_generations g WHERE g.channel_id=lc_channels.id AND g.status='pending') THEN 'generating' ELSE state END WHERE id=?`, id); e != nil {
			return unavailable(e)
		}
		out, e = readChannel(ctx, tx, id)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO lc_receipts VALUES(?,?,?)`, in.RequestID, hash, encode(out))
		return e
	})
	return out, e
}
func (s *Store) Delete(ctx context.Context, a Authority, id string, in Mutation) error {
	if !validID(id) || !validID(in.RequestID) || in.ExpectedRevision < 1 {
		return ErrInvalid
	}
	hash := digest("delete", id, encode(in))
	return s.transaction(ctx, a, true, func(tx *sql.Tx, _ Scope) error {
		var old string
		e := tx.QueryRowContext(ctx, `SELECT digest FROM lc_receipts WHERE request_id=?`, in.RequestID).Scan(&old)
		if e == nil {
			if old != hash {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return unavailable(e)
		}
		c, e := readChannel(ctx, tx, id)
		if e != nil {
			return e
		}
		if c.Revision != in.ExpectedRevision {
			return ErrConflict
		}
		var refs int
		if cause := tx.QueryRowContext(ctx, `SELECT count(*) FROM lc_playback_refs WHERE channel_id=? AND lease_until_ms>?`, id, s.now().UnixMilli()).Scan(&refs); cause != nil {
			return unavailable(cause)
		}
		if refs > 0 {
			return ErrInUse
		}
		if _, e = tx.ExecContext(ctx, `UPDATE lc_channels SET enabled=0,removed=1,revision=revision+1,state='removed' WHERE id=?`, id); e != nil {
			return unavailable(e)
		}
		if _, e = tx.ExecContext(ctx, `UPDATE lc_generations SET status='superseded' WHERE channel_id=? AND status='pending'`, id); e != nil {
			return unavailable(e)
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO lc_receipts VALUES(?,?,'{}')`, in.RequestID, hash)
		return e
	})
}
