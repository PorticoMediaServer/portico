package writer

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io/fs"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type mpPhysicalKey struct {
	media, execution, request string
	generation, revision      int64
}

func (k mpPhysicalKey) root() mpPhysicalRoot {
	return mpPhysicalRoot{k.media, k.execution, k.generation}
}
func (k mpPhysicalKey) valid() bool {
	return k.root().valid() && mpWorkerID(k.request) && k.revision > 0
}

// Only a joined bridge mints this root observation. Retirement identity is
// deliberately absent: it is proved from the native row in the consuming turn.
type mpPhysicalObservation struct {
	self        *mpPhysicalObservation
	owner       *mpPhysicalSupervisor
	execution   *mpPhysicalExecution
	root        mpPhysicalRoot
	incarnation [16]byte
	digest      [32]byte
}
type mpPhysicalConsumption struct {
	self        *mpPhysicalConsumption
	observation *mpPhysicalObservation
	turn        *commandTurn
	purpose     CommandID
	key         mpPhysicalKey
	digest      [32]byte
}
type mpPhysicalValue struct {
	supervisor  [16]byte
	digest      [32]byte
	consumption *mpPhysicalConsumption
}
type mpPhysicalRelease struct {
	self        *mpPhysicalRelease
	consumption *mpPhysicalConsumption
	released    bool // registry.mu
}

func validMPPhysicalPath(p string) bool {
	return len(p) <= 4096 && fs.ValidPath(p) && p != "." && !strings.ContainsRune(p, 0)
}
func mpPhysicalCommitment(e *mpPhysicalExecution) [32]byte {
	h := sha256.New()
	h.Write([]byte("portico.mp.joined-exit-cleanup.v2\x00"))
	h.Write(e.owner.incarnation[:])
	for _, s := range append([]string{e.owner.server, e.physical.media, e.physical.execution}, e.cleanup...) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(e.physical.generation))
	h.Write(n[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func mpPhysicalRetirementDigest(o *mpPhysicalObservation, request string) [32]byte {
	h := sha256.New()
	h.Write([]byte("portico.mp.retirement.v1\x00"))
	h.Write(o.digest[:])
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(request)))
	h.Write(n[:])
	h.Write([]byte(request))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

const mpPhysicalCurrent stmt.ID = "mp.physical.current-retirement"
const mpPhysicalServer stmt.ID = "mp.physical.current-server"
const mpPhysicalExited stmt.ID = "mp.physical.committed-exit"

func mpPhysicalDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	point := func(table, index, key string) stmt.Plan {
		return stmt.Plan{Hot: true, Indexes: []string{index}, AutoIndexes: []stmt.AutoIndexBinding{{Table: table, Name: index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: key, Collation: "BINARY"}}}}}
	}
	defs := []stmt.Definition{
		{ID: mpPhysicalCurrent, SQL: `SELECT mp_revision FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND generation=? AND mp_execution_id=? AND mp_revision=? AND mp_retired_generation=generation AND mp_retire_request_id=? AND state='stopped' AND mp_grant_retired=1`, Params: []stmt.Param{text("media"), integer("generation"), text("execution"), integer("revision"), text("request")}, Plan: point("playback_sessions", "sqlite_autoindex_playback_sessions_1", "id"), Cohorts: []stmt.Cohort{{Name: "missing-root", Bindings: []any{"missing", int64(1), "missing", int64(1), "missing"}}}},
		{ID: mpPhysicalServer, SQL: `SELECT 1 FROM configuration INDEXED BY sqlite_autoindex_configuration_1 WHERE key='id' AND value=?`, Params: []stmt.Param{text("server")}, Plan: point("configuration", "sqlite_autoindex_configuration_1", "key"), Cohorts: []stmt.Cohort{{Name: "missing-server", Bindings: []any{"missing"}}}},
		{ID: mpPhysicalExited, SQL: `SELECT 1 FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND generation=? AND mp_execution_id=? AND mp_revision>=? AND state='stopped' AND mp_grant_retired=1 AND mp_retired_generation=generation AND mp_retire_request_id=? AND mp_exited_generation=generation AND mp_exit_request_id=? AND mp_exit_supervisor_incarnation=? AND mp_exited_ms>0 AND mp_exit_digest=? AND mp_cleanup_complete=1 AND mp_cleanup_cursor=?`, Params: []stmt.Param{text("media"), integer("generation"), text("execution"), integer("revision"), text("request"), text("exit_request"), {Name: "supervisor", Type: stmt.Blob}, text("digest"), integer("cleanup_cursor")}, Plan: point("playback_sessions", "sqlite_autoindex_playback_sessions_1", "id"), Cohorts: []stmt.Cohort{{Name: "missing-exit", Bindings: []any{"missing", int64(1), "missing", int64(1), "missing", "missing", make([]byte, 16), strings.Repeat("a", 64), int64(0)}}}},
	}
	for i := range defs {
		defs[i].Owner = "writer.mp.physical"
		defs[i].CallSites = []string{"writer/mp_physical_observation.go"}
		defs[i].Kind = stmt.Read
	}
	return defs
}
func mpPhysicalPurpose(id CommandID) bool {
	switch id {
	case 705, 719, 727, 736, 746, 749, 764:
		return true
	}
	return false
}

