package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private result/history primitives on the parent's one commandTurn. These
// helpers neither authorize disclosure nor select admission, events or COMMIT.
// Lookup returns native historical facts, not Applied. Parent checks current
// disclosure and ACK/retirement delivery policy before releasing any result.
type mgGuideReceiptKind string

const (
	mgGuideReceiptBegin      mgGuideReceiptKind = "begin"
	mgGuideReceiptChannels   mgGuideReceiptKind = "channels"
	mgGuideReceiptProgrammes mgGuideReceiptKind = "programmes"
	mgGuideReceiptChoices    mgGuideReceiptKind = "choices"
	mgGuideReceiptSeal       mgGuideReceiptKind = "seal"
	mgGuideReceiptPublish    mgGuideReceiptKind = "publish"
	mgGuideReceiptDiscard    mgGuideReceiptKind = "discard"
	mgGuideReceiptReclaim    mgGuideReceiptKind = "reclaim"
)

func (k mgGuideReceiptKind) valid() bool {
	switch k {
	case mgGuideReceiptBegin, mgGuideReceiptChannels, mgGuideReceiptProgrammes, mgGuideReceiptChoices, mgGuideReceiptSeal, mgGuideReceiptPublish, mgGuideReceiptDiscard, mgGuideReceiptReclaim:
		return true
	}
	return false
}

// Frozen original display/parser/capacity projection, explicitly supplied by the
// closed driver. None of these fields is native authority or permission to use
// a source. The receipt binds them once and never refreshes them on lookup.
type mgGuideReceiptProjection struct {
	capacityKnown, planningEstimate                        int64
	tunerCountMode, deliveryValidation, acquisition        string
	skippedChannels, skippedProgrammes, adjustedProgrammes int64
	exampleCount                                           int64
	examples                                               [10]string
}

