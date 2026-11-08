package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Caller-prepared source witnesses and the exact desired state of ONE existing
// source. The witnesses are the caller's own observation of the live row and of
// the root; the command never opens, stats or re-reads the filesystem, a
// provider or a tuner.
type iaConfigureSourceRequest struct {
	reference  ReceiptReference
	libraryID  int64
	libraryKey string
	sourceID   string

	// Exact current row witnesses. A mismatch is a conflict, not a retry.
	expectIncarnation  string
	expectGeneration   int64
	expectRoot         string
	expectRootIdentity string

	// Desired post-command facts. The incarnation changes only with the root.
	incarnation     string
	name            string
	configuredRoot  string
	resolvedRoot    string
	rootIdentity    string
	kind            string
	classification  string
	followSymlinks  bool
	intervalSeconds int64
	enabled         bool
}

// Live scheduling/state facts read after the CAS, before the write. They are
// snapshotted into the immutable cohort so a replay never consults live rows.
type iaConfigureSourceState struct {
	enabled           bool
	identityConfirmed bool
	nextScanAt        string
	lastCompleteAt    string
	lastProgressAt    string
}

// Private same-turn protocol value, not a public ingress result. Fresh carries
// the exact typed result and digest this command wrote; replay carries the
// decoded original; retired carries no result.
type iaConfigureSourceOutcome struct {
	decision receiptDecision
	result   iaConfigureSourceResult
	digest   ResultDigest
}

type iaConfigureSourceHistory struct {
	decision receiptDecision
	result   iaConfigureSourceResult
	digest   ResultDigest
}

// A denial (disable) revokes current access, so it may only run on the explicit
// FULL descriptor. Every other configure action is NORMAL. Durability is fixed
// by the compiled purpose, never by the caller.
func iaConfigureSourceDenial(q iaConfigureSourceRequest) bool { return !q.enabled }

// Complete validation precedes EVERY write. A root move must present a new
// incarnation and a new root-identity observation; a same-root change must
// present the exact current ones. Reusing an identity across a root move (or
// inventing a new one for an unchanged root) is a stale IO witness and refuses.
func validateIAConfigureSourceRequest(q iaConfigureSourceRequest) error {
	if q.libraryID <= 0 || !iaConfigureLibraryText(q.libraryKey, 128) || !iaConfigureLibraryText(q.sourceID, 128) ||
		!iaConfigureLibraryText(q.expectIncarnation, 128) || q.expectGeneration <= 0 || q.expectGeneration == math.MaxInt64 ||
		!iaConfigureLibraryText(q.expectRoot, iaConfigureLibraryMaxRoot) || !iaConfigureLibraryText(q.expectRootIdentity, 256) ||
		!iaConfigureLibraryText(q.incarnation, 128) || !iaConfigureLibraryText(q.name, 100) || strings.TrimSpace(q.name) != q.name ||
		!iaConfigureLibraryText(q.configuredRoot, iaConfigureLibraryMaxRoot) || !iaConfigureLibraryText(q.resolvedRoot, iaConfigureLibraryMaxRoot) ||
		!iaConfigureLibraryText(q.rootIdentity, 256) ||
		(q.kind != "local" && q.kind != "remote") || (q.classification != "local" && q.classification != "network") ||
		q.intervalSeconds < 0 || q.intervalSeconds > 0 && q.intervalSeconds < 900 || q.kind == "remote" && q.followSymlinks {
		return ErrInvalid
	}
	ref := q.reference
	if ref.Identity.Incarnation == [16]byte{} || ref.Identity.OperationID == [16]byte{} || ref.Identity.Sequence != 1 || ref.RequestDigest == [32]byte{} {
		return ErrInvalid
	}
	// A local root becomes the live source root AND its rescan intent's native
	// root, so it must already be one native absolute, clean path. Remote roots
	// are opaque provider locations.
	if !iaConfigureLibraryRoot(q.configuredRoot, q.kind) || !iaConfigureLibraryRoot(q.resolvedRoot, q.kind) {
		return ErrInvalid
	}
	for _, text := range []string{q.name, q.expectRoot, q.expectRootIdentity} {
		if strings.IndexFunc(text, unicode.IsControl) >= 0 {
			return ErrInvalid
		}
	}
	rootMoved := q.resolvedRoot != q.expectRoot
	if rootMoved != (q.incarnation != q.expectIncarnation) {
		return ErrInvalid
	}
	if rootMoved != (q.rootIdentity != q.expectRootIdentity) {
		return ErrConflict
	}
	total := len(q.libraryKey) + len(q.sourceID) + len(q.expectIncarnation) + len(q.expectRoot) + len(q.expectRootIdentity) +
		len(q.incarnation) + len(q.name) + len(q.configuredRoot) + len(q.resolvedRoot) + len(q.rootIdentity) + len(q.kind) + len(q.classification)
	if total > maxCanonicalRequestBytes {
		return ErrInvalid
	}
	return nil
}

