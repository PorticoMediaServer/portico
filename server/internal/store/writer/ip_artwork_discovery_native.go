package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type ipArtworkDiscoveryOutcome struct {
	decision receiptDecision
	result   ipArtworkDiscoveryResult
}

func ipArtworkDiscoveryStep(turn *commandTurn, id stmt.ID) (statementStep, error) {
	if turn == nil {
		return statementStep{}, ErrInvalid
	}
	step := statementStep{id, ipArtworkDiscoveryWork(id)}
	allowed, ok := turn.steps[id]
	if ok && allowed.statementStep == step {
		return step, nil
	}
	return statementStep{}, ErrInvalid
}
func ipArtworkDiscoveryRead(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, err := ipArtworkDiscoveryStep(turn, id)
	if err != nil {
		return commandRow{}, err
	}
	return turn.queryRow(ctx, step, args...)
}
func ipArtworkDiscoveryWrite(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) error {
	step, err := ipArtworkDiscoveryStep(turn, id)
	if err != nil {
		return err
	}
	result, err := turn.exec(ctx, step, args...)
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
	return nil
}

func ipArtworkDiscoveryLookup(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkDiscoveryRequest, digest RequestDigest) (out ipArtworkDiscoveryOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := ipArtworkDiscoveryRead(ctx, turn, ipADOwner, q.owner.id)
	if err != nil {
		return out, err
	}
	var o receiptOwnerFacts
	var inc []byte
	if err = row.Scan(&inc, &o.lastSequence, &o.acknowledgedSequence); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if len(inc) != 16 {
		return out, ErrResolveRequired
	}
	o.exists = true
	copy(o.incarnation[:], inc)
	row, err = ipArtworkDiscoveryRead(ctx, turn, ipADReceipt, q.owner.id, q.identity.Incarnation[:], q.identity.Sequence)
	if err != nil {
		return out, err
	}
	var f receiptFacts
	var operation, request, result []byte
	var nativeKind int64
	var stage sql.NullInt64
	r := ipArtworkDiscoveryResult{}
	err = row.Scan(&operation, &request, &result, &nativeKind, &stage, &r.revision, &r.count, &r.afterOrdinal)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 || !stage.Valid {
			return out, ErrResolveRequired
		}
		f.exists = true
		f.kind = CommandID(nativeKind)
		f.resultCount = r.count
		f.identity.command = q.identity
		copy(f.identity.command.OperationID[:], operation)
		copy(f.identity.digest[:], request)
		copy(f.resultDigest[:], result)
		r.stageID = stage.Int64
	}
	out.decision, err = classifyReceipt(receiptIdentity{q.identity, digest}, kind, o, f)
	if err != nil {
		return out, err
	}
	if out.decision != receiptReplay {
		return out, nil
	}
	if r.stageID <= 0 || r.revision <= 0 || r.count < 0 || r.count > q.spec.expectedCount || r.afterOrdinal != r.count ||
		(kind == IPArtworkDiscoveryClaim && (r.revision != 1 || r.count != 0)) ||
		(kind == IPArtworkDiscoveryPage && r.count != q.afterOrdinal+int64(len(q.candidates))) ||
		(kind == IPArtworkDiscoveryFinish && r.count != q.spec.expectedCount) ||
		(kind != IPArtworkDiscoveryClaim && (r.stageID != q.stageID || r.revision != q.stageRevision+1)) {
		return out, ErrResolveRequired
	}
	expected, e := ipArtworkDiscoveryResultDigest(kind, q.identity, digest, r)
	if e != nil || !bytes.Equal(expected[:], result) {
		return out, ErrResolveRequired
	}
	out.result = r
	return out, nil
}

