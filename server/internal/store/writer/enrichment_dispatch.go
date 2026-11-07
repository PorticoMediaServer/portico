package writer

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// One closed optional union in the existing dispatcher. No callback or second
// admission/transaction/terminal loop is introduced by this domain adapter.
type commandInvocation struct {
	publisher  *publisherInvocation
	enrichment *enrichmentInvocation
}
type enrichmentCommandResult struct {
	Outcome ReceiptOutcome
	Value   enrichmentNativeResult
	Digest  ResultDigest
}
type enrichmentTurnLookup struct {
	owner    receiptOwnerFacts
	found    receiptFacts
	decision receiptDecision
	saved    savedSealReceipt
}

const enrichmentHistoryFixedBytes uint64 = 64 << 10
const enrichmentDisclosureOwner stmt.ID = "enrichment.seal.disclosure_owner"

func enrichmentPlanIndex(id CommandID) (int, bool) {
	switch id {
	case EvidenceStage:
		return 0, true
	case CreditStage:
		return 1, true
	case EvidenceSeal:
		return 2, true
	case CreditSourceSeal:
		return 3, true
	default:
		return 0, false
	}
}
func enrichmentPurpose(id CommandID) bool { _, ok := enrichmentPlanIndex(id); return ok }

func validateEnrichmentInvocation(r *effectiveRuntime, w *Writer, in *enrichmentInvocation) error {
	if r == nil || w == nil || w.effective != r || in == nil || !enrichmentPurpose(in.purpose) {
		return ErrInvalid
	}
	if err := r.currentDisclosure(w, in.disclosure, in.request); err != nil {
		return err
	}
	key := in.disclosure.scope.libraryKey
	if key == "" || len(key) > maxCanonicalRequestBytes || !utf8.ValidString(key) {
		return ErrDisclosureDenied
	}
	switch in.action {
	case enrichmentResolve, enrichmentAcknowledge:
		if in.payload != nil {
			return ErrInvalid
		}
	case enrichmentApply:
		if in.payload == nil || in.payload.scope.identity != in.request.Receipt.Identity || in.payload.scope.request != in.request.Receipt.RequestDigest || in.payload.scope.ownerID != in.request.ProviderOperationID || in.payload.scope.entityID != in.request.EntityID {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func (r *effectiveRuntime) runEnrichment(ctx context.Context, w *Writer, in *enrichmentInvocation) (out enrichmentCommandResult, ack AckResult, err error) {
	defer func() {
		if r != nil && w != nil && in != nil {
			err = errors.Join(err, r.currentDisclosure(w, in.disclosure, in.request))
		}
		v, e := finishCommand(ctx, ctx, commandCompletion{output: commandOutput{enrichment: out}, ack: ack}, err)
		out, ack, err = v.output.enrichment, v.ack, e
	}()
	if ctx == nil {
		return out, ack, ErrInvalid
	}
	if in != nil {
		frozen := *in
		in = &frozen
	}
	if err = validateEnrichmentInvocation(r, w, in); err != nil {
		return out, ack, err
	}
	value, ack, err := r.runCommand(ctx, w, nil, EnrichmentReceiptRequest{}, nil, in.action == enrichmentAcknowledge, nil, commandInvocation{enrichment: in})
	return value.enrichment, ack, err
}

func enrichmentSealHistoryCommandDefinitions() []stmt.Definition {
	defs := append(enrichmentSealHistoryDefinitions(), effectiveDispatchDefinitions()...)
	for _, d := range enrichmentSealProofLengthDefinitions() {
		if d.ID == sealReceiptLengths {
			defs = append(defs, d)
		}
	}
	return append(defs, stmt.Definition{ID: enrichmentDisclosureOwner, Owner: "writer.enrichment", Kind: stmt.Read,
		SQL: "SELECT entity_id FROM metadata_provider_operations WHERE id=?", Params: []stmt.Param{{Name: "owner", Type: stmt.Text}},
		CallSites: []string{"writer/enrichment_dispatch.go:discloseEnrichment"}, Cohorts: []stmt.Cohort{{Name: "missing_owner", Bindings: []any{"missing"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_1"}}})
}
func enrichmentHistoryMaxUses(id stmt.ID) int {
	switch id {
	case sealHistoryOwner, sealHistoryHeader, sealHistoryPayload, sealReceiptLengths, enrichmentDisclosureOwner, effectiveDisclosureLibrary, effectiveDisclosureSubject, effectiveReceiptAck:
		return 1
	default:
		return 0
	}
}

// Trusted one-time profile composition, following the existing scan-history
// installation pattern. Only the two sealed-result families are executable in
// this slice; stage and fresh provenance remain explicitly unqualified.
func composeEnrichmentSealHistory(ctx context.Context, r *effectiveRuntime, evidence, source effectivePlan) (err error) {
	var locked, executorLocked, installed bool
	var oldRegistry *stmt.Registry
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry = oldRegistry
			r.enrichmentPlans = [4]*effectivePlan{}
		}
		if executorLocked {
			r.executor.mu.Unlock()
		}
		if locked {
			r.mu.Unlock()
		}
	}()
	if ctx == nil || r == nil || r.executor == nil || r.registry == nil {
		return ErrInvalid
	}
	if !r.mu.TryLock() {
		return ErrBusy
	}
	locked = true
	if !r.executor.mu.TryLock() {
		return ErrBusy
	}
	executorLocked = true
	if r.fenced || r.executor.closed || r.executor.fenced || r.executor.ambiguous {
		return ErrClosed
	}
	if r.enrichmentPlans != ([4]*effectivePlan{}) {
		return ErrBusy
	}
	defs := enrichmentSealHistoryCommandDefinitions()
	evidence, err = freezeCommandPlan(r.executor, evidence, defs, EvidenceSeal)
	if err != nil {
		return err
	}
	source, err = freezeCommandPlan(r.executor, source, defs, CreditSourceSeal)
	if err != nil {
		return err
	}
	for _, p := range []effectivePlan{evidence, source} {
		if p.enrichmentMemory.preparationBytes < enrichmentHistoryFixedBytes {
			return &Refusal{Code: RefusalCapacity}
		}
	}
	all := make([]stmt.Definition, 0, len(r.registry.Manifest())+len(defs))
	for _, entry := range r.registry.Manifest() {
		all = append(all, entry.Definition)
	}
	for _, d := range defs {
		exists := false
		for _, old := range all {
			if old.ID == d.ID {
				if !reflect.DeepEqual(old, d) {
					return invalid("enrichment_definition")
				}
				exists = true
				break
			}
		}
		if !exists {
			all = append(all, d)
		}
	}
	registry, err := stmt.New(all, r.commandRegistrations(evidence.registration, source.registration)...)
	if err != nil {
		return err
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	oldRegistry, r.registry = r.registry, registry
	r.enrichmentPlans[2], r.enrichmentPlans[3] = &evidence, &source
	installed = true
	return nil
}

// Finite installed plans only; a later composition cannot discard existing
// domains' Capture receipts or use another profile to rescue a missing one.
func (r *effectiveRuntime) commandRegistrations(extra ...stmt.Receipt) []stmt.Receipt {
	out := []stmt.Receipt{r.plan.registration}
	if r.inventoryPlan != nil {
		out = append(out, r.inventoryPlan.registration)
	}
	for _, p := range r.lifecyclePlans {
		if p != nil {
			out = append(out, p.registration)
		}
	}
	for _, p := range r.issuerPlans {
		if p != nil {
			out = append(out, p.registration)
		}
	}
	for _, p := range r.enrichmentPlans {
		if p != nil {
			out = append(out, p.registration)
		}
	}
	return append(out, extra...)
}

func discloseEnrichment(ctx context.Context, t *commandTurn, in *enrichmentInvocation) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	required := enrichmentHistoryFixedBytes + 16*uint64(len(in.request.ProviderOperationID)+len(in.disclosure.scope.libraryKey))
	if err := t.requireCommandMemory(ctx, in.purpose, required, "enrichment_memory_partition"); err != nil {
		return err
	}
	for _, p := range []struct {
		id   stmt.ID
		args []any
		want int64
	}{
		{effectiveDisclosureLibrary, []any{in.request.LibraryID, in.disclosure.scope.libraryKey}, in.request.LibraryID},
		{effectiveDisclosureSubject, []any{in.request.EntityID, in.request.LibraryID}, in.request.EntityID},
	} {
		rows, err := enrichmentReadValues(ctx, t, p.id, p.args...)
		if err != nil {
			return err
		}
		if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != p.want {
			return ErrDisclosureDenied
		}
	}
	rows, err := enrichmentReadValues(ctx, t, enrichmentDisclosureOwner, in.request.ProviderOperationID)
	if err != nil {
		return err
	}
	if len(rows) > 1 || len(rows) == 1 && (len(rows[0]) != 1 || rows[0][0] != in.request.EntityID) {
		return ErrDisclosureDenied
	}
	return nil
}

func lookupEnrichmentSealTurn(ctx context.Context, t *commandTurn, in *enrichmentInvocation) (out enrichmentTurnLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if in.purpose != EvidenceSeal && in.purpose != CreditSourceSeal {
		return out, &Refusal{Code: RefusalUnqualified}
	}
	q := in.request
	ownerRows, err := enrichmentReadValues(ctx, t, sealHistoryOwner, q.ProviderOperationID, q.EntityID)
	if err != nil {
		return out, err
	}
	if len(ownerRows) == 1 {
		if len(ownerRows[0]) != 3 {
			return out, ErrResolveRequired
		}
		n := savedSealNativeRow{values: ownerRows[0]}
		copy(out.owner.incarnation[:], n.blob(0, 16))
		out.owner.lastSequence, out.owner.acknowledgedSequence = n.integer(1), n.integer(2)
		if n.err != nil {
			return out, n.err
		}
		out.owner.exists = true
	}
	rows, err := enrichmentReadValues(ctx, t, sealHistoryHeader, q.ProviderOperationID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence)
	if err != nil {
		return out, err
	}
	if len(rows) == 1 {
		if len(rows[0]) != 6 {
			return out, ErrResolveRequired
		}
		n := savedSealNativeRow{values: rows[0]}
		out.found.identity.command = q.Receipt.Identity
		copy(out.found.identity.command.OperationID[:], n.blob(0, 16))
		copy(out.found.identity.digest[:], n.blob(1, 32))
		switch n.text(2) {
		case "evidence_seal":
			out.found.kind = EvidenceSeal
		case "credit_source_seal":
			out.found.kind = CreditSourceSeal
		default:
			return out, ErrConflict
		}
		out.found.resultCount = n.integer(3)
		copy(out.found.resultDigest[:], n.blob(4, 32))
		if n.integer(5) != q.EntityID {
			return out, ErrConflict
		}
		if n.err != nil {
			return out, n.err
		}
		out.found.exists = true
	}
	out.decision, err = classifyReceipt(receiptIdentity{q.Receipt.Identity, q.Receipt.RequestDigest}, in.purpose, out.owner, out.found)
	if err != nil || out.decision != receiptReplay {
		return out, err
	}
	sizes, err := enrichmentReadValues(ctx, t, sealReceiptLengths, q.ProviderOperationID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence)
	if err != nil {
		return out, err
	}
	if len(sizes) != 1 || len(sizes[0]) != 26 {
		return out, ErrResolveRequired
	}
	required := enrichmentHistoryFixedBytes + 16*uint64(len(q.ProviderOperationID)+len(in.disclosure.scope.libraryKey))
	for _, size := range sizes[0] {
		if size == nil {
			continue
		}
		v, ok := size.(int64)
		if !ok || v < 0 || uint64(v) > (math.MaxUint64-required)/4 {
			return out, ErrResolveRequired
		}
		required += 4 * uint64(v)
	}
	if err = ctx.Err(); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	values, err := enrichmentReadValues(ctx, t, sealHistoryPayload, q.ProviderOperationID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence)
	if err != nil {
		return out, err
	}
	if len(values) != 1 || len(values[0]) != savedSealColumnCount {
		return out, ErrResolveRequired
	}
	for i, column := range []int{0, 1, 3, 4, 5, 9, 11, 15, 17, 19, 20, 21, 22, 23, 24, 25, 26, 28, 31, 32, 36, 37, 39, 40, 42, 43} {
		want, got := sizes[0][i], values[0][column]
		if want == nil {
			if got != nil {
				return out, ErrResolveRequired
			}
			continue
		}
		var actual int
		switch v := got.(type) {
		case string:
			actual = len(v)
		case []byte:
			actual = len(v)
		default:
			return out, ErrResolveRequired
		}
		if int64(actual) != want.(int64) {
			return out, ErrResolveRequired
		}
	}
	out.saved, err = decodeSavedSealReceipt(values[0])
	if err != nil {
		return out, err
	}
	if out.saved.identity != out.found.identity || out.saved.kind != out.found.kind || [32]byte(out.saved.resultDigest) != out.found.resultDigest || out.saved.facts.EntityID != q.EntityID || out.saved.facts.ProviderOperationID != q.ProviderOperationID {
		return out, ErrConflict
	}
	return out, nil
}

func enrichmentHistoricalResult(in *enrichmentInvocation, lookup enrichmentTurnLookup) enrichmentCommandResult {
	out := enrichmentCommandResult{Outcome: ReceiptOutcome{Reference: in.request.Receipt}}
	switch lookup.decision {
	case receiptFresh:
		out.Outcome.State = Pending
	case receiptRetired:
		out.Outcome.State = ResultRetired
	case receiptReplay:
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		out.Digest = lookup.saved.resultDigest
		if in.purpose == EvidenceSeal {
			v := evidenceSealResult{lookup.saved.facts}
			out.Value.evidenceSeal = &v
		} else {
			v := creditSourceSealResult{lookup.saved.facts}
			out.Value.sourceSeal = &v
		}
	}
	return out
}
func acknowledgeEnrichment(ctx context.Context, t *commandTurn, in *enrichmentInvocation, lookup enrichmentTurnLookup) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if lookup.decision == receiptRetired {
		return lookup.owner.acknowledgedSequence, nil
	}
	if lookup.decision != receiptReplay || lookup.owner.lastSequence != in.request.Receipt.Identity.Sequence {
		return 0, ErrResolveRequired
	}
	row, err := t.exec(ctx, t.steps[effectiveReceiptAck].statementStep, in.request.Receipt.Identity.Sequence, in.request.ProviderOperationID, in.request.Receipt.Identity.Incarnation[:], lookup.owner.lastSequence, lookup.owner.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	n, err := row.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, ErrConflict
	}
	return in.request.Receipt.Identity.Sequence, nil
}

// Protected W rows only. Each registered point is native-PK bounded to one row;
// no scanner/borrowed buffer escapes native locks or the domain decoder.
func enrichmentReadValues(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (out [][]any, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	step, ok := t.steps[id]
	if !ok {
		return nil, ErrInvalid
	}
	rows, err := t.query(ctx, step.statementStep, args...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if len(columns) == 0 || len(columns) > 44 {
		return nil, ErrResolveRequired
	}
	if !rows.Next() {
		return nil, rows.Err()
	}
	row, dest := make([]any, len(columns)), make([]any, len(columns))
	for i := range row {
		dest[i] = &row[i]
	}
	if err = rows.Scan(dest...); err != nil {
		return nil, err
	}
	for i, v := range row {
		switch x := v.(type) {
		case nil, int64:
		case string:
			row[i] = strings.Clone(x)
		case []byte:
			b := make([]byte, len(x))
			copy(b, x)
			row[i] = b
		default:
			return nil, ErrResolveRequired
		}
	}
	if rows.Next() {
		return nil, ErrResolveRequired
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return [][]any{row}, nil
}
