package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	iaConfigureLibraryMaxSources = 64
	iaConfigureLibraryMaxRoot    = 8192
)

// Caller-prepared source evidence for ONE new library root. Roots are exact
// configured/resolved witnesses already observed outside this command; the
// command never touches the filesystem, provider or tuner. Overlapping roots
// are legal; only an exact duplicate root within the same request is refused.
type iaConfigureLibrarySource struct {
	id, name, incarnation              string
	configuredRoot, resolvedRoot       string
	rootIdentity, kind, classification string
	followSymlinks                     bool
	intervalSeconds                    int64
}

// Historical references and new birth share this closed shape. Sequence is
// always one: initial library configuration has exactly one original receipt.
type iaConfigureLibraryRequest struct {
	reference  ReceiptReference
	libraryID  int64
	libraryKey string
	name, kind string
	sources    []iaConfigureLibrarySource
}

// Private same-turn protocol value, not a public ingress result. Fresh carries
// the exact typed result and digest this command wrote; replay carries the
// decoded original; retired carries no result.
type iaConfigureLibraryOutcome struct {
	decision receiptDecision
	result   iaConfigureLibraryResult
	digest   ResultDigest
}

func iaConfigureLibraryText(s string, limit int) bool {
	return len(s) > 0 && len(s) <= limit && utf8.ValidString(s) && strings.IndexByte(s, 0) < 0
}

func iaConfigureLibraryRoot(s string) bool {
	return len(s) > 0 && len(s) <= iaConfigureLibraryMaxRoot && utf8.ValidString(s) && strings.IndexByte(s, 0) < 0 && strings.IndexFunc(s, unicode.IsControl) < 0
}

func iaConfigureLibraryKindName(kind string) bool {
	switch kind {
	case "movie", "tv", "anime", "music", "audiobook", "book", "comic":
		return true
	}
	return false
}

// Complete validation precedes EVERY write, including namespace settlement.
// Zero roots and 65 roots are refused; 64 is the accepted maximum. A library is
// never left configured with nothing queued.
func validateIAConfigureLibraryRequest(q iaConfigureLibraryRequest) error {
	if q.libraryID <= 0 || !iaConfigureLibraryText(q.libraryKey, 128) || strings.TrimSpace(q.name) != q.name || q.name == "" || len(q.name) > 100 ||
		!utf8.ValidString(q.name) || strings.IndexFunc(q.name, unicode.IsControl) >= 0 || !iaConfigureLibraryKindName(q.kind) {
		return ErrInvalid
	}
	ref := q.reference
	if ref.Identity.Incarnation == [16]byte{} || ref.Identity.OperationID == [16]byte{} || ref.Identity.Sequence != 1 || ref.RequestDigest == [32]byte{} {
		return ErrInvalid
	}
	if len(q.sources) == 0 || len(q.sources) > iaConfigureLibraryMaxSources {
		return ErrInvalid
	}
	total := len(q.libraryKey) + len(q.name) + len(q.kind)
	ids := make(map[string]bool, len(q.sources))
	roots := make(map[string]bool, len(q.sources))
	for _, s := range q.sources {
		if !iaConfigureLibraryText(s.id, 128) || !iaConfigureLibraryText(s.name, 100) || !iaConfigureLibraryText(s.incarnation, 128) || !iaConfigureLibraryText(s.rootIdentity, 256) ||
			!iaConfigureLibraryRoot(s.configuredRoot) || !iaConfigureLibraryRoot(s.resolvedRoot) || ids[s.id] || roots[s.resolvedRoot] ||
			(s.kind != "local" && s.kind != "remote") || (s.classification != "local" && s.classification != "network") ||
			s.intervalSeconds < 0 || s.intervalSeconds > 0 && s.intervalSeconds < 900 || s.kind == "remote" && s.followSymlinks {
			return ErrInvalid
		}
		ids[s.id], roots[s.resolvedRoot] = true, true
		total += len(s.id) + len(s.name) + len(s.incarnation) + len(s.configuredRoot) + len(s.resolvedRoot) + len(s.rootIdentity) + len(s.kind) + len(s.classification)
	}
	if total > maxCanonicalRequestBytes {
		return ErrInvalid
	}
	return nil
}