func ipArtworkDiscoveryFresh(ctx context.Context, turn *commandTurn, q ipArtworkDiscoveryRequest) error {
	o := q.owner
	row, err := ipArtworkDiscoveryRead(ctx, turn, ipADFreshOwner, o.id, o.incarnation[:], o.entityID, o.personID, o.sourceIncarnation[:], o.sourceRevision,
		o.revision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS, turn.transaction.writer.now().UnixMilli())
	if err != nil {
		return err
	}
	var one int64
	if err = row.Scan(&one); err != nil {
		return errors.Join(ErrConflict, err)
	}
	var source []byte
	var revision, retired, language int64
	if o.entityID > 0 {
		row, err = ipArtworkDiscoveryRead(ctx, turn, ipADEntity, q.spec.language, o.entityID)
		if err == nil {
			err = row.Scan(&source, &revision, &retired, &language)
		}
	} else {
		row, err = ipArtworkDiscoveryRead(ctx, turn, ipADPerson, q.spec.language, o.personID)
		if err == nil {
			err = row.Scan(&source, &revision, &language)
		}
	}
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	if !bytes.Equal(source, o.sourceIncarnation[:]) || revision != o.sourceRevision || retired != 0 || language != 1 {
		return ErrConflict
	}
	row, err = ipArtworkDiscoveryRead(ctx, turn, ipADPolicy, q.spec.provider)
	if err != nil {
		return err
	}
	var policy, online, enabled int64
	if err = row.Scan(&policy, &online, &enabled); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if policy != q.spec.policyRevision || online != 1 || enabled != 1 {
		return ErrConflict
	}
	return nil
}

func ipArtworkDiscoveryStageRead(ctx context.Context, turn *commandTurn, q ipArtworkDiscoveryRequest) (s ipArtworkDiscoveryStage, err error) {
	defer func() { s, err = finishCommand(ctx, commandLifetime(turn), s, err) }()
	o, v := q.owner, q.spec
	row, err := ipArtworkDiscoveryRead(ctx, turn, ipADStage, q.stageID, o.id, o.incarnation[:], v.provider, o.sourceIncarnation[:], o.sourceRevision,
		v.policyRevision, v.language, v.generation, v.expectedCount, v.expectedBytes, v.manifest[:])
	if err != nil {
		return s, err
	}
	err = row.Scan(&s.revision, &s.state, &s.count, &s.bytes, &s.reclaimAfter)
	if err != nil {
		return s, errors.Join(ErrConflict, err)
	}
	if s.revision != q.stageRevision || s.count < 0 || s.count > v.expectedCount || s.bytes < 0 || s.bytes > v.expectedBytes || s.reclaimAfter < -1 {
		return s, ErrConflict
	}
	return s, nil
}

