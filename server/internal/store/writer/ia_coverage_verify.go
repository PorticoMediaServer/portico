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

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const iaCoverageVerifyRequestVersion uint16 = 1

const (
	iaCoverageMaxKeyBytes  = 512
	iaCoverageMaxPathBytes = 16384
)

// iaCoveragePinsUnresolved is the named seam for the MP-10 source pins, which are
// not built yet. Until MP publishes resolved pin evidence, an unresolved pin can
// never let a coverage proof authorize absence: the fresh turn refuses instead of
// widening the window. The future MP integration replaces this prepared boolean
// with the exact resolved pin set; no provider or pin SQL is invented here.
var iaCoveragePinsUnresolved = errors.New("ia coverage: MP-10 source pins unresolved")

// IA-18 verifies one complete sealed directory subtree before any reconciliation
// may infer absence. Its private outcome and history carry the exact typed result;
// no public facade, worker or producer is wired, so the accepting body stays
// uncalled outside this package.
type iaCoverageAcceptOutcome struct {
	decision receiptDecision
	result   iaCoverageVerifyResult
	digest   ResultDigest
}
type iaCoverageVerifyHistory struct {
	decision receiptDecision
	result   iaCoverageVerifyResult
	digest   ResultDigest
}

// Same-turn saved header, never a disclosure capability. Coverage has no child
// cohort; the exact directory seal and the raw/filtered progress are the whole
// persisted result.
type iaCoverageSavedHeader struct {
	facts                              receiptFacts
	acceptedRevision, appliedMS        int64
	sourceID, sourceIncarnation        string
	beforeKey, afterKey, phase, health string
	authoritative, processed, analyzed int64
	warnings                           int64
	directory, identity, revision      string
}

// Prepared witnesses only. The sealed directory and the observed progress are
// what the caller believes its traversal proved; a fresh turn re-reads the live
// directory, frontier and protected issues before any coverage is recorded.
type iaCoverageVerifyRequest struct {
	scope  inventoryScope
	source inventorySource
	run    inventoryRun

	directory iaCoverageWitness

	// Raw progress is the complete observed listing: every raw entry is either
	// accepted, filtered or refused. A refused entry can never authorize absence.
	raw, filtered, accepted, refused int64

	// Completeness is evidence, never permission: an incomplete or unstable
	// observation refuses coverage instead of inferring an empty window.
	subtreeComplete, rawComplete, stable bool

	// pinsResolved is the prepared MP-10 seam. False refuses coverage rather than
	// treating an unresolved source pin as an empty or absent window.
	pinsResolved bool

	phase         string
	authoritative int64
}

type iaCoverageWitness struct {
	relativePath, identity, revision, cursor, state string
	pages, skipped                                  int64
}

type iaCoverageVerifyCommand struct {
	identity CommandIdentity
	request  iaCoverageVerifyRequest
	digest   RequestDigest
	valid    bool
}

