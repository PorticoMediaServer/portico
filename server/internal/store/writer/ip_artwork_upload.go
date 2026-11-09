package writer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"
	"unicode/utf8"
)

// 228 body. Owner upload acceptance is the atomic handoff of one already-encoded
// upload material set into an accepted artwork source and its locked owner
// choice. Current owner authority precedes historical receipt disclosure; the
// ORIGINAL saved result is served without any fresh source/lease/policy probe.
// A fresh command re-proves the retained subject job, the current entity/person
// artwork revision and policy, the completed native upload job, the installed
// material intent and its COMPLETE manifest, then publishes the source/blobs and
// writes exactly one locked selection row, its normalized history entry and pin,
// the subject artwork revision and this command's receipt.
//
// Every file was verified by the caller before this turn; nothing here touches
// the filesystem, a provider or the network. The request repeats the verified
// installed manifest, and the writer refuses any difference from the manifest
// IP-19 recorded, so a missing or changed file cannot become an accepted source.
//
// E016 v1 ownership rules, binding for this command:
//   - the owner upload writes choice='locked' and never a 'matched' observation;
//   - the previous locked choice's digest is pinned in history-artwork before it
//     is replaced, so the old source and its original bytes stay charged for
//     undo;
//   - artwork_material_slots and artwork_material_intents.source_digest stay
//     OUTSIDE that closure (IP-19 custody retained past ACK).

const ipArtworkUploadTrigger = "artwork_upload"

// The upload request reuses IP-20's exact publish declaration and manifest; it
// adds only the native upload provider label and the owner's selection
// observation. before is the exact current locked row as the caller saw it.
type ipArtworkUploadSelection struct {
	role   string
	actor  string
	before ipArtworkSelectionBefore
	after  ipArtworkSelectionFacts
}

type ipArtworkUploadRequest struct {
	owner     ipArtworkMaterialOwner
	spec      ipArtworkMaterialPublishSpec
	slots     []ipArtworkMaterialSlot
	provider  string
	selection ipArtworkUploadSelection
}

// The saved result is the accepted source head and the owner choice it locked.
// The blobs, source and material slots stay in their own tables and are never
// relabelled as a result or proof.
type ipArtworkUploadResult struct {
	IntentID, SourceDigest string
	Revision               int64
	Slots, Bytes           int64
	OriginalBytes          int64
	Role                   string
	SelectionRevision      int64
	Previous, Selected     string
	History                EvidenceInteger
	AppliedMS              int64
}

type ipArtworkUploadCommand struct {
	identity CommandIdentity
	request  ipArtworkUploadRequest
	digest   RequestDigest
	valid    bool
}

type ipArtworkUploadCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipArtworkUploadResult
	Digest  ResultDigest
}

type ipArtworkUploadOutcome struct {
	decision receiptDecision
	result   ipArtworkUploadResult
	digest   ResultDigest
}

type ipArtworkUploadSavedResult struct {
	Kind  CommandID
	Value ipArtworkUploadResult
}

