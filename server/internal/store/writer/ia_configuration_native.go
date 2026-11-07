package writer

import (
	"context"
	"database/sql"
	"errors"
)

func iaConfigurationKey(q iaConfigurationReference) []any {
	return []any{q.original.operationID[:], q.identity.Incarnation[:]}
}

func iaConfigurationHeaderKey(q iaConfigurationReference) ([]any, error) {
	kind, err := iaConfigurationResultKindName(q.original.purpose)
	if err != nil {
		return nil, err
	}
	return append(iaConfigurationKey(q), kind), nil
}

func loadIAConfigurationOwner(ctx context.Context, turn *commandTurn, q iaConfigurationReference) (iaConfigurationOwner, error) {
	var o iaConfigurationOwner
	var request []byte
	var kind string
	var source sql.NullString
	o.original.operationID = q.original.operationID
	o.incarnation = q.identity.Incarnation
	err := iaRetirementPoint(ctx, turn, iaConfigurationOwnerPoint, iaConfigurationKey(q), &request, &kind, &o.original.libraryID, &o.original.libraryKey, &source,
		&o.last, &o.acknowledged, &o.cursor.sequence, &o.cursor.ordinal, &o.rows, &o.bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return iaConfigurationOwner{}, nil
	}
	if err != nil {
		return iaConfigurationOwner{}, err
	}
	if len(request) != 32 {
		return iaConfigurationOwner{}, ErrResolveRequired
	}
	switch kind {
	case "configure_library":
		o.original.purpose = IAConfigureLibrary
	case "configure_source":
		o.original.purpose = IAConfigureSource
	default:
		return iaConfigurationOwner{}, ErrResolveRequired
	}
	if source.Valid != (o.original.purpose == IAConfigureSource) {
		return iaConfigurationOwner{}, ErrResolveRequired
	}
	copy(o.original.requestDigest[:], request)
	o.original.sourceID = source.String
	o.exists = true
	return o, nil
}

// Private adapter only. The owning issuer must prove CURRENT owner/disclosure
// authority and admit actual complete native row/copy bytes before fetching.
// This does not supply that preflight/profile, mint ingress, or return Applied.
// Scalar ACK classification precedes fetching names/roots/source children.
func lookupIAConfiguration(ctx context.Context, turn *commandTurn, q iaConfigurationReference) (value iaConfigurationHistory, owner iaConfigurationOwner, err error) {
	defer func() {
		combined := struct {
			history iaConfigurationHistory
			owner   iaConfigurationOwner
		}{value, owner}
		combined, err = finishCommand(ctx, commandLifetime(turn), combined, err)
		value, owner = combined.history, combined.owner
	}()
	if err = validateIAConfigurationReference(q); err != nil {
		return
	}
	if turn == nil || turn.transaction == nil || (turn.transaction.descriptor.ID != q.original.purpose && turn.transaction.descriptor.ID != IAConfigurationReceiptReclaim) {
		err = ErrInvalid
		return
	}
	owner, err = loadIAConfigurationOwner(ctx, turn, q)
	if err != nil {
		return
	}
	var h iaConfigurationReceipt
	if !owner.exists {
		value, err = classifyIAConfigurationHistory(q, owner, h)
		return
	}
	// Check original scope before looking up any retained result.
	if err = checkIAConfigurationOwnerScope(q, owner); err != nil {
		return
	}
	key, e := iaConfigurationHeaderKey(q)
	if e != nil {
		err = e
		return
	}
	var operation, request, digest []byte
	err = iaRetirementPoint(ctx, turn, iaConfigurationHeaderPoint, key, &operation, &request, &h.version, &h.resultCount, &digest, &h.headerBytes)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(digest) != 32 {
			err = ErrResolveRequired
			return
		}
		h.exists = true
		h.purpose = q.original.purpose
		h.identity.command = q.identity
		copy(h.identity.command.OperationID[:], operation)
		copy(h.identity.digest[:], request)
		copy(h.digest[:], digest)
	}
	if err != nil {
		return
	}
	decision, e := classifyIAConfigurationMetadata(q, owner, h)
	if e != nil {
		err = e
		return
	}
	if decision != receiptReplay {
		value = iaConfigurationHistory{decision: decision}
		return
	}
	if h.resultCount <= 0 || h.resultCount > iaConfigurationMaxSources {
		err = ErrResolveRequired
		return
	}
	h.result.original = owner.original
	h.result.resultCount = h.resultCount
	var name, kind sql.NullString
	err = iaRetirementPoint(ctx, turn, iaConfigurationBodyPoint, key, &h.result.acceptedRevision, &h.result.appliedMS, &name, &kind)
	if err != nil {
		return
	}
	if name.Valid != (q.original.purpose == IAConfigureLibrary) || kind.Valid != name.Valid {
		err = ErrResolveRequired
		return
	}
	h.result.libraryName, h.result.libraryKind = name.String, kind.String
	step, ok := turn.steps[iaConfigurationSourcesPoint]
	if !ok {
		err = ErrInvalid
		return
	}
	rows, e := turn.query(ctx, step.statementStep, key...)
	if e != nil {
		err = e
		return
	}
	for rows.Next() {
		if len(h.result.sources) >= iaConfigurationMaxSources {
			err = ErrResolveRequired
			break
		}
		var s iaConfigurationSourceResult
		var enabled, follow, confirmed, n int64
		e = rows.Scan(&s.ordinal, &s.id, &s.name, &s.incarnation, &s.generation, &s.configuredRoot, &s.resolvedRoot, &s.rootIdentity, &s.kind, &s.classification,
			&enabled, &s.health, &follow, &s.intervalSeconds, &confirmed, &s.nextScanAt, &s.lastCompleteAt, &s.lastProgressAt, &n)
		if e != nil {
			err = e
			break
		}
		if enabled < 0 || enabled > 1 || follow < 0 || follow > 1 || confirmed < 0 || confirmed > 1 {
			err = ErrResolveRequired
			break
		}
		s.enabled, s.followSymlinks, s.identityConfirmed = enabled == 1, follow == 1, confirmed == 1
		h.result.sources = append(h.result.sources, s)
		h.sourceBytes = append(h.sourceBytes, n)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return
	}
	value, err = classifyIAConfigurationHistory(q, owner, h)
	return
}

// The sole native effect is the ACK floor plus a scheduled data-GC due point.
// Due may be in the past: the original accepted timestamp makes it immediately
// eligible. Source/header debt and the105 fixed slot are never refunded/cleared.
func acknowledgeIAConfiguration(ctx context.Context, turn *commandTurn, q iaConfigurationReference) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	h, owner, err := lookupIAConfiguration(ctx, turn, q)
	if err != nil {
		return 0, err
	}
	if h.decision == receiptRetired {
		return owner.acknowledged, nil
	}
	if h.decision != receiptReplay || owner.last != 1 {
		return 0, ErrResolveRequired
	}
	key, err := iaConfigurationHeaderKey(q)
	if err != nil {
		return 0, err
	}
	args := append([]any{h.result.appliedMS}, key...)
	args = append(args, q.original.requestDigest[:], q.original.libraryID, q.original.libraryKey)
	id := iaConfigurationAckCAS
	if q.original.purpose == IAConfigureSource {
		id = iaConfigurationSourceAckCAS
		args = append(args, q.original.sourceID)
	}
	args = append(args, owner.rows, owner.bytes)
	if err = iaRetirementOneEffect(ctx, turn, id, args...); err != nil {
		return 0, err
	}
	return 1, nil
}
