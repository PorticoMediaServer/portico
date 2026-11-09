package writer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"golang.org/x/text/language"
)

// 226/227 bodies: one owner choice for one artwork category. Current owner
// authority precedes historical receipt disclosure; the ORIGINAL saved result is
// served without any fresh source/lease/policy probe. A fresh command re-proves
// the retained subject job, the accepted artwork source, the resolved language
// and the current policy, then writes exactly one selection row, its normalized
// history entry and pin, the subject artwork revision and this command's receipt.
//
// E016 v1 ownership rules, binding for this command:
//   - every owner select writes choice='locked'; the automatic 'matched' row that
//     discovery may write beneath it is never treated as the owner's choice;
//   - a locked-empty choice is a persistent locked row whose digest is NULL, not
//     a deletion; it keeps no history-artwork pin because there is no digest;
//   - the v1 closure is locked non-NULL selections plus their history-artwork
//     pins. artwork_material_slots and artwork_material_intents.source_digest stay
//     OUTSIDE that closure (IP-19 custody);
//   - the legacy actor column is provenance, never ownership evidence, and its
//     existing query is untouched;
//   - clear/unlock deletes only the locked row; it never deletes a selected,
//     matched, installed, unpublished or retiring source, blob or material slot.
//
// No file, image, provider or network IO happens inside this hold.

const (
	// The empty choice has no provider object; its stored fence says the owner
	// deliberately chose nothing rather than that discovery has not run.
	ipArtworkSelectionEmptyFence = "owner-empty"

	ipArtworkSelectionMaxField    = 4096
	ipArtworkSelectionMaxText     = 16 << 10
	ipArtworkSelectionMaxRole     = 64
	ipArtworkSelectionMaxLanguage = 64
)

// One locked selection's provenance. digest=="" means an explicit locked-empty
// choice; every other field is then empty and the row stores a NULL digest.
type ipArtworkSelectionFacts struct {
	digest, provider, imageID, origin, attribution string
	locale, language, sourceFence                  string
	objectID, objectRevision                       EvidenceText
}

// The exact current locked row as the caller last observed it. An absent row is
// distinct from a locked-empty row (present with an empty digest).
type ipArtworkSelectionBefore struct {
	present  bool
	revision int64
	actor    string
	facts    ipArtworkSelectionFacts
}

type ipArtworkSelectionRequest struct {
	owner          ipArtworkMaterialOwner
	role, language string
	policyRevision int64
	actor          string
	before         ipArtworkSelectionBefore
	after          ipArtworkSelectionFacts
}

type ipArtworkSelectionCommand struct {
	identity CommandIdentity
	kind     CommandID
	request  ipArtworkSelectionRequest
	digest   RequestDigest
	valid    bool
}

type ipArtworkSelectionCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipArtworkSelectionResult
	Digest  ResultDigest
}

type ipArtworkSelectionOutcome struct {
	decision receiptDecision
	result   ipArtworkSelectionResult
	digest   ResultDigest
}

// The saved result is the exact owner choice and its history pointer. The source
// and blob rows stay in their own tables and are never relabelled as a result.
type ipArtworkSelectionResult struct {
	OwnerID            int64
	Role, Choice       string
	SelectionRevision  int64
	Previous, Selected string
	History            EvidenceInteger
	Count              int64
	AppliedMS          int64
}

type ipArtworkSelectionSavedResult struct {
	Kind  CommandID
	Value ipArtworkSelectionResult
}

