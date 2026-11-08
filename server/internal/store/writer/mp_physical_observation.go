package writer

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io/fs"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Only the joined bridge mints this identity-sealed observation. Facts and
// receipts are not constructors. Consumption rechecks its owning supervisor,
// exact native root occurrence, stop request and current server in THIS turn.
type mpPhysicalObservation struct {
	self        *mpPhysicalObservation
	owner       *mpPhysicalSupervisor
	execution   *mpPhysicalExecution
	key         mpPhysicalKey
	incarnation [16]byte
	digest      [32]byte
}

type mpPhysicalValue struct {
	supervisor [16]byte
	digest     [32]byte
}

func (k mpPhysicalKey) valid() bool {
	return mpWorkerID(k.media) && mpWorkerID(k.execution) && mpWorkerID(k.request) && k.generation > 0 && k.revision > 0
}
func validMPPhysicalPath(p string) bool {
	return len(p) <= 4096 && fs.ValidPath(p) && p != "." && !strings.ContainsRune(p, 0)
}
func mpPhysicalCommitment(e *mpPhysicalExecution) [32]byte {
	h := sha256.New()
	h.Write([]byte("portico.mp.joined-exit-cleanup.v1\x00"))
	h.Write(e.owner.incarnation[:])
	for _, s := range append([]string{e.owner.server, e.key.media, e.key.execution, e.key.request}, e.cleanup...) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	for _, v := range []int64{e.key.generation, e.key.revision} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(v))
		h.Write(n[:])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

const mpPhysicalCurrent stmt.ID = "mp.physical.current-retirement"
const mpPhysicalServer stmt.ID = "mp.physical.current-server"

func mpPhysicalDefinitions() []stmt.Definition {
	defs := []stmt.Definition{{ID: mpPhysicalCurrent, Owner: "writer.mp.physical", CallSites: []string{"writer/mp_physical_observation.go"}, Kind: stmt.Read,
		SQL:    `SELECT 1 FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND generation=? AND mp_execution_id=? AND mp_revision=? AND mp_retired_generation=generation AND mp_retire_request_id=? AND state='stopped' AND mp_grant_retired=1`,
		Params: []stmt.Param{{Name: "media", Type: stmt.Text}, {Name: "generation", Type: stmt.Integer}, {Name: "execution", Type: stmt.Text}, {Name: "revision", Type: stmt.Integer}, {Name: "request", Type: stmt.Text}},
		Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_sessions_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "playback_sessions", Name: "sqlite_autoindex_playback_sessions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}, Cohorts: []stmt.Cohort{{Name: "missing-root", Bindings: []any{"missing", int64(1), "missing", int64(1), "missing"}}}},
		{ID: mpPhysicalServer, Owner: "writer.mp.physical", CallSites: []string{"writer/mp_physical_observation.go"}, Kind: stmt.Read, SQL: `SELECT 1 FROM configuration INDEXED BY sqlite_autoindex_configuration_1 WHERE key='id' AND value=?`, Params: []stmt.Param{{Name: "server", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_configuration_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "configuration", Name: "sqlite_autoindex_configuration_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "key", Collation: "BINARY"}}}}}, Cohorts: []stmt.Cohort{{Name: "missing-server", Bindings: []any{"missing"}}}}}
	return defs
}

func consumeMPPhysicalObservation(ctx context.Context, t *commandTurn, s *mpPhysicalSupervisor, o *mpPhysicalObservation, key mpPhysicalKey) (out mpPhysicalValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || s == nil || o == nil {
		return out, ErrDisclosureDenied
	}
	// Only closed physical exit consumers. A descriptor number alone cannot mint
	// a proof or open a runnable recipe; composition still supplies admission.
	switch t.transaction.descriptor.ID {
	case 705, 719, 727, 736, 746, 749, 764:
	default:
		return out, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.check(s.writer); err != nil {
		return out, err
	}
	if s.writer.effective == nil || s.writer.effective.executor != t.transaction.writer || o.self != o || o.owner != s || o.incarnation != s.incarnation || o.key != key || !key.valid() || s.executions[key] != o.execution || o.execution == nil || !o.execution.joined || o.digest != o.execution.digest {
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
	var present int64
	if err = row.Scan(&present); err != nil {
		return out, usAuthorityReadError(err)
	}
	if present != 1 {
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
	if err = row.Scan(&present); err != nil {
		return out, usAuthorityReadError(err)
	}
	if present != 1 {
		return out, ErrResolveRequired
	}
	return mpPhysicalValue{s.incarnation, o.digest}, nil
}