// One governed IA configuration birth or original replay. Current owner
// authority and original classification come from the shared IA-05 entry; no
// fresh receipt is born from a saved, retired or foreign original.
func configureIALibrary(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, q iaConfigureLibraryRequest) (out iaConfigureLibraryOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAConfigureLibrary {
		return out, ErrInvalid
	}
	if err = validateIAConfigureLibraryRequest(q); err != nil {
		return iaConfigureLibraryOutcome{}, err
	}
	entry, err := enterIACommand(ctx, turn, iaEntryRequest{
		purpose: IAConfigureLibrary, action: iaEntryApply, reference: q.reference,
		libraryID: q.libraryID, libraryKey: q.libraryKey,
	}, p, lifetime, budget)
	if err != nil {
		return iaConfigureLibraryOutcome{}, err
	}
	switch entry.decision {
	case receiptRetired:
		return iaConfigureLibraryOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIAConfigureLibrary(ctx, turn, entry, q)
		if err != nil {
			return iaConfigureLibraryOutcome{}, err
		}
		return iaConfigureLibraryOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIAConfigureLibrary(ctx, turn, entry, q)
	default:
		return iaConfigureLibraryOutcome{}, ErrResolveRequired
	}
}

func iaConfigureLibraryFacts(q iaConfigureLibraryRequest, appliedMS int64) iaConfigurationResult {
	facts := iaConfigurationResult{
		original: iaConfigurationReceiptOriginal{
			operationID: q.reference.Identity.OperationID, requestDigest: q.reference.RequestDigest,
			purpose: IAConfigureLibrary, libraryID: q.libraryID, libraryKey: q.libraryKey,
		},
		acceptedRevision: 1, appliedMS: appliedMS, libraryName: q.name, libraryKind: q.kind,
		resultCount: int64(len(q.sources)), sources: make([]iaConfigurationSourceResult, 0, len(q.sources)),
	}
	for i, s := range q.sources {
		facts.sources = append(facts.sources, iaConfigurationSourceResult{
			ordinal: int64(i), id: s.id, name: s.name, incarnation: s.incarnation, generation: 1,
			configuredRoot: s.configuredRoot, resolvedRoot: s.resolvedRoot, rootIdentity: s.rootIdentity,
			kind: s.kind, classification: s.classification, enabled: true, health: "ready",
			followSymlinks: s.followSymlinks, intervalSeconds: s.intervalSeconds, identityConfirmed: true,
		})
	}
	return facts
}