func consumeMPPhysicalObservation(ctx context.Context, t *commandTurn, s *mpPhysicalSupervisor, o *mpPhysicalObservation, key mpPhysicalKey) (out mpPhysicalValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || s == nil || s.registry == nil || o == nil {
		return out, ErrDisclosureDenied
	}
	if !mpPhysicalPurpose(t.transaction.descriptor.ID) {
		return out, ErrInvalid
	}
	registry := s.registry
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err = s.check(s.writer); err != nil {
		return out, err
	}
	if s.writer.effective == nil || s.writer.effective.executor != t.transaction.writer || o.self != o || o.owner != s || o.incarnation != s.incarnation || !key.valid() || o.root != key.root() || registry.executions[o.root.occurrence()] != o.execution || o.execution == nil || !o.execution.joined || o.digest != o.execution.digest {
		return out, ErrDisclosureDenied
	}
	step, ok := t.steps[mpPhysicalCurrent]
	if !ok {
		return out, ErrInvalid
	}
	row, err := t.queryRow(ctx, step.statementStep, key.media, key.generation, key.execution, key.revision, key.request)
	if err != nil {
		return out, err
	}
	var revision int64
	if err = row.Scan(&revision); err != nil {
		return out, usAuthorityReadError(err)
	}
	if revision != key.revision {
		return out, ErrResolveRequired
	}
	step, ok = t.steps[mpPhysicalServer]
	if !ok {
		return out, ErrInvalid
	}
	row, err = t.queryRow(ctx, step.statementStep, s.server)
	if err != nil {
		return out, err
	}
	var present int64
	if err = row.Scan(&present); err != nil {
		return out, usAuthorityReadError(err)
	}
	if present != 1 {
		return out, ErrResolveRequired
	}
	// SQL bound comparisons establish request identity from the current native
	// row. No future request/revision was accepted by start or asserted by join.
	key.revision = revision
	c := &mpPhysicalConsumption{observation: o, turn: t, purpose: t.transaction.descriptor.ID, key: key, digest: mpPhysicalRetirementDigest(o, key.request)}
	c.self = c
	return mpPhysicalValue{s.incarnation, c.digest, c}, nil
}

// Called AFTER the closed consumer's exit/cleanup effect, BEFORE its COMMIT.
// This point proves the actual native exit fields, not a caller "committed"
// boolean. The resulting token is bound to this exact transaction, root and
// retirement. Unknown/rolled-back commits cannot retire registry ownership.
func sealMPPhysicalRelease(ctx context.Context, t *commandTurn, v mpPhysicalValue) (out *mpPhysicalRelease, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	c := v.consumption
	if c == nil || c.self != c || c.turn != t || t == nil || t.transaction == nil || c.purpose != t.transaction.descriptor.ID || !mpPhysicalPurpose(c.purpose) || v.digest != c.digest || c.observation == nil || v.supervisor != c.observation.incarnation {
		return nil, ErrDisclosureDenied
	}
	if t.transaction.mpPhysicalRelease != nil {
		return nil, ErrConflict
	}
	out = &mpPhysicalRelease{consumption: c}
	out.self = out
	if err = verifyMPPhysicalRelease(ctx, t, out); err != nil {
		return nil, err
	}
	t.transaction.mpPhysicalRelease = out
	return out, nil
}

