package selectionactions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

const captureReservation = 8 << 20
const historyLimit = 64

// Service owns one bounded supervisor. The caller validates the ready descriptor
// and exact confirmation before Accept. No query or source cursor is durable.
type Service struct {
	// Set before Run/Accept; domain ownership transitions are atomic with journal state.
	OnAccepted Lifecycle
	OnTerminal Lifecycle
	db         *sql.DB
	cfg        Config
	authorize  Authorize
	apply      Apply
	mu         sync.Mutex // serializes admission, capture quanta and execution (not Cancel)
	captures   map[int64]*capture
	ctx        context.Context
	stop       context.CancelFunc
	running    bool
	closed     bool
	last       int64
}
type capture struct {
	source  Source
	check   func(context.Context, *sql.Tx) error
	release func()
	digest  Digest
	open    int64
	seq     int64
	pending *Batch // read-only source output retained until its admission commits
}
type command struct {
	id                              int64
	binding, hash, account, library string
	principal                       identity.Principal
	options                         Options
	job                             Job
	terminal                        string // durable outcome; job.State remains the worker phase
}

func (c command) publicJob() Job {
	j := c.job
	// A requested cancel stays in progress until its effects are reconciled.
	// A completed/failed outcome is already final while bounded purge catches up.
	if j.State == "cancelling" && c.terminal != "" && c.terminal != "cancelled" {
		j.State, j.RetryAfterMs = c.terminal, 0
		if j.State != "complete" && (j.Done > 0 || j.FilesDeleted > 0 || j.BytesDeleted > 0) {
			j.State = "partial"
		}
	}
	return j
}