// Keys always consume RAW ordinals. A probe asks for cap+1 and fails before any
// decode/effect if it exceeds the finite bound. No OFFSET, aggregate or fallback.
func ipArtworkDiscoveryKeys(ctx context.Context, turn *commandTurn, stage, after, limit int64) (out []int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if limit <= 0 || limit > ipArtworkDiscoveryMaxCandidates+1 || after < -1 {
		return nil, ErrInvalid
	}
	step, err := ipArtworkDiscoveryStep(turn, ipADKeys)
	if err != nil {
		return nil, err
	}
	rows, err := turn.query(ctx, step, stage, after, limit)
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
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Point octet preflight precedes every native TEXT decode in this component.
// W must reserve these actual bytes in its OWN admitted memory scope before
// integrating this body. Bounded allocation here is not a governor capability.
func ipArtworkDiscoveryDecode(ctx context.Context, turn *commandTurn, stage, ordinal, budget int64) (out ipArtworkDiscoveryCandidate, size int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			candidate ipArtworkDiscoveryCandidate
			bytes     int64
		}{out, size}, err)
		out, size, err = v.candidate, v.bytes, e
	}()
	row, err := ipArtworkDiscoveryRead(ctx, turn, ipADOctets, stage, ordinal)
	if err != nil {
		return out, 0, err
	}
	var lengths [5]int64
	if err = row.Scan(&lengths[0], &lengths[1], &lengths[2], &lengths[3], &lengths[4]); err != nil {
		return out, 0, err
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
		return out, 0, invalid("ip_artwork_native_byte_budget")
	}
	row, err = ipArtworkDiscoveryRead(ctx, turn, ipADCandidate, stage, ordinal)
	if err != nil {
		return out, 0, err
	}
	if err = row.Scan(&out.ordinal, &out.sourceOrdinal, &out.role, &out.imageID, &out.origin, &out.locale, &out.attribution, &out.score); err != nil {
		return out, 0, err
	}
	for n, value := range []string{out.role, out.imageID, out.origin, out.locale, out.attribution} {
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

func ipArtworkDiscoveryApply(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkDiscoveryRequest) (out ipArtworkDiscoveryOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != kind ||
		(kind != IPArtworkDiscoveryClaim && kind != IPArtworkDiscoveryPage && kind != IPArtworkDiscoveryFinish) {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	if err = ipArtworkDiscoveryValidate(q, kind); err != nil {
		return out, err
	}
	digest, err := canonicalRequestDigest(kind, 1, q.identity, q)
	if err != nil {
		return out, err
	}
	out, err = ipArtworkDiscoveryLookup(ctx, turn, kind, q, digest)
	if err != nil || out.decision != receiptFresh {
		return out, err
	}
	if err = ipArtworkDiscoveryFresh(ctx, turn, q); err != nil {
		return out, err
	}
	o, v := q.owner, q.spec
	r := ipArtworkDiscoveryResult{stageID: q.stageID, revision: q.stageRevision + 1}
	if kind == IPArtworkDiscoveryClaim {
		if v.expectedCount == 0 && v.manifest != ipArtworkDiscoveryManifestDigest(o, v, nil) {
			return out, ErrConflict
		}
		row, e := ipArtworkDiscoveryRead(ctx, turn, ipADBegin, o.id, o.incarnation[:], v.provider, o.sourceIncarnation[:], o.sourceRevision,
			v.policyRevision, v.language, v.generation, v.expectedCount, v.expectedBytes, v.manifest[:])
		if e != nil {
			return out, e
		}
		if err = row.Scan(&r.stageID); err != nil {
			return out, err
		}
	} else {
		s, e := ipArtworkDiscoveryStageRead(ctx, turn, q)
		if e != nil {
			return out, e
		}
		if s.state != 1 || s.count != q.afterOrdinal {
			return out, ErrConflict
		}
		if kind == IPArtworkDiscoveryPage {
			// A prefix cannot silently overwrite an already present raw key.
			keys, e := ipArtworkDiscoveryKeys(ctx, turn, q.stageID, s.count-1, 1)
			if e != nil {
				return out, e
			}
			if len(keys) != 0 {
				return out, ErrConflict
			}
			var added int64
			for _, c := range q.candidates {
				b, e := ipArtworkDiscoveryCandidateBytes(c)
				if e != nil {
					return out, e
				}
				added += b
			}
			if added > v.expectedBytes-s.bytes {
				return out, ErrConflict
			}
			for _, c := range q.candidates {
				if err = ipArtworkDiscoveryWrite(ctx, turn, ipADAppend, q.stageID, c.ordinal, c.role, c.sourceOrdinal, c.imageID, c.origin, c.locale, c.attribution, c.score); err != nil {
					return out, err
				}
			}
			r.count = s.count + int64(len(q.candidates))
			err = ipArtworkDiscoveryWrite(ctx, turn, ipADAdvance, r.count, s.bytes+added, q.stageID, s.revision, s.count, s.bytes, v.expectedCount, v.expectedBytes)
		} else {
			if s.count != v.expectedCount || s.bytes != v.expectedBytes {
				return out, ErrConflict
			}
			keys, e := ipArtworkDiscoveryKeys(ctx, turn, q.stageID, -1, v.expectedCount+1)
			if e != nil {
				return out, e
			}
			if int64(len(keys)) != v.expectedCount {
				return out, ErrConflict
			}
			candidates := make([]ipArtworkDiscoveryCandidate, 0, len(keys))
			var actualBytes int64
			for n, key := range keys {
				if key != int64(n) {
					return out, ErrConflict
				}
				c, b, e := ipArtworkDiscoveryDecode(ctx, turn, q.stageID, key, v.expectedBytes-actualBytes)
				if e != nil {
					return out, e
				}
				actualBytes += b
				if actualBytes > v.expectedBytes {
					return out, ErrConflict
				}
				candidates = append(candidates, c)
			}
			if actualBytes != v.expectedBytes || ipArtworkDiscoveryManifestDigest(o, v, candidates) != v.manifest {
				return out, ErrConflict
			}
			r.count = s.count
			err = ipArtworkDiscoveryWrite(ctx, turn, ipADFinish, q.stageID, s.revision, s.count, s.bytes, v.expectedCount, v.expectedBytes)
		}
		if err != nil {
			return out, err
		}
	}
	r.afterOrdinal = r.count
	// Cover the original sequence and owner revision in the same transaction as
	// parent count/bytes/revision, child writes and immutable saved result.
	err = ipArtworkDiscoveryWrite(ctx, turn, ipADOwnerCAS, q.identity.Sequence, o.id, o.incarnation[:], q.identity.Sequence-1, q.identity.Sequence-1,
		o.revision, o.sourceIncarnation[:], o.sourceRevision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS, turn.transaction.writer.now().UnixMilli())
	if err != nil {
		return out, err
	}
	resultDigest, e := ipArtworkDiscoveryResultDigest(kind, q.identity, digest, r)
	if e != nil {
		return out, e
	}
	command := "discovery_claim"
	if kind == IPArtworkDiscoveryPage {
		command = "discovery_page"
	}
	if kind == IPArtworkDiscoveryFinish {
		command = "discovery_finish"
	}
	if err = ipArtworkDiscoveryWrite(ctx, turn, ipADSave, o.id, o.incarnation[:], q.identity.Sequence, q.identity.OperationID[:], digest[:],
		turn.transaction.writer.now().UnixMilli(), resultDigest[:], command, r.revision, r.count, r.afterOrdinal, r.stageID); err != nil {
		return out, err
	}
	out.result = r
	return out, nil
}

// Bounded retiring-stage body for purpose252, not an accepting maintenance entry.
// Outer252 still needs its receipt/proof/resource integration. The draft receipt
// CHECK lacks abandoned_reclaim and fields for retired bytes/terminal status;
// parent must request that hunk before implementing its accepting result codec.
// It must persist
// this exact result before W's terminal commit. Existing proof pins are checked
// natively BEFORE deleting anything, including ACKed-but-not-retired receipts.
// Source may already be obsolete: original stage source/policy/manifest equality
// is checked, while maintenance CAS fences the current exact owner incarnation
// and revision. Requiring a current source here would strand abandoned debt.
func ipArtworkDiscoveryReclaim(ctx context.Context, turn *commandTurn, q ipArtworkDiscoveryRequest) (out ipArtworkDiscoveryReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPAbandonedReclaim {
		return out, ErrInvalid
	}
	defer turn.transaction.guardPanic()
	if err = ipArtworkDiscoveryValidate(q, IPAbandonedReclaim); err != nil {
		return out, err
	}
	o := q.owner
	row, err := ipArtworkDiscoveryRead(ctx, turn, ipADMaintenanceOwner, o.id, o.incarnation[:], o.revision, o.entityID, o.personID)
	if err != nil {
		return out, err
	}
	var one int64
	if err = row.Scan(&one); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	row, err = ipArtworkDiscoveryRead(ctx, turn, ipADPin, q.stageID)
	if err != nil {
		return out, err
	}
	err = row.Scan(&one)
	if err == nil {
		return out, ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	s, err := ipArtworkDiscoveryStageRead(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if s.state != 3 || q.afterOrdinal != s.reclaimAfter+1 {
		return out, ErrConflict
	}
	keys, err := ipArtworkDiscoveryKeys(ctx, turn, q.stageID, s.reclaimAfter, ipArtworkDiscoveryChunk+1)
	if err != nil {
		return out, err
	}
	hasNext := len(keys) > ipArtworkDiscoveryChunk
	if hasNext {
		keys = keys[:ipArtworkDiscoveryChunk]
	}
	out.revision = s.revision + 1
	out.afterOrdinal = s.reclaimAfter
	out.done = !hasNext
	for _, key := range keys {
		_, b, e := ipArtworkDiscoveryDecode(ctx, turn, q.stageID, key, s.bytes-out.deletedBytes)
		if e != nil {
			return out, e
		}
		if err = ipArtworkDiscoveryWrite(ctx, turn, ipADDelete, q.stageID, key); err != nil {
			return out, err
		}
		out.deletedCount++
		out.deletedBytes += b
		out.afterOrdinal = key
	}
	if out.deletedCount > s.count || out.deletedBytes > s.bytes || (out.done && (out.deletedCount != s.count || out.deletedBytes != s.bytes)) {
		return out, ErrConflict
	}
	done := int64(0)
	if out.done {
		done = 1
	}
	if err = ipArtworkDiscoveryWrite(ctx, turn, ipADReclaim, out.deletedCount, out.deletedBytes, out.afterOrdinal, done, done,
		q.stageID, s.revision, s.count, s.bytes, s.reclaimAfter, turn.transaction.writer.now().UnixMilli()); err != nil {
		return out, err
	}
	if err = ipArtworkDiscoveryWrite(ctx, turn, ipADMaintenanceCAS, o.id, o.incarnation[:], o.revision, o.entityID, o.personID); err != nil {
		return out, err
	}
	return out, nil
}