func (c *ipArtworkUploadCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func ipArtworkUploadOriginalBytes(slots []ipArtworkMaterialSlot) int64 {
	if len(slots) == 4 {
		return slots[3].bytes
	}
	return 0
}

func ipArtworkUploadSelectionRevision(before ipArtworkSelectionBefore) int64 {
	if before.present {
		return before.revision + 1
	}
	return 1
}

// Every string in the retained request must be bounded before any digest or
// hold: a malformed manifest or selection is refused whole, never truncated.
func ipArtworkUploadRequestShape(identity CommandIdentity, q ipArtworkUploadRequest) error {
	if !ipPolicyCommandIdentityValid(identity) || q.provider != "upload" ||
		q.spec.expectedSlots != 3 && q.spec.expectedSlots != 4 {
		return ErrInvalid
	}
	if err := ipArtworkMaterialPublishRequestShape(identity, ipArtworkMaterialPublishRequest{
		owner: q.owner, spec: q.spec, slots: q.slots,
	}); err != nil {
		return err
	}
	s := q.selection
	if !ipArtworkSelectionRole(s.role) || len(s.actor) == 0 || len(s.actor) > ipTextActorMax ||
		!utf8.ValidString(s.actor) || !ipArtworkSelectionBeforeShape(s.before) ||
		!ipArtworkSelectionFactsShape(s.after) ||
		s.after.digest != q.spec.sourceDigest || s.after.provider != "upload" ||
		s.after.language != q.spec.language {
		return ErrInvalid
	}
	return nil
}

// Called under W preparation custody before any hold. No SQL, no allocation
// beyond the retained request copy and its canonical digest.
func newIPArtworkUploadCommand(identity CommandIdentity, in ipArtworkUploadRequest) (*ipArtworkUploadCommand, error) {
	if err := ipArtworkUploadRequestShape(identity, in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IPArtworkUploadAccept, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipArtworkUploadCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

// Current authority, historical receipt and original result are resolved in the
// same order as the other IP artwork bodies. The receipt is read by this
// command's OWN statement: only the frozen 'upload_accept' label maps to 228.
func ipArtworkUploadLookup(ctx context.Context, turn *commandTurn, q ipArtworkUploadRequest, identity CommandIdentity, digest RequestDigest) (out ipArtworkUploadOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPArtworkUploadAccept {
		return out, ErrInvalid
	}
	rows, err := ipTextRows(ctx, turn, ipAMOwner, q.owner.id)
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

	rows, err = ipTextRows(ctx, turn, ipAUReceiptOctets, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var intentBytes, roleBytes, selectedBytes, previousBytes, operationBytes, requestBytes, resultBytes, tokenBytes int64
	err = ipTextOne(rows, func() error {
		return rows.Scan(&intentBytes, &roleBytes, &selectedBytes, &previousBytes,
			&operationBytes, &requestBytes, &resultBytes, &tokenBytes)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil && (intentBytes < 1 || intentBytes > ipArtworkMaterialMaxIntent ||
		roleBytes < 1 || roleBytes > ipArtworkSelectionMaxRole || selectedBytes != 64 ||
		(previousBytes != 0 && previousBytes != 64) || operationBytes != 16 ||
		requestBytes != 32 || resultBytes != 32 || tokenBytes != 0) {
		return out, ErrResolveRequired
	}
	rows, err = ipTextRows(ctx, turn, ipAUReceipt, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var found receiptFacts
	var saved ipArtworkUploadResult
	var operation, request, result []byte
	var nativeKind, accepted, appliedMS, count, totalBytes, complete int64
	var leaseGeneration, leaseUntil, selectionRevision, storageRevision, history int64
	var intent, role, choice, selected, previous string
	err = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &nativeKind, &accepted, &appliedMS,
			&count, &totalBytes, &complete, &leaseGeneration, &leaseUntil, &intent,
			&role, &choice, &selectionRevision, &selected, &previous, &storageRevision,
			&history, &tokenBytes)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 ||
			CommandID(nativeKind) != IPArtworkUploadAccept ||
			accepted != ipArtworkMaterialPublishRevision || appliedMS < 0 ||
			count != q.spec.expectedSlots || count < 3 || count > 4 ||
			totalBytes != ipArtworkMaterialSlotBytes(q.slots) || totalBytes < 1 ||
			totalBytes > ipArtworkMaterialMaxBytes || complete != 1 ||
			leaseGeneration <= 0 || leaseUntil <= 0 || intent != q.spec.intentID ||
			int64(len(intent)) != intentBytes || int64(len(intent)) > ipArtworkMaterialMaxIntent ||
			role != q.selection.role || int64(len(role)) != roleBytes ||
			choice != "locked" || selectionRevision != ipArtworkUploadSelectionRevision(q.selection.before) ||
			selected != q.spec.sourceDigest || int64(len(selected)) != selectedBytes ||
			previous != q.selection.before.facts.digest ||
			(previous != "" && int64(len(previous)) != previousBytes) ||
			storageRevision != 1 || history <= 0 || tokenBytes != 0 {
			return out, ErrResolveRequired
		}
		found.exists = true
		found.kind = IPArtworkUploadAccept
		found.resultCount = count
		actual := receiptIdentity{command: identity}
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], request)
		found.identity = actual
		copy(found.resultDigest[:], result)
		saved = ipArtworkUploadResult{
			IntentID: intent, SourceDigest: selected, Revision: accepted,
			Slots: count, Bytes: totalBytes,
			OriginalBytes: ipArtworkUploadOriginalBytes(q.slots), Role: role,
			SelectionRevision: selectionRevision, Previous: previous, Selected: selected,
			History: EvidenceInteger{Value: history, Present: true}, AppliedMS: appliedMS,
		}
	}
	out.decision, err = classifyReceipt(receiptIdentity{identity, digest}, IPArtworkUploadAccept, owner, found)
	if err != nil {
		return out, err
	}
	if out.decision != receiptReplay {
		return out, nil
	}
	if saved.IntentID != q.spec.intentID || saved.SourceDigest != q.spec.sourceDigest ||
		saved.Slots != q.spec.expectedSlots || saved.Bytes != ipArtworkMaterialSlotBytes(q.slots) ||
		saved.OriginalBytes != ipArtworkUploadOriginalBytes(q.slots) ||
		saved.Role != q.selection.role || saved.Previous != q.selection.before.facts.digest ||
		saved.SelectionRevision != ipArtworkUploadSelectionRevision(q.selection.before) {
		return out, ErrResolveRequired
	}
	expected, e := ipArtworkUploadResultDigest(identity, digest, saved)
	if e != nil || !bytes.Equal(expected[:], result) {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.result, out.digest = saved, expected
	return out, nil
}

// The retained subject job, current source/policy and the exact completed native
// upload job, installed intent and complete manifest are re-proved before any
// source, blob or selection byte is written.
func ipArtworkUploadFresh(ctx context.Context, turn *commandTurn, q ipArtworkUploadRequest, nowMS int64) (total int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), total, err)
		total, err = v, e
	}()
	if err = ipArtworkMaterialFreshSource(ctx, turn, ipArtworkMaterialRequest{
		owner: q.owner,
		spec:  ipArtworkMaterialSpec{language: q.spec.language, policyRevision: q.spec.policyRevision},
	}, nowMS); err != nil {
		return 0, err
	}
	namespace, subject := ipArtworkSelectionNamespace(q.owner)
	rows, err := ipTextRows(ctx, turn, ipAUJob, q.spec.intentID, q.owner.id, namespace, subject, q.selection.role)
	if err != nil {
		return 0, err
	}
	var one int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&one) }); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if err = ipArtworkMaterialPublishIntent(ctx, turn, ipArtworkMaterialPublishRequest{
		owner: q.owner, spec: q.spec, slots: q.slots,
	}); err != nil {
		return 0, err
	}
	return ipArtworkMaterialManifest(ctx, turn, q.spec.intentID, q.slots)
}