func New(db *sql.DB, config *Config, authorize Authorize, apply Apply) *Service {
	cfg := DefaultConfig()
	if config != nil {
		cfg = *config
	}
	if cfg.PerAccount <= 0 || cfg.PerAccount > 2 {
		cfg.PerAccount = 2
	}
	if cfg.Active <= 0 || cfg.Active > 4 {
		cfg.Active = 4
	}
	if cfg.HistoryTTL <= 0 || cfg.HistoryTTL > time.Hour {
		cfg.HistoryTTL = time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	ctx, stop := context.WithCancel(context.Background())
	return &Service{db: db, cfg: cfg, authorize: authorize, apply: apply, captures: make(map[int64]*capture), ctx: ctx, stop: stop}
}

// Accept is called only after exact explicit confirmation. Replay lookup comes
// before preparation validation so an evicted preparation cannot defeat replay.
func (s *Service) Accept(ctx context.Context, a Acceptance) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil || s.authorize == nil || s.apply == nil {
		return Job{}, ErrInput
	}
	if !validID(a.OperationID) || a.Binding == "" || len(a.Binding) > 4096 || a.RequestHash == "" || len(a.RequestHash) > 256 {
		return Job{}, ErrInput
	}
	var replay *command
	err := s.write(ctx, func(tx *sql.Tx) error {
		c, e := s.find(ctx, tx, a.Binding, a.OperationID)
		if e == nil {
			if c.hash != a.RequestHash {
				return ErrConflict
			}
			if e = s.auth(ctx, tx, c, nil); e != nil {
				return e
			}
			replay = &c
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	if replay != nil {
		if a.Release != nil {
			a.Release()
		}
		return replay.publicJob(), nil
	}
	if a.Factory == nil || a.Check == nil || a.Reserve == nil || a.Release == nil || a.Total <= 0 || a.Total > 1<<53-1 || len(a.Digest) != 64 || a.Account == "" || a.Account != a.Principal.AccountID || a.Library == "" || (a.Action != "delete" && a.Action != "download") {
		return Job{}, ErrInput
	}
	if a.Action == "delete" && a.Options.DeleteFiles == nil || a.Action == "download" && (a.Options.DeviceID == "" || a.Options.Quality == "") {
		return Job{}, ErrInput
	}
	// Reserve includes source, callback captures, bounded batch and delivery copy.
	if err = a.Reserve(captureReservation); err != nil {
		return Job{}, err
	}
	keep := false
	defer func() {
		if !keep {
			a.Release()
		}
	}()
	source, err := a.Factory(ctx)
	if err != nil {
		return Job{}, err
	}
	if source == nil || source.Bytes() < 0 || source.Bytes() > MaxSourceBytes {
		return Job{}, ErrCapacity
	}
	c := command{binding: a.Binding, hash: a.RequestHash, account: a.Account, library: a.Library, principal: a.Principal, options: a.Options, job: Job{OperationID: a.OperationID, Action: a.Action, State: "capturing", Total: a.Total, RetryAfterMs: RetryAfterMs}}
	if a.Options.DeleteFiles != nil {
		v := *a.Options.DeleteFiles
		c.options.DeleteFiles = &v
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		// The write gate serializes competing services as well as callers.
		old, e := s.find(ctx, tx, a.Binding, a.OperationID)
		if e == nil {
			if old.hash != a.RequestHash {
				return ErrConflict
			}
			if e = s.auth(ctx, tx, old, nil); e != nil {
				return e
			}
			replay = &old
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		if e = s.auth(ctx, tx, c, nil); e != nil {
			return e
		}
		if e = a.Check(ctx, tx); e != nil {
			return e
		}
		var all, account int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM selection_action_groups`).Scan(&all); e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM selection_action_groups WHERE account_id=?`, a.Account).Scan(&account); e != nil {
			return e
		}
		if all >= s.cfg.Active || account >= s.cfg.PerAccount {
			return ErrCapacity
		}
		// Expired replay keys can be reused. Remove that one old summary now so
		// its unique key cannot collide with this command's eventual summary.
		if _, e = tx.ExecContext(ctx, `DELETE FROM selection_action_history WHERE binding=? AND operation_id=? AND expires_ms<=?`, a.Binding, a.OperationID, s.cfg.Now().UnixMilli()); e != nil {
			return e
		}
		r, e := tx.ExecContext(ctx, `INSERT INTO selection_action_groups(binding,operation_id,request_hash,account_id,library_id,action,profile_id,server_id,authority,role,session_hash,session_epoch,expires_at,principal_device_id,grant_kind,delete_files,device_id,quality,state,total,expected_digest,created_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'capturing',?,?,?)`, a.Binding, a.OperationID, a.RequestHash, a.Account, a.Library, a.Action, a.Principal.ProfileID, a.Principal.ServerID, a.Principal.Authority, a.Principal.Role, a.Principal.Hash, a.Principal.Epoch, a.Principal.ExpiresAt.Format(time.RFC3339Nano), a.Principal.DeviceID, a.Principal.Grant, c.options.DeleteFiles, c.options.DeviceID, c.options.Quality, a.Total, a.Digest, s.cfg.Now().UnixMilli())
		if e != nil {
			return e
		}
		c.id, e = r.LastInsertId()
		if e != nil {
			return e
		}
		if s.OnAccepted != nil {
			return s.OnAccepted(ctx, tx, c.principal, c.job, c.options)
		}
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	if replay != nil {
		return replay.publicJob(), nil
	}
	s.captures[c.id] = &capture{source: source, check: a.Check, release: a.Release}
	keep = true
	return c.job, nil
}

func validID(v string) bool {
	if len(v) < 1 || len(v) > 160 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (s *Service) Lookup(ctx context.Context, binding, operationID string) (Job, error) {
	var out Job
	err := dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		c, e := s.find(ctx, tx, binding, operationID)
		if e != nil {
			return e
		}
		if e = s.auth(ctx, tx, c, nil); e != nil {
			return e
		}
		out = c.publicJob()
		return nil
	})
	return out, err
}

// Replay checks the immutable original confirmation hash before a caller tries
// to reopen a preparation that may have expired or disappeared on restart.
func (s *Service) Replay(ctx context.Context, binding, operationID, requestHash string) (Job, bool, error) {
	var out Job
	found := false
	err := dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		c, err := s.find(ctx, tx, binding, operationID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = s.auth(ctx, tx, c, nil); err != nil {
			return err
		}
		if c.hash != requestHash {
			return ErrConflict
		}
		out, found = c.publicJob(), true
		return nil
	})
	return out, found, err
}
func (s *Service) Cancel(ctx context.Context, binding, operationID string) (Job, error) {
	var out Job
	err := s.write(ctx, func(tx *sql.Tx) error {
		c, e := s.find(ctx, tx, binding, operationID)
		if e != nil {
			return e
		}
		if e = s.auth(ctx, tx, c, nil); e != nil {
			return e
		}
		if !Terminal(c.job.State) && c.terminal == "" {
			_, e = tx.ExecContext(ctx, `UPDATE selection_action_groups SET state='cancelling',terminal_state='cancelled',error_code='cancelled' WHERE id=?`, c.id)
			if e != nil {
				return e
			}
			c.job.State = "cancelling"
			c.job.ErrorCode = "cancelled"
			if s.OnTerminal != nil {
				if e = s.OnTerminal(ctx, tx, c.principal, c.job, c.options); e != nil {
					return e
				}
			}
		}
		out = c.publicJob()
		return nil
	})
	return out, err
}

// Run executes one bounded quantum per active group in round-robin order. It
// resumes sealed commands, but never reconstructs a source after restart.
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.running || s.closed {
		s.mu.Unlock()
		return ErrInput
	}
	s.running = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	detach := context.AfterFunc(s.ctx, cancel)
	defer detach()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		started := time.Now()
		s.mu.Lock()
		worked, err := s.step(ctx)
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if worked {
			if !dbwork.PaceBackground(ctx, started) {
				return ctx.Err()
			}
			continue
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (s *Service) Close() error {
	s.stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for id := range s.captures {
		s.release(id)
	}
	return nil
}
func (s *Service) release(id int64) {
	if c := s.captures[id]; c != nil {
		delete(s.captures, id)
		if c.release != nil {
			c.release()
		}
	}
}
func (s *Service) write(ctx context.Context, fn func(*sql.Tx) error) error {
	// Commands, their seal and external-effect intent must survive a machine
	// crash as well as a process restart. Keep background admission priority.
	return dbwork.WithWriteTx(dbwork.WithDurableUser(dbwork.WithOwner(ctx, "selection-actions")), s.db, dbwork.ClassBackgroundMedia, fn)
}
func (s *Service) auth(ctx context.Context, tx *sql.Tx, c command, t *Target) error {
	if s.authorize == nil {
		return ErrInput
	}
	return s.authorize(ctx, tx, c.principal, c.job.Action, c.library, t)
}

const principalColumns = `account_id,library_id,profile_id,server_id,authority,role,session_hash,session_epoch,expires_at,principal_device_id,grant_kind`
const jobColumns = `operation_id,action,state,total,admitted,done,failed,bytes_deleted,files_deleted,error_code`

func scanCommand(row interface{ Scan(...any) error }, history bool) (command, error) {
	var c command
	var expiry string
	var deleteFiles sql.NullBool
	args := []any{&c.id, &c.binding, &c.hash, &c.account, &c.library, &c.principal.ProfileID, &c.principal.ServerID, &c.principal.Authority, &c.principal.Role, &c.principal.Hash, &c.principal.Epoch, &expiry, &c.principal.DeviceID, &c.principal.Grant, &c.job.OperationID, &c.job.Action, &c.job.State, &c.job.Total, &c.job.Admitted, &c.job.Done, &c.job.Failed, &c.job.BytesDeleted, &c.job.FilesDeleted, &c.job.ErrorCode}
	if !history {
		args = append(args, &deleteFiles, &c.options.DeviceID, &c.options.Quality, &c.terminal)
	}
	err := row.Scan(args...)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	c.principal.AccountID = c.account
	c.principal.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiry)
	if deleteFiles.Valid {
		c.options.DeleteFiles = &deleteFiles.Bool
	}
	if !Terminal(c.job.State) {
		c.job.RetryAfterMs = RetryAfterMs
	}
	return c, nil
}
func (s *Service) find(ctx context.Context, tx *sql.Tx, binding, op string) (command, error) {
	c, e := scanCommand(tx.QueryRowContext(ctx, `SELECT id,binding,request_hash,`+principalColumns+`,`+jobColumns+`,delete_files,device_id,quality,terminal_state FROM selection_action_groups WHERE binding=? AND operation_id=?`, binding, op), false)
	if !errors.Is(e, ErrNotFound) {
		return c, e
	}
	return scanCommand(tx.QueryRowContext(ctx, `SELECT id,binding,request_hash,`+principalColumns+`,`+jobColumns+` FROM selection_action_history WHERE binding=? AND operation_id=? AND expires_ms>?`, binding, op, s.cfg.Now().UnixMilli()), true)
}
func (s *Service) load(ctx context.Context, id int64) (command, error) {
	return scanCommand(dbwork.QueryRow(ctx, s.db, `SELECT id,binding,request_hash,`+principalColumns+`,`+jobColumns+`,delete_files,device_id,quality,terminal_state FROM selection_action_groups WHERE id=?`, id), false)
}

func (s *Service) step(ctx context.Context) (bool, error) {
	// Retention continues even while a large confirmed command is running.
	var expired int
	if e := dbwork.QueryRow(ctx, s.db, `SELECT EXISTS(SELECT 1 FROM selection_action_history WHERE expires_ms<=? LIMIT 1)`, s.cfg.Now().UnixMilli()).Scan(&expired); e != nil {
		return false, e
	}
	if expired != 0 {
		if e := s.prune(ctx, ""); e != nil {
			return false, e
		}
	}
	var id int64
	err := dbwork.QueryRow(ctx, s.db, `SELECT id FROM selection_action_groups WHERE id>? ORDER BY id LIMIT 1`, s.last).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		s.last = 0
		err = dbwork.QueryRow(ctx, s.db, `SELECT id FROM selection_action_groups ORDER BY id LIMIT 1`).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.last = id
	c, err := s.load(ctx, id)
	if err != nil {
		return true, err
	}
	switch c.job.State {
	case "capturing":
		cap := s.captures[id]
		if cap == nil {
			return true, s.abort(ctx, c, "interrupted", "failed")
		}
		return true, s.capture(ctx, c, cap)
	case "cancelling":
		s.release(id)
		return true, s.purge(ctx, c)
	case "sealed", "running":
		e := s.execute(ctx, c)
		if _, _, ok := identity.Refusal(e); ok {
			return true, s.abort(ctx, c, "authority_revoked", "failed")
		}
		return true, e
	default:
		return true, ErrInput
	}
}

func (s *Service) abort(ctx context.Context, c command, code, state string) error {
	err := s.write(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `UPDATE selection_action_groups SET state='cancelling',error_code=?,terminal_state=? WHERE id=? AND state!='cancelling'`, code, state, c.id)
		if e != nil {
			return e
		}
		if s.OnTerminal != nil {
			return s.OnTerminal(ctx, tx, c.principal, c.job, c.options)
		}
		return nil
	})
	if err == nil {
		s.release(c.id)
	}
	return err
}