// One governed IA source configure, disable or re-enable birth, or an original
// replay. Current owner authority and the original classification come from the
// shared IA-05 entry; no fresh receipt is born from a saved, retired or foreign
// original.
func configureIASource(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, q iaConfigureSourceRequest) (out iaConfigureSourceOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil {
		return out, ErrInvalid
	}
	switch turn.transaction.descriptor.ID {
	case IAConfigureSource, IAConfigureSourceDisable:
	default:
		return out, ErrInvalid
	}
	if err = validateIAConfigureSourceRequest(q); err != nil {
		return iaConfigureSourceOutcome{}, err
	}
	// The denial boundary is part of the compiled purpose: a disable may not run
	// on the NORMAL descriptor and a non-denial may not borrow the FULL one.
	if iaConfigureSourceDenial(q) != (turn.transaction.descriptor.ID == IAConfigureSourceDisable) {
		return out, ErrInvalid
	}
	entry, err := enterIACommand(ctx, turn, iaEntryRequest{
		purpose: IAConfigureSource, action: iaEntryApply, reference: q.reference,
		libraryID: q.libraryID, libraryKey: q.libraryKey, sourceID: q.sourceID,
	}, p, lifetime, budget)
	if err != nil {
		return iaConfigureSourceOutcome{}, err
	}
	switch entry.decision {
	case receiptRetired:
		return iaConfigureSourceOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIAConfigureSource(ctx, turn, entry, q)
		if err != nil {
			return iaConfigureSourceOutcome{}, err
		}
		return iaConfigureSourceOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIAConfigureSource(ctx, turn, entry, q)
	default:
		return iaConfigureSourceOutcome{}, ErrResolveRequired
	}
}

// The result is the exact post-command row, never a fabricated availability.
// A disable records the honest "disabled" health and keeps the prior identity
// confirmation. A root move cannot confirm the new root from inside this
// command, so it records "unknown" with identity_confirmed=0 and clears the old
// root's completion facts; the queued rescan confirms them later.
func iaConfigureSourceFacts(q iaConfigureSourceRequest, appliedMS int64, live iaConfigureSourceState) iaConfigurationResult {
	rootMoved := q.resolvedRoot != q.expectRoot
	health, confirmed := "ready", true
	switch {
	case !q.enabled:
		health, confirmed = "disabled", live.identityConfirmed
	case rootMoved:
		health, confirmed = "unknown", false
	}
	next, complete := live.nextScanAt, live.lastCompleteAt
	if rootMoved {
		next, complete = "", ""
	}
	return iaConfigurationResult{
		original: iaConfigurationReceiptOriginal{
			operationID: q.reference.Identity.OperationID, requestDigest: q.reference.RequestDigest,
			purpose: IAConfigureSource, libraryID: q.libraryID, libraryKey: q.libraryKey, sourceID: q.sourceID,
		},
		acceptedRevision: 1, appliedMS: appliedMS, resultCount: 1,
		sources: []iaConfigurationSourceResult{{
			ordinal: 0, id: q.sourceID, name: q.name, incarnation: q.incarnation, generation: q.expectGeneration + 1,
			configuredRoot: q.configuredRoot, resolvedRoot: q.resolvedRoot, rootIdentity: q.rootIdentity,
			kind: q.kind, classification: q.classification, enabled: q.enabled, health: health,
			followSymlinks: q.followSymlinks, intervalSeconds: q.intervalSeconds, identityConfirmed: confirmed,
			nextScanAt: next, lastCompleteAt: complete, lastProgressAt: live.lastProgressAt,
		}},
	}
}

