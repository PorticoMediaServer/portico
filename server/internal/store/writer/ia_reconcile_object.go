package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"path"
	"reflect"
	"strings"
	"unicode/utf8"
)

const iaReconcileObjectRequestVersion uint16 = 1

const (
	iaReconcileMaxKeyBytes   = 512
	iaReconcileMaxPathBytes  = 16384
	iaReconcileMaxMoveWindow = 2
	iaReconcileMaxCandidates = iaReconcileMaxMoveWindow + 1
)

// IA-17 reconciles exactly one retained object against one complete native
// observation. Its private outcome and history carry the exact typed result; no
// public facade, worker or producer is wired, so the accepting body stays
// uncalled outside this package.
type iaReconcileAcceptOutcome struct {
	decision receiptDecision
	result   iaReconcileObjectResult
	digest   ResultDigest
}
type iaReconcileObjectHistory struct {
	decision receiptDecision
	result   iaReconcileObjectResult
	digest   ResultDigest
}

// Same-turn saved header, never a disclosure capability. The complete typed
// child cohort is decoded only after current authority and the receipt floor.
type iaReconcileSavedHeader struct {
	facts                              receiptFacts
	acceptedRevision, appliedMS        int64
	sourceID, sourceIncarnation        string
	beforeKey, afterKey, phase, health string
	authoritative, processed, analyzed int64
	warnings                           int64
}

// Prepared witnesses only. The retained object is the exact old state the
// caller believes it is reconciling; the current observation is the native
// file as it exists now. Neither is accepted from this request: a fresh turn
// re-reads both. No authority, asset row, catalogue row or Basic decision is
// captured here.
type iaReconcileObjectRequest struct {
	scope  inventoryScope
	source inventorySource
	run    inventoryRun

	accepted iaReconcileWitness
	current  iaReconcileWitness

	moveWindowComplete bool
	phase              string
	authoritative      int64
	// afterObjectID names the prepared replacement object only when the current
	// native identity is a different file at the same path. It is empty for an
	// unchanged, changed or moved retained object.
	afterObjectID string
}

type iaReconcileWitness struct {
	objectID, relativePath, objectIdentity, revision string
	size, modifiedNS                                 int64
}

type iaReconcileObjectCommand struct {
	identity CommandIdentity
	request  iaReconcileObjectRequest
	digest   RequestDigest
	valid    bool
}

