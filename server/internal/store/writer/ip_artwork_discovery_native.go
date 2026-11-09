package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
)

// 220-222 bodies. Current owner authority precedes historical receipt
// disclosure; the ORIGINAL saved result is served without any fresh
// source/lease/policy probe. A fresh command then re-proves the exact claimed
// job, accepted artwork source, resolved language, provider enablement and
// screen consent before it mutates the finite stage.
func ipArtworkDiscoveryLookup(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkDiscoveryRequest, identity CommandIdentity, digest RequestDigest) (out ipArtworkDiscoveryOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != kind {
		return out, ErrInvalid
	}
	rows, err := ipTextRows(ctx, turn, ipADOwner, q.owner.id)
	if err != nil {
		return out, err
	}
	var owner receiptOwnerFacts
	var incarnation []byte
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&incarnation, &owner.lastSequence, &owner.acknowledgedSequence)
	}); err != nil {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable, err)
	}
	if len(incarnation) != 16 {
		return out, ErrResolveRequired
	}
	owner.exists = true
	copy(owner.incarnation[:], incarnation)

	rows, err = ipTextRows(ctx, turn, ipADReceipt, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var found receiptFacts
	var operation, request, result []byte
	var nativeKind int64
	var stage sql.NullInt64
	var saved ipArtworkDiscoveryResult
	err = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &nativeKind, &stage,
			&saved.Revision, &saved.Count, &saved.AfterOrdinal)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 || !stage.Valid ||
			stage.Int64 <= 0 || !ipArtworkDiscoveryKind(CommandID(nativeKind)) {
			return out, ErrResolveRequired
		}
		found.exists = true
		found.kind = CommandID(nativeKind)
		found.resultCount = saved.Count
		actual := receiptIdentity{command: identity}
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], request)
		found.identity = actual
		copy(found.resultDigest[:], result)
		saved.StageID = stage.Int64
	}
	out.decision, err = classifyReceipt(receiptIdentity{identity, digest}, kind, owner, found)
	if err != nil {
		return out, err
	}
	if out.decision != receiptReplay {
		return out, nil
	}
	if saved.Revision <= 0 || saved.Count < 0 || saved.Count > ipArtworkDiscoveryMaxCandidates ||
		saved.AfterOrdinal != saved.Count ||
		(kind == IPArtworkDiscoveryClaim && (saved.Revision != 1 || saved.Count != 0)) ||
		(kind == IPArtworkDiscoveryPage && saved.Count != q.afterOrdinal+int64(len(q.candidates))) ||
		(kind == IPArtworkDiscoveryFinish && saved.Count != q.spec.expectedCount) ||
		(kind != IPArtworkDiscoveryClaim && (saved.StageID != q.stageID || saved.Revision != q.stageRevision+1)) {
		return out, ErrResolveRequired
	}
	expected, e := ipArtworkDiscoveryResultDigest(kind, identity, digest, saved)
	if e != nil || !bytes.Equal(expected[:], result) {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.result, out.digest = saved, expected
	return out, nil
}

// The claimed job must still be in providers, under the same live lease, exact
// job revision and accepted artwork source. The effective language is re-derived
// from current settings/source; it is never taken from the provider response.
func ipArtworkDiscoveryFresh(ctx context.Context, turn *commandTurn, q ipArtworkDiscoveryRequest, nowMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	o := q.owner
	rows, err := ipTextRows(ctx, turn, ipADFreshOwner, o.id, o.incarnation[:], o.entityID, o.personID,
		o.sourceIncarnation[:], o.sourceRevision, o.revision, o.leaseOwner, o.leaseGeneration,
		o.leaseUntilMS, nowMS)
	if err != nil {
		return err
	}
	var one int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&one) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	var source []byte
	var revision, retired, language int64
	if o.entityID > 0 {
		rows, err = ipTextRows(ctx, turn, ipADEntity, q.spec.language, o.entityID)
		if err != nil {
			return err
		}
		if err = ipTextOne(rows, func() error {
			return rows.Scan(&source, &revision, &retired, &language)
		}); err != nil {
			return errors.Join(ErrConflict, err)
		}
	} else {
		rows, err = ipTextRows(ctx, turn, ipADPerson, q.spec.language, o.personID)
		if err != nil {
			return err
		}
		if err = ipTextOne(rows, func() error {
			return rows.Scan(&source, &revision, &language)
		}); err != nil {
			return errors.Join(ErrConflict, err)
		}
	}
	if !bytes.Equal(source, o.sourceIncarnation[:]) || revision != o.sourceRevision || retired != 0 || language != 1 {
		return ErrConflict
	}
	rows, err = ipTextRows(ctx, turn, ipADPolicy, q.spec.provider)
	if err != nil {
		return err
	}
	var policy, enabled int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&policy, &enabled) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if policy != q.spec.policyRevision || enabled != 1 {
		return ErrConflict
	}
	return ipArtworkDiscoveryConsent(ctx, turn, q.spec.consentRevision)
}