func mgGuideReceiptText(s string, required bool, max int) bool {
	return (!required || s != "") && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func (p mgGuideReceiptProjection) valid() bool {
	if (p.capacityKnown != 0 && p.capacityKnown != 1) || p.planningEstimate < 1 || p.skippedChannels < 0 || p.skippedProgrammes < 0 || p.adjustedProgrammes < 0 || p.exampleCount < 0 || p.exampleCount > 10 {
		return false
	}
	if p.tunerCountMode != "" && p.tunerCountMode != "configured" && p.tunerCountMode != "defaulted" && p.tunerCountMode != "discovered" {
		return false
	}
	if p.acquisition != "" && p.acquisition != "upload" && p.acquisition != "remote" {
		return false
	}
	if !mgGuideReceiptText(p.deliveryValidation, false, 4096) {
		return false
	}
	for i, s := range p.examples {
		if !mgGuideReceiptText(s, false, 4096) || (int64(i) >= p.exampleCount && s != "") {
			return false
		}
	}
	return true
}

type mgGuideReceiptKey struct {
	request       string
	identity      CommandIdentity
	requestDigest RequestDigest
	kind          mgGuideReceiptKind
}

func (k mgGuideReceiptKey) valid() bool {
	return mgGuideReceiptText(k.request, true, 256) && k.identity.Incarnation != ([16]byte{}) && k.identity.OperationID != ([16]byte{}) && k.identity.Sequence > 0 && k.requestDigest != (RequestDigest{}) && k.kind.valid()
}

type mgGuideReceipt struct {
	key                                                    mgGuideReceiptKey
	resultDigest                                           [32]byte
	appliedMS                                              int64
	source, generation                                     string
	revision, acknowledged                                 int64
	staged                                                 [3]int64
	bytes                                                  int64
	chain                                                  [32]byte
	sealed                                                 int64
	sourceName                                             string
	tuner                                                  int64
	projection                                             mgGuideReceiptProjection
	sourceRevision                                         int64
	sourceState, publishedAt, reclaimFamily, reclaimCursor string
}

func (r mgGuideReceipt) valid() bool {
	if !r.key.valid() || r.appliedMS < 0 || r.revision <= 0 || r.acknowledged != r.key.identity.Sequence-1 || r.bytes < 0 || (r.sealed != 0 && r.sealed != 1) || r.tuner < 0 || r.tuner > 256 || r.sourceRevision < 0 || !r.projection.valid() {
		return false
	}
	for _, n := range r.staged {
		if n < 0 {
			return false
		}
	}
	if !mgGuideReceiptText(r.source, true, 256) || !mgGuideReceiptText(r.generation, true, 256) || !mgGuideReceiptText(r.sourceName, true, 4096) || !mgGuideReceiptText(r.publishedAt, false, 64) || !mgGuideReceiptText(r.reclaimCursor, false, 4096) {
		return false
	}
	if r.sourceState != "" && r.sourceState != "active" && r.sourceState != "disabled" {
		return false
	}
	switch r.reclaimFamily {
	case "none", "programmes", "channels", "choices", "generation", "complete":
	default:
		return false
	}
	if r.key.kind == mgGuideReceiptPublish && (r.sealed != 1 || r.staged[0] <= 0 || r.sourceRevision <= 0 || r.sourceState == "" || r.publishedAt == "") {
		return false
	}
	return r.resultDigest == r.digest()
}

// Closed binary v1 digest: fixed fields, big-endian integers, byte-length UTF-8
// strings and each ordered diagnostic including its count. No JSON/result blob
// or resource/kind-dispatched generic result codec exists.
func (r mgGuideReceipt) digest() [32]byte {
	h := sha256.New()
	text := func(s string) {
		_, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(s))))
		_, _ = h.Write([]byte(s))
	}
	number := func(n int64) { _, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(n))) }
	text("portico.mg.guide.receipt.result.v1")
	text(r.key.request)
	_, _ = h.Write(r.key.identity.Incarnation[:])
	number(r.key.identity.Sequence)
	_, _ = h.Write(r.key.identity.OperationID[:])
	_, _ = h.Write(r.key.requestDigest[:])
	text(string(r.key.kind))
	number(r.appliedMS)
	text(r.source)
	text(r.generation)
	number(r.revision)
	number(r.acknowledged)
	for _, v := range r.staged {
		number(v)
	}
	number(r.bytes)
	_, _ = h.Write(r.chain[:])
	number(r.sealed)
	text(r.sourceName)
	number(r.tuner)
	p := r.projection
	number(p.capacityKnown)
	number(p.planningEstimate)
	text(p.tunerCountMode)
	text(p.deliveryValidation)
	text(p.acquisition)
	number(p.skippedChannels)
	number(p.skippedProgrammes)
	number(p.adjustedProgrammes)
	number(p.exampleCount)
	for i := int64(0); i < p.exampleCount && i < 10; i++ {
		text(p.examples[i])
	}
	number(r.sourceRevision)
	text(r.sourceState)
	text(r.publishedAt)
	text(r.reclaimFamily)
	text(r.reclaimCursor)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

const (
	mgGuideReceiptRead             stmt.ID = "mg.guide.receipt.read"
	mgGuideReceiptByOperation      stmt.ID = "mg.guide.receipt.operation"
	mgGuideReceiptInsert           stmt.ID = "mg.guide.receipt.insert"
	mgGuideReceiptDiagnosticRead   stmt.ID = "mg.guide.receipt.diagnostics"
	mgGuideReceiptDiagnosticInsert stmt.ID = "mg.guide.receipt.diagnostic_insert"
	mgGuideReceiptReclaimRead      stmt.ID = "mg.guide.receipt.reclaim"
	mgGuideReceiptACK              stmt.ID = "mg.guide.receipt.ack"
	mgGuideReceiptFloorRead        stmt.ID = "mg.guide.receipt.floor"
	mgGuideReceiptFloorInsert      stmt.ID = "mg.guide.receipt.floor_insert"
	mgGuideReceiptBirthHistory     stmt.ID = "mg.guide.receipt.birth_history"
	mgGuideReceiptBirthRead        stmt.ID = "mg.guide.receipt.birth"
)
const mgGuideReceiptColumns = `request_id,command_incarnation,command_sequence,operation_id,request_digest,result_digest,applied_ms,command_kind,source_id,generation_id,accepted_revision,accepted_acknowledged_sequence,result_channel_count,result_programme_count,result_choice_count,result_staged_bytes,result_stage_digest,result_sealed,result_source_name,result_tuner_count,result_capacity_known,result_planning_estimate,result_tuner_count_mode,result_delivery_validation,result_acquisition,result_skipped_channels,result_skipped_programmes,result_adjusted_programmes,result_diagnostic_example_count,result_source_revision,result_source_state,result_published_at,result_reclaim_family,result_reclaim_cursor`

func mgGuideReceiptDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	header := []stmt.Param{s("request"), b("incarnation"), i("sequence"), b("operation"), b("request_digest"), b("result_digest"), i("applied"), s("kind"), s("source"), s("generation"), i("revision"), i("ack"), i("channels"), i("programmes"), i("choices"), i("bytes"), b("chain"), i("sealed"), s("name"), i("tuners"), i("capacity_known"), i("planning"), s("tuner_mode"), s("delivery"), s("acquisition"), i("skipped_channels"), i("skipped_programmes"), i("adjusted"), i("example_count"), i("source_revision"), s("source_state"), s("published"), s("reclaim_family"), s("reclaim_cursor")}
	defs := []stmt.Definition{
		{ID: mgGuideReceiptRead, Kind: stmt.Read, SQL: `SELECT ` + mgGuideReceiptColumns + ` FROM live_operation_command_receipts WHERE request_id=? AND command_incarnation=? AND command_sequence=?`, Params: header[:3]},
		{ID: mgGuideReceiptByOperation, Kind: stmt.Read, SQL: `SELECT ` + mgGuideReceiptColumns + ` FROM live_operation_command_receipts INDEXED BY sqlite_autoindex_live_operation_command_receipts_2 WHERE request_id=? AND command_incarnation=? AND operation_id=?`, Params: []stmt.Param{header[0], header[1], header[3]}},
		{ID: mgGuideReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO live_operation_command_receipts(` + mgGuideReceiptColumns + `) VALUES(` + strings.TrimSuffix(strings.Repeat("?,", len(header)), ",") + `)`, Params: header},
		{ID: mgGuideReceiptDiagnosticRead, Kind: stmt.Read, SQL: `SELECT ordinal,message FROM live_operation_receipt_diagnostics WHERE request_id=? AND command_incarnation=? AND command_sequence=? ORDER BY ordinal LIMIT 10`, Params: header[:3]},
		{ID: mgGuideReceiptDiagnosticInsert, Kind: stmt.Write, SQL: `INSERT INTO live_operation_receipt_diagnostics(request_id,command_incarnation,command_sequence,ordinal,message) VALUES(?,?,?,?,?)`, Params: []stmt.Param{header[0], header[1], header[2], i("ordinal"), s("message")}},
		{ID: mgGuideReceiptReclaimRead, Kind: stmt.Read, SQL: `SELECT reclaim_family,reclaim_cursor FROM live_operations INDEXED BY sqlite_autoindex_live_operations_1 WHERE request_id=?`, Params: header[:1]},
		{ID: mgGuideReceiptACK, Kind: stmt.Write, SQL: `UPDATE live_operations INDEXED BY sqlite_autoindex_live_operations_1 SET acknowledged_command_sequence=? WHERE request_id=? AND command_incarnation=? AND source_id=? AND generation_id=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND stage_revision=?`, Params: []stmt.Param{i("next_ack"), s("request"), b("incarnation"), s("source"), s("generation"), i("last"), i("expected_ack"), i("revision")}},
		{ID: mgGuideReceiptFloorRead, Kind: stmt.Read, SQL: `SELECT request_id,command_incarnation,create_operation_id,birth_request_digest,source_id,final_sequence,final_result_digest,retired_ms FROM live_operation_retirements WHERE request_id=?`, Params: header[:1]},
		{ID: mgGuideReceiptFloorInsert, Kind: stmt.Write, SQL: `INSERT INTO live_operation_retirements(request_id,command_incarnation,create_operation_id,birth_request_digest,source_id,final_sequence,final_result_digest,retired_ms) VALUES(?,?,?,?,?,?,?,?)`, Params: []stmt.Param{s("request"), b("incarnation"), b("create_operation"), b("birth_digest"), s("source"), i("final_sequence"), b("final_digest"), i("retired")}},
		{ID: mgGuideReceiptBirthHistory, Kind: stmt.Read, SQL: `SELECT 1 FROM live_operation_command_receipts WHERE request_id=? LIMIT 1`, Params: header[:1]},
		{ID: mgGuideReceiptBirthRead, Kind: stmt.Read, SQL: `SELECT request_id FROM live_operations INDEXED BY sqlite_autoindex_live_operations_1 WHERE request_id=?`, Params: header[:1]},
	}
	for j := range defs {
		d := &defs[j]
		d.Owner = "writer.mg.guide.receipts"
		d.CallSites = []string{"writer/mg_guide_receipts.go"}
		switch d.ID {
		case mgGuideReceiptInsert, mgGuideReceiptDiagnosticInsert, mgGuideReceiptFloorInsert:
		case mgGuideReceiptBirthHistory, mgGuideReceiptRead:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"live_operation_command_receipts"}}
		case mgGuideReceiptByOperation:
			d.Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operation_command_receipts_2"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "live_operation_command_receipts", Name: "sqlite_autoindex_live_operation_command_receipts_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "request_id", Collation: "BINARY"}, {Name: "command_incarnation", Collation: "BINARY"}, {Name: "operation_id", Collation: "BINARY"}}}}}
		case mgGuideReceiptDiagnosticRead:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"live_operation_receipt_diagnostics"}}
		case mgGuideReceiptFloorRead:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"live_operation_retirements"}}
		default:
			d.Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "live_operations", Name: "sqlite_autoindex_live_operations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "request_id", Collation: "BINARY"}}}}}
		}
		args := make([]any, len(d.Params))
		for n, p := range d.Params {
			switch p.Type {
			case stmt.Text:
				args[n] = "missing"
			case stmt.Blob:
				size := 16
				if strings.Contains(p.Name, "digest") || p.Name == "chain" {
					size = 32
				}
				args[n] = make([]byte, size)
			default:
				args[n] = int64(1)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing_native_key", Bindings: args}}
	}
	return defs
}
func mgGuideReceiptStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgGuideReceiptRead, mgGuideReceiptByOperation, mgGuideReceiptInsert, mgGuideReceiptDiagnosticRead, mgGuideReceiptDiagnosticInsert, mgGuideReceiptReclaimRead, mgGuideReceiptACK, mgGuideReceiptFloorRead, mgGuideReceiptFloorInsert, mgGuideReceiptBirthRead, mgGuideReceiptBirthHistory:
	default:
		return statementStep{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return step.statementStep, nil
}
func mgGuideReceiptQuery(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, err := mgGuideReceiptStep(t, id)
	if err != nil {
		return commandRow{}, err
	}
	return t.queryRow(ctx, step, args...)
}
func mgGuideReceiptChange(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	step, err := mgGuideReceiptStep(t, id)
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

func (r mgGuideReceipt) values() []any {
	k, p := r.key, r.projection
	return []any{k.request, k.identity.Incarnation[:], k.identity.Sequence, k.identity.OperationID[:], k.requestDigest[:], r.resultDigest[:], r.appliedMS, string(k.kind), r.source, r.generation, r.revision, r.acknowledged, r.staged[0], r.staged[1], r.staged[2], r.bytes, r.chain[:], r.sealed, r.sourceName, r.tuner, p.capacityKnown, p.planningEstimate, p.tunerCountMode, p.deliveryValidation, p.acquisition, p.skippedChannels, p.skippedProgrammes, p.adjustedProgrammes, p.exampleCount, r.sourceRevision, r.sourceState, r.publishedAt, r.reclaimFamily, r.reclaimCursor}
}
func mgReadGuideReceiptRow(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (mgGuideReceipt, error) {
	var r mgGuideReceipt
	row, err := mgGuideReceiptQuery(ctx, t, id, args...)
	if err != nil {
		return r, err
	}
	var inc, op, requestDigest, resultDigest, chain []byte
	var kind string
	p := &r.projection
	err = row.Scan(&r.key.request, &inc, &r.key.identity.Sequence, &op, &requestDigest, &resultDigest, &r.appliedMS, &kind, &r.source, &r.generation, &r.revision, &r.acknowledged, &r.staged[0], &r.staged[1], &r.staged[2], &r.bytes, &chain, &r.sealed, &r.sourceName, &r.tuner, &p.capacityKnown, &p.planningEstimate, &p.tunerCountMode, &p.deliveryValidation, &p.acquisition, &p.skippedChannels, &p.skippedProgrammes, &p.adjustedProgrammes, &p.exampleCount, &r.sourceRevision, &r.sourceState, &r.publishedAt, &r.reclaimFamily, &r.reclaimCursor)
	if err != nil {
		return mgGuideReceipt{}, err
	}
	if len(inc) != 16 || len(op) != 16 || len(requestDigest) != 32 || len(resultDigest) != 32 || len(chain) != 32 || p.exampleCount < 0 || p.exampleCount > 10 {
		return mgGuideReceipt{}, ErrConflict
	}
	copy(r.key.identity.Incarnation[:], inc)
	copy(r.key.identity.OperationID[:], op)
	copy(r.key.requestDigest[:], requestDigest)
	copy(r.resultDigest[:], resultDigest)
	copy(r.chain[:], chain)
	r.key.kind = mgGuideReceiptKind(kind)
	step, err := mgGuideReceiptStep(t, mgGuideReceiptDiagnosticRead)
	if err != nil {
		return mgGuideReceipt{}, err
	}
	rows, err := t.query(ctx, step, r.key.request, r.key.identity.Incarnation[:], r.key.identity.Sequence)
	if err != nil {
		return mgGuideReceipt{}, err
	}
	var count int64
	for rows.Next() {
		var ordinal int64
		var message string
		if err := rows.Scan(&ordinal, &message); err != nil {
			return mgGuideReceipt{}, errors.Join(err, rows.Close())
		}
		if ordinal != count || count >= p.exampleCount || !mgGuideReceiptText(message, false, 4096) {
			return mgGuideReceipt{}, errors.Join(ErrConflict, rows.Close())
		}
		p.examples[count] = message
		count++
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return mgGuideReceipt{}, err
	}
	if count != p.exampleCount || !r.valid() {
		return mgGuideReceipt{}, ErrConflict
	}
	return r, nil
}

// Raw original-history lookup; a missing history is NOT permission to execute a
// fresh body. The parent resolves live namespace/ACK/floor and fresh disclosure.
// A matching receipt is returned without touching today's operation/generation.
func mgLookupGuideReceipt(ctx context.Context, t *commandTurn, key mgGuideReceiptKey) (out mgGuideReceipt, found bool, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
		if err != nil {
			found = false
		}
	}()
	if !key.valid() {
		return out, false, ErrInvalid
	}
	out, err = mgReadGuideReceiptRow(ctx, t, mgGuideReceiptRead, key.request, key.identity.Incarnation[:], key.identity.Sequence)
	if errors.Is(err, sql.ErrNoRows) {
		_, opErr := mgReadGuideReceiptRow(ctx, t, mgGuideReceiptByOperation, key.request, key.identity.Incarnation[:], key.identity.OperationID[:])
		if errors.Is(opErr, sql.ErrNoRows) {
			return mgGuideReceipt{}, false, nil
		}
		if opErr != nil {
			return mgGuideReceipt{}, false, opErr
		}
		return mgGuideReceipt{}, false, ErrConflict
	}
	if err != nil {
		return mgGuideReceipt{}, false, err
	}
	if out.key != key {
		return mgGuideReceipt{}, false, ErrConflict
	}
	return out, true, nil
}

func (r mgGuideReceipt) matchesNative(op mgGuideNative) bool {
	p := op.progress
	m := p.manifest
	return r.key.request == op.request && r.key.identity.Incarnation == op.incarnation && r.key.identity.Sequence == p.sequence && r.source == m.source && r.generation == m.generation && r.revision == p.revision && r.staged == p.staged && r.bytes == p.bytes && r.chain == p.chain && (r.sealed == 1) == p.sealed && r.sourceName == op.name && r.tuner == op.tuner && r.publishedAt == op.publishedAt
}

// Existing original history wins before any current-fact/prepared-projection
// checks: zero new effects, including after head replacement/owner deletion.
// For a new save every mutable result field comes from exact post-operation
// native readback. Frozen parser/capacity metadata is deliberately explicit.
// Any error after a body/INSERT requires the parent to roll back the WHOLE frame.
func mgSaveGuideReceipt(ctx context.Context, t *commandTurn, result mgGuideNativeResult, identity CommandIdentity, requestDigest RequestDigest, kind mgGuideReceiptKind, projection mgGuideReceiptProjection) (out mgGuideReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	key := mgGuideReceiptKey{result.operation.request, identity, requestDigest, kind}
	saved, found, err := mgLookupGuideReceipt(ctx, t, key)
	if err != nil {
		return out, err
	}
	if found {
		return saved, nil
	}
	op := result.operation
	p := op.progress
	if (identity.Sequence == 1 && (kind != mgGuideReceiptBegin || identity.OperationID != op.createOperation || [32]byte(requestDigest) != op.birthDigest)) || (identity.Sequence != 1 && kind == mgGuideReceiptBegin) {
		return out, ErrConflict
	}
	if !projection.valid() || !validMGGuideProgress(p) || identity.Incarnation != op.incarnation || identity.Sequence != p.sequence || op.acknowledged != identity.Sequence-1 || result.appliedMS < 0 {
		return out, ErrConflict
	}
	// A durable floor is checked before admitting any new receipt/effect lineage.
	if _, exists, e := mgReadGuideRetirementFloor(ctx, t, op.request); e != nil {
		return out, e
	} else if exists {
		return out, ErrConflict
	}
	actual, err := mgReadGuideNative(ctx, t, op.request)
	if err != nil {
		return out, err
	}
	if actual != op {
		return out, ErrConflict
	}
	head, err := mgGuideCurrentHead(ctx, t, p.manifest.source)
	if err != nil {
		return out, err
	}
	if head != result.head {
		return out, ErrConflict
	}
	now, err := mgGuideNativeTime(ctx, t)
	if err != nil {
		return out, err
	}
	if result.appliedMS > now {
		return out, ErrConflict
	}
	if kind == mgGuideReceiptPublish && (op.status != "published" || op.resultRevision != head.revision || op.resultState != head.state || head.generation != p.manifest.generation || op.publishedAt != time.UnixMilli(result.appliedMS).UTC().Format("2006-01-02T15:04:05.000Z")) {
		return out, ErrConflict
	}
	if kind == mgGuideReceiptSeal && (op.status != "staging" || !p.sealed) {
		return out, ErrConflict
	}
	row, err := mgGuideReceiptQuery(ctx, t, mgGuideReceiptReclaimRead, op.request)
	if err != nil {
		return out, err
	}
	out = mgGuideReceipt{key: key, appliedMS: result.appliedMS, source: p.manifest.source, generation: p.manifest.generation, revision: p.revision, acknowledged: op.acknowledged, staged: p.staged, bytes: p.bytes, chain: p.chain, sourceName: op.name, tuner: op.tuner, projection: projection, sourceRevision: head.revision, sourceState: head.state, publishedAt: op.publishedAt}
	if p.sealed {
		out.sealed = 1
	}
	if err := row.Scan(&out.reclaimFamily, &out.reclaimCursor); err != nil {
		return out, err
	}
	out.resultDigest = out.digest()
	if !out.valid() {
		return out, ErrInvalid
	}
	if err = mgGuideReceiptChange(ctx, t, mgGuideReceiptInsert, out.values()...); err != nil {
		return out, err
	}
	for i := int64(0); i < projection.exampleCount; i++ {
		if err = mgGuideReceiptChange(ctx, t, mgGuideReceiptDiagnosticInsert, key.request, identity.Incarnation[:], identity.Sequence, i, projection.examples[i]); err != nil {
			return out, err
		}
	}
	got, found, err := mgLookupGuideReceipt(ctx, t, key)
	if err != nil {
		return out, err
	}
	if !found || got != out {
		return out, ErrConflict
	}
	return got, nil
}

// ACK is exact original-result acknowledgement, not source disclosure or a new
// command body. It cannot ACK an unknown operation or advance over a gap. An
// already matching native ACK is an idempotent zero-effect result.
func mgACKGuideReceipt(ctx context.Context, t *commandTurn, before mgGuideNative, key mgGuideReceiptKey, resultDigest [32]byte) (out mgGuideNative, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	receipt, found, err := mgLookupGuideReceipt(ctx, t, key)
	if err != nil {
		return out, err
	}
	if !found || resultDigest == ([32]byte{}) || receipt.resultDigest != resultDigest || !receipt.matchesNative(before) || before.acknowledged < receipt.acknowledged || before.acknowledged > key.identity.Sequence {
		return out, ErrConflict
	}
	actual, err := mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if actual != before {
		return out, ErrConflict
	}
	if before.acknowledged == key.identity.Sequence {
		return actual, nil
	}
	if before.acknowledged != receipt.acknowledged {
		return out, ErrConflict
	}
	p := before.progress
	if err = mgGuideReceiptChange(ctx, t, mgGuideReceiptACK, key.identity.Sequence, before.request, before.incarnation[:], p.manifest.source, p.manifest.generation, p.sequence, before.acknowledged, p.revision); err != nil {
		return out, err
	}
	want := before
	want.acknowledged = key.identity.Sequence
	out, err = mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if out != want {
		return out, ErrConflict
	}
	return out, nil
}

type mgGuideRetirementFloor struct {
	request                      string
	incarnation, createOperation [16]byte
	birthDigest                  [32]byte
	source                       string
	finalSequence                int64
	resultDigest                 [32]byte
	retiredMS                    int64
}

func (f mgGuideRetirementFloor) valid() bool {
	return mgGuideReceiptText(f.request, true, 256) && f.incarnation != ([16]byte{}) && f.createOperation != ([16]byte{}) && f.birthDigest != ([32]byte{}) && mgGuideReceiptText(f.source, true, 256) && f.finalSequence > 0 && f.resultDigest != ([32]byte{}) && f.retiredMS >= 0
}
func mgReadGuideRetirementFloor(ctx context.Context, t *commandTurn, request string) (out mgGuideRetirementFloor, found bool, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(t), out, err)
		if err != nil {
			found = false
		}
	}()
	if !mgGuideReceiptText(request, true, 256) {
		return out, false, ErrInvalid
	}
	row, err := mgGuideReceiptQuery(ctx, t, mgGuideReceiptFloorRead, request)
	if err != nil {
		return out, false, err
	}
	var inc, create, birth, digest []byte
	err = row.Scan(&out.request, &inc, &create, &birth, &out.source, &out.finalSequence, &digest, &out.retiredMS)
	if errors.Is(err, sql.ErrNoRows) {
		return mgGuideRetirementFloor{}, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if len(inc) != 16 || len(create) != 16 || len(birth) != 32 || len(digest) != 32 {
		return out, false, ErrConflict
	}
	copy(out.incarnation[:], inc)
	copy(out.createOperation[:], create)
	copy(out.birthDigest[:], birth)
	copy(out.resultDigest[:], digest)
	if !out.valid() {
		return out, false, ErrConflict
	}
	return out, true, nil
}

// This only installs a non-resurrection floor. It neither deletes the live
// owner/generation nor asserts that session/physical/follower debt is clear.
// Parent must prove actual debt retirement before its later bounded deletion.
func mgSaveGuideRetirementFloor(ctx context.Context, t *commandTurn, before mgGuideNative, key mgGuideReceiptKey, resultDigest [32]byte) (out mgGuideRetirementFloor, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !key.valid() || before.request != key.request || before.incarnation != key.identity.Incarnation || before.progress.sequence != key.identity.Sequence || before.acknowledged != key.identity.Sequence || (before.status != "published" && before.status != "superseded") || resultDigest == ([32]byte{}) {
		return out, ErrConflict
	}
	if key.kind != mgGuideReceiptPublish && key.kind != mgGuideReceiptDiscard && key.kind != mgGuideReceiptReclaim {
		return out, ErrConflict
	}
	if key.kind == mgGuideReceiptPublish && before.status != "published" {
		return out, ErrConflict
	}
	want := mgGuideRetirementFloor{request: before.request, incarnation: before.incarnation, createOperation: before.createOperation, birthDigest: before.birthDigest, source: before.progress.manifest.source, finalSequence: before.progress.sequence, resultDigest: resultDigest}
	// Exact final identity/digest remains required even on an idempotent save.
	// After history GC, use the floor read/birth guard; do not manufacture an old
	// command result from a floor that does not retain its operation/request key.
	receipt, found, err := mgLookupGuideReceipt(ctx, t, key)
	if err != nil {
		return out, err
	}
	if !found || receipt.resultDigest != resultDigest || !receipt.matchesNative(before) {
		return out, ErrConflict
	}
	floor, found, err := mgReadGuideRetirementFloor(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if found {
		want.retiredMS = floor.retiredMS
		if floor != want {
			return out, ErrConflict
		}
		return floor, nil
	}
	actual, err := mgReadGuideNative(ctx, t, before.request)
	if err != nil {
		return out, err
	}
	if actual != before {
		return out, ErrConflict
	}
	want.retiredMS, err = mgGuideNativeTime(ctx, t)
	if err != nil {
		return out, err
	}
	if !want.valid() {
		return out, ErrInvalid
	}
	if err = mgGuideReceiptChange(ctx, t, mgGuideReceiptFloorInsert, want.request, want.incarnation[:], want.createOperation[:], want.birthDigest[:], want.source, want.finalSequence, want.resultDigest[:], want.retiredMS); err != nil {
		return out, err
	}
	out, found, err = mgReadGuideRetirementFloor(ctx, t, want.request)
	if err != nil {
		return out, err
	}
	if !found || out != want {
		return out, ErrConflict
	}
	return out, nil
}

// Call before a birth INSERT on the same turn. A retained request ID can never
// be reborn under a new namespace/birth digest, even after every receipt is GC'd.
// Nil error means only absence of native request/floor, never authorization.
func mgCheckGuideReceiptBirth(ctx context.Context, t *commandTurn, request string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if _, found, e := mgReadGuideRetirementFloor(ctx, t, request); e != nil {
		return e
	} else if found {
		return ErrConflict
	}
	row, err := mgGuideReceiptQuery(ctx, t, mgGuideReceiptBirthRead, request)
	if err != nil {
		return err
	}
	var existing string
	err = row.Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		history, e := mgGuideReceiptQuery(ctx, t, mgGuideReceiptBirthHistory, request)
		if e != nil {
			return e
		}
		var unit int64
		e = history.Scan(&unit)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		return ErrConflict
	}
	if err != nil {
		return err
	}
	return ErrConflict
}