func applyIPArtworkUpload(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipPolicyBudget, c *ipArtworkUploadCommand) (out ipArtworkUploadCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipArtworkUploadCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != IPArtworkUploadAccept {
		return zero, ErrInvalid
	}
	q := c.request
	if err = ipArtworkUploadRequestShape(c.identity, q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(IPArtworkUploadAccept, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipArtworkUploadLookup(ctx, turn, q, c.identity, c.digest)
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
	total, err := ipArtworkUploadFresh(ctx, turn, q, appliedMS)
	if err != nil {
		return zero, err
	}
	value, err := ipArtworkUploadBody(ctx, turn, q, appliedMS, total)
	if err != nil {
		return zero, err
	}
	digest, err := ipArtworkUploadResultDigest(c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	if err = ipArtworkUploadRecord(ctx, turn, q, c.identity, c.digest, digest, value, appliedMS); err != nil {
		return zero, err
	}
	saved, err := ipArtworkUploadLookup(ctx, turn, q, c.identity, c.digest)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.result != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.result, saved.digest, Applied
	return out, nil
}

// ipArtworkUploadBody publishes the installed upload material into an accepted
// source and its blobs, then writes exactly one locked owner choice with its
// normalized history and pin. The previous locked digest is pinned before it is
// replaced, so old source bytes stay charged for undo.
func ipArtworkUploadBody(ctx context.Context, turn *commandTurn, q ipArtworkUploadRequest, nowMS, total int64) (out ipArtworkUploadResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	published, err := ipArtworkMaterialPublishBody(ctx, turn, ipArtworkMaterialPublishRequest{
		owner: q.owner, spec: q.spec, slots: q.slots,
	}, nowMS, total)
	if err != nil {
		return out, err
	}
	if published.IntentID != q.spec.intentID || published.Slots != q.spec.expectedSlots ||
		published.Bytes != total || int64(len(q.slots)) != q.spec.expectedSlots {
		return out, ErrResolveRequired
	}
	selection := q.selection
	request := ipArtworkSelectionRequest{
		owner: q.owner, role: selection.role, language: q.spec.language,
		policyRevision: q.spec.policyRevision, actor: selection.actor,
		before: selection.before, after: selection.after,
	}
	before, err := ipArtworkSelectionRead(ctx, turn, request)
	if err != nil {
		return out, err
	}
	if before != selection.before {
		return out, ErrConflict
	}
	if err = ipArtworkUploadSourceReady(ctx, turn, q.spec.sourceDigest); err != nil {
		return out, err
	}
	after := ipArtworkSelectionBefore{
		present: true, revision: ipArtworkUploadSelectionRevision(before),
		actor: selection.actor, facts: selection.after,
	}
	namespace, subject := ipArtworkSelectionNamespace(q.owner)
	stamp := time.UnixMilli(nowMS).UTC().Format(time.RFC3339Nano)
	if before.present {
		if err = ipTextExecOne(ctx, turn, ipASSelectionUpdate, after.revision,
			ipArtworkSelectionDigestValue(after.facts.digest), after.facts.provider,
			after.facts.imageID, after.facts.origin, after.facts.attribution,
			after.facts.locale, after.facts.language, ipArtworkSelectionFence(after.facts),
			after.actor, ipArtworkSelectionObject(after.facts.objectID),
			ipArtworkSelectionObject(after.facts.objectRevision), stamp, namespace, subject,
			selection.role, before.revision, ipArtworkSelectionDigestValue(before.facts.digest)); err != nil {
			return out, err
		}
	} else if err = ipTextExecOne(ctx, turn, ipASSelectionInsert, namespace, subject,
		selection.role, after.revision, ipArtworkSelectionDigestValue(after.facts.digest),
		after.facts.provider, after.facts.imageID, after.facts.origin, after.facts.attribution,
		after.facts.locale, after.facts.language, ipArtworkSelectionFence(after.facts),
		after.actor, ipArtworkSelectionObject(after.facts.objectID),
		ipArtworkSelectionObject(after.facts.objectRevision), stamp); err != nil {
		return out, err
	}
	if q.owner.entityID > 0 {
		err = ipTextExecOne(ctx, turn, ipASEntityBump, q.owner.entityID,
			q.owner.sourceIncarnation[:], q.owner.sourceRevision)
	} else {
		err = ipTextExecOne(ctx, turn, ipASPersonBump, q.owner.personID,
			q.owner.sourceIncarnation[:], q.owner.sourceRevision)
	}
	if err != nil {
		return out, err
	}
	beforeJSON, err := json.Marshal(ipArtworkSelectionAuditValue(selection.role, before.actor, before))
	if err != nil {
		return out, err
	}
	afterJSON, err := json.Marshal(ipArtworkSelectionAuditValue(selection.role, after.actor, after))
	if err != nil {
		return out, err
	}
	rows, err := ipTextRows(ctx, turn, ipASHistory, namespace, subject,
		strconv.FormatInt(q.owner.sourceRevision, 10),
		strconv.FormatInt(q.owner.sourceRevision+1, 10),
		ipArtworkUploadTrigger, selection.actor, stamp, string(beforeJSON), string(afterJSON))
	if err != nil {
		return out, err
	}
	var history int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&history) }); err != nil || history <= 0 {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if before.present && before.facts.digest != "" {
		if err = ipTextExecOne(ctx, turn, ipASHistoryPin, history, selection.role,
			before.facts.digest); err != nil {
			return out, err
		}
	}
	if after.facts.digest != before.facts.digest {
		if err = ipTextExecOne(ctx, turn, ipASHistoryPin, history, selection.role,
			after.facts.digest); err != nil {
			return out, err
		}
	}
	return ipArtworkUploadResult{
		IntentID: q.spec.intentID, SourceDigest: q.spec.sourceDigest,
		Revision: published.Revision, Slots: published.Slots, Bytes: published.Bytes,
		OriginalBytes: ipArtworkUploadOriginalBytes(q.slots), Role: selection.role,
		SelectionRevision: after.revision, Previous: before.facts.digest,
		Selected: after.facts.digest,
		History:  EvidenceInteger{Value: history, Present: true}, AppliedMS: nowMS,
	}, nil
}

// ipArtworkUploadSourceReady proves the just-published source is ready custody:
// all three encoded blobs are present and ready and the source is not retiring.
// It uses IP-21's point statement; the local helper name avoids depending on the
// production readiness helper that currently shares a name with a test seed.
func ipArtworkUploadSourceReady(ctx context.Context, turn *commandTurn, digest string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	rows, err := ipTextRows(ctx, turn, ipASSourceReady, digest)
	if err != nil {
		return err
	}
	var one int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&one) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if one != 1 {
		return ErrConflict
	}
	return nil
}