// Artwork discovery uses the screen-image consent, never the MusicBrainz
// online-lookup epoch: a screen provider never borrows that eligibility.
func ipArtworkDiscoveryConsent(ctx context.Context, turn *commandTurn, revision int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if revision <= 0 {
		return ErrInvalid
	}
	rows, err := ipTextRows(ctx, turn, ipADConsent, revision)
	if err != nil {
		return err
	}
	var one int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&one) }); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDisclosureDenied
		}
		return err
	}
	if one != 1 {
		return ErrDisclosureDenied
	}
	return nil
}

func ipArtworkDiscoveryStageRead(ctx context.Context, turn *commandTurn, q ipArtworkDiscoveryRequest) (out ipArtworkDiscoveryStage, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := ipTextRows(ctx, turn, ipADStage, q.stageID, q.owner.id, q.owner.incarnation[:],
		q.spec.provider, q.owner.sourceIncarnation[:], q.owner.sourceRevision, q.spec.policyRevision,
		q.spec.language, q.spec.generation, q.spec.expectedCount, q.spec.expectedBytes,
		ipArtworkDiscoveryManifest, q.spec.manifest[:])
	if err != nil {
		return out, err
	}
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&out.revision, &out.state, &out.count, &out.bytes, &out.reclaimAfter)
	}); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if out.revision != q.stageRevision || out.count < 0 || out.count > q.spec.expectedCount ||
		out.bytes < 0 || out.bytes > q.spec.expectedBytes || out.reclaimAfter < -1 {
		return out, ErrConflict
	}
	return out, nil
}

// Keys always consume RAW ordinals. Callers ask for cap+1 and refuse before any
// decode/effect when the finite stage exceeds its declared bound. No OFFSET.
func ipArtworkDiscoveryKeys(ctx context.Context, turn *commandTurn, stage, after, limit int64) (out []int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if limit <= 0 || limit > ipArtworkDiscoveryMaxCandidates+1 || after < -1 {
		return nil, ErrInvalid
	}
	rows, err := ipTextRows(ctx, turn, ipADKeys, stage, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var ordinal int64
		if err = rows.Scan(&ordinal); err != nil {
			return nil, err
		}
		if ordinal <= after || int64(len(out)) >= limit {
			return nil, ErrConflict
		}
		out = append(out, ordinal)
		after = ordinal
	}
	return out, rows.Err()
}