func newIACoverageVerifyCommand(id CommandIdentity, r iaCoverageVerifyRequest) (*iaCoverageVerifyCommand, error) {
	// Counts precede all text traversal, copying and hashing.
	if !iaEpisodeCounts(reflect.ValueOf(r)) {
		return nil, invalid("ia_coverage_count")
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaEpisodeWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_coverage_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !iaEpisodeValidText(reflect.ValueOf(r)) || !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return nil, invalid("ia_coverage_text")
	}
	if err := validateIACoverageOriginal(id, r); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IACoverageVerify, iaCoverageVerifyRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	r.phase = strings.Clone(r.phase)
	for _, s := range iaCoverageTexts(&r) {
		*s = strings.Clone(*s)
	}
	return &iaCoverageVerifyCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

// Every accepting gate is a closed witness comparison. The traversal-complete or
// EOF receipt is never an alias for a sealed directory: only an exact done row
// with its identity, revision and cursor can prove coverage.
func validateIACoverageOriginal(id CommandIdentity, r iaCoverageVerifyRequest) error {
	s, u, d := r.source, r.run, r.directory
	if id.Sequence <= 0 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) ||
		r.scope.runID == "" || r.scope.libraryKey == "" || r.scope.libraryID <= 0 ||
		s.id == "" || s.incarnation == "" || s.rootIdentity == "" || s.generation <= 0 || len(s.resolvedRoot) == 0 || s.resolvedRoot[0] != '/' ||
		u.revision <= 0 || u.policyRevision <= 0 || u.leaseOwner <= 0 || u.leaseGeneration <= 0 || u.leaseExpiresMS <= 0 ||
		u.lastSequence != id.Sequence-1 || u.acknowledgedSequence != u.lastSequence || u.phase != "verifying" || !inventoryScanMode(u.scanMode) {
		return invalid("ia_coverage_original")
	}
	root := string(s.resolvedRoot)
	if strings.ContainsAny(root, `\`) || path.Clean(root) != root {
		return invalid("ia_coverage_root")
	}
	if r.phase != "reconciling" || (r.authoritative != 0 && r.authoritative != 1) {
		return invalid("ia_coverage_phase")
	}
	if !inventoryPath(d.relativePath, true) || !iaCoverageText(d.identity, iaCoverageMaxKeyBytes) ||
		!iaCoverageText(d.revision, iaCoverageMaxKeyBytes) || !iaCoverageTextAllowEmpty(d.cursor, iaCoverageMaxKeyBytes) ||
		d.state != "done" || d.pages < 0 || d.skipped < 0 {
		return invalid("ia_coverage_directory")
	}
	if r.raw < 0 || r.filtered < 0 || r.accepted < 0 || r.refused < 0 ||
		r.accepted > r.raw || r.raw-r.accepted < r.filtered || r.raw-r.accepted-r.filtered != r.refused {
		return invalid("ia_coverage_progress")
	}
	return nil
}

func iaCoverageText(value string, limit int) bool {
	return len(value) > 0 && iaCoverageTextAllowEmpty(value, limit)
}

func iaCoverageTextAllowEmpty(value string, limit int) bool {
	if len(value) > limit || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func iaCoverageTexts(r *iaCoverageVerifyRequest) []*string {
	return []*string{
		&r.scope.runID, &r.scope.libraryKey, &r.source.id, &r.source.incarnation, &r.source.rootIdentity,
		&r.run.phase, &r.run.scanMode, &r.phase,
		&r.directory.relativePath, &r.directory.identity, &r.directory.revision, &r.directory.cursor, &r.directory.state,
	}
}

func (c *iaCoverageVerifyCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_coverage_command")
	}
	checked, err := newIACoverageVerifyCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}

// One governed coverage verify or original replay. Current owner authority is
// established BEFORE any retained owner/header read; fresh source, run, directory
// and frontier witnesses are compared only after the receipt is Fresh. A saved
// result is decoded from the immutable receipt, never a live row.
func acceptIACoverageVerify(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, r iaCoverageVerifyRequest) (out iaCoverageAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IACoverageVerify {
		return out, ErrInvalid
	}
	command, err := newIACoverageVerifyCommand(reference.Identity, r)
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
	owner, header, err := iaCoverageReceiptFacts(ctx, turn, r.scope.runID, reference)
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(receiptIdentity{reference.Identity, reference.RequestDigest}, IACoverageVerify, owner, header.facts)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	switch decision {
	case receiptRetired:
		return iaCoverageAcceptOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIACoverageVerify(ctx, turn, authority, command, header)
		if err != nil {
			return out, err
		}
		return iaCoverageAcceptOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIACoverageVerify(ctx, turn, authority, command, reference)
	default:
		return out, ErrResolveRequired
	}
}

// Owner and header facts are decoded by finite points scoped to the exact
// original run/incarnation/sequence. No current source/lease/directory row filters
// history; a kind mismatch is a conflict, never a fresh admission.
func iaCoverageReceiptFacts(ctx context.Context, turn *commandTurn, runID string, reference ReceiptReference) (owner receiptOwnerFacts, header iaCoverageSavedHeader, err error) {
	row, err := wc008DraftQuery(ctx, turn, iaCoverageOwner, runID)
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

	row, err = wc008DraftQuery(ctx, turn, iaCoverageHeader, runID, reference.Identity.Incarnation[:], reference.Identity.Sequence)
	if err != nil {
		return owner, header, err
	}
	var operation, request, digest []byte
	var kind string
	var finished int64
	var directory sql.NullString
	var identity, revision []byte
	err = row.Scan(&operation, &request, &kind, &header.facts.resultCount, &digest, &header.acceptedRevision, &header.appliedMS,
		&header.sourceID, &header.sourceIncarnation, &header.beforeKey, &header.afterKey, &header.phase,
		&header.authoritative, &header.processed, &header.analyzed, &header.warnings, &finished, &header.health,
		&directory, &identity, &revision)
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
	if kind == iaCoverageReceiptKind {
		if !directory.Valid || len(identity) == 0 || len(revision) == 0 {
			return owner, header, ErrResolveRequired
		}
		header.facts.kind = IACoverageVerify
		header.directory, header.identity, header.revision = directory.String, string(identity), string(revision)
	}
	return owner, header, nil
}

// Fresh effects are one complete unit: the exact sealed directory proof, the
// partial/unreadable/refused frontier refusals and the typed receipt. No object,
// catalogue or Basic row is mutated; coverage only authorizes a later reconcile.
func applyIACoverageVerify(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaCoverageVerifyCommand, reference ReceiptReference) (out iaCoverageAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	r := command.request
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return out, ErrInvalid
	}
	appliedMS := turn.transaction.writer.now().UnixMilli()
	if appliedMS < 0 || appliedMS >= r.run.leaseExpiresMS {
		return out, ErrConflict
	}
	if id, err := wc008DraftInteger(ctx, turn, iaCoverageLibrary, r.scope.libraryID, r.scope.libraryKey); err != nil || id != r.scope.libraryID {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := wc008DraftQuery(ctx, turn, iaCoverageSource, r.source.id, r.scope.libraryKey, r.source.generation,
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
	if sourceID != r.source.id || !iaCoverageText(health, 64) {
		return out, ErrConflict
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	// Incomplete or unstable observations can never imply absence, and a refused
	// entry is not a complete stable window.
	if !r.subtreeComplete || !r.rawComplete || !r.stable {
		return out, ErrResolveRequired
	}
	// MP-10 is not built: an unresolved source pin refuses absence rather than
	// widening the observation window.
	if !r.pinsResolved {
		return out, errors.Join(ErrResolveRequired, iaCoveragePinsUnresolved)
	}
	if r.refused != 0 {
		return out, ErrConflict
	}
	if err = iaCoverageCheckSeal(ctx, turn, r); err != nil {
		return out, err
	}
	if err = iaCoverageCheckFrontier(ctx, turn, r); err != nil {
		return out, err
	}
	// The reads above are current-authority evidence too: a same-turn revocation
	// observed there still precedes every write.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	if r.run.revision == math.MaxInt64 {
		return out, ErrCap
	}
	runRevision, err := iaCoverageAdvanceRun(ctx, turn, command.identity, r)
	if err != nil {
		return out, err
	}
	facts := iaCoverageFacts(r, runRevision, appliedMS, health)
	if err = preflightIACoverageVerifyResult(command.identity, command.digest, facts); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaCoverageVerifyResult{facts})
	if err != nil {
		return out, err
	}
	if err = iaCoverageWriteReceipt(ctx, turn, command.identity, command.digest, facts, digest); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaCoverageAcceptOutcome{decision: receiptFresh, result: iaCoverageVerifyResult{facts}, digest: digest}, nil
}

// The exact directory seal is the CAS: identity, revision, cursor, state and the
// page/skip counters must all still equal the prepared observation. A changed
// directory is unstable, never an empty or absent subtree.
func iaCoverageCheckSeal(ctx context.Context, turn *commandTurn, r iaCoverageVerifyRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaCoverageSeal, r.scope.runID, r.directory.relativePath)
	if err != nil {
		return err
	}
	var got iaCoverageWitness
	got.relativePath = r.directory.relativePath
	if err = row.Scan(&got.identity, &got.revision, &got.cursor, &got.state, &got.pages, &got.skipped); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	if got != r.directory || got.state != "done" {
		return ErrConflict
	}
	return nil
}

// Durable frontier evidence: no descendant directory may still be pending
// (partial) or blocked (unreadable), and no protected issue may sit in the
// subtree (refused). The exact directory row itself is sealed above.
func iaCoverageCheckFrontier(ctx context.Context, turn *commandTurn, r iaCoverageVerifyRequest) error {
	low, high := iaCoverageBounds(r.directory.relativePath)
	for _, check := range []struct {
		id stmt.ID
	}{
		{iaCoveragePending},
		{iaCoverageBlocked},
		{iaCoverageProtected},
	} {
		found, err := iaCoverageExists(ctx, turn, check.id, r.scope.runID, low, high)
		if err != nil {
			return err
		}
		if found {
			return ErrConflict
		}
	}
	return nil
}

func iaCoverageExists(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (found bool, err error) {
	defer func() { found, err = finishCommand(ctx, commandLifetime(turn), found, err) }()
	rows, err := turn.query(ctx, turn.steps[id].statementStep, args...)
	if err != nil {
		return false, err
	}
	found = rows.Next()
	return found, errors.Join(rows.Err(), rows.Close())
}

// Half-open native path bounds select only descendants of exactly this
// directory. UTF-8 cannot produce 0xff, so the root window stays complete without
// a LIKE pattern or a temporary sort.
func iaCoverageBounds(directory string) (low, high string) {
	if directory == "." {
		return "", "\xff"
	}
	return directory + "/", directory + "0"
}

// The accept consumes exactly one run sequence and one run revision, and moves
// the run from verifying to reconciling. It leaves the ACK floor untouched: only
// a separate durable ACK retires this original.
func iaCoverageAdvanceRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, r iaCoverageVerifyRequest) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if r.run.revision == math.MaxInt64 || identity.Sequence <= 0 || r.run.lastSequence != identity.Sequence-1 {
		return 0, ErrCap
	}
	row, err := wc008DraftQuery(ctx, turn, iaCoverageRunCAS, r.run.revision+1, identity.Sequence,
		r.scope.runID, identity.Incarnation[:], r.scope.libraryID, r.scope.libraryKey, string(r.source.resolvedRoot), r.source.followSymlinks,
		r.source.id, r.source.generation, r.source.incarnation, r.source.rootIdentity, r.run.policyRevision, r.run.scanMode,
		r.run.leaseOwner, r.run.leaseGeneration, r.run.leaseExpiresMS, r.run.revision, r.run.lastSequence, r.run.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	var revision, sequence int64
	var phase string
	if err = row.Scan(&revision, &sequence, &phase); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if revision != r.run.revision+1 || sequence != identity.Sequence || phase != "reconciling" {
		return 0, ErrConflict
	}
	return revision, nil
}

// One exact sealed directory and the complete raw progress. Every raw entry is
// accounted for as accepted or filtered; refused entries never reach this result.
func iaCoverageFacts(r iaCoverageVerifyRequest, runRevision, appliedMS int64, health string) iaCoverageSaved {
	return iaCoverageSaved{
		runID: r.scope.runID, sourceID: r.source.id, sourceIncarnation: r.source.incarnation,
		acceptedRevision: runRevision, appliedMS: appliedMS, phase: r.phase,
		authoritative: r.authoritative, processed: r.raw, analyzed: r.accepted, warnings: r.filtered,
		sourceHealth: health, directory: r.directory.relativePath, identity: r.directory.identity,
		revision: r.directory.revision, resultVersion: 1,
	}
}

func iaCoverageWriteReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, facts iaCoverageSaved, digest ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	return wc008DraftChanged(ctx, turn, iaCoverageReceiptInsert, facts.runID, identity.Incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], iaCoverageReceiptKind, facts.acceptedRevision, facts.appliedMS,
		facts.sourceID, facts.sourceIncarnation, nil, nil, "", int64(0), digest[:],
		facts.directory, facts.directory, facts.phase, facts.authoritative, facts.processed, facts.analyzed, facts.warnings, int64(0), facts.sourceHealth,
		facts.directory, []byte(facts.identity), []byte(facts.revision))
}

// Replay decodes only the immutable receipt. Current source, lease, directory and
// frontier rows are never consulted.
func lookupIACoverageVerify(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaCoverageVerifyCommand, header iaCoverageSavedHeader) (out iaCoverageVerifyHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || authority == nil || command == nil || !command.valid ||
		!header.facts.exists || header.facts.kind != IACoverageVerify || header.facts.identity != (receiptIdentity{command.identity, command.digest}) ||
		header.facts.resultCount != 0 || header.acceptedRevision <= 0 || header.appliedMS < 0 ||
		header.sourceID == "" || header.sourceIncarnation == "" || header.beforeKey == "" || header.afterKey == "" ||
		header.beforeKey != header.afterKey || header.beforeKey != header.directory ||
		header.phase != "reconciling" || (header.authoritative != 0 && header.authoritative != 1) ||
		header.processed < 0 || header.analyzed < 0 || header.warnings < 0 || header.analyzed > header.processed ||
		header.processed-header.analyzed != header.warnings || !iaCoverageText(header.health, 64) ||
		!inventoryPath(header.directory, true) || !iaCoverageText(header.identity, iaCoverageMaxKeyBytes) ||
		!iaCoverageText(header.revision, iaCoverageMaxKeyBytes) {
		return out, ErrResolveRequired
	}
	facts := iaCoverageSaved{
		runID: command.request.scope.runID, sourceID: header.sourceID, sourceIncarnation: header.sourceIncarnation,
		acceptedRevision: header.acceptedRevision, appliedMS: header.appliedMS, phase: header.phase,
		authoritative: header.authoritative, processed: header.processed, analyzed: header.analyzed, warnings: header.warnings,
		sourceHealth: header.health, directory: header.directory, identity: header.identity, revision: header.revision, resultVersion: 1,
	}
	bytes := int64(len(header.directory) + len(header.identity) + len(header.revision) + len(header.sourceID) + len(header.sourceIncarnation))
	if err = authority.budget.check(ctx, turn, bytes); err != nil {
		return out, err
	}
	if err = preflightIACoverageVerifyResult(command.identity, command.digest, facts); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaCoverageVerifyResult{facts})
	if err != nil || !equalBytes(digest[:], header.facts.resultDigest[:]) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	return iaCoverageVerifyHistory{decision: receiptReplay, result: iaCoverageVerifyResult{facts}, digest: digest}, nil
}

type iaCoverageVerifyResult struct{ facts iaCoverageSaved }

type iaCoverageSaved struct {
	runID, sourceID, sourceIncarnation           string
	acceptedRevision, appliedMS                  int64
	phase, sourceHealth                          string
	authoritative, processed, analyzed, warnings int64
	directory, identity, revision                string
	resultVersion                                int64
}

// The private field walk fixes v1 field order. Coverage persists no child cohort;
// result_count is always zero.
func iaCoverageFields(id CommandIdentity, r RequestDigest, v iaCoverageSaved, f func(any)) {
	f([]byte(canonicalResultCodec))
	f([]byte{0, 1})
	f(iaCoverageReceiptKind)
	f("coverage")
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
	f(v.directory)
	f(v.identity)
	f(v.revision)
	f(v.resultVersion)
	f(int64(0))
}

func preflightIACoverageVerifyResult(id CommandIdentity, r RequestDigest, v iaCoverageSaved) error {
	if id.Sequence <= 0 || id.Incarnation == ([16]byte{}) || id.OperationID == ([16]byte{}) ||
		v.runID == "" || v.sourceID == "" || v.sourceIncarnation == "" || v.acceptedRevision <= 0 || v.appliedMS < 0 ||
		v.phase != "reconciling" || (v.authoritative != 0 && v.authoritative != 1) ||
		v.processed < 0 || v.analyzed < 0 || v.warnings < 0 || v.analyzed > v.processed || v.processed-v.analyzed != v.warnings ||
		!iaCoverageText(v.sourceHealth, 64) || !inventoryPath(v.directory, true) ||
		!iaCoverageText(v.identity, iaCoverageMaxKeyBytes) || !iaCoverageText(v.revision, iaCoverageMaxKeyBytes) ||
		v.resultVersion != 1 {
		return invalid("ia_coverage_result_header")
	}
	remaining := uint64(maxCanonicalResultBytes)
	tooBig := false
	iaCoverageFields(id, r, v, func(x any) {
		var n uint64
		switch p := x.(type) {
		case string:
			n = 8 + uint64(len(p))
		case []byte:
			n = uint64(len(p))
		case int64:
			n = 8
		}
		if n > remaining {
			tooBig = true
		} else {
			remaining -= n
		}
	})
	if tooBig {
		return invalid("ia_coverage_result_bytes")
	}
	var valid = true
	iaCoverageFields(id, r, v, func(x any) {
		if p, ok := x.(string); ok {
			valid = valid && utf8.ValidString(p)
		}
	})
	if !valid {
		return invalid("ia_coverage_result_text")
	}
	return nil
}

// Caller must preflight the complete result before invoking this writer.
func writeIACoverageVerifyResult(e *requestEncoder, id CommandIdentity, r RequestDigest, v iaCoverageSaved) error {
	iaCoverageFields(id, r, v, func(x any) {
		switch p := x.(type) {
		case string:
			e.text(p)
		case []byte:
			e.bytes(p)
		case int64:
			e.number(uint64(p))
		}
	})
	return e.err
}