// The owner CAS is the command allowance and the receipt is saved in the same
// transaction. ACK alone does not retire the pinned selection, history, source
// or its original bytes.
func ipArtworkUploadRecord(ctx context.Context, turn *commandTurn, q ipArtworkUploadRequest, identity CommandIdentity, request RequestDigest, result ResultDigest, value ipArtworkUploadResult, appliedMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	o := q.owner
	if err = ipTextExecOne(ctx, turn, ipAMOwnerCAS, identity.Sequence, o.id, o.incarnation[:],
		o.revision, identity.Sequence-1, identity.Sequence-1, o.entityID, o.personID,
		o.sourceIncarnation[:], o.sourceRevision, o.leaseOwner, o.leaseGeneration,
		o.leaseUntilMS, appliedMS); err != nil {
		return err
	}
	previous := any(nil)
	if value.Previous != "" {
		previous = value.Previous
	}
	return ipTextExecOne(ctx, turn, ipAUSave, o.id, o.incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], appliedMS, result[:], value.Revision, value.Slots,
		value.Bytes, o.leaseGeneration, o.leaseUntilMS, value.IntentID, value.Role,
		value.SelectionRevision, value.Selected, previous, value.History.Value)
}

func ipArtworkUploadResultDigest(identity CommandIdentity, request RequestDigest, value ipArtworkUploadResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipArtworkUploadSavedResult{IPArtworkUploadAccept, value})
}