// Fresh change: the live source CAS, the library-root UNIQUE key, the single-row
// update, the queued whole-root rescan, the original owner/receipt/cohort and
// one revision event, all in the caller's single transaction. Any later failure
// rolls the whole set back; a stale witness or a root already claimed in this
// library is refused before writes.
func applyIAConfigureSource(ctx context.Context, turn *commandTurn, entry *iaCommandEntry, q iaConfigureSourceRequest) (out iaConfigureSourceOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	// Exact current-source gate from the shared IA-05 entry: generation,
	// incarnation, root and root identity, rechecking authority before and after.
	if err = entry.checkFreshSource(ctx, turn, q.expectGeneration, q.expectIncarnation, q.expectRoot, q.expectRootIdentity); err != nil {
		return out, err
	}
	// library_sources UNIQUE(library_id,root): another source in this library
	// may not already own the desired root.
	row, err := wc008DraftQuery(ctx, turn, iaSourceConfigureRootPoint, q.libraryKey, q.resolvedRoot)
	if err != nil {
		return out, err
	}
	var rootOwner string
	switch err = row.Scan(&rootOwner); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return out, err
	case rootOwner != q.sourceID:
		return out, ErrConflict
	}
	row, err = wc008DraftQuery(ctx, turn, iaSourceConfigureStatePoint, q.sourceID, q.libraryKey)
	if err != nil {
		return out, err
	}
	var live iaConfigureSourceState
	var enabled, confirmed int64
	var progress sql.NullString
	if err = row.Scan(&enabled, &confirmed, &live.nextScanAt, &live.lastCompleteAt, &progress); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if enabled < 0 || enabled > 1 || confirmed < 0 || confirmed > 1 {
		return out, ErrResolveRequired
	}
	live.enabled, live.identityConfirmed = enabled == 1, confirmed == 1
	live.lastProgressAt = progress.String
	appliedMS := turn.transaction.writer.now().UnixMilli()
	facts := iaConfigureSourceFacts(q, appliedMS, live)
	if err = preflightIAConfigurationResult(IAConfigureSource, q.reference.Identity, q.reference.RequestDigest, facts); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(q.reference.Identity, q.reference.RequestDigest, iaConfigureSourceResult{facts})
	if err != nil {
		return out, err
	}
	headerBytes, err := iaConfigurationHeaderRetainedBytes(IAConfigureSource, facts)
	if err != nil {
		return out, err
	}
	sourceBytes, err := iaConfigurationSourceRetainedBytes(IAConfigureSource, facts.sources[0])
	if err != nil {
		return out, err
	}
	if err = entry.authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	s := facts.sources[0]
	if err = wc008DraftChanged(ctx, turn, iaSourceConfigureUpdate,
		s.name, s.configuredRoot, s.resolvedRoot, s.classification, s.rootIdentity, s.followSymlinks, s.intervalSeconds,
		s.incarnation, s.generation, s.health, s.enabled, s.identityConfirmed, s.nextScanAt, s.lastCompleteAt, s.kind,
		q.sourceID, q.libraryKey, q.expectGeneration, q.expectIncarnation, q.expectRoot, q.expectRootIdentity); err != nil {
		return out, err
	}
	// Re-enabling or moving a root queues exactly one whole-root rescan bound to
	// the new generation/incarnation. A disabled source queues nothing.
	if q.enabled && (q.resolvedRoot != q.expectRoot || !live.enabled) {
		if err = wc008DraftChanged(ctx, turn, iaSourceConfigureIntent, q.sourceID, ".", q.libraryKey, s.generation, s.incarnation,
			s.rootIdentity, s.resolvedRoot, s.configuredRoot, s.kind, s.followSymlinks, "incremental", appliedMS); err != nil {
			return out, err
		}
	}
	if err = wc008DraftChanged(ctx, turn, iaSourceConfigureOwnerInsert, q.reference.Identity.OperationID[:], q.reference.RequestDigest[:],
		q.reference.Identity.Incarnation[:], "configure_source", q.libraryID, q.libraryKey, q.sourceID, int64(1), int64(2), headerBytes+sourceBytes); err != nil {
		return out, err
	}
	if err = wc008DraftChanged(ctx, turn, iaSourceConfigureReceipt, q.reference.Identity.OperationID[:], q.reference.Identity.Incarnation[:], int64(1),
		q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], "configure_source", int64(1), int64(1), appliedMS,
		nil, nil, int64(1), digest[:], headerBytes); err != nil {
		return out, err
	}
	if err = wc008DraftChanged(ctx, turn, iaSourceConfigureCohortInsert, q.reference.Identity.OperationID[:], q.reference.Identity.Incarnation[:], int64(1),
		"configure_source", int64(0), s.id, s.name, s.incarnation, s.generation, s.configuredRoot, s.resolvedRoot, s.rootIdentity,
		s.kind, s.classification, s.enabled, s.health, s.followSymlinks, s.intervalSeconds, s.identityConfirmed,
		s.nextScanAt, s.lastCompleteAt, s.lastProgressAt, sourceBytes); err != nil {
		return out, err
	}
	if _, err = appendWCAPIEvent(ctx, turn, wcAPIEvent{
		audience: "server", eventType: "source.changed", resourceKind: "source",
		resourceID: q.sourceID, revision: strconv.FormatInt(s.generation, 10), data: "{}", atMS: appliedMS,
	}); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = entry.authority.recheck(ctx, turn); err != nil {
		return iaConfigureSourceOutcome{}, err
	}
	return iaConfigureSourceOutcome{decision: receiptFresh, result: iaConfigureSourceResult{facts}, digest: digest}, nil
}