func (c *ipArtworkSelectionCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func ipArtworkSelectionKind(kind CommandID) bool {
	return kind == IPArtworkSelect || kind == IPArtworkClear
}

func ipArtworkSelectionCommandKind(kind CommandID) string {
	switch kind {
	case IPArtworkSelect:
		return "select"
	case IPArtworkClear:
		return "clear"
	}
	return ""
}

func ipArtworkSelectionCommandName(kind CommandID) string {
	switch kind {
	case IPArtworkSelect:
		return "artwork_select"
	case IPArtworkClear:
		return "artwork_clear"
	}
	return ""
}

func ipArtworkSelectionNamespace(o ipArtworkMaterialOwner) (string, int64) {
	if o.entityID > 0 {
		return "entity", o.entityID
	}
	return "person", o.personID
}

func ipArtworkSelectionRole(role string) bool {
	return ipArtworkDiscoveryRole(role)
}

// 64 lowercase hex characters, or empty for the locked-empty choice.
func ipArtworkSelectionMaybeDigest(digest string) bool {
	if digest == "" {
		return true
	}
	if len(digest) != 64 {
		return false
	}
	for _, c := range digest {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func ipArtworkSelectionLanguage(value string) bool {
	if value == "" {
		return true
	}
	return ipArtworkDiscoveryText(value, ipArtworkSelectionMaxLanguage)
}

// The locked-empty row stores a stable fence so it is distinguishable from an
// absent row without inventing a digest. The caller's empty facts are normalized
// to this value on write and back to empty on read.
func ipArtworkSelectionFence(facts ipArtworkSelectionFacts) string {
	if facts.digest == "" {
		return ipArtworkSelectionEmptyFence
	}
	return facts.sourceFence
}

// factsShape validates one complete locked-selection provenance value. An empty
// digest is the explicit locked-empty choice and must carry no other fact.
func ipArtworkSelectionFactsShape(v ipArtworkSelectionFacts) bool {
	if !ipArtworkSelectionMaybeDigest(v.digest) ||
		!ipArtworkDiscoveryText(v.provider, ipArtworkSelectionMaxRole) ||
		!ipArtworkDiscoveryText(v.imageID, ipArtworkSelectionMaxField) ||
		!ipArtworkDiscoveryText(v.origin, ipArtworkSelectionMaxField) ||
		!ipArtworkDiscoveryText(v.attribution, ipArtworkSelectionMaxText) ||
		!ipArtworkDiscoveryText(v.locale, ipArtworkSelectionMaxLanguage) ||
		!ipArtworkDiscoveryText(v.language, ipArtworkSelectionMaxLanguage) ||
		!ipArtworkDiscoveryText(v.sourceFence, ipArtworkSelectionMaxField) {
		return false
	}
	if v.objectID.Present != v.objectRevision.Present {
		return false
	}
	if v.objectID.Present {
		if !ipArtworkDiscoveryText(v.objectID.Value, ipArtworkSelectionMaxField) || v.objectID.Value == "" ||
			!ipArtworkDiscoveryText(v.objectRevision.Value, ipArtworkSelectionMaxField) || v.objectRevision.Value == "" {
			return false
		}
	} else if v.objectID.Value != "" || v.objectRevision.Value != "" {
		return false
	}
	if !ipArtworkSelectionLanguage(v.locale) || !ipArtworkSelectionLanguage(v.language) {
		return false
	}
	if v.locale != "" {
		if _, err := language.Parse(v.locale); err != nil {
			return false
		}
	}
	if v.language != "" {
		if _, err := language.Parse(v.language); err != nil {
			return false
		}
	}
	if v.digest == "" {
		return v == ipArtworkSelectionFacts{}
	}
	switch v.provider {
	case "tmdb", "tvdb", "upload":
	default:
		return false
	}
	return v.imageID != "" && v.origin != "" && v.sourceFence != ""
}

func ipArtworkSelectionBeforeShape(b ipArtworkSelectionBefore) bool {
	if !b.present {
		return b.revision == 0 && b.actor == "" && b.facts == (ipArtworkSelectionFacts{})
	}
	return b.revision > 0 && b.revision < math.MaxInt64 && len(b.actor) > 0 &&
		len(b.actor) <= ipTextActorMax && utf8.ValidString(b.actor) &&
		ipArtworkSelectionFactsShape(b.facts)
}

func ipArtworkSelectionRequestShape(kind CommandID, identity CommandIdentity, q ipArtworkSelectionRequest) error {
	if !ipArtworkSelectionKind(kind) || !ipPolicyCommandIdentityValid(identity) {
		return ErrInvalid
	}
	o := q.owner
	if o.id <= 0 || o.incarnation != identity.Incarnation || (o.entityID > 0) == (o.personID > 0) ||
		o.entityID < 0 || o.personID < 0 || o.sourceIncarnation == [16]byte{} || o.sourceRevision <= 0 ||
		o.revision <= 0 || o.revision == math.MaxInt64 || o.leaseOwner <= 0 || o.leaseGeneration <= 0 ||
		o.leaseUntilMS <= 0 || !ipArtworkSelectionRole(q.role) || q.policyRevision <= 0 ||
		!ipArtworkSelectionLanguage(q.language) || len(q.actor) == 0 || len(q.actor) > ipTextActorMax ||
		!utf8.ValidString(q.actor) || !ipArtworkSelectionBeforeShape(q.before) {
		return ErrInvalid
	}
	if kind == IPArtworkClear {
		if q.after != (ipArtworkSelectionFacts{}) {
			return ErrInvalid
		}
		return nil
	}
	if !ipArtworkSelectionFactsShape(q.after) {
		return ErrInvalid
	}
	// A non-empty locked choice records the language it was chosen under; a
	// locked-empty choice records none. Both are re-derived by the writer from
	// current settings before acceptance.
	if q.after.digest != "" && q.after.language != q.language {
		return ErrInvalid
	}
	return nil
}

// Called under W preparation custody before any hold. No SQL, no allocation
// beyond the retained request copy and its canonical digest.
func newIPArtworkSelectionCommand(kind CommandID, identity CommandIdentity, in ipArtworkSelectionRequest) (*ipArtworkSelectionCommand, error) {
	if err := ipArtworkSelectionRequestShape(kind, identity, in); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(kind, 1, identity, in)
	if err != nil {
		return nil, err
	}
	return &ipArtworkSelectionCommand{identity: identity, kind: kind, request: in, digest: digest, valid: true}, nil
}

// Current authority, historical receipt and original result are resolved in the
// same order as the other IP artwork bodies. The receipt is read by this
// command's OWN statement: only the frozen 'select'/'clear' labels map to 226/227.
func ipArtworkSelectionLookup(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkSelectionRequest, identity CommandIdentity, digest RequestDigest) (out ipArtworkSelectionOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != kind {
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

	rows, err = ipTextRows(ctx, turn, ipASReceiptOctets, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var roleBytes, selectedBytes, previousBytes int64
	err = ipTextOne(rows, func() error { return rows.Scan(&roleBytes, &selectedBytes, &previousBytes) })
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil && (roleBytes < 1 || roleBytes > ipArtworkSelectionMaxRole ||
		(selectedBytes != 0 && selectedBytes != 64) || (previousBytes != 0 && previousBytes != 64)) {
		return out, ErrResolveRequired
	}
	rows, err = ipTextRows(ctx, turn, ipASReceipt, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var found receiptFacts
	var saved ipArtworkSelectionResult
	var operation, request, result []byte
	var nativeKind, accepted, appliedMS, count, selectionRevision int64
	var role, choice, selected, previous string
	var history sql.NullInt64
	err = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &nativeKind, &accepted, &appliedMS, &count,
			&role, &choice, &selectionRevision, &selected, &previous, &history)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 ||
			CommandID(nativeKind) != kind || accepted <= 0 || appliedMS < 0 ||
			count < 0 || count > 1 || role != q.role || choice != "locked" ||
			selectionRevision != accepted || !ipArtworkSelectionMaybeDigest(selected) ||
			!ipArtworkSelectionMaybeDigest(previous) ||
			int64(len(role)) != roleBytes || int64(len(selected)) != selectedBytes ||
			int64(len(previous)) != previousBytes || history.Valid != (count == 1) ||
			(history.Valid && history.Int64 <= 0) ||
			(kind == IPArtworkClear && selected != "") ||
			(count == 0 && selected != previous) {
			return out, ErrResolveRequired
		}
		found.exists = true
		found.kind = kind
		found.resultCount = count
		actual := receiptIdentity{command: identity}
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], request)
		found.identity = actual
		copy(found.resultDigest[:], result)
		saved = ipArtworkSelectionResult{
			OwnerID: q.owner.id, Role: role, Choice: choice, SelectionRevision: accepted,
			Previous: previous, Selected: selected, Count: count, AppliedMS: appliedMS,
			History: EvidenceInteger{Value: history.Int64, Present: history.Valid},
		}
	}
	out.decision, err = classifyReceipt(receiptIdentity{identity, digest}, kind, owner, found)
	if err != nil {
		return out, err
	}
	if out.decision != receiptReplay {
		return out, nil
	}
	if saved.Previous != q.before.facts.digest ||
		(kind == IPArtworkSelect && saved.Selected != q.after.digest) ||
		(kind == IPArtworkClear && saved.Selected != "") {
		return out, ErrResolveRequired
	}
	expected, e := ipArtworkSelectionResultDigest(kind, identity, digest, saved)
	if e != nil || !bytes.Equal(expected[:], found.resultDigest[:]) {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.result, out.digest = saved, expected
	return out, nil
}

// The complete current locked row for the exact kind/subject/role. A missing row
// is successful absence; a corrupted row is refused rather than compared loosely.
func ipArtworkSelectionRead(ctx context.Context, turn *commandTurn, q ipArtworkSelectionRequest) (out ipArtworkSelectionBefore, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	ns, subject := ipArtworkSelectionNamespace(q.owner)
	rows, err := ipTextRows(ctx, turn, ipASSelectionRead, ns, subject, q.role)
	if err != nil {
		return out, err
	}
	var revision int64
	var digest, objectID, objectRevision sql.NullString
	var facts ipArtworkSelectionFacts
	err = ipTextOne(rows, func() error {
		return rows.Scan(&revision, &digest, &facts.provider, &facts.imageID, &facts.origin,
			&facts.attribution, &facts.locale, &facts.language, &facts.sourceFence, &out.actor,
			&objectID, &objectRevision)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ipArtworkSelectionBefore{}, nil
	}
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	facts.digest = digest.String
	if !digest.Valid {
		if facts.sourceFence != ipArtworkSelectionEmptyFence {
			return ipArtworkSelectionBefore{}, ErrResolveRequired
		}
		facts.sourceFence = ""
	}
	facts.objectID = EvidenceText{Value: objectID.String, Present: objectID.Valid}
	facts.objectRevision = EvidenceText{Value: objectRevision.String, Present: objectRevision.Valid}
	out.present = true
	out.revision = revision
	out.facts = facts
	if revision <= 0 || revision == math.MaxInt64 || len(out.actor) == 0 || len(out.actor) > ipTextActorMax ||
		!ipArtworkSelectionFactsShape(facts) {
		return ipArtworkSelectionBefore{}, ErrResolveRequired
	}
	return out, nil
}

func ipArtworkSelectionSame(before ipArtworkSelectionBefore, after ipArtworkSelectionFacts) bool {
	// Observation time and actor do not make a repeated choice a new effect.
	return before.present && before.facts == after
}

// The exact selected source must still be ready content custody: all three
// slots present and ready, and the source not marked for retirement. This is not
// a provider freshness claim; policy/consent/source were proved separately.
func ipArtworkSelectionSource(ctx context.Context, turn *commandTurn, digest string) (err error) {
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

// applyIPArtworkSelection is the closed command body. A fresh request runs the
// selection/history/pin/revision/receipt cohort; a saved receipt returns its
// exact original result even after the source, lease or policy moved.
func applyIPArtworkSelection(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipPolicyBudget, c *ipArtworkSelectionCommand) (out ipArtworkSelectionCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipArtworkSelectionCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != c.kind {
		return zero, ErrInvalid
	}
	q := c.request
	if err = ipArtworkSelectionRequestShape(c.kind, c.identity, q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(c.kind, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipArtworkSelectionLookup(ctx, turn, c.kind, q, c.identity, c.digest)
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
	if err = ipArtworkMaterialFreshSource(ctx, turn, ipArtworkMaterialRequest{
		owner: q.owner,
		spec:  ipArtworkMaterialSpec{language: q.language, policyRevision: q.policyRevision},
	}, appliedMS); err != nil {
		return zero, err
	}
	value, err := ipArtworkSelectionApply(ctx, turn, c.kind, q, appliedMS)
	if err != nil {
		return zero, err
	}
	digest, err := ipArtworkSelectionResultDigest(c.kind, c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	if err = ipArtworkSelectionRecord(ctx, turn, c.kind, q, c.identity, c.digest, digest, value, appliedMS); err != nil {
		return zero, err
	}
	saved, err := ipArtworkSelectionLookup(ctx, turn, c.kind, q, c.identity, c.digest)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.result != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.result, saved.digest, Applied
	return out, nil
}

// ipArtworkSelectionApply re-reads the exact current locked row, refuses a
// mismatch, then writes only the requested category. Clear deletes the locked
// row alone: a matched row, sources, blobs and installed material stay charged.
func ipArtworkSelectionApply(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkSelectionRequest, appliedMS int64) (out ipArtworkSelectionResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	if !ipArtworkSelectionKind(kind) || appliedMS < 0 {
		return out, ErrInvalid
	}
	before, err := ipArtworkSelectionRead(ctx, turn, q)
	if err != nil {
		return out, err
	}
	if before != q.before {
		return out, ErrConflict
	}
	changed := false
	switch kind {
	case IPArtworkSelect:
		changed = !ipArtworkSelectionSame(q.before, q.after)
	case IPArtworkClear:
		changed = q.before.present
	}
	value := ipArtworkSelectionResult{
		OwnerID: q.owner.id, Role: q.role, Choice: "locked",
		Previous: q.before.facts.digest, AppliedMS: appliedMS,
	}
	if kind == IPArtworkSelect {
		value.Selected = q.after.digest
	}
	if !changed {
		value.SelectionRevision = q.before.revision
		if value.SelectionRevision == 0 {
			value.SelectionRevision = q.owner.sourceRevision
		}
		return value, nil
	}
	if kind == IPArtworkSelect && q.after.digest != "" {
		if err = ipArtworkSelectionSource(ctx, turn, q.after.digest); err != nil {
			return out, err
		}
	}
	after := ipArtworkSelectionBefore{}
	if kind == IPArtworkSelect {
		after = ipArtworkSelectionBefore{present: true, revision: q.before.revision + 1, actor: q.actor, facts: q.after}
	}
	ns, subject := ipArtworkSelectionNamespace(q.owner)
	stamp := ipArtworkSelectionStamp(appliedMS)
	switch kind {
	case IPArtworkSelect:
		if q.before.present {
			if err = ipTextExecOne(ctx, turn, ipASSelectionUpdate, after.revision,
				ipArtworkSelectionDigestValue(after.facts.digest), after.facts.provider, after.facts.imageID,
				after.facts.origin, after.facts.attribution, after.facts.locale, after.facts.language,
				ipArtworkSelectionFence(after.facts), after.actor, ipArtworkSelectionObject(after.facts.objectID),
				ipArtworkSelectionObject(after.facts.objectRevision), stamp, ns, subject, q.role,
				q.before.revision, ipArtworkSelectionDigestValue(q.before.facts.digest)); err != nil {
				return out, err
			}
		} else if err = ipTextExecOne(ctx, turn, ipASSelectionInsert, ns, subject, q.role, after.revision,
			ipArtworkSelectionDigestValue(after.facts.digest), after.facts.provider, after.facts.imageID,
			after.facts.origin, after.facts.attribution, after.facts.locale, after.facts.language,
			ipArtworkSelectionFence(after.facts), after.actor, ipArtworkSelectionObject(after.facts.objectID),
			ipArtworkSelectionObject(after.facts.objectRevision), stamp); err != nil {
			return out, err
		}
	case IPArtworkClear:
		if err = ipTextExecOne(ctx, turn, ipASSelectionDelete, ns, subject, q.role,
			q.before.revision, ipArtworkSelectionDigestValue(q.before.facts.digest)); err != nil {
			return out, err
		}
	}
	if q.owner.entityID > 0 {
		err = ipTextExecOne(ctx, turn, ipASEntityBump, q.owner.entityID, q.owner.sourceIncarnation[:], q.owner.sourceRevision)
	} else {
		err = ipTextExecOne(ctx, turn, ipASPersonBump, q.owner.personID, q.owner.sourceIncarnation[:], q.owner.sourceRevision)
	}
	if err != nil {
		return out, err
	}
	history, err := ipArtworkSelectionHistory(ctx, turn, kind, q, after, stamp)
	if err != nil {
		return out, err
	}
	value.SelectionRevision = after.revision
	if kind == IPArtworkClear {
		value.SelectionRevision = q.before.revision + 1
	}
	value.Count = 1
	value.History = history
	return value, nil
}

func ipArtworkSelectionStamp(appliedMS int64) string {
	return time.UnixMilli(appliedMS).UTC().Format(time.RFC3339Nano)
}

func ipArtworkSelectionDigestValue(digest string) any {
	if digest == "" {
		return nil
	}
	return digest
}

func ipArtworkSelectionObject(v EvidenceText) any {
	if !v.Present {
		return nil
	}
	return v.Value
}

// Legacy audit JSON is history only; the current choice stays normalized and GC
// uses the exact history-artwork FK. A locked-empty after has Present=false and
// therefore pins nothing.
type ipArtworkSelectionAudit struct {
	Present        bool
	Role, Choice   string
	Digest         string
	Provider       string
	ImageID        string
	Origin         string
	Attribution    string
	Locale         string
	SourceFence    string
	Actor          string
	ObjectID       string
	ObjectRevision string
}

func ipArtworkSelectionAuditValue(role, actor string, v ipArtworkSelectionBefore) ipArtworkSelectionAudit {
	if !v.present {
		return ipArtworkSelectionAudit{Role: role, Choice: "locked"}
	}
	return ipArtworkSelectionAudit{
		Present: true, Role: role, Choice: "locked", Digest: v.facts.digest,
		Provider: v.facts.provider, ImageID: v.facts.imageID, Origin: v.facts.origin,
		Attribution: v.facts.attribution, Locale: v.facts.locale,
		SourceFence: v.facts.sourceFence, Actor: v.actor,
		ObjectID: v.facts.objectID.Value, ObjectRevision: v.facts.objectRevision.Value,
	}
}

func ipArtworkSelectionHistory(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkSelectionRequest, after ipArtworkSelectionBefore, stamp string) (out EvidenceInteger, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	ns, subject := ipArtworkSelectionNamespace(q.owner)
	beforeJSON, err := json.Marshal(ipArtworkSelectionAuditValue(q.role, q.before.actor, q.before))
	if err != nil {
		return out, err
	}
	afterJSON, err := json.Marshal(ipArtworkSelectionAuditValue(q.role, after.actor, after))
	if err != nil {
		return out, err
	}
	rows, err := ipTextRows(ctx, turn, ipASHistory, ns, subject,
		strconv.FormatInt(q.owner.sourceRevision, 10), strconv.FormatInt(q.owner.sourceRevision+1, 10),
		ipArtworkSelectionCommandName(kind), q.actor, stamp, string(beforeJSON), string(afterJSON))
	if err != nil {
		return out, err
	}
	var id int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&id) }); err != nil || id <= 0 {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if q.before.present && q.before.facts.digest != "" {
		if err = ipTextExecOne(ctx, turn, ipASHistoryPin, id, q.role, q.before.facts.digest); err != nil {
			return out, err
		}
	}
	if after.present && after.facts.digest != "" && after.facts.digest != q.before.facts.digest {
		if err = ipTextExecOne(ctx, turn, ipASHistoryPin, id, q.role, after.facts.digest); err != nil {
			return out, err
		}
	}
	return EvidenceInteger{Value: id, Present: true}, nil
}

// The owner CAS is the command allowance and the receipt is saved in the same
// transaction. ACK alone does not retire the pinned selection, history or source.
func ipArtworkSelectionRecord(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkSelectionRequest, identity CommandIdentity, request RequestDigest, result ResultDigest, value ipArtworkSelectionResult, appliedMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	o := q.owner
	if err = ipTextExecOne(ctx, turn, ipAMOwnerCAS, identity.Sequence, o.id, o.incarnation[:], o.revision,
		identity.Sequence-1, identity.Sequence-1, o.entityID, o.personID, o.sourceIncarnation[:],
		o.sourceRevision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS, appliedMS); err != nil {
		return err
	}
	history := any(nil)
	if value.History.Present {
		history = value.History.Value
	}
	return ipTextExecOne(ctx, turn, ipASSave, o.id, o.incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], appliedMS, result[:], ipArtworkSelectionCommandKind(kind),
		value.SelectionRevision, value.Count, o.leaseGeneration, o.leaseUntilMS, value.Role,
		value.SelectionRevision, ipArtworkSelectionDigestValue(value.Selected),
		ipArtworkSelectionDigestValue(value.Previous), history)
}