// Fresh birth: retained namespace first, then live roots, policy, the initial
// per-source scan intent, the original owner/receipt/cohort and one revision
// event, all in the caller's single transaction. Any later failure rolls the
// whole set back; a conflicting namespace or source is refused before writes.
func applyIAConfigureLibrary(ctx context.Context, turn *commandTurn, entry *iaCommandEntry, q iaConfigureLibraryRequest) (out iaConfigureLibraryOutcome, err error) {
	appliedMS := turn.transaction.writer.now().UnixMilli()
	facts := iaConfigureLibraryFacts(q, appliedMS)
	if err = preflightIAConfigurationResult(IAConfigureLibrary, q.reference.Identity, q.reference.RequestDigest, facts); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(q.reference.Identity, q.reference.RequestDigest, iaConfigureLibraryResult{facts})
	if err != nil {
		return out, err
	}
	headerBytes, err := iaConfigurationHeaderRetainedBytes(IAConfigureLibrary, facts)
	if err != nil {
		return out, err
	}
	sourceBytes := make([]int64, len(facts.sources))
	var retained int64 = headerBytes
	for i, s := range facts.sources {
		if sourceBytes[i], err = iaConfigurationSourceRetainedBytes(IAConfigureLibrary, s); err != nil {
			return out, err
		}
		retained += sourceBytes[i]
	}
	// The retained namespace is immutable. A different numeric id may not claim
	// this public key, and a retired namespace is never reopened.
	row, err := wc008DraftQuery(ctx, turn, iaLibraryNamespaceKey, q.libraryKey)
	if err != nil {
		return out, err
	}
	var namespaceID, namespaceRetired int64
	err = row.Scan(&namespaceID, &namespaceRetired)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else if namespaceID != q.libraryID || namespaceRetired != 0 {
		return out, ErrConflict
	}
	// A live library row is part of the same immutable namespace: an existing
	// row must carry exactly the requested name and kind.
	row, err = wc008DraftQuery(ctx, turn, iaLibraryLivePoint, q.libraryKey)
	if err != nil {
		return out, err
	}
	var liveName, liveKind, liveRoot string
	err = row.Scan(&liveName, &liveKind, &liveRoot)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return out, err
	} else if liveName != q.name || liveKind != q.kind {
		return out, ErrConflict
	}
	for _, s := range q.sources {
		row, err = wc008DraftQuery(ctx, turn, iaLibrarySourcePoint, s.id)
		if err != nil {
			return out, err
		}
		var owner string
		err = row.Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return out, err
		}
		return out, ErrConflict
	}
	if err = entry.authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	root := facts.sources[0].resolvedRoot
	for _, step := range []struct {
		id   stmt.ID
		args []any
	}{
		{iaLibraryNamespaceInsert, []any{q.libraryID, q.libraryKey, root}},
		{iaLibraryLiveInsert, []any{q.libraryKey, q.name, q.kind, root, int64(1), int64(1), int64(1)}},
		{iaLibraryPolicyInsert, []any{q.libraryKey, int64(1), "basic"}},
	} {
		if err = wc008DraftChanged(ctx, turn, step.id, step.args...); err != nil {
			return out, err
		}
	}
	for _, s := range q.sources {
		if err = wc008DraftChanged(ctx, turn, iaLibrarySourceInsert, s.id, q.libraryKey, s.name, s.configuredRoot, s.resolvedRoot,
			s.classification, s.rootIdentity, s.followSymlinks, s.intervalSeconds, "", true, s.incarnation, int64(1), "ready", true, s.kind); err != nil {
			return out, err
		}
		if err = wc008DraftChanged(ctx, turn, iaLibraryIntentInsert, s.id, ".", q.libraryKey, int64(1), s.incarnation, s.rootIdentity,
			s.resolvedRoot, s.configuredRoot, s.kind, s.followSymlinks, "incremental", appliedMS); err != nil {
			return out, err
		}
	}
	if err = wc008DraftChanged(ctx, turn, iaLibraryOwnerInsert, q.reference.Identity.OperationID[:], q.reference.RequestDigest[:],
		q.reference.Identity.Incarnation[:], "configure_library", q.libraryID, q.libraryKey, int64(1), int64(1+len(facts.sources)), retained); err != nil {
		return out, err
	}
	if err = wc008DraftChanged(ctx, turn, iaLibraryReceiptInsert, q.reference.Identity.OperationID[:], q.reference.Identity.Incarnation[:], int64(1),
		q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], "configure_library", int64(1), int64(1), appliedMS,
		q.name, q.kind, int64(len(facts.sources)), digest[:], headerBytes); err != nil {
		return out, err
	}
	for i, s := range facts.sources {
		if err = wc008DraftChanged(ctx, turn, iaLibraryCohortInsert, q.reference.Identity.OperationID[:], q.reference.Identity.Incarnation[:], int64(1),
			"configure_library", int64(i), s.id, s.name, s.incarnation, s.generation, s.configuredRoot, s.resolvedRoot, s.rootIdentity,
			s.kind, s.classification, s.enabled, s.health, s.followSymlinks, s.intervalSeconds, s.identityConfirmed,
			s.nextScanAt, s.lastCompleteAt, s.lastProgressAt, sourceBytes[i]); err != nil {
			return out, err
		}
	}
	if _, err = appendWCAPIEvent(ctx, turn, wcAPIEvent{
		audience: "server", eventType: "library.changed", resourceKind: "library",
		resourceID: q.libraryKey, revision: "1", data: "{}", atMS: appliedMS,
	}); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = entry.authority.recheck(ctx, turn); err != nil {
		return iaConfigureLibraryOutcome{}, err
	}
	return iaConfigureLibraryOutcome{decision: receiptFresh, result: iaConfigureLibraryResult{facts}, digest: digest}, nil
}