func newIAReconcileObjectCommand(id CommandIdentity, r iaReconcileObjectRequest) (*iaReconcileObjectCommand, error) {
	// Counts precede all text traversal, copying and hashing.
	if !iaEpisodeCounts(reflect.ValueOf(r)) {
		return nil, invalid("ia_reconcile_count")
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaEpisodeWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_reconcile_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !iaEpisodeValidText(reflect.ValueOf(r)) || !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return nil, invalid("ia_reconcile_text")
	}
	if err := validateIAReconcileOriginal(id, r); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IAReconcileObject, iaReconcileObjectRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	r.phase = strings.Clone(r.phase)
	for _, s := range iaReconcileTexts(&r) {
		*s = strings.Clone(*s)
	}
	return &iaReconcileObjectCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

// Every accepting gate is a closed witness comparison. Phase and authority are
// prepared evidence, never permission; the fresh turn re-proves eligibility.
func validateIAReconcileOriginal(id CommandIdentity, r iaReconcileObjectRequest) error {
	s, u := r.source, r.run
	if id.Sequence <= 0 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) ||
		r.scope.runID == "" || r.scope.libraryKey == "" || r.scope.libraryID <= 0 ||
		s.id == "" || s.incarnation == "" || s.rootIdentity == "" || s.generation <= 0 || len(s.resolvedRoot) == 0 || s.resolvedRoot[0] != '/' ||
		u.revision <= 0 || u.policyRevision <= 0 || u.leaseOwner <= 0 || u.leaseGeneration <= 0 || u.leaseExpiresMS <= 0 ||
		u.lastSequence != id.Sequence-1 || u.acknowledgedSequence != u.lastSequence || u.phase != "inventory" || !inventoryScanMode(u.scanMode) {
		return invalid("ia_reconcile_original")
	}
	root := string(s.resolvedRoot)
	if strings.ContainsAny(root, `\`) || path.Clean(root) != root {
		return invalid("ia_reconcile_root")
	}
	if r.phase != "reconciling" || (r.authoritative != 0 && r.authoritative != 1) {
		return invalid("ia_reconcile_phase")
	}
	if !iaReconcileWitnessValid(r.accepted, true) || !iaReconcileWitnessValid(r.current, false) {
		return invalid("ia_reconcile_witness")
	}
	if r.current.relativePath != r.accepted.relativePath && r.current.objectIdentity == "" {
		return invalid("ia_reconcile_move_identity")
	}
	if r.afterObjectID != "" {
		if !iaReconcileText(r.afterObjectID, iaReconcileMaxKeyBytes) || r.afterObjectID == r.accepted.objectID ||
			r.current.objectID != r.afterObjectID {
			return invalid("ia_reconcile_after")
		}
	} else if r.current.objectID != r.accepted.objectID {
		// A move or refresh keeps the one retained durable object id; only an
		// explicit replacement may name a different prepared object.
		return invalid("ia_reconcile_object_id")
	}
	return nil
}

// The retained side always carries a native identity: a safe move cannot be
// inferred without one. The current observation may omit it only when it stays
// at the retained path.
func iaReconcileWitnessValid(w iaReconcileWitness, retained bool) bool {
	if !iaReconcileText(w.objectID, iaReconcileMaxKeyBytes) ||
		!inventoryPath(w.relativePath, false) || !iaReconcileText(w.revision, iaReconcileMaxKeyBytes) ||
		w.size < 0 || (retained && w.objectIdentity == "") {
		return false
	}
	if w.objectIdentity != "" && !iaReconcileText(w.objectIdentity, iaReconcileMaxKeyBytes) {
		return false
	}
	return true
}

func iaReconcileText(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func iaReconcileTexts(r *iaReconcileObjectRequest) []*string {
	return []*string{
		&r.scope.runID, &r.scope.libraryKey, &r.source.id, &r.source.incarnation, &r.source.rootIdentity,
		&r.run.phase, &r.run.scanMode, &r.phase, &r.afterObjectID,
		&r.accepted.objectID, &r.accepted.relativePath, &r.accepted.objectIdentity, &r.accepted.revision,
		&r.current.objectID, &r.current.relativePath, &r.current.objectIdentity, &r.current.revision,
	}
}

func (c *iaReconcileObjectCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_reconcile_command")
	}
	checked, err := newIAReconcileObjectCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}

// One governed reconcile or original replay. Current owner authority is
// established BEFORE any retained owner/header read; fresh source, run and
// object witnesses are compared only after the receipt is Fresh. A saved result
// is decoded from the immutable receipt/child cohort, never a live row.
func acceptIAReconcileObject(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, r iaReconcileObjectRequest) (out iaReconcileAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAReconcileObject {
		return out, ErrInvalid
	}
	command, err := newIAReconcileObjectCommand(reference.Identity, r)
	if err != nil {
		return out, err
	}
	// The canonical request digest is recomputed by this writer. A caller's
	// mismatched digest is a conflicting original, never a fresh admission.
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	authority, err := authorizeIAEntryOwner(ctx, turn, p, lifetime, budget)
	if err != nil {
		return out, err
	}
	owner, header, err := iaReconcileReceiptFacts(ctx, turn, r.scope.runID, reference)
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(receiptIdentity{reference.Identity, reference.RequestDigest}, IAReconcileObject, owner, header.facts)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	switch decision {
	case receiptRetired:
		return iaReconcileAcceptOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIAReconcileObject(ctx, turn, authority, command, header)
		if err != nil {
			return out, err
		}
		return iaReconcileAcceptOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIAReconcileObject(ctx, turn, authority, command, reference)
	default:
		return out, ErrResolveRequired
	}
}

// Owner and header facts are decoded by finite points scoped to the exact
// original run/incarnation/sequence. No current source/lease/object row filters
// history; a kind mismatch is a conflict, never a fresh admission.
func iaReconcileReceiptFacts(ctx context.Context, turn *commandTurn, runID string, reference ReceiptReference) (owner receiptOwnerFacts, header iaReconcileSavedHeader, err error) {
	row, err := wc008DraftQuery(ctx, turn, iaReconcileOwner, runID)
	if err != nil {
		return owner, header, err
	}
	var incarnation []byte
	err = row.Scan(&incarnation, &owner.lastSequence, &owner.acknowledgedSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, header, nil
	}
	if err != nil {
		return owner, header, err
	}
	if len(incarnation) != 16 {
		return owner, header, ErrResolveRequired
	}
	owner.exists = true
	copy(owner.incarnation[:], incarnation)

	row, err = wc008DraftQuery(ctx, turn, iaReconcileHeader, runID, reference.Identity.Incarnation[:], reference.Identity.Sequence)
	if err != nil {
		return owner, header, err
	}
	var operation, request, digest []byte
	var kind string
	var finished int64
	err = row.Scan(&operation, &request, &kind, &header.facts.resultCount, &digest, &header.acceptedRevision, &header.appliedMS,
		&header.sourceID, &header.sourceIncarnation, &header.beforeKey, &header.afterKey, &header.phase,
		&header.authoritative, &header.processed, &header.analyzed, &header.warnings, &finished, &header.health)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, header, nil
	}
	if err != nil {
		return owner, header, err
	}
	if len(operation) != 16 || len(request) != 32 || len(digest) != 32 || finished != 0 {
		return owner, header, ErrResolveRequired
	}
	header.facts.exists = true
	header.facts.identity.command = reference.Identity
	copy(header.facts.identity.command.OperationID[:], operation)
	copy(header.facts.identity.digest[:], request)
	copy(header.facts.resultDigest[:], digest)
	if kind == iaReconcileReceiptKind {
		header.facts.kind = IAReconcileObject
	}
	return owner, header, nil
}

// Fresh effects are one complete unit: the exact single-object disposition, the
// run chronology advance and the typed receipt/child cohort. The catalogue,
// inventory object, asset and Basic rows are never mutated mid-walk; a changed
// witness is recorded so the producer can queue a new Basic pass, and IA-23/24
// gate any refresh that consumes it.
func applyIAReconcileObject(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaReconcileObjectCommand, reference ReceiptReference) (out iaReconcileAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	r := command.request
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return out, ErrInvalid
	}
	appliedMS := turn.transaction.writer.now().UnixMilli()
	if appliedMS < 0 || appliedMS >= r.run.leaseExpiresMS {
		return out, ErrConflict
	}
	if id, err := wc008DraftInteger(ctx, turn, iaReconcileLibrary, r.scope.libraryID, r.scope.libraryKey); err != nil || id != r.scope.libraryID {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := wc008DraftQuery(ctx, turn, iaReconcileSource, r.source.id, r.scope.libraryKey, r.source.generation,
		r.source.incarnation, r.source.rootIdentity, string(r.source.resolvedRoot), r.source.followSymlinks)
	if err != nil {
		return out, err
	}
	var sourceID, health string
	if err = row.Scan(&sourceID, &health); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if sourceID != r.source.id || !iaReconcileText(health, 64) {
		return out, ErrConflict
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	previous, err := iaReconcileCheckObject(ctx, turn, r)
	if err != nil {
		return out, err
	}
	disposition, err := iaReconcilePlanDisposition(ctx, turn, r, previous)
	if err != nil {
		return out, err
	}
	if disposition.kind != "replaced" && r.afterObjectID != "" {
		return out, ErrConflict
	}
	// The object read and planning are current-authority evidence too: a
	// same-turn revocation observed there still precedes every write.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	runRevision, err := iaReconcileAdvanceRun(ctx, turn, command.identity, r)
	if err != nil {
		return out, err
	}
	facts := iaReconcileFacts(r, disposition, runRevision, appliedMS, health)
	if err = preflightIAReconcileResult(command.identity, command.digest, facts); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaReconcileObjectResult{facts})
	if err != nil {
		return out, err
	}
	if err = iaReconcileWriteReceipt(ctx, turn, command.identity, command.digest, facts, digest); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaReconcileAcceptOutcome{decision: receiptFresh, result: iaReconcileObjectResult{facts}, digest: digest}, nil
}

// The retained row is an exact old-state CAS, independent of live source or
// lease facts. A changed identity, revision, size or mtime refuses the unit.
func iaReconcileCheckObject(ctx context.Context, turn *commandTurn, r iaReconcileObjectRequest) (out iaReconcileWitness, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaReconcileObject, r.accepted.objectID, r.source.id, r.accepted.relativePath)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.objectIdentity, &out.revision, &out.size, &out.modifiedNS); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	out.objectID, out.relativePath = r.accepted.objectID, r.accepted.relativePath
	if out != r.accepted || out.revision == "" || out.size < 0 {
		return out, ErrConflict
	}
	return out, nil
}

// The merged scan preparation owns the scheduling decision. The writer package
// cannot import scan (scan imports writer for its Basic preparation values), so
// this is a local re-proof of the same closed rules, not a caller-supplied plan:
// exact unchanged reuse, changed bytes, replacement on a changed native identity
// and a bounded unambiguous native move. No filename similarity is used.
func iaReconcilePlanDisposition(ctx context.Context, turn *commandTurn, r iaReconcileObjectRequest, previous iaReconcileWitness) (out iaReconcileDisposition, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if r.current.relativePath == r.accepted.relativePath {
		switch {
		case r.current == previous:
			return iaReconcileDisposition{kind: "unchanged"}, nil
		case r.current.size != previous.size || r.current.modifiedNS != previous.modifiedNS:
			if r.current.objectIdentity != "" && previous.objectIdentity != "" && r.current.objectIdentity != previous.objectIdentity {
				if r.afterObjectID == "" || r.afterObjectID == r.accepted.objectID {
					return out, ErrConflict
				}
				return iaReconcileDisposition{kind: "replaced", after: r.current, objectID: r.afterObjectID, prior: r.accepted.objectID}, nil
			}
			return iaReconcileDisposition{kind: "changed", after: r.current, prior: r.accepted.objectID}, nil
		default:
			// A remount can renumber inodes with unchanged content evidence. The
			// retained path is preserved and the new Basic vector is requested.
			return iaReconcileDisposition{kind: "changed", after: r.current, prior: r.accepted.objectID}, nil
		}
	}
	window, err := iaReconcileCandidates(ctx, turn, r)
	if err != nil {
		return out, err
	}
	if !window.complete || !r.moveWindowComplete {
		return out, ErrResolveRequired
	}
	var match *iaReconcileWitness
	for i := range window.candidates {
		candidate := window.candidates[i]
		if r.current.objectIdentity == "" || r.current.objectIdentity != candidate.objectIdentity ||
			r.current.relativePath == candidate.relativePath || r.current.size != candidate.size ||
			r.current.modifiedNS != candidate.modifiedNS || r.current.revision != candidate.revision {
			continue
		}
		if match != nil {
			return out, ErrConflict
		}
		match = &candidate
	}
	if match == nil {
		// A retained object cannot become a brand-new location through reconcile.
		return out, ErrConflict
	}
	if *match != r.accepted || r.afterObjectID != "" {
		return out, ErrConflict
	}
	if err = iaReconcileCheckMoveTarget(ctx, turn, r); err != nil {
		return out, err
	}
	return iaReconcileDisposition{kind: "moved", after: r.current, prior: r.accepted.objectID}, nil
}

type iaReconcileDisposition struct {
	kind, objectID, prior string
	after                 iaReconcileWitness
}

// The identity window is bounded and scoped to the exact source. Two other
// retained placements are the most the admitted shape can disambiguate; a third
// raw row proves the window is wider than the admitted shape.
type iaReconcileCandidateWindow struct {
	candidates []iaReconcileWitness
	complete   bool
}

func iaReconcileCandidates(ctx context.Context, turn *commandTurn, r iaReconcileObjectRequest) (out iaReconcileCandidateWindow, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if r.current.objectIdentity == "" {
		return out, ErrConflict
	}
	rows, err := turn.query(ctx, turn.steps[iaReconcileIdentityWindow].statementStep,
		r.source.id, r.current.objectIdentity, r.current.size, r.current.modifiedNS, int64(iaReconcileMaxCandidates))
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var candidate iaReconcileWitness
		if err = rows.Scan(&candidate.objectID, &candidate.relativePath, &candidate.objectIdentity,
			&candidate.revision, &candidate.size, &candidate.modifiedNS); err != nil {
			return out, err
		}
		if len(out.candidates) == iaReconcileMaxCandidates {
			return out, ErrConflict
		}
		out.candidates = append(out.candidates, candidate)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.complete = len(out.candidates) < iaReconcileMaxCandidates
	return out, nil
}

// A move may not land on a path another unretired object already owns. A
// retired placement is not a conflict; the unique live path index is the only
// containment witness used here.
func iaReconcileCheckMoveTarget(ctx context.Context, turn *commandTurn, r iaReconcileObjectRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaReconcilePathLive, r.source.id, r.current.relativePath)
	if err != nil {
		return err
	}
	var live string
	err = row.Scan(&live)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if live == r.accepted.objectID && r.current.relativePath == r.accepted.relativePath {
		return nil
	}
	return ErrConflict
}

// The accept consumes exactly one run sequence and one run revision. It leaves
// the ACK floor untouched: only a separate durable ACK retires this original.
func iaReconcileAdvanceRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, r iaReconcileObjectRequest) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if r.run.revision == math.MaxInt64 || identity.Sequence <= 0 || r.run.lastSequence != identity.Sequence-1 {
		return 0, ErrCap
	}
	row, err := wc008DraftQuery(ctx, turn, iaReconcileRunCAS, r.run.revision+1, identity.Sequence,
		r.scope.runID, identity.Incarnation[:], r.scope.libraryID, r.scope.libraryKey, string(r.source.resolvedRoot), r.source.followSymlinks,
		r.source.id, r.source.generation, r.source.incarnation, r.source.rootIdentity, r.run.policyRevision, r.run.phase, r.run.scanMode,
		r.run.leaseOwner, r.run.leaseGeneration, r.run.leaseExpiresMS, r.run.revision, r.run.lastSequence, r.run.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	var revision, sequence int64
	if err = row.Scan(&revision, &sequence); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if revision != r.run.revision+1 || sequence != identity.Sequence {
		return 0, ErrConflict
	}
	return revision, nil
}

// One exact typed disposition. An unchanged object has no child row at all, so
// it schedules zero probes and proves the zero-probe chronology by its count.
func iaReconcileFacts(r iaReconcileObjectRequest, d iaReconcileDisposition, runRevision, appliedMS int64, health string) iaReconcileSaved {
	facts := iaReconcileSaved{
		runID: r.scope.runID, sourceID: r.source.id, sourceIncarnation: r.source.incarnation,
		acceptedRevision: runRevision, appliedMS: appliedMS, phase: r.phase,
		authoritative: r.authoritative, processed: 1, analyzed: 0, warnings: 0, sourceHealth: health,
		beforeKey: r.accepted.objectID, afterKey: r.accepted.objectID, resultVersion: 1,
	}
	if d.kind == "unchanged" {
		return facts
	}
	after := d.after
	objectID := d.objectID
	if objectID == "" {
		objectID = r.accepted.objectID
	}
	facts.afterKey = objectID
	facts.analyzed = 1
	facts.members = []iaReconcileMember{{
		ordinal: 0, objectID: objectID, beforeRevision: r.accepted.revision, afterRevision: after.revision,
		beforePath: r.accepted.relativePath, afterPath: after.relativePath, disposition: d.kind,
	}}
	if d.kind == "replaced" {
		prior := d.prior
		facts.members[0].priorObjectID = &prior
	}
	facts.resultCount = int64(len(facts.members))
	return facts
}

func iaReconcileWriteReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, facts iaReconcileSaved, digest ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = wc008DraftChanged(ctx, turn, iaReconcileReceiptInsert, facts.runID, identity.Incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], iaReconcileReceiptKind, facts.acceptedRevision, facts.appliedMS,
		facts.sourceID, facts.sourceIncarnation, nil, nil, "", facts.resultCount, digest[:],
		facts.beforeKey, facts.afterKey, facts.phase, facts.authoritative, facts.processed, facts.analyzed, facts.warnings, int64(0), facts.sourceHealth); err != nil {
		return err
	}
	for _, m := range facts.members {
		if err = wc008DraftChanged(ctx, turn, iaReconcileMemberInsert, facts.runID, identity.Incarnation[:], identity.Sequence,
			iaReconcileReceiptKind, m.ordinal, m.objectID, iaReconcileNullableText(m.priorObjectID),
			m.beforeRevision, m.afterRevision, m.beforePath, m.afterPath, m.disposition); err != nil {
			return err
		}
	}
	return nil
}

func iaReconcileNullableText(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// Replay decodes only the immutable receipt and its complete typed child
// cohort. Current source, lease and object rows are never consulted.
func lookupIAReconcileObject(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaReconcileObjectCommand, header iaReconcileSavedHeader) (out iaReconcileObjectHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || authority == nil || command == nil || !command.valid ||
		!header.facts.exists || header.facts.kind != IAReconcileObject || header.facts.identity != (receiptIdentity{command.identity, command.digest}) ||
		header.facts.resultCount < 0 || header.facts.resultCount > 1 || header.acceptedRevision <= 0 || header.appliedMS < 0 ||
		header.sourceID == "" || header.sourceIncarnation == "" || header.beforeKey == "" || header.afterKey == "" ||
		header.phase != "reconciling" || (header.authoritative != 0 && header.authoritative != 1) ||
		header.processed < 0 || header.analyzed < 0 || header.warnings < 0 || !iaReconcileText(header.health, 64) {
		return out, ErrResolveRequired
	}
	limit := header.facts.resultCount + 1
	rows, err := turn.query(ctx, turn.steps[iaReconcileMembers].statementStep, command.request.scope.runID,
		command.identity.Incarnation[:], command.identity.Sequence, limit)
	if err != nil {
		return out, err
	}
	facts := iaReconcileSaved{
		runID: command.request.scope.runID, sourceID: header.sourceID, sourceIncarnation: header.sourceIncarnation,
		acceptedRevision: header.acceptedRevision, appliedMS: header.appliedMS, phase: header.phase,
		authoritative: header.authoritative, processed: header.processed, analyzed: header.analyzed, warnings: header.warnings,
		sourceHealth: header.health, beforeKey: header.beforeKey, afterKey: header.afterKey, resultVersion: 1,
	}
	var bytes int64
	readErr := func() error {
		for rows.Next() {
			var m iaReconcileMember
			var prior sql.NullString
			if err := rows.Scan(&m.ordinal, &m.objectID, &prior, &m.beforeRevision, &m.afterRevision, &m.beforePath, &m.afterPath, &m.disposition); err != nil {
				return err
			}
			if m.ordinal != int64(len(facts.members)) || int64(len(facts.members)) >= header.facts.resultCount {
				return ErrResolveRequired
			}
			if prior.Valid {
				v := prior.String
				m.priorObjectID = &v
			}
			n, err := iaReconcileMemberBytes(facts.runID, m)
			if err != nil || n > math.MaxInt64-bytes {
				return ErrResolveRequired
			}
			bytes += n
			facts.members = append(facts.members, m)
		}
		return rows.Err()
	}()
	if err := errors.Join(readErr, rows.Close()); err != nil {
		return out, err
	}
	if int64(len(facts.members)) != header.facts.resultCount {
		return out, ErrResolveRequired
	}
	facts.resultCount = int64(len(facts.members))
	if err = authority.budget.check(ctx, turn, bytes); err != nil {
		return out, err
	}
	if err = preflightIAReconcileResult(command.identity, command.digest, facts); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaReconcileObjectResult{facts})
	if err != nil || !equalBytes(digest[:], header.facts.resultDigest[:]) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	return iaReconcileObjectHistory{decision: receiptReplay, result: iaReconcileObjectResult{facts}, digest: digest}, nil
}

type iaReconcileObjectResult struct{ facts iaReconcileSaved }

type iaReconcileSaved struct {
	runID, sourceID, sourceIncarnation           string
	acceptedRevision, appliedMS                  int64
	phase, beforeKey, afterKey, sourceHealth     string
	authoritative, processed, analyzed, warnings int64
	resultVersion, resultCount                   int64
	members                                      []iaReconcileMember
}

type iaReconcileMember struct {
	ordinal                                              int64
	objectID                                             string
	priorObjectID                                        *string
	beforeRevision, afterRevision, beforePath, afterPath string
	disposition                                          string
}

// The private field walk fixes v1 field order. One reconcile command persists
// at most one exact disposition; an unchanged object persists none.
func iaReconcileFields(id CommandIdentity, r RequestDigest, v iaReconcileSaved, f func(any)) {
	f([]byte(canonicalResultCodec))
	f([]byte{0, 1})
	f(iaReconcileReceiptKind)
	f("single_object")
	f("applied")
	f(id.Incarnation[:])
	f(int64(id.Sequence))
	f(id.OperationID[:])
	f(r[:])
	f(v.runID)
	f(v.sourceID)
	f(v.sourceIncarnation)
	f(v.acceptedRevision)
	f(v.appliedMS)
	f(v.phase)
	f(v.authoritative)
	f(v.processed)
	f(v.analyzed)
	f(v.warnings)
	f(v.sourceHealth)
	f(v.beforeKey)
	f(v.afterKey)
	f(v.resultVersion)
	f(v.resultCount)
	f(int64(len(v.members)))
	for _, m := range v.members {
		f(m.ordinal)
		f(m.objectID)
		f(m.priorObjectID)
		f(m.beforeRevision)
		f(m.afterRevision)
		f(m.beforePath)
		f(m.afterPath)
		f(m.disposition)
	}
}

func preflightIAReconcileResult(id CommandIdentity, r RequestDigest, v iaReconcileSaved) error {
	if id.Sequence <= 0 || id.Incarnation == ([16]byte{}) || id.OperationID == ([16]byte{}) ||
		v.runID == "" || v.sourceID == "" || v.sourceIncarnation == "" || v.acceptedRevision <= 0 || v.appliedMS < 0 ||
		v.phase != "reconciling" || (v.authoritative != 0 && v.authoritative != 1) ||
		v.processed < 0 || v.analyzed < 0 || v.warnings < 0 || !iaReconcileText(v.sourceHealth, 64) ||
		!iaReconcileText(v.beforeKey, iaReconcileMaxKeyBytes) || !iaReconcileText(v.afterKey, iaReconcileMaxKeyBytes) ||
		v.resultVersion != 1 || v.resultCount != int64(len(v.members)) || v.resultCount < 0 || v.resultCount > 1 {
		return invalid("ia_reconcile_result_header")
	}
	remaining := uint64(maxCanonicalResultBytes)
	tooBig := false
	iaReconcileFields(id, r, v, func(x any) {
		var n uint64
		switch p := x.(type) {
		case string:
			n = 8 + uint64(len(p))
		case []byte:
			n = uint64(len(p))
		case int64:
			n = 8
		case *string:
			n = 1
			if p != nil {
				n += 8 + uint64(len(*p))
			}
		}
		if n > remaining {
			tooBig = true
		} else {
			remaining -= n
		}
	})
	if tooBig {
		return invalid("ia_reconcile_result_bytes")
	}
	if v.resultCount == 0 {
		if v.analyzed != 0 || v.beforeKey != v.afterKey {
			return invalid("ia_reconcile_unchanged")
		}
		return nil
	}
	if v.analyzed != 1 {
		return invalid("ia_reconcile_analyzed")
	}
	m := v.members[0]
	if m.ordinal != 0 || !iaReconcileText(m.objectID, iaReconcileMaxKeyBytes) ||
		!iaReconcileText(m.beforeRevision, iaReconcileMaxKeyBytes) || !iaReconcileText(m.afterRevision, iaReconcileMaxKeyBytes) ||
		!inventoryPath(m.beforePath, false) || !inventoryPath(m.afterPath, false) {
		return invalid("ia_reconcile_result_member")
	}
	prior := m.priorObjectID != nil
	switch m.disposition {
	case "changed":
		if prior || m.beforePath != m.afterPath {
			return invalid("ia_reconcile_changed")
		}
	case "moved":
		if prior || m.beforePath == m.afterPath || m.beforeRevision != m.afterRevision {
			return invalid("ia_reconcile_moved")
		}
	case "replaced":
		if !prior || m.beforePath != m.afterPath ||
			!iaReconcileText(*m.priorObjectID, iaReconcileMaxKeyBytes) || *m.priorObjectID == m.objectID || v.afterKey != m.objectID {
			return invalid("ia_reconcile_replaced")
		}
	default:
		return invalid("ia_reconcile_disposition")
	}
	if v.beforeKey != *reconcilePriorOrSelf(m) {
		return invalid("ia_reconcile_before_key")
	}
	if v.afterKey != m.objectID {
		return invalid("ia_reconcile_after_key")
	}
	var valid = true
	iaReconcileFields(id, r, v, func(x any) {
		switch p := x.(type) {
		case string:
			valid = valid && utf8.ValidString(p)
		case *string:
			if p != nil {
				valid = valid && utf8.ValidString(*p)
			}
		}
	})
	if !valid {
		return invalid("ia_reconcile_result_text")
	}
	return nil
}

func reconcilePriorOrSelf(m iaReconcileMember) *string {
	if m.priorObjectID != nil {
		return m.priorObjectID
	}
	v := m.objectID
	return &v
}

// Exact generated native scalar charge for one child row, independently of wire
// limits. The child has no stored byte ledger, so replay recomputes it from the
// immutable values it decoded.
func iaReconcileMemberBytes(runID string, m iaReconcileMember) (int64, error) {
	n := int64(64)
	add := func(v int) bool {
		if uint64(v) > uint64(math.MaxInt64-n) {
			return false
		}
		n += int64(v)
		return true
	}
	for _, s := range []string{runID, iaReconcileReceiptKind, m.objectID, m.beforeRevision, m.afterRevision, m.beforePath, m.afterPath, m.disposition} {
		if !add(len(s)) {
			return 0, invalid("ia_reconcile_retained_bytes")
		}
	}
	if m.priorObjectID != nil && !add(len(*m.priorObjectID)) {
		return 0, invalid("ia_reconcile_retained_bytes")
	}
	return n, nil
}

// Caller must preflight the complete result before invoking this writer.
func writeIAReconcileResult(e *requestEncoder, id CommandIdentity, r RequestDigest, v iaReconcileSaved) error {
	iaReconcileFields(id, r, v, func(x any) {
		switch p := x.(type) {
		case string:
			e.text(p)
		case []byte:
			e.bytes(p)
		case int64:
			e.number(uint64(p))
		case *string:
			e.flag(p != nil)
			if p != nil {
				e.text(*p)
			}
		}
	})
	return e.err
}
