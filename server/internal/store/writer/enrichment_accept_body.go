package writer

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"time"
)

// Fresh domain body only. The SAME common dispatcher establishes current
// disclosure and resolves ORIGINAL history first, then alone publishes Applied
// after physical commit/finish. No callback, fixture permit or Outcome here.
func applyEnrichAccept(ctx context.Context, turn *commandTurn, input *enrichAcceptCommand) (out enrichAcceptResult, digest ResultDigest, err error) {
	defer func() {
		checked, e := finishCommand(ctx, commandLifetime(turn), struct {
			Result enrichAcceptResult
			Digest ResultDigest
		}{out, digest}, err)
		out, digest, err = checked.Result, checked.Digest, e
	}()
	if input == nil || !input.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != EnrichAccept {
		return out, digest, ErrInvalid
	}
	q, b := input.request, input.originalBegin
	owner, err := effectiveBeginOwnerFacts(ctx, turn, turn.steps[enrichmentEffectiveOwner].statementStep, b.request.ownerID)
	if err != nil {
		return out, digest, err
	}
	if owner != q.owner {
		return out, digest, ErrConflict
	}
	entity, err := effectiveBeginEntityFacts(ctx, turn, turn.steps[enrichmentEffectiveEntity].statementStep, q.entity.id)
	if err != nil {
		return out, digest, err
	}
	if entity != q.entity {
		return out, digest, ErrConflict
	}
	producer, err := effectiveBeginReadProducer(ctx, turn, b.request.ownerID, entity, owner.provider, b.request.evidenceID, b.request.publicationID, b.request.objectID)
	if err != nil {
		return out, digest, err
	}
	if producer != q.producer || !effectiveBeginProducerShape(producer, entity, owner, q.image.header) {
		return out, digest, ErrConflict
	}
	expires, err := parseEnrichAcceptLease(owner.leaseUntil)
	if err != nil {
		return out, digest, err
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return out, digest, err
	}
	if !now.Before(expires) {
		return out, digest, ErrConflict
	}
	proof, err := verifyEnrichAcceptSource(ctx, turn, input)
	if err != nil {
		return out, digest, err
	}
	if err = verifyEnrichAcceptImage(ctx, turn, input); err != nil {
		return out, digest, err
	}
	if err = checkEnrichAcceptVerifiedSource(ctx, turn, input, proof); err != nil {
		return out, digest, err
	}
	// Full pinner charge comes from the two ACTUAL immutable native proofs,
	// not a caller limit, SQLite maximum or a column-count multiplication.
	var retained int64
	for _, p := range []struct {
		id  stmt.ID
		ref effectiveBeginSealReference
	}{{enrichAcceptEvidenceBytes, b.sourceReferences.evidence}, {enrichAcceptSourceBytes, b.sourceReferences.source}} {
		n, e := enrichAcceptReturning(ctx, turn, p.id, p.ref.owner, p.ref.receipt.Identity.Incarnation[:], p.ref.receipt.Identity.Sequence)
		if e != nil {
			return out, digest, e
		}
		if n > maxSealRetainedBytes {
			return out, digest, ErrConflict
		}
		retained += n
	}
	if q.reservedBytes < retained {
		return out, digest, &Refusal{Code: RefusalCapacity}
	}
	// All native cohort/context proofs are complete BEFORE the first write.
	now, err = turn.transaction.clockTime()
	if err != nil {
		return out, digest, err
	}
	if !now.Before(expires) {
		return out, digest, ErrConflict
	}
	ms := now.UnixMilli()
	if ms < 0 {
		return out, digest, ErrInvalid
	}
	imageRevision, err := enrichAcceptReturning(ctx, turn, enrichAcceptSealImage, q.image.id, entity.id, q.image.revision)
	if err != nil {
		return out, digest, err
	}
	headArgs := []any{q.image.id, entity.id, entity.revision}
	head := enrichAcceptHeadEmpty
	if entity.imageID.Valid {
		head = enrichAcceptHeadReplace
		headArgs = append(headArgs, entity.imageID.Int64)
	}
	headArgs = append(headArgs, entity.incarnation[:], entity.sourceRevision, entity.identityRevision, entity.metadataRevision, entity.ownerRevision)
	acceptedRevision, err := enrichAcceptReturning(ctx, turn, head, headArgs...)
	if err != nil {
		return out, digest, err
	}
	if producer.CreditHead.Exists {
		err = execSealOne(ctx, turn, enrichAcceptSourceReplace, b.request.publicationID, entity.id, owner.provider, producer.CreditHead.PublicationID, producer.CreditHead.Revision)
	} else {
		err = execSealOne(ctx, turn, enrichAcceptSourceFirst, entity.id, owner.provider, b.request.publicationID, int64(1))
	}
	if err != nil {
		return out, digest, err
	}
	if q.prior.exists {
		p := q.prior
		if err = execSealOne(ctx, turn, enrichAcceptReleaseCurrent, nil, p.owner, p.incarnation[:], p.generation, p.image, p.pinUnits, p.reservedBytes); err != nil {
			return out, digest, err
		}
	}
	if err = execSealOne(ctx, turn, enrichAcceptOwnerFinish, input.identity.Sequence, nil, b.request.ownerID, input.identity.Incarnation[:], owner.generation, entity.id, owner.provider, q.image.id, owner.lastSequence, owner.acknowledgedSequence, owner.lease, owner.leaseUntil, b.request.publicationID, q.stageSlot, q.reservedBytes); err != nil {
		return out, digest, err
	}
	if q.prior.exists {
		p := q.prior
		if err = execSealOne(ctx, turn, enrichAcceptOldStage, q.stageSlot, p.owner, p.incarnation[:], p.generation); err != nil {
			return out, digest, err
		}
		if err = execSealOne(ctx, turn, enrichAcceptRetireImage, ms, p.image, entity.id); err != nil {
			return out, digest, err
		}
	}
	origin, err := readEnrichAcceptOrigin(ctx, turn, b.request.evidenceID)
	if err != nil {
		return out, digest, err
	}
	if err = execSealOne(ctx, turn, enrichAcceptOrigin, origin.subjectKind, origin.providerID, entity.id, "credits", origin.provider, "provider", origin.sourceURL, origin.confidence, origin.observedAt, origin.language, origin.region, producer.Selection.Revision, nil, b.request.evidenceID); err != nil {
		return out, digest, err
	}
	if err = execSealOne(ctx, turn, enrichAcceptSelection, b.request.evidenceID, entity.id, owner.provider, producer.Selection.Incarnation[:], producer.Selection.Revision, owner.selectedID); err != nil {
		return out, digest, err
	}
	s := b.sourceReferences.source
	if err = execSealOne(ctx, turn, enrichAcceptRetain, s.owner, s.receipt.Identity.Incarnation[:], s.receipt.Identity.Sequence, b.request.publicationID, entity.id, owner.provider); err != nil {
		return out, digest, err
	}
	out = enrichAcceptResult{ProviderOperationID: b.request.ownerID, EntityID: entity.id, AcceptedCatalogRevision: acceptedRevision, AppliedMS: ms, AfterItem: owner.afterItem, ImageID: q.image.id, ImageRevision: imageRevision, RetainedCount: int64(len(q.rows))}
	digest, err = enrichAcceptResultDigest(input.identity, input.digest, out)
	if err != nil {
		return out, digest, err
	}
	if err = execSealOne(ctx, turn, enrichAcceptReceipt, b.request.ownerID, input.identity.Incarnation[:], input.identity.Sequence, input.identity.OperationID[:], input.digest[:], "enrich_accept", acceptedRevision, ms, entity.id, owner.afterItem, int64(len(q.rows)), q.image.id, imageRevision, int64(len(q.rows)), digest[:], s.owner, s.receipt.Identity.Incarnation[:], s.receipt.Identity.Sequence, b.request.publicationID, owner.provider); err != nil {
		return out, digest, err
	}
	// Actual deletion, never ACK/status alone, discharges the older ordinary
	// BEGIN result's outgoing pin. The image and new result remain retained.
	step, e := sealStep(turn, enrichAcceptDropBegin)
	if e != nil {
		return out, digest, e
	}
	removed, e := turn.exec(ctx, step, b.request.ownerID, b.identity.Incarnation[:], b.identity.Sequence, b.digest[:], q.image.id, q.beginResult.Digest[:], s.owner, s.receipt.Identity.Incarnation[:], s.receipt.Identity.Sequence)
	if e != nil {
		return out, digest, e
	}
	n, e := removed.RowsAffected()
	if e != nil || n < 0 || n > 1 {
		return out, digest, errors.Join(ErrConflict, e)
	}
	if n == 1 {
		err = execSealOne(ctx, turn, enrichAcceptDropPin, s.owner, s.receipt.Identity.Incarnation[:], s.receipt.Identity.Sequence)
	}
	if err == nil {
		now, err = turn.transaction.clockTime()
		if err == nil && !now.Before(expires) {
			err = ErrConflict
		}
	}
	return out, digest, err
}

