package writer

import (
	"context"
	"database/sql"
	"errors"
)

// Replayed configuration is decoded only from the immutable operation, header
// and source cohort of the ORIGINAL operation/incarnation. Live libraries and
// library_sources are never joined: a later rename, disable or root move cannot
// edit a saved cohort. Current owner authority was established by the entry
// before this lookup; the entry also fixed the ACK floor and Receipt decision.
type iaConfigureLibraryHistory struct {
	decision receiptDecision
	result   iaConfigureLibraryResult
	digest   ResultDigest
}

func lookupIAConfigureLibrary(ctx context.Context, turn *commandTurn, entry *iaCommandEntry, q iaConfigureLibraryRequest) (out iaConfigureLibraryHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || entry == nil || entry.authority == nil || entry.decision != receiptReplay ||
		entry.header.kind != IAConfigureLibrary || !entry.header.exists || entry.header.resultCount < 1 || entry.header.resultCount > iaConfigureLibraryMaxSources {
		return out, ErrResolveRequired
	}
	operation := q.reference.Identity.OperationID
	incarnation := q.reference.Identity.Incarnation
	row, err := wc008DraftQuery(ctx, turn, iaLibraryOwnerPoint, operation[:], incarnation[:])
	if err != nil {
		return out, err
	}
	var request, savedKind []byte
	var libraryID int64
	var libraryKey string
	var last, acknowledged, rows, bytes int64
	if err = row.Scan(&request, &savedKind, &libraryID, &libraryKey, &last, &acknowledged, &rows, &bytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if len(request) != 32 || string(savedKind) != "configure_library" || libraryID != q.libraryID || libraryKey != q.libraryKey ||
		last != 1 || acknowledged != 0 || rows != 1+entry.header.resultCount || bytes <= 0 ||
		!equalBytes(request, q.reference.RequestDigest[:]) || entry.owner.lastSequence != last || entry.owner.acknowledgedSequence != acknowledged {
		return out, ErrResolveRequired
	}
	row, err = wc008DraftQuery(ctx, turn, iaLibraryHeaderPoint, operation[:], incarnation[:], int64(1), "configure_library")
	if err != nil {
		return out, err
	}
	var headerOperation, headerRequest, headerDigest []byte
	var version, count, retainedBytes, acceptedRevision, appliedMS int64
	var libraryName, libraryKind sql.NullString
	if err = row.Scan(&headerOperation, &headerRequest, &version, &count, &headerDigest, &retainedBytes, &acceptedRevision, &appliedMS, &libraryName, &libraryKind); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if len(headerOperation) != 16 || len(headerRequest) != 32 || len(headerDigest) != 32 || version != 1 || count != entry.header.resultCount ||
		acceptedRevision <= 0 || appliedMS < 0 || !libraryName.Valid || !libraryKind.Valid ||
		!equalBytes(headerOperation, operation[:]) || !equalBytes(headerRequest, q.reference.RequestDigest[:]) ||
		!equalBytes(headerDigest, entry.header.resultDigest[:]) {
		return out, ErrResolveRequired
	}
	facts := iaConfigurationResult{
		original: iaConfigurationReceiptOriginal{
			operationID: operation, requestDigest: q.reference.RequestDigest,
			purpose: IAConfigureLibrary, libraryID: q.libraryID, libraryKey: q.libraryKey,
		},
		acceptedRevision: acceptedRevision, appliedMS: appliedMS,
		libraryName: libraryName.String, libraryKind: libraryKind.String,
		resultCount: count, sources: make([]iaConfigurationSourceResult, 0, count),
	}
	step, ok := turn.steps[iaLibrarySourcesPoint]
	if !ok {
		return out, invalid("ia_configure_library_steps")
	}
	sourceRows, err := turn.query(ctx, step.statementStep, operation[:], incarnation[:], int64(1), "configure_library")
	if err != nil {
		return out, err
	}
	sourceBytes := make([]int64, 0, count)
	seen := make(map[string]bool, count)
	for sourceRows.Next() {
		if int64(len(facts.sources)) >= count {
			return iaConfigureLibraryHistory{}, ErrResolveRequired
		}
		var s iaConfigurationSourceResult
		var enabled, follow, confirmed, n int64
		if err = sourceRows.Scan(&s.ordinal, &s.id, &s.name, &s.incarnation, &s.generation, &s.configuredRoot, &s.resolvedRoot,
			&s.rootIdentity, &s.kind, &s.classification, &enabled, &s.health, &follow, &s.intervalSeconds, &confirmed,
			&s.nextScanAt, &s.lastCompleteAt, &s.lastProgressAt, &n); err != nil {
			return iaConfigureLibraryHistory{}, errors.Join(ErrResolveRequired, err)
		}
		if s.ordinal != int64(len(facts.sources)) || seen[s.id] || enabled < 0 || enabled > 1 || follow < 0 || follow > 1 || confirmed < 0 || confirmed > 1 || n <= 0 {
			return iaConfigureLibraryHistory{}, ErrResolveRequired
		}
		seen[s.id] = true
		s.enabled, s.followSymlinks, s.identityConfirmed = enabled == 1, follow == 1, confirmed == 1
		facts.sources = append(facts.sources, s)
		sourceBytes = append(sourceBytes, n)
	}
	err = errors.Join(err, sourceRows.Err(), sourceRows.Close())
	if err != nil {
		return iaConfigureLibraryHistory{}, err
	}
	// The complete cohort, its exact ordinals and the full retained ledger must
	// match the header before any result is returned as Applied.
	if int64(len(facts.sources)) != count {
		return iaConfigureLibraryHistory{}, ErrResolveRequired
	}
	result := iaConfigureLibraryResult{facts}
	if err = preflightIAConfigurationResult(IAConfigureLibrary, q.reference.Identity, q.reference.RequestDigest, facts); err != nil {
		return iaConfigureLibraryHistory{}, errors.Join(ErrResolveRequired, err)
	}
	headerBytes, err := iaConfigurationHeaderRetainedBytes(IAConfigureLibrary, facts)
	if err != nil || headerBytes != retainedBytes || headerBytes > bytes {
		return iaConfigureLibraryHistory{}, errors.Join(ErrResolveRequired, err)
	}
	remaining := bytes - headerBytes
	for i, s := range facts.sources {
		n, e := iaConfigurationSourceRetainedBytes(IAConfigureLibrary, s)
		if e != nil || n != sourceBytes[i] || n > remaining {
			return iaConfigureLibraryHistory{}, errors.Join(ErrResolveRequired, e)
		}
		remaining -= n
	}
	if remaining != 0 {
		return iaConfigureLibraryHistory{}, ErrResolveRequired
	}
	digest, err := canonicalResultDigest(q.reference.Identity, q.reference.RequestDigest, result)
	if err != nil || !equalBytes(digest[:], headerDigest) {
		return iaConfigureLibraryHistory{}, errors.Join(ErrResolveRequired, err)
	}
	return iaConfigureLibraryHistory{decision: receiptReplay, result: result, digest: digest}, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