// W result wire: shared identity/request prefix, the native purpose and the
// actual command variant, then the accepted source head and the locked owner
// choice. Source, blob and material rows are never relabelled as a result.
func preflightIPArtworkUploadResult(v ipArtworkUploadSavedResult) error {
	r := v.Value
	if v.Kind != IPArtworkUploadAccept || len(r.IntentID) == 0 ||
		len(r.IntentID) > ipArtworkMaterialMaxIntent || !utf8.ValidString(r.IntentID) ||
		!ipArtworkSelectionMaybeDigest(r.SourceDigest) || r.SourceDigest == "" ||
		r.Revision != ipArtworkMaterialPublishRevision ||
		(r.Slots != 3 && r.Slots != 4) || r.Bytes < 1 || r.Bytes > ipArtworkMaterialMaxBytes ||
		r.OriginalBytes < 0 || (r.Slots == 4) != (r.OriginalBytes > 0) ||
		!ipArtworkSelectionRole(r.Role) || r.SelectionRevision <= 0 ||
		!ipArtworkSelectionMaybeDigest(r.Previous) || r.Selected != r.SourceDigest ||
		!r.History.Present || r.History.Value <= 0 || r.AppliedMS < 0 {
		return ErrInvalid
	}
	return nil
}

func writeIPArtworkUploadResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipArtworkUploadSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_artwork_upload")
	e.text("accept")
	e.text("applied")
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	r := v.Value
	e.text(r.IntentID)
	e.text(r.SourceDigest)
	for _, n := range []int64{r.Revision, r.Slots, r.Bytes, r.OriginalBytes, r.SelectionRevision} {
		e.number(uint64(n))
	}
	e.text(r.Role)
	e.text(r.Previous)
	e.text(r.Selected)
	e.flag(r.History.Present)
	e.number(uint64(r.History.Value))
	e.number(uint64(r.AppliedMS))
	return e.err
}