func verifyEnrichAcceptImage(ctx context.Context, turn *commandTurn, input *enrichAcceptCommand) error {
	q, b := input.request, input.originalBegin
	h := q.image.header
	s := b.sourceReferences.source
	args := []any{q.image.id, q.image.revision, h.entityID, h.entityIncarnation[:], h.sourceRevision, h.identityRevision, h.metadataRevision, h.ownerRevision, h.policyIncarnation[:], h.policyRevision, h.consentRevision, h.language, h.sourceManifest[:], b.digest[:], effectiveCreditManifestAlgorithm, h.expectedCount, h.expectedBytes, h.expectedManifest[:], q.image.stagedCount, q.image.stagedBytes, q.image.seed[:], q.image.chain[:], h.expectedCount - 1, s.owner, s.receipt.Identity.Incarnation[:], s.receipt.Identity.Sequence, b.request.publicationID, q.owner.provider}
	if err := enrichAcceptHasOne(ctx, turn, enrichAcceptImagePoint, args...); err != nil {
		return err
	}
	for _, guard := range []struct {
		id   stmt.ID
		args []any
	}{{enrichAcceptOwnerEntries, []any{q.entity.id, q.entity.incarnation[:], q.entity.ownerRevision}}, {enrichAcceptOtherProvider, []any{q.entity.id, q.owner.provider}}, {enrichAcceptNoExtra, []any{q.image.id, int64(len(q.rows))}}} {
		if err := enrichAcceptHasNone(ctx, turn, guard.id, guard.args...); err != nil {
			return err
		}
	}
	for _, r := range q.rows {
		id := enrichAcceptCreditScoped
		args := []any{b.request.publicationID, q.image.id, r.ordinal, r.entityID, r.roleID, r.departmentID, r.creditedName, r.provider, r.creditID, r.providerPersonID, r.sourceOrdinal, r.sourceRowID.String}
		if r.personID.Valid {
			id = enrichAcceptCreditPerson
			args = append(args, r.personID.Int64)
		}
		if err := enrichAcceptHasOne(ctx, turn, id, args...); err != nil {
			return err
		}
	}
	if !q.prior.exists {
		return enrichAcceptHasNone(ctx, turn, enrichAcceptNoCurrent, q.entity.id, q.owner.provider)
	}
	p := q.prior
	return enrichAcceptHasOne(ctx, turn, enrichAcceptOldContext, q.entity.id, q.owner.provider, p.owner, p.incarnation[:], p.generation, p.image, p.pinUnits, p.reservedBytes)
}