// Point octet preflight precedes every native TEXT decode. The caller reserves
// the actual bytes under the same admitted frame before this component runs.
func ipArtworkDiscoveryDecode(ctx context.Context, turn *commandTurn, stage, ordinal, budget int64) (out ipArtworkDiscoveryCandidate, size int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			candidate ipArtworkDiscoveryCandidate
			bytes     int64
		}{out, size}, err)
		out, size, err = v.candidate, v.bytes, e
	}()
	if budget < 0 {
		return out, 0, ErrConflict
	}
	rows, err := ipTextRows(ctx, turn, ipADOctets, stage, ordinal)
	if err != nil {
		return out, 0, err
	}
	var lengths [5]int64
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&lengths[0], &lengths[1], &lengths[2], &lengths[3], &lengths[4])
	}); err != nil {
		return out, 0, errors.Join(ErrConflict, err)
	}
	actualBytes := int64(64)
	limits := [5]int64{64, 4096, 4096, 64, ipArtworkDiscoveryMaxText}
	for n, length := range lengths {
		if length < 0 || length > limits[n] {
			return out, 0, invalid("ip_artwork_native_octets")
		}
		actualBytes += length
	}
	if actualBytes > budget {
		return out, 0, ErrConflict
	}
	rows, err = ipTextRows(ctx, turn, ipADCandidate, stage, ordinal)
	if err != nil {
		return out, 0, err
	}
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&out.ordinal, &out.sourceOrdinal, &out.role, &out.imageID, &out.origin,
			&out.locale, &out.attribution, &out.score)
	}); err != nil {
		return out, 0, errors.Join(ErrConflict, err)
	}
	for n, value := range [5]string{out.role, out.imageID, out.origin, out.locale, out.attribution} {
		if int64(len(value)) != lengths[n] {
			return out, 0, ErrConflict
		}
	}
	if out.ordinal != ordinal {
		return out, 0, ErrConflict
	}
	size, err = ipArtworkDiscoveryCandidateBytes(out)
	return out, size, err
}