// Also called by W immediately before COMMIT, within its same reserved work.
// A composed suffix cannot undo the exit/cleanup row after sealing a token.
func verifyMPPhysicalRelease(ctx context.Context, t *commandTurn, r *mpPhysicalRelease) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if r == nil || r.self != r || r.consumption == nil {
		return ErrDisclosureDenied
	}
	c := r.consumption
	o := c.observation
	if c.self != c || c.turn != t || t == nil || t.transaction == nil || o == nil || o.owner == nil || o.owner.registry == nil || c.purpose != t.transaction.descriptor.ID {
		return ErrDisclosureDenied
	}
	s := o.owner
	registry := s.registry
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if err = s.check(s.writer); err != nil {
		return err
	}
	if s.writer.effective == nil || s.writer.effective.executor != t.transaction.writer || o.self != o || o.root != c.key.root() || o.incarnation != s.incarnation || o.execution == nil || !o.execution.joined || registry.executions[o.root.occurrence()] != o.execution || o.digest != o.execution.digest || c.digest != mpPhysicalRetirementDigest(o, c.key.request) {
		return ErrDisclosureDenied
	}
	step, ok := t.steps[mpPhysicalExited]
	if !ok {
		return ErrInvalid
	}
	k := c.key
	row, err := t.queryRow(ctx, step.statementStep, k.media, k.generation, k.execution, k.revision, k.request, k.request, o.incarnation[:], hex.EncodeToString(c.digest[:]), int64(len(o.execution.cleanup)))
	if err != nil {
		return err
	}
	var present int64
	if err = row.Scan(&present); err != nil {
		return usAuthorityReadError(err)
	}
	if present != 1 {
		return ErrResolveRequired
	}
	step, ok = t.steps[mpPhysicalServer]
	if !ok {
		return ErrInvalid
	}
	row, err = t.queryRow(ctx, step.statementStep, s.server)
	if err != nil {
		return err
	}
	if err = row.Scan(&present); err != nil {
		return usAuthorityReadError(err)
	}
	if present != 1 {
		return ErrResolveRequired
	}
	return nil
}

func (tx *transaction) checkMPPhysicalCommit() error {
	if tx.mpPhysicalRelease == nil {
		return nil
	}
	r := tx.mpPhysicalRelease
	if r.consumption == nil || r.consumption.turn == nil || r.consumption.turn.transaction != tx {
		return ErrDisclosureDenied
	}
	return verifyMPPhysicalRelease(tx.ctx, r.consumption.turn, r)
}

// Sole registry refund. It takes the private sealed token and frozen physical
// transaction outcome, never a caller flag or a result DTO. Release remains
// idempotent even if the same occurrence is later registered again. Revoking
// or replacing a supervisor does not prevent an already committed safe refund.
func releaseMPPhysicalAfterCommit(t *commandTurn, r *mpPhysicalRelease) error {
	if r == nil || r.self != r || r.consumption == nil || r.consumption.self != r.consumption || r.consumption.turn != t || t == nil || t.transaction == nil {
		return ErrDisclosureDenied
	}
	tx := t.transaction
	c := r.consumption
	if !tx.done || tx.result.state != committed || !tx.cleanupComplete || !tx.connectionReturned || !tx.retirementComplete || tx.descriptor.ID != c.purpose || tx.mpPhysicalRelease != r {
		return ErrConflict
	}
	e := c.observation.execution
	registry := c.observation.owner.registry
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if r.released {
		return nil
	}
	if !e.joined || registry.executions[e.physical.occurrence()] != e {
		return ErrConflict
	}
	delete(registry.executions, e.physical.occurrence())
	r.released = true
	return nil
}