func enrichAcceptReturning(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (int64, error) {
	step, err := sealStep(t, id)
	if err != nil {
		return 0, err
	}
	row, err := t.queryRow(ctx, step, args...)
	if err != nil {
		return 0, err
	}
	var n int64
	if err = row.Scan(&n); err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, ErrConflict
	}
	return n, nil
}
func enrichAcceptHasOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	n, err := enrichAcceptReturning(ctx, t, id, args...)
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func enrichAcceptHasNone(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (err error) {
	step, err := sealStep(t, id)
	if err != nil {
		return err
	}
	rows, err := t.query(ctx, step, args...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if rows.Next() {
		return ErrConflict
	}
	return rows.Err()
}

func parseEnrichAcceptLease(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }

// Origin fields are read from the sealed evidence BEFORE the upsert. The closed
// grammar has no INSERT...SELECT, and the value set must be an immutable typed
// fact, not a destination-read expression.
type enrichAcceptOriginFacts struct {
	subjectKind, providerID, provider, sourceURL, observedAt, language, region string
	confidence                                                                 float64
}

func readEnrichAcceptOrigin(ctx context.Context, turn *commandTurn, evidence string) (out enrichAcceptOriginFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	step, err := sealStep(turn, enrichAcceptOriginSource)
	if err != nil {
		return out, err
	}
	row, err := turn.queryRow(ctx, step, evidence)
	if err != nil {
		return out, err
	}
	out.confidence = 1.0
	if err = row.Scan(&out.subjectKind, &out.providerID, &out.provider, &out.sourceURL, &out.observedAt, &out.language, &out.region); err != nil {
		return out, ErrResolveRequired
	}
	if out.subjectKind == "" || out.providerID == "" || out.provider == "" || out.sourceURL == "" || out.observedAt == "" || out.language == "" || out.region == "" {
		return out, ErrResolveRequired
	}
	return out, nil
}