func applyIPArtworkDiscovery(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipPolicyBudget, kind CommandID, c *ipArtworkDiscoveryCommand) (out ipArtworkDiscoveryCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipArtworkDiscoveryCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != kind {
		return zero, ErrInvalid
	}
	q := c.request
	if err = ipArtworkDiscoveryRequestShape(kind, c.identity, q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(kind, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipArtworkDiscoveryLookup(ctx, turn, kind, q, c.identity, c.digest)
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.result, lookup.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.ipPolicyAuthorityRecheck(ctx, turn); err != nil {
		return zero, err
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	if err = ipArtworkDiscoveryFresh(ctx, turn, q, appliedMS); err != nil {
		return zero, err
	}
	var value ipArtworkDiscoveryResult
	switch kind {
	case IPArtworkDiscoveryClaim:
		value, err = ipArtworkDiscoveryClaim(ctx, turn, q)
	case IPArtworkDiscoveryPage, IPArtworkDiscoveryFinish:
		value, err = ipArtworkDiscoveryContinue(ctx, turn, kind, q)
	}
	if err != nil {
		return zero, err
	}
	digest, err := ipArtworkDiscoveryResultDigest(kind, c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	if err = ipArtworkDiscoveryRecord(ctx, turn, kind, q, c.identity, c.digest, digest, value, appliedMS); err != nil {
		return zero, err
	}
	saved, err := ipArtworkDiscoveryLookup(ctx, turn, kind, q, c.identity, c.digest)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.result != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.result, saved.digest, Applied
	return out, nil
}

// A claim declares the complete page but appends nothing: the caller already
// computed its manifest from the original provider response.
func ipArtworkDiscoveryClaim(ctx context.Context, turn *commandTurn, q ipArtworkDiscoveryRequest) (out ipArtworkDiscoveryResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	spec := q.spec
	if spec.expectedCount == 0 && spec.manifest != ipArtworkDiscoveryManifestDigest(q.owner, spec, nil) {
		return out, ErrConflict
	}
	rows, err := ipTextRows(ctx, turn, ipADBegin, q.owner.id, q.owner.incarnation[:], spec.provider,
		q.owner.sourceIncarnation[:], q.owner.sourceRevision, spec.policyRevision, spec.language,
		spec.generation, spec.expectedCount, spec.expectedBytes, spec.manifest[:])
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	var stage int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&stage) }); err != nil || stage <= 0 {
		return out, errors.Join(ErrResolveRequired, err)
	}
	return ipArtworkDiscoveryResult{StageID: stage, Revision: 1}, nil
}

func ipArtworkDiscoveryContinue(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkDiscoveryRequest) (out ipArtworkDiscoveryResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	spec := q.spec
	stage, err := ipArtworkDiscoveryStageRead(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if stage.state != "building" || stage.count != q.afterOrdinal {
		return out, ErrConflict
	}
	if kind == IPArtworkDiscoveryPage {
		return ipArtworkDiscoveryPageBody(ctx, turn, q, stage)
	}
	if stage.count != spec.expectedCount || stage.bytes != spec.expectedBytes {
		return out, ErrConflict
	}
	keys, err := ipArtworkDiscoveryKeys(ctx, turn, q.stageID, -1, spec.expectedCount+1)
	if err != nil {
		return out, err
	}
	if int64(len(keys)) != spec.expectedCount {
		return out, ErrConflict
	}
	candidates := make([]ipArtworkDiscoveryCandidate, 0, len(keys))
	var actualBytes int64
	for n, key := range keys {
		if key != int64(n) {
			return out, ErrConflict
		}
		candidate, size, e := ipArtworkDiscoveryDecode(ctx, turn, q.stageID, key, spec.expectedBytes-actualBytes)
		if e != nil {
			return out, e
		}
		actualBytes += size
		if actualBytes > spec.expectedBytes {
			return out, ErrConflict
		}
		candidates = append(candidates, candidate)
	}
	if actualBytes != spec.expectedBytes ||
		ipArtworkDiscoveryManifestDigest(q.owner, spec, candidates) != spec.manifest {
		return out, ErrConflict
	}
	if err = ipTextExecOne(ctx, turn, ipADFinish, q.stageID, stage.revision, stage.count,
		stage.bytes, spec.expectedCount, spec.expectedBytes); err != nil {
		return out, err
	}
	return ipArtworkDiscoveryResult{StageID: q.stageID, Revision: stage.revision + 1,
		Count: stage.count, AfterOrdinal: stage.count}, nil
}

func ipArtworkDiscoveryPageBody(ctx context.Context, turn *commandTurn, q ipArtworkDiscoveryRequest, stage ipArtworkDiscoveryStage) (out ipArtworkDiscoveryResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	spec := q.spec
	// A prefix cannot append over an already present raw key.
	keys, err := ipArtworkDiscoveryKeys(ctx, turn, q.stageID, stage.count-1, 1)
	if err != nil {
		return out, err
	}
	if len(keys) != 0 {
		return out, ErrConflict
	}
	var added int64
	for _, candidate := range q.candidates {
		size, e := ipArtworkDiscoveryCandidateBytes(candidate)
		if e != nil {
			return out, e
		}
		added += size
	}
	if added > spec.expectedBytes-stage.bytes {
		return out, ErrConflict
	}
	for _, candidate := range q.candidates {
		if err = ipTextExecOne(ctx, turn, ipADAppend, q.stageID, candidate.ordinal, candidate.role,
			candidate.sourceOrdinal, candidate.imageID, candidate.origin, candidate.locale,
			candidate.attribution, candidate.score); err != nil {
			return out, err
		}
	}
	count := stage.count + int64(len(q.candidates))
	if err = ipTextExecOne(ctx, turn, ipADAdvance, count, stage.bytes+added, q.stageID,
		stage.revision, stage.count, stage.bytes, spec.expectedCount, spec.expectedBytes); err != nil {
		return out, err
	}
	return ipArtworkDiscoveryResult{StageID: q.stageID, Revision: stage.revision + 1,
		Count: count, AfterOrdinal: count}, nil
}

// The owner CAS is the page/finish allowance and the receipt is saved in the
// same transaction. ACK alone does not retire the pinned stage or its proof.
func ipArtworkDiscoveryRecord(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkDiscoveryRequest, identity CommandIdentity, request RequestDigest, result ResultDigest, value ipArtworkDiscoveryResult, appliedMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	o := q.owner
	if err = ipTextExecOne(ctx, turn, ipADOwnerCAS, identity.Sequence, o.id, o.incarnation[:],
		identity.Sequence-1, identity.Sequence-1, o.revision, o.sourceIncarnation[:],
		o.sourceRevision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS, appliedMS); err != nil {
		return err
	}
	command := "discovery_" + ipArtworkDiscoveryKindName(kind)
	return ipTextExecOne(ctx, turn, ipADSave, o.id, o.incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], appliedMS, result[:], command,
		value.Revision, value.Count, value.AfterOrdinal, value.StageID)
}