// Capture validates grammar in memory and inserts at most twenty concrete
// records. Source.Step is outside the writer and must remain read-only.
func (s *Service) capture(ctx context.Context, c command, cap *capture) error {
	// Check before the next read quantum as well as in its accepting writer.
	err := s.write(ctx, func(tx *sql.Tx) error {
		if e := s.live(ctx, tx, c, "capturing"); e != nil {
			return e
		}
		if e := s.auth(ctx, tx, c, nil); e != nil {
			return e
		}
		return cap.check(ctx, tx)
	})
	if err != nil {
		return s.captureFailure(ctx, c, err)
	}
	var batch Batch
	if cap.pending != nil {
		batch = *cap.pending
	} else {
		batch, err = cap.source.Step(ctx)
		if err != nil {
			return s.captureFailure(ctx, c, err)
		}
	}
	if len(batch.Records) > MaxRecords || cap.source.Bytes() < 0 || cap.source.Bytes() > MaxSourceBytes {
		return s.abort(ctx, c, "capacity_exhausted", "failed")
	}
	size := int64(0)
	open, seq, admitted := cap.open, cap.seq, c.job.Admitted
	for _, r := range batch.Records {
		branches := 0
		if r.Target != nil {
			branches++
		}
		if r.File != nil {
			branches++
		}
		if r.Finish != 0 {
			branches++
		}
		if branches != 1 {
			return s.abort(ctx, c, "invalid_command", "failed")
		}
		switch {
		case r.Target != nil:
			t := r.Target
			size += int64(len(t.ItemID) + len(t.LibraryID) + len(t.Kind) + len(t.Title) + len(t.Identity) + len(t.VersionID) + len(t.DownloadIdentity) + len(t.Lifetime) + 256)
			if open != 0 || t.EntityID <= 0 || t.LibraryID != c.library || t.ItemID == "" || t.Identity == "" || t.OperationID != "" {
				return s.abort(ctx, c, "invalid_command", "failed")
			}
			open = t.EntityID
			admitted++
		case r.File != nil:
			f := r.File
			size += int64(len(f.Token) + len(f.Path) + len(f.Root) + len(f.RootIdentity) + len(f.SourceID) + len(f.SourceIncarnation) + len(f.Edition) + 256)
			if open == 0 || f.TargetID != open || f.Started || f.Size < 0 || math.IsNaN(f.Start) || math.IsInf(f.Start, 0) || f.End != nil && (math.IsNaN(*f.End) || math.IsInf(*f.End, 0)) {
				return s.abort(ctx, c, "invalid_command", "failed")
			}
		default:
			if open == 0 || open != r.Finish {
				return s.abort(ctx, c, "invalid_command", "failed")
			}
			open = 0
		}
		seq++
	}
	if size > MaxSourceBytes || admitted > c.job.Total {
		return s.abort(ctx, c, "selection_changed", "failed")
	}
	// The source has advanced, but its bounded output must survive a refused or
	// failed writer. Never call Step again until this exact batch commits.
	cap.pending = &batch
	// Digest advances only after successful admission, so writer retry cannot
	// double-admit or skip concrete commands.
	err = s.write(ctx, func(tx *sql.Tx) error {
		if e := s.live(ctx, tx, c, "capturing"); e != nil {
			return e
		}
		if e := s.auth(ctx, tx, c, nil); e != nil {
			return e
		}
		if e := cap.check(ctx, tx); e != nil {
			return e
		}
		n := cap.seq
		for _, r := range batch.Records {
			n++
			switch {
			case r.Target != nil:
				t := r.Target
				if e := s.auth(ctx, tx, c, t); e != nil {
					return e
				}
				if _, e := tx.ExecContext(ctx, `INSERT INTO selection_action_targets(group_id,entity_id,seq,item_id,library_id,kind,title,identity,version_id,download_identity,lifetime) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, c.id, t.EntityID, n, t.ItemID, t.LibraryID, t.Kind, t.Title, t.Identity, t.VersionID, t.DownloadIdentity, t.Lifetime); e != nil {
					return e
				}
			case r.File != nil:
				f := r.File
				if _, e := tx.ExecContext(ctx, `INSERT INTO selection_action_files(group_id,seq,target_id,asset_id,token,path,root,root_identity,size,modified_ns,shared,present,part,start,end,edition,source_id,source_incarnation,source_generation) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, c.id, n, f.TargetID, f.AssetID, f.Token, f.Path, f.Root, f.RootIdentity, f.Size, f.ModifiedNS, f.Shared, f.Present, f.Part, f.Start, f.End, f.Edition, f.SourceID, f.SourceIncarnation, f.SourceGeneration); e != nil {
					return e
				}
			default:
				if _, e := tx.ExecContext(ctx, `UPDATE selection_action_targets SET ready=1 WHERE group_id=? AND entity_id=?`, c.id, r.Finish); e != nil {
					return e
				}
			}
		}
		_, e := tx.ExecContext(ctx, `UPDATE selection_action_groups SET admitted=? WHERE id=?`, admitted, c.id)
		return e
	})
	if err != nil {
		return s.captureFailure(ctx, c, err)
	}
	for _, r := range batch.Records {
		cap.digest.Add(r)
	}
	cap.open, cap.seq = open, seq
	cap.pending = nil
	if !batch.Done {
		return nil
	}
	// A failed seal retries the seal boundary without replaying the final batch.
	cap.pending = &Batch{Done: true}
	var expected string
	err = s.write(ctx, func(tx *sql.Tx) error {
		if e := s.live(ctx, tx, c, "capturing"); e != nil {
			return e
		}
		if e := s.auth(ctx, tx, c, nil); e != nil {
			return e
		}
		if e := cap.check(ctx, tx); e != nil {
			return e
		}
		if e := tx.QueryRowContext(ctx, `SELECT expected_digest FROM selection_action_groups WHERE id=?`, c.id).Scan(&expected); e != nil {
			return e
		}
		if open != 0 || admitted != c.job.Total || cap.digest.String() != expected {
			return ErrStale
		}
		_, e := tx.ExecContext(ctx, `UPDATE selection_action_groups SET state='sealed',seal_count=?,seal_digest=? WHERE id=?`, admitted, cap.digest.String(), c.id)
		return e
	})
	if err != nil {
		return s.captureFailure(ctx, c, err)
	}
	s.release(c.id)
	return nil
}
func (s *Service) captureFailure(ctx context.Context, c command, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	code := ""
	if _, _, ok := identity.Refusal(err); ok {
		code = "authority_revoked"
	} else if errors.Is(err, ErrStale) || errors.Is(err, readprep.ErrStale) {
		code = "selection_changed"
	} else if errors.Is(err, ErrCapacity) || errors.Is(err, readprep.ErrCapacity) {
		code = "capacity_exhausted"
	} else if errors.Is(err, ErrCancelled) {
		return nil // the cancel transaction already owns terminal publication
	} else {
		return err // retain the source and pending batch across transient failure
	}
	return s.abort(ctx, c, code, "failed")
}

