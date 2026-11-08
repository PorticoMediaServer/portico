package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Native guide operation facts on the caller's admitted private writer turn.
// They are not disclosure permission, original receipt resolution, a physical
// generation pin or Applied/COMMIT evidence. W reads them after authority and
// before any effect, and again after the exact RETURNING/CAS to prove what was
// actually accepted.
type mgGuideNative struct {
	request, input, name, createdAt, status string
	tuner                                   int64
	progress                                mgGuideProgress
	incarnation, createOperation            [16]byte
	birthDigest                             [32]byte
	acknowledged                            int64
	resultRevision                          int64
	resultState, publishedAt                string
}

// A private effect result: the post-effect native row, the exact source head
// observed on the same turn, and that turn's applied time. The caller still owns
// admission, receipts, events and the actual COMMIT.
type mgGuideNativeResult struct {
	operation mgGuideNative
	head      mgGuideHead
	appliedMS int64
}

const (
	mgGuideRead     stmt.ID = "mg.guide.operation"
	mgGuideSource   stmt.ID = "mg.guide.source"
	mgGuideIdentity stmt.ID = "mg.guide.identity"
)

func mgGuideNativeDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: mgGuideRead, Kind: stmt.Read, SQL: `SELECT o.request_id,o.input_hash,o.source_id,o.generation_id,o.expected_revision,o.expected_generation,o.expected_choice_count,o.command_incarnation,o.create_operation_id,o.birth_request_digest,o.last_command_sequence,o.acknowledged_command_sequence,o.stage_revision,o.staged_channel_count,o.staged_programme_count,o.staged_choice_count,o.staged_bytes,o.manifest_digest,o.stage_digest,o.sealed,o.status,o.result_revision,o.result_state,o.published_at,g.name,g.tuner_count,g.channel_count,g.programme_count,g.created_at
FROM live_operations AS o INDEXED BY sqlite_autoindex_live_operations_1
CROSS JOIN live_generations AS g INDEXED BY sqlite_autoindex_live_generations_1 ON g.id=o.generation_id AND g.source_id=o.source_id
WHERE o.request_id=?`, Params: []stmt.Param{s("request")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1", "sqlite_autoindex_live_generations_1"}, Seeks: []string{"request_id=?", "id=?"}}},
		{ID: mgGuideSource, Kind: stmt.Read, SQL: `SELECT revision,state,active_generation FROM live_sources INDEXED BY sqlite_autoindex_live_sources_1 WHERE id=?`, Params: []stmt.Param{s("source")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_sources_1"}, Seeks: []string{"id=?"}}},
		{ID: mgGuideIdentity, Kind: stmt.Read, SQL: `SELECT has_live_source_removals FROM live_source_identities INDEXED BY sqlite_autoindex_live_source_identities_1 WHERE id=?`, Params: []stmt.Param{s("source")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_source_identities_1"}, Seeks: []string{"id=?"}}},
	}
	pins := map[string]stmt.AutoIndexBinding{
		"sqlite_autoindex_live_operations_1":        {Table: "live_operations", Name: "sqlite_autoindex_live_operations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "request_id", Collation: "BINARY"}}},
		"sqlite_autoindex_live_generations_1":       {Table: "live_generations", Name: "sqlite_autoindex_live_generations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}},
		"sqlite_autoindex_live_sources_1":           {Table: "live_sources", Name: "sqlite_autoindex_live_sources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}},
		"sqlite_autoindex_live_source_identities_1": {Table: "live_source_identities", Name: "sqlite_autoindex_live_source_identities_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}},
	}
	for j := range defs {
		d := &defs[j]
		d.Owner = "writer.mg.guide.native"
		d.CallSites = []string{"writer/mg_guide_native.go"}
		for _, index := range d.Plan.Indexes {
			if pin, ok := pins[index]; ok {
				d.Plan.AutoIndexes = append(d.Plan.AutoIndexes, pin)
			}
		}
		args := make([]any, len(d.Params))
		for k, p := range d.Params {
			switch p.Type {
			case stmt.Text:
				args[k] = "missing"
			case stmt.Integer:
				args[k] = int64(1)
			case stmt.Blob:
				args[k] = make([]byte, 32)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing-native-keys", Bindings: args}}
	}
	return defs
}

func mgGuideStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgGuideRead, mgGuideSource, mgGuideIdentity, mgGuideInsertChannel, mgGuideInsertProgramme, mgGuideInsertChoice, mgGuideAdvanceStage, mgGuideInsertIdentity, mgGuideInsertGeneration, mgGuideInsertOperation, mgGuideCapacity:
	default:
		return statementStep{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return step.statementStep, nil
}

func mgGuideQuery(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, err := mgGuideStep(t, id)
	if err != nil {
		return commandRow{}, err
	}
	return t.queryRow(ctx, step, args...)
}

func mgGuideChange(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	step, err := mgGuideStep(t, id)
	if err != nil {
		return err
	}
	result, err := t.exec(ctx, step, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return t.checkLifetimes(ctx)
}

func mgReadGuideNative(ctx context.Context, t *commandTurn, request string) (out mgGuideNative, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mgGuideText(request, true, 256) {
		return out, ErrInvalid
	}
	row, err := mgGuideQuery(ctx, t, mgGuideRead, request)
	if err != nil {
		return out, err
	}
	var inc, op, birth, manifest, chain []byte
	var sealed int64
	p := &out.progress
	m := &p.manifest
	err = row.Scan(&out.request, &out.input, &m.source, &m.generation, &m.expectedRevision, &m.previousGeneration, &m.expected[2], &inc, &op, &birth, &p.sequence, &out.acknowledged, &p.revision, &p.staged[0], &p.staged[1], &p.staged[2], &p.bytes, &manifest, &chain, &sealed, &out.status, &out.resultRevision, &out.resultState, &out.publishedAt, &out.name, &out.tuner, &m.expected[0], &m.expected[1], &out.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if len(inc) != 16 || len(op) != 16 || len(birth) != 32 || len(manifest) != 32 || len(chain) != 32 || (sealed != 0 && sealed != 1) {
		return out, ErrInvalid
	}
	copy(out.incarnation[:], inc)
	copy(out.createOperation[:], op)
	copy(out.birthDigest[:], birth)
	copy(m.expectedChain[:], manifest)
	copy(p.chain[:], chain)
	p.sealed = sealed == 1
	if !validMGGuideProgress(*p) || out.acknowledged < 0 || out.acknowledged > p.sequence || !mgGuideText(out.name, true, 4096) || out.tuner < 0 || out.tuner > 256 {
		return out, ErrInvalid
	}
	return out, nil
}

// A current serving head is disclosure-eligible only while the source identity
// is not retired. The native row remains readable through the retained owner.
func mgGuideCurrentHead(ctx context.Context, t *commandTurn, source string) (out mgGuideHead, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out, err = mgGuideHeadFacts(ctx, t, source)
	if err == nil && out.removed {
		err = ErrDisclosureDenied
	}
	return out, err
}

func mgGuideHeadFacts(ctx context.Context, t *commandTurn, source string) (out mgGuideHead, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mgGuideText(source, true, 256) {
		return out, ErrInvalid
	}
	row, err := mgGuideQuery(ctx, t, mgGuideIdentity, source)
	if err != nil {
		return out, err
	}
	var removed int64
	if err = row.Scan(&removed); err != nil {
		return out, err
	}
	if removed != 0 && removed != 1 {
		return out, ErrInvalid
	}
	out.removed = removed == 1
	out.source = source
	row, err = mgGuideQuery(ctx, t, mgGuideSource, source)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.revision, &out.state, &out.generation)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return out, err
}

// The turn's applied time. No real-clock assertion or latency claim: this is a
// native observation on the admitted frame, not a qualification measurement.
func mgGuideNativeTime(ctx context.Context, t *commandTurn) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.writer == nil || t.transaction.writer.now == nil {
		return 0, ErrInvalid
	}
	switch t.transaction.descriptor.ID {
	case MGGuideBegin, MGGuideStageChannels, MGGuideStageProgrammes, MGGuideStageChoices:
	default:
		return 0, ErrInvalid
	}
	if t.transaction.descriptor.Durability != Normal {
		return 0, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	defer t.transaction.guardPanic()
	now := t.transaction.writer.now()
	if now.UnixMilli() < 0 {
		return 0, ErrInvalid
	}
	if err = t.transaction.observeTime(now); err != nil {
		return 0, err
	}
	return now.UnixMilli(), nil
}

// A staging command must continue the operation's original command incarnation
// and carry a fresh operation identity. Sequence continuity, staging status,
// pre-seal state and single-unacknowledged-command order are independently
// enforced by the closed protocol planner and the native CAS/CHECK constraints.
func mgGuideNextNative(t *commandTurn, before mgGuideNative, identity CommandIdentity) error {
	if t == nil || t.transaction == nil || before.status != "staging" || before.progress.sealed ||
		before.progress.sequence == math.MaxInt64 || before.progress.revision == math.MaxInt64 ||
		identity.OperationID == ([16]byte{}) || identity.Incarnation != before.incarnation {
		return ErrConflict
	}
	return nil
}

func mgGuideText(s string, required bool, max int) bool {
	return (!required || s != "") && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// Fixed-format applied timestamp used by native guide documents.
func mgGuideTimestamp(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}