// Replayed configuration is decoded only from the immutable operation, header
// and single-source cohort of the ORIGINAL operation/incarnation. Live libraries
// and library_sources are never joined: a later rename, disable, root move or
// deletion cannot edit a saved cohort. Current owner authority was established
// by the entry before this lookup; the entry also fixed the ACK floor and the
// Receipt decision.
func lookupIAConfigureSource(ctx context.Context, turn *commandTurn, entry *iaCommandEntry, q iaConfigureSourceRequest) (out iaConfigureSourceHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || entry == nil || entry.authority == nil || entry.decision != receiptReplay ||
		entry.header.kind != IAConfigureSource || !entry.header.exists || entry.header.resultCount != 1 {
		return out, ErrResolveRequired
	}
	operation := q.reference.Identity.OperationID
	incarnation := q.reference.Identity.Incarnation
	row, err := wc008DraftQuery(ctx, turn, iaSourceConfigureOwnerPoint, operation[:], incarnation[:])
	if err != nil {
		return out, err
	}
	var request, savedKind []byte
	var libraryID int64
	var libraryKey string
	var sourceID sql.NullString
	var last, acknowledged, rows, bytes int64
	if err = row.Scan(&request, &savedKind, &libraryID, &libraryKey, &sourceID, &last, &acknowledged, &rows, &bytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if len(request) != 32 || string(savedKind) != "configure_source" || libraryID != q.libraryID || libraryKey != q.libraryKey ||
		!sourceID.Valid || sourceID.String != q.sourceID || last != 1 || acknowledged != 0 || rows != 2 || bytes <= 0 ||
		!equalBytes(request, q.reference.RequestDigest[:]) || entry.owner.lastSequence != last || entry.owner.acknowledgedSequence != acknowledged {
		return out, ErrResolveRequired
	}
	row, err = wc008DraftQuery(ctx, turn, iaSourceConfigureHeaderPoint, operation[:], incarnation[:], int64(1), "configure_source")
	if err != nil {
		return out, err
	}
	var headerOperation, headerRequest, headerDigest []byte
	var version, count, retainedBytes, acceptedRevision, appliedMS int64
	var libraryName, libraryKind sql.NullString
	if err = row.Scan(&headerOperation, &headerRequest, &version, &count, &headerDigest, &retainedBytes, &acceptedRevision, &appliedMS, &libraryName, &libraryKind); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if len(headerOperation) != 16 || len(headerRequest) != 32 || len(headerDigest) != 32 || version != 1 || count != 1 ||
		acceptedRevision <= 0 || appliedMS < 0 || libraryName.Valid || libraryKind.Valid ||
		!equalBytes(headerOperation, operation[:]) || !equalBytes(headerRequest, q.reference.RequestDigest[:]) ||
		!equalBytes(headerDigest, entry.header.resultDigest[:]) {
		return out, ErrResolveRequired
	}
	facts := iaConfigurationResult{
		original: iaConfigurationReceiptOriginal{
			operationID: operation, requestDigest: q.reference.RequestDigest,
			purpose: IAConfigureSource, libraryID: q.libraryID, libraryKey: q.libraryKey, sourceID: q.sourceID,
		},
		acceptedRevision: acceptedRevision, appliedMS: appliedMS,
		resultCount: count, sources: make([]iaConfigurationSourceResult, 0, count),
	}
	step, ok := turn.steps[iaSourceConfigureCohortPoint]
	if !ok {
		return out, invalid("ia_configure_source_steps")
	}
	sourceRows, err := turn.query(ctx, step.statementStep, operation[:], incarnation[:], int64(1), "configure_source")
	if err != nil {
		return out, err
	}
	var sourceBytes int64
	for sourceRows.Next() {
		if len(facts.sources) != 0 {
			return iaConfigureSourceHistory{}, ErrResolveRequired
		}
		var s iaConfigurationSourceResult
		var enabled, follow, confirmed int64
		var n int64
		if err = sourceRows.Scan(&s.ordinal, &s.id, &s.name, &s.incarnation, &s.generation, &s.configuredRoot, &s.resolvedRoot,
			&s.rootIdentity, &s.kind, &s.classification, &enabled, &s.health, &follow, &s.intervalSeconds, &confirmed,
			&s.nextScanAt, &s.lastCompleteAt, &s.lastProgressAt, &n); err != nil {
			return iaConfigureSourceHistory{}, errors.Join(ErrResolveRequired, err)
		}
		if s.ordinal != 0 || s.id != q.sourceID || enabled < 0 || enabled > 1 || follow < 0 || follow > 1 || confirmed < 0 || confirmed > 1 || n <= 0 {
			return iaConfigureSourceHistory{}, ErrResolveRequired
		}
		s.enabled, s.followSymlinks, s.identityConfirmed = enabled == 1, follow == 1, confirmed == 1
		sourceBytes = n
		facts.sources = append(facts.sources, s)
	}
	err = errors.Join(err, sourceRows.Err(), sourceRows.Close())
	if err != nil {
		return iaConfigureSourceHistory{}, err
	}
	// The complete cohort, its exact ordinal and the full retained ledger must
	// match the header before any result is returned as Applied.
	if len(facts.sources) != 1 {
		return iaConfigureSourceHistory{}, ErrResolveRequired
	}
	result := iaConfigureSourceResult{facts}
	if err = preflightIAConfigurationResult(IAConfigureSource, q.reference.Identity, q.reference.RequestDigest, facts); err != nil {
		return iaConfigureSourceHistory{}, errors.Join(ErrResolveRequired, err)
	}
	headerBytes, err := iaConfigurationHeaderRetainedBytes(IAConfigureSource, facts)
	if err != nil || headerBytes != retainedBytes || headerBytes > bytes || sourceBytes > bytes-headerBytes || headerBytes+sourceBytes != bytes {
		return iaConfigureSourceHistory{}, errors.Join(ErrResolveRequired, err)
	}
	digest, err := canonicalResultDigest(q.reference.Identity, q.reference.RequestDigest, result)
	if err != nil || !equalBytes(digest[:], headerDigest) {
		return iaConfigureSourceHistory{}, errors.Join(ErrResolveRequired, err)
	}
	return iaConfigureSourceHistory{decision: receiptReplay, result: result, digest: digest}, nil
}