var errCancelled = ErrCancelled

func (s *Service) live(ctx context.Context, tx *sql.Tx, c command, state string) error {
	var actual string
	if e := tx.QueryRowContext(ctx, `SELECT state FROM selection_action_groups WHERE id=?`, c.id).Scan(&actual); e != nil {
		return e
	}
	if actual != state {
		return errCancelled
	}
	return nil
}

func (s *Service) execute(ctx context.Context, c command) error {
	var t Target
	var ready bool
	var code string
	err := dbwork.QueryRow(ctx, s.db, `SELECT entity_id,item_id,library_id,kind,title,identity,version_id,download_identity,lifetime,ready,error_code FROM selection_action_targets WHERE group_id=? ORDER BY seq LIMIT 1`, c.id).Scan(&t.EntityID, &t.ItemID, &t.LibraryID, &t.Kind, &t.Title, &t.Identity, &t.VersionID, &t.DownloadIdentity, &t.Lifetime, &ready, &code)
	if errors.Is(err, sql.ErrNoRows) {
		return s.finish(ctx, c)
	}
	if err != nil {
		return err
	}
	if !ready {
		return s.abort(ctx, c, "invalid_command", "failed")
	}
	t.OperationID = c.job.OperationID
	if code != "" {
		// A failed file can never grant catalogue finalization. Drain its remaining
		// file commands before counting this target as failed.
		return s.write(ctx, func(tx *sql.Tx) error {
			if e := s.executionLive(ctx, tx, c); e != nil {
				if errors.Is(e, errCancelled) {
					return nil
				}
				return e
			}
			// Refresh authority, but no fresh domain effect is granted here.
			// A post-unlink refusal must still drain/count its failed command.
			if e := s.auth(ctx, tx, c, &t); e != nil {
				if _, _, refused := identity.Refusal(e); !refused && !errors.Is(e, errCancelled) {
					return e
				}
			}
			r, e := tx.ExecContext(ctx, `DELETE FROM selection_action_files WHERE group_id=? AND seq IN (SELECT seq FROM selection_action_files WHERE group_id=? AND target_id=? ORDER BY seq LIMIT 20)`, c.id, c.id, t.EntityID)
			if e != nil {
				return e
			}
			n, e := r.RowsAffected()
			if e != nil || n != 0 {
				return e
			}
			return s.completeTarget(ctx, tx, c, t, Effect{Code: code})
		})
	}
	var f File
	var seq int64
	var end sql.NullFloat64
	err = dbwork.QueryRow(ctx, s.db, `SELECT seq,target_id,asset_id,token,path,root,root_identity,size,modified_ns,shared,present,part,start,end,edition,source_id,source_incarnation,source_generation,started FROM selection_action_files WHERE group_id=? AND target_id=? ORDER BY seq LIMIT 1`, c.id, t.EntityID).Scan(&seq, &f.TargetID, &f.AssetID, &f.Token, &f.Path, &f.Root, &f.RootIdentity, &f.Size, &f.ModifiedNS, &f.Shared, &f.Present, &f.Part, &f.Start, &end, &f.Edition, &f.SourceID, &f.SourceIncarnation, &f.SourceGeneration, &f.Started)
	var file *File
	if err == nil {
		if end.Valid {
			f.End = &end.Float64
		}
		file = &f
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		if e := s.executionLive(ctx, tx, c); e != nil {
			return e
		}
		if e := s.auth(ctx, tx, c, &t); e != nil {
			return e
		}
		if file != nil {
			if _, e := tx.ExecContext(ctx, `UPDATE selection_action_files SET started=1 WHERE group_id=? AND seq=?`, c.id, seq); e != nil {
				return e
			}
		}
		_, e := tx.ExecContext(ctx, `UPDATE selection_action_groups SET state='running' WHERE id=?`, c.id)
		return e
	})
	if errors.Is(err, errCancelled) {
		return nil
	}
	if err != nil {
		if _, _, ok := identity.Refusal(err); ok {
			return s.abort(ctx, c, "authority_revoked", "failed")
		}
		return err
	}
	checkpointed := false
	checkpoint := func(tx *sql.Tx, effect Effect) error {
		if checkpointed {
			return ErrInput
		}
		if e := dbwork.RequireGatedWriteTx(tx); e != nil {
			return e
		}
		if effect.Bytes < 0 || effect.Files < 0 || len(effect.Code) > 160 {
			return ErrInput
		}
		liveErr := s.executionLive(ctx, tx, c)
		authErr := s.auth(ctx, tx, c, &t)
		if file == nil {
			if liveErr != nil {
				return liveErr
			}
			if authErr != nil {
				return authErr
			}
			if e := s.completeTarget(ctx, tx, c, t, effect); e != nil {
				return e
			}
		} else {
			// Intent has crossed the external-effect boundary. A cancellation or
			// revoked authority can stop new effects, but cannot erase an observed
			// unlink. The domain executor independently checks immediately pre-unlink.
			if liveErr != nil && !errors.Is(liveErr, errCancelled) {
				return liveErr
			}
			if authErr != nil {
				if _, _, refused := identity.Refusal(authErr); !refused && !errors.Is(authErr, errCancelled) {
					return authErr
				}
				if effect.Code == "" {
					effect.Code = "authority_revoked"
				}
			}
			r, e := tx.ExecContext(ctx, `DELETE FROM selection_action_files WHERE group_id=? AND seq=? AND started=1`, c.id, seq)
			if e != nil {
				return e
			}
			n, e := r.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return ErrConflict
			}
			if effect.Code != "" {
				if _, e = tx.ExecContext(ctx, `UPDATE selection_action_targets SET error_code=? WHERE group_id=? AND entity_id=?`, effect.Code, c.id, t.EntityID); e != nil {
					return e
				}
			}
			if _, e = tx.ExecContext(ctx, `UPDATE selection_action_groups SET bytes_deleted=bytes_deleted+?,files_deleted=files_deleted+?,error_code=CASE WHEN error_code='' THEN ? ELSE error_code END WHERE id=?`, effect.Bytes, effect.Files, effect.Code, c.id); e != nil {
				return e
			}
		}
		checkpointed = true
		return nil
	}
	err = s.apply(dbwork.WithDurableUser(dbwork.WithOwner(ctx, "selection-actions")), c.principal, c.job.Action, c.options, t, file, checkpoint)
	// A proved domain refusal (changed file, gone target or policy mismatch) is
	// not a storage failure. Record it with the same atomic checkpoint; a file
	// refusal drains the rest of that target without granting final deletion.
	var refusal *TargetFailure
	if !checkpointed && errors.As(err, &refusal) && refusal.Code != "" {
		err = s.write(ctx, func(tx *sql.Tx) error { return checkpoint(tx, Effect{Code: refusal.Code}) })
	}

	if checkpointed {
		// Domain code can reconcile an observed unlink, commit its checkpoint,
		// then return the refusal that stopped further effects. Verify commit:
		// merely invoking a callback inside a rolled-back tx is insufficient.
		var remains int
		var checkErr error
		if file != nil {
			checkErr = dbwork.QueryRow(ctx, s.db, `SELECT EXISTS(SELECT 1 FROM selection_action_files WHERE group_id=? AND seq=?)`, c.id, seq).Scan(&remains)
		} else {
			checkErr = dbwork.QueryRow(ctx, s.db, `SELECT EXISTS(SELECT 1 FROM selection_action_targets WHERE group_id=? AND entity_id=?)`, c.id, t.EntityID).Scan(&remains)
		}
		if checkErr != nil {
			return checkErr
		}
		if remains == 0 {
			return nil
		}
		if err == nil {
			return fmt.Errorf("selection action executor did not commit checkpoint")
		}
	}
	if errors.Is(err, errCancelled) {
		return nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, _, ok := identity.Refusal(err); ok {
			return s.abort(ctx, c, "authority_revoked", "failed")
		}
		// Storage/transport uncertainty leaves concrete commands and durable intent
		// intact. A supervisor restart retries the same command, never its query.
		return err
	}
	if !checkpointed {
		return fmt.Errorf("selection action executor returned without checkpoint")
	}
	return nil
}
func (s *Service) executionLive(ctx context.Context, tx *sql.Tx, c command) error {
	var state string
	var count sql.NullInt64
	var digest sql.NullString
	var expected string
	if e := tx.QueryRowContext(ctx, `SELECT state,seal_count,seal_digest,expected_digest FROM selection_action_groups WHERE id=?`, c.id).Scan(&state, &count, &digest, &expected); e != nil {
		return e
	}
	if state == "cancelling" {
		return errCancelled
	}
	if state != "sealed" && state != "running" || !count.Valid || count.Int64 != c.job.Total || !digest.Valid || digest.String != expected {
		return ErrStale
	}
	return nil
}
func (s *Service) completeTarget(ctx context.Context, tx *sql.Tx, c command, t Target, e Effect) error {
	var remains int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM selection_action_files WHERE group_id=? AND target_id=? LIMIT 1)`, c.id, t.EntityID).Scan(&remains); err != nil {
		return err
	}
	if remains != 0 {
		return ErrConflict
	}
	r, err := tx.ExecContext(ctx, `DELETE FROM selection_action_targets WHERE group_id=? AND entity_id=?`, c.id, t.EntityID)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	done, failed := 0, 0
	if e.Code == "" {
		done = 1
	} else {
		failed = 1
	}
	_, err = tx.ExecContext(ctx, `UPDATE selection_action_groups SET done=done+?,failed=failed+?,bytes_deleted=bytes_deleted+?,files_deleted=files_deleted+?,error_code=CASE WHEN error_code='' THEN ? ELSE error_code END WHERE id=?`, done, failed, e.Bytes, e.Files, e.Code, c.id)
	return err
}
func (s *Service) finish(ctx context.Context, c command) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if e := s.executionLive(ctx, tx, c); e != nil {
			if errors.Is(e, errCancelled) {
				return nil
			}
			return e
		}
		if e := s.auth(ctx, tx, c, nil); e != nil {
			return e
		}
		var done, failed int64
		if e := tx.QueryRowContext(ctx, `SELECT done,failed FROM selection_action_groups WHERE id=?`, c.id).Scan(&done, &failed); e != nil {
			return e
		}
		state := "complete"
		if done+failed != c.job.Total {
			state = "failed"
		} else if failed > 0 {
			state = "failed"
			if done > 0 || c.job.FilesDeleted > 0 || c.job.BytesDeleted > 0 {
				state = "partial"
			}
		}
		_, e := tx.ExecContext(ctx, `UPDATE selection_action_groups SET state='cancelling',terminal_state=? WHERE id=?`, state, c.id)
		if e != nil {
			return e
		}
		if s.OnTerminal != nil {
			return s.OnTerminal(ctx, tx, c.principal, c.job, c.options)
		}
		return nil
	})
}

// Purge removes at most twenty file or target rows per writer. No cascading
// whole-command delete can hide an unbounded batch. Only aggregates survive.
func (s *Service) purge(ctx context.Context, c command) error {
	err := s.write(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"selection_action_files", "selection_action_targets"} {
			key := "seq"
			if table == "selection_action_targets" {
				key = "entity_id"
			}
			r, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE group_id=? AND `+key+` IN (SELECT `+key+` FROM `+table+` WHERE group_id=? ORDER BY `+key+` LIMIT 20)`, c.id, c.id)
			if e != nil {
				return e
			}
			n, e := r.RowsAffected()
			if e != nil {
				return e
			}
			if n > 0 {
				return nil
			}
		}
		var terminal string
		var done, files, bytes int64
		if e := tx.QueryRowContext(ctx, `SELECT terminal_state,done,files_deleted,bytes_deleted FROM selection_action_groups WHERE id=?`, c.id).Scan(&terminal, &done, &files, &bytes); e != nil {
			return e
		}
		if terminal == "" {
			terminal = "failed"
		}
		if terminal != "complete" && (done > 0 || files > 0 || bytes > 0) {
			terminal = "partial"
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO selection_action_history(binding,operation_id,request_hash,`+principalColumns+`,state,total,admitted,done,failed,bytes_deleted,files_deleted,error_code,action,expires_ms) SELECT binding,operation_id,request_hash,`+principalColumns+`,?,total,admitted,done,failed,bytes_deleted,files_deleted,error_code,action,? FROM selection_action_groups WHERE id=?`, terminal, s.cfg.Now().Add(s.cfg.HistoryTTL).UnixMilli(), c.id)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `DELETE FROM selection_action_groups WHERE id=?`, c.id)
		if e != nil {
			return e
		}
		// Trim atomically with publication, including if the process stops
		// immediately after the summary commit.
		return trimHistoryAccount(ctx, tx, c.account)
	})
	return err
}
func (s *Service) prune(ctx context.Context, account string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `DELETE FROM selection_action_history WHERE id IN (SELECT id FROM selection_action_history WHERE expires_ms<=? ORDER BY expires_ms,id LIMIT 20)`, s.cfg.Now().UnixMilli()); e != nil {
			return e
		}
		if account == "" {
			return nil
		}
		// Each completion adds one summary, so this bounded trim maintains the cap
		// incrementally without a full-account scan or aggregate sort.
		return trimHistoryAccount(ctx, tx, account)
	})
}

func trimHistoryAccount(ctx context.Context, tx *sql.Tx, account string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM selection_action_history WHERE id IN (SELECT id FROM selection_action_history WHERE account_id=? ORDER BY id DESC LIMIT 20 OFFSET ?)`, account, historyLimit)
	return err
}
