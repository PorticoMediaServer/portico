package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
)

// Descriptive bulk page 244 consumes ONE bounded raw-keyset page of the exact
// captured entity cohort and applies the captured finite field bundle to each
// still-eligible target. An ineligible target is recorded as an explicit stale
// failure; no target is re-read from the caller or silently replaced. Every
// applied target pins the exact atomic field-edit receipt it produced, and the
// page result carries only the truthful durable cursor and counts. Command 245
// records the terminal only when nothing remains.
const (
	ipEditBulkPageSize    = 16
	ipEditBulkPageRawRows = ipEditBulkPageSize + 1
)

type ipEditBulkPageRequest struct {
	Kind CommandID
	Root ipEditBulkRoot
	// Revision is the expected repair-owner job revision; AfterOrdinal is the
	// exact raw cursor this page starts at.
	Revision, AfterOrdinal int64
	Targets                []ipEditBulkTarget
}

type ipEditBulkPageCommand struct {
	identity CommandIdentity
	request  ipEditBulkPageRequest
	digest   RequestDigest
	valid    bool
}

// ipEditBulkPageResult is the complete saved page result: the POST-CAS owner
// revision, the exact durable cursor, the truthful totals and whether the raw
// captured cohort is exhausted.
type ipEditBulkPageResult struct {
	OwnerID                          string
	Revision, AfterOrdinal, Captured int64
	Processed, Failed, AppliedMS     int64
	NoNext                           bool
	Manifest                         [32]byte
}

type ipEditBulkPageSavedResult struct {
	Kind  CommandID
	Value ipEditBulkPageResult
}

type ipEditBulkPageApplied struct {
	value  ipEditBulkPageResult
	digest ResultDigest
}

type ipEditBulkPageCommandResult struct {
	Outcome ReceiptOutcome
	Value   ipEditBulkPageResult
	Digest  ResultDigest
}

// ipEditBulkApplyOwner is the complete retained descriptive-bulk header read by
// page and terminal. It is never caller input.
type ipEditBulkApplyOwner struct {
	incarnation               []byte
	manifest, capturedDigest  []byte
	revision, last, ack       int64
	processed, failed         int64
	entity                    int64
	expected, captured        sql.NullInt64
	expectedBytes, capturedBy sql.NullInt64
	complete                  sql.NullInt64
	algorithm                 sql.NullString
	kind, library, actor      string
	base, intent, status      string
	after                     string
}

type ipEditBulkPageLookup struct {
	decision receiptDecision
	owner    ipEditBulkApplyOwner
	applied  ipEditBulkPageApplied
}

func (c *ipEditBulkPageCommand) reference() ReceiptReference {
	return ReceiptReference{Identity: c.identity, RequestDigest: c.digest}
}

func (ipEditBulkPageCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEditBulkPageCommand")
}

func (ipEditBulkPageCommandResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "ipEditBulkPageCommandResult")
}

func ipEditBulkValidOperation(value string) bool {
	if len(value) != 32 {
		return false
	}
	for j := 0; j < len(value); j++ {
		c := value[j]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ipEditBulkRootShape validates the FULL declared birth header without
// recomputing the target chain: a page only holds one bounded prefix, while the
// retained owner proves the complete original cohort and manifest.
func ipEditBulkRootShape(r ipEditBulkRoot) error {
	if !ipEditBulkValidOperation(r.Operation) || !ipTextEditEntityKind(r.Kind) ||
		!ipEditBulkValidText(r.Kind, 64) || !ipEditBulkValidText(r.Library, 512) || r.LibraryID <= 0 ||
		!ipEditBulkValidText(r.Actor, ipEditBulkActorMax) ||
		!ipEditBulkValidText(r.BaseRevision, ipEditBulkBaseMax) ||
		r.RootEntity <= 0 || r.ExpectedCount <= 0 || r.ExpectedCount > ipEditBulkMaxTargets ||
		r.ExpectedBytes <= 0 || len(r.Fields) == 0 || len(r.Fields) > ipEditBulkMaxFields ||
		r.Manifest == [32]byte{} {
		return ErrInvalid
	}
	base, err := ipEditBulkCanonicalRevision(r.BaseRevision)
	if err != nil || base <= 0 {
		return ErrInvalid
	}
	var fieldByte int64
	last := ""
	for _, f := range r.Fields {
		if string(f.Field) <= last || !ipEditBulkFieldAllowed(f.Field) {
			return ErrInvalid
		}
		last = string(f.Field)
		if ipEditBulkFieldAction(f.Edit) == "" || f.Edit.Lock > fieldmeta.ForceUnlocked ||
			(f.Edit.Action != fieldmeta.SetText && (f.Edit.Value != "" || f.Edit.Lock != fieldmeta.KeepDefaultLock)) {
			return ErrInvalid
		}
		if _, err = fieldmeta.PrepareTextEdit(f.Field, fieldmeta.TextState{Effective: fieldmeta.Text{Present: true}}, f.Edit); err != nil {
			return ErrInvalid
		}
		value := ""
		if f.Edit.Action == fieldmeta.SetText {
			value = f.Edit.Value
		}
		fieldByte += ipEditBulkFieldPad + int64(len(f.Field)) + int64(len(value))
	}
	if fieldByte > math.MaxInt64-r.ExpectedCount*ipEditBulkTargetBytes ||
		r.ExpectedBytes != fieldByte+r.ExpectedCount*ipEditBulkTargetBytes {
		return ErrInvalid
	}
	return nil
}

func cloneIPEditBulkRoot(r ipEditBulkRoot) ipEditBulkRoot {
	r.Operation = strings.Clone(r.Operation)
	r.Kind = strings.Clone(r.Kind)
	r.Library = strings.Clone(r.Library)
	r.Actor = strings.Clone(r.Actor)
	r.BaseRevision = strings.Clone(r.BaseRevision)
	r.Fields = append([]ipEditBulkField(nil), r.Fields...)
	for j := range r.Fields {
		r.Fields[j].Field = fieldmeta.TextField(strings.Clone(string(r.Fields[j].Field)))
		r.Fields[j].Edit.Value = strings.Clone(r.Fields[j].Edit.Value)
	}
	return r
}

// newIPEditBulkPageCommand freezes the complete original page request under W
// preparation custody, before any hold and without SQL.
func newIPEditBulkPageCommand(identity CommandIdentity, in ipEditBulkPageRequest) (*ipEditBulkPageCommand, error) {
	if !ipTextEditIdentityValid(identity) || in.Kind != IPEditBulkPage ||
		in.Revision <= 0 || in.Revision == math.MaxInt64 ||
		in.AfterOrdinal < 0 || in.AfterOrdinal > in.Root.ExpectedCount ||
		len(in.Targets) > ipEditBulkPageSize || ipEditBulkRootShape(in.Root) != nil {
		return nil, ErrInvalid
	}
	seen := make(map[int64]bool, len(in.Targets))
	for j, t := range in.Targets {
		if t.Ordinal != in.AfterOrdinal+int64(j) || t.Ordinal >= in.Root.ExpectedCount ||
			t.EntityID <= 0 || t.Incarnation == [16]byte{} ||
			t.Revision <= 0 || t.Revision == math.MaxInt64 || seen[t.EntityID] {
			return nil, ErrInvalid
		}
		seen[t.EntityID] = true
	}
	digest, err := canonicalRequestDigest(in.Kind, 1, identity, in)
	if err != nil {
		return nil, err
	}
	in.Root = cloneIPEditBulkRoot(in.Root)
	in.Targets = append([]ipEditBulkTarget(nil), in.Targets...)
	return &ipEditBulkPageCommand{identity: identity, request: in, digest: digest, valid: true}, nil
}

// ipEditBulkTargetOperation derives the ONE deterministic atomic edit identity
// for a target inside a page. It is never caller input: the pinned receipt is
// the proof of the exact field bundle that was applied.
func ipEditBulkTargetOperation(page CommandIdentity, ordinal int64) [16]byte {
	h := sha256.New()
	_, _ = h.Write([]byte("portico-ip-descriptive-target-operation-v1\x00"))
	_, _ = h.Write(page.OperationID[:])
	ipBulkPut(h, page.Sequence)
	ipBulkPut(h, ordinal)
	var out [16]byte
	copy(out[:], h.Sum(nil))
	if out == [16]byte{} {
		out[15] = 1
	}
	return out
}

// ipEditBulkCursor accepts only the canonical text form of the durable raw
// cursor. ” is the birth cursor zero.
func ipEditBulkCursor(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != value {
		return 0, ErrResolveRequired
	}
	return n, nil
}

func ipEditBulkCursorText(value int64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func ipEditBulkPageReadOwner(ctx context.Context, turn *commandTurn, operation string) (out ipEditBulkApplyOwner, found bool, err error) {
	rows, err := ipBulkRows(ctx, turn, ipBulkPageOwner, operation)
	if err != nil {
		return out, false, err
	}
	err = ipBulkOne(rows, func() error {
		return rows.Scan(&out.incarnation, &out.revision, &out.last, &out.ack, &out.after,
			&out.processed, &out.failed, &out.status, &out.intent, &out.kind, &out.entity,
			&out.library, &out.actor, &out.base, &out.expected, &out.captured, &out.complete,
			&out.algorithm, &out.manifest, &out.expectedBytes, &out.capturedBy,
			&out.capturedDigest)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if len(out.incarnation) != 16 || out.revision <= 0 || out.last < 0 || out.ack < 0 || out.ack > out.last ||
		out.last-out.ack > 1 || !out.expected.Valid || !out.captured.Valid || !out.complete.Valid ||
		!out.expectedBytes.Valid || !out.capturedBy.Valid || !out.algorithm.Valid ||
		len(out.manifest) != 32 || len(out.capturedDigest) != 32 ||
		out.processed < 0 || out.failed < 0 || out.processed+out.failed > out.expected.Int64 ||
		out.intent != ipEditBulkIntent || out.algorithm.String != ipEditBulkManifest ||
		out.complete.Int64 != 1 || out.entity <= 0 || out.kind == "" || out.library == "" ||
		out.actor == "" || out.base == "" || out.status == "" {
		return out, false, ErrResolveRequired
	}
	return out, true, nil
}

// ipEditBulkApplyOwnerMatches proves the retained owner is the SAME complete
// birth header the request declared.
func ipEditBulkApplyOwnerMatches(r ipEditBulkRoot, f ipEditBulkApplyOwner) bool {
	return f.kind == r.Kind && f.entity == r.RootEntity && f.library == r.Library &&
		f.actor == r.Actor && f.base == r.BaseRevision &&
		f.expected.Int64 == r.ExpectedCount && f.captured.Int64 == r.ExpectedCount &&
		f.expectedBytes.Int64 == r.ExpectedBytes && f.capturedBy.Int64 == r.ExpectedBytes &&
		bytes.Equal(f.manifest, r.Manifest[:]) && bytes.Equal(f.capturedDigest, r.Manifest[:])
}

func (f ipEditBulkApplyOwner) ownerFacts() receiptOwnerFacts {
	out := receiptOwnerFacts{exists: true, lastSequence: f.last, acknowledgedSequence: f.ack}
	copy(out.incarnation[:], f.incarnation)
	return out
}

func ipEditBulkPageLookupReceipt(ctx context.Context, turn *commandTurn, c *ipEditBulkPageCommand) (out ipEditBulkPageLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEditBulkPage || c == nil || !c.valid {
		return out, ErrInvalid
	}
	q := c.request
	f, found, err := ipEditBulkPageReadOwner(ctx, turn, q.Root.Operation)
	if err != nil {
		return out, err
	}
	if !found {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if !ipEditBulkApplyOwnerMatches(q.Root, f) {
		return out, ErrConflict
	}
	out.owner = f
	var operation, request, result []byte
	var variant int64
	var receipt receiptFacts
	rows, e := ipBulkRows(ctx, turn, ipBulkPageReceiptHead, q.Root.Operation, c.identity.Incarnation[:], c.identity.Sequence)
	if e != nil {
		return out, e
	}
	e = ipBulkOne(rows, func() error { return rows.Scan(&operation, &request, &result, &variant) })
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if e == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 || variant != 1 {
			return out, ErrResolveRequired
		}
		receipt.exists = true
		receipt.kind = IPEditBulkPage
		receipt.resultCount = 1
		receipt.identity.command = c.identity
		copy(receipt.identity.command.OperationID[:], operation)
		copy(receipt.identity.digest[:], request)
		copy(receipt.resultDigest[:], result)
	}
	out.decision, err = classifyReceipt(receiptIdentity{c.identity, c.digest}, IPEditBulkPage, f.ownerFacts(), receipt)
	if err != nil {
		return out, err
	}
	if out.decision == receiptReplay {
		out.applied, err = ipEditBulkPageReadSaved(ctx, turn, c)
		if err != nil {
			return out, err
		}
		if out.applied.digest != ResultDigest(receipt.resultDigest) {
			return out, ErrResolveRequired
		}
	}
	return out, nil
}

// ipEditBulkPageReadSaved reconstructs the COMPLETE original page result from
// the saved receipt and the exact retained owner header.
func ipEditBulkPageReadSaved(ctx context.Context, turn *commandTurn, c *ipEditBulkPageCommand) (out ipEditBulkPageApplied, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || c == nil || !c.valid {
		return out, ErrInvalid
	}
	r := c.request.Root
	f, found, err := ipEditBulkPageReadOwner(ctx, turn, r.Operation)
	if err != nil {
		return out, err
	}
	if !found || !ipEditBulkApplyOwnerMatches(r, f) {
		return out, ErrResolveRequired
	}
	rows, err := ipBulkRows(ctx, turn, ipBulkPageReceipt, r.Operation, c.identity.Incarnation[:], c.identity.Sequence)
	if err != nil {
		return out, err
	}
	var operation, request, result, manifest []byte
	var variant, accepted, after, captured, processed, failed, complete, applied int64
	var algorithm string
	if err = ipBulkOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &variant, &accepted, &after, &captured,
			&processed, &failed, &complete, &algorithm, &manifest, &applied)
	}); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if len(operation) != 16 || len(request) != 32 || len(result) != 32 || variant != 1 ||
		accepted <= 0 || after < 0 || captured != f.expected.Int64 || processed < 0 || failed < 0 ||
		processed+failed != after || after > captured || complete != 1 ||
		algorithm != ipEditBulkManifest || len(manifest) != 32 || applied < 0 ||
		!bytes.Equal(manifest, r.Manifest[:]) {
		return out, ErrResolveRequired
	}
	if !bytes.Equal(operation, c.identity.OperationID[:]) || !bytes.Equal(request, c.digest[:]) {
		return out, ErrResolveRequired
	}
	value := ipEditBulkPageResult{OwnerID: r.Operation, Revision: accepted, AfterOrdinal: after,
		Captured: captured, Processed: processed, Failed: failed,
		NoNext: after == captured, AppliedMS: applied}
	copy(value.Manifest[:], manifest)
	digest, err := ipEditBulkPageResultDigest(c.identity, c.digest, value)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	var saved ResultDigest
	copy(saved[:], result)
	if saved != digest {
		return out, ErrResolveRequired
	}
	out.value, out.digest = value, digest
	return out, nil
}

// ipEditBulkPageFields proves the declared field cohort is exactly the original
// captured one before any page effects.
func ipEditBulkPageFields(ctx context.Context, turn *commandTurn, r ipEditBulkRoot) (out []fieldmeta.TextEdit, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := ipBulkRows(ctx, turn, ipBulkPageFields, r.Operation)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name, action string
		var value sql.NullString
		var lock int64
		if err = rows.Scan(&name, &action, &value, &lock); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if len(out) >= len(r.Fields) {
			_ = rows.Close()
			return nil, ErrResolveRequired
		}
		want := r.Fields[len(out)]
		if name != string(want.Field) || action != ipEditBulkFieldAction(want.Edit) ||
			lock != int64(want.Edit.Lock) || !ipEditBulkValueMatches(want.Edit, value) {
			_ = rows.Close()
			return nil, ErrResolveRequired
		}
		out = append(out, want.Edit)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if len(out) != len(r.Fields) {
		return nil, ErrResolveRequired
	}
	return out, nil
}

func ipEditBulkPageRaw(ctx context.Context, turn *commandTurn, operation string, after int64) (out []ipEditBulkTarget, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := ipBulkRows(ctx, turn, ipBulkPageRaw, operation, after)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var t ipEditBulkTarget
		var incarnation []byte
		var captured int64
		if err = rows.Scan(&t.Ordinal, &t.EntityID, &incarnation, &t.Revision, &captured); err != nil {
			return nil, err
		}
		if len(incarnation) != 16 || t.Ordinal != after+int64(len(out)) || captured != 1 || len(out) >= ipEditBulkPageRawRows {
			return nil, ErrResolveRequired
		}
		copy(t.Incarnation[:], incarnation)
		out = append(out, t)
	}
	return out, rows.Err()
}

type ipEditBulkPageEntity struct {
	kind, library, source, identity, revision, metadata int64
	incarnation                                         [16]byte
	title, sortTitle, language                          string
	sortKey, sortHead, keepKey, keepHead                string
}

func ipEditBulkPageEntityFacts(ctx context.Context, turn *commandTurn, entity, library int64, key string) (out ipEditBulkPageEntity, found bool, err error) {
	rows, err := ipBulkRows(ctx, turn, ipBulkPageEntity, entity, library, key)
	if err != nil {
		return out, false, err
	}
	var incarnation []byte
	err = ipBulkOne(rows, func() error {
		return rows.Scan(&out.kind, &out.library, &incarnation, &out.source, &out.identity,
			&out.revision, &out.metadata, &out.title, &out.sortTitle, &out.language,
			&out.sortKey, &out.sortHead, &out.keepKey, &out.keepHead)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if len(incarnation) != 16 || out.kind <= 0 || out.library != library ||
		out.source <= 0 || out.identity <= 0 || out.revision <= 0 || out.metadata <= 0 {
		return out, false, ErrResolveRequired
	}
	copy(out.incarnation[:], incarnation)
	return out, true, nil
}

type ipEditBulkPageEditOwner struct {
	id, revision, last, ack int64
	incarnation             []byte
	found                   bool
}

func ipEditBulkPageEditOwnerFacts(ctx context.Context, turn *commandTurn, kind string, entity int64) (out ipEditBulkPageEditOwner, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	rows, err := ipBulkRows(ctx, turn, ipBulkPageEditOwner, kind, entity)
	if err != nil {
		return out, err
	}
	err = ipBulkOne(rows, func() error {
		return rows.Scan(&out.id, &out.incarnation, &out.revision, &out.last, &out.ack)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if out.id <= 0 || len(out.incarnation) != 16 || out.revision <= 0 || out.last < 0 ||
		out.ack < 0 || out.ack > out.last || out.last-out.ack > 1 {
		return out, ErrResolveRequired
	}
	out.found = true
	return out, nil
}

func ipEditBulkPageTargetMatches(raw, want ipEditBulkTarget) bool {
	return raw.Ordinal == want.Ordinal && raw.EntityID == want.EntityID &&
		raw.Revision == want.Revision && raw.Incarnation == want.Incarnation
}

// ipEditBulkPageMarkStale records the target's explicit stale failure. It is
// the page's only non-applied disposition, so an ineligible target never aborts
// the bounded page or the honest terminal count.
func ipEditBulkPageMarkStale(ctx context.Context, turn *commandTurn, operation string, target ipEditBulkTarget) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	return ipBulkExpectOne(ctx, turn, ipBulkPageTargetStale, operation, target.Ordinal,
		target.EntityID, target.Incarnation[:], target.Revision)
}

// ipEditBulkTargetOutcome is the page's per-target disposition: applied, or an
// explicit stale failure recorded in the target row.
type ipEditBulkTargetOutcome struct {
	applied bool
}

// ipEditBulkPageTarget applies the captured field bundle to one eligible target
// as ONE atomic typed owner edit under the target edit owner's own next command
// sequence. It returns applied=false when the target is no longer eligible and
// was marked stale.
func ipEditBulkPageTarget(ctx context.Context, turn *commandTurn, c *ipEditBulkPageCommand, fields []fieldmeta.TextEdit, target ipEditBulkTarget, appliedMS int64) (out ipEditBulkTargetOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	r := c.request.Root
	facts, found, err := ipEditBulkPageEntityFacts(ctx, turn, target.EntityID, r.LibraryID, r.Library)
	if err != nil {
		return out, err
	}
	// Eligibility proves the exact captured identity: kind, revision AND the
	// metadata incarnation begin froze. A reborn entity under the same id and
	// revision was never captured and must never be edited here.
	if found && (ipEntryOwnerKind(facts.kind) != r.Kind || facts.revision != target.Revision ||
		facts.incarnation != target.Incarnation) {
		found = false
	}
	if !found {
		return out, ipEditBulkPageMarkStale(ctx, turn, r.Operation, target)
	}
	owner, err := ipEditBulkPageEditOwnerFacts(ctx, turn, r.Kind, target.EntityID)
	if err != nil {
		return out, err
	}
	// The target edit owner has its OWN command sequence, independent of this
	// page's identity. A brand-new owner is born at 1; an existing owner enters
	// at last+1 exactly like the normal typed edit. An unacknowledged
	// predecessor (or a saturated sequence) cannot admit a fresh command: the
	// target is stale for this page instead of breaking last-ack<=1.
	sequence := int64(1)
	childIncarnation := c.identity.Incarnation
	source := ipTextEditSource{
		EntityID: target.EntityID, LibraryID: r.LibraryID, Kind: r.Kind, Actor: r.Actor,
		LibraryKey: r.Library, TargetKind: facts.kind, OwnerRevision: 1,
		SubjectRevision: facts.revision, MetadataRevision: facts.metadata,
		SourceRevision: facts.source, IdentityRevision: facts.identity,
		MetadataIncarnation: facts.incarnation, Projection: ipTextProjection{},
	}
	if owner.found {
		if owner.last > owner.ack || owner.last == math.MaxInt64 {
			return out, ipEditBulkPageMarkStale(ctx, turn, r.Operation, target)
		}
		sequence = owner.last + 1
		childIncarnation = [16]byte{}
		copy(childIncarnation[:], owner.incarnation)
		source.OwnerRevision = owner.revision
	}
	hasKeys := false
	for j := range fields {
		if ipTextEditKeyField(c.request.Root.Fields[j].Field) {
			hasKeys = true
		}
	}
	if hasKeys {
		source.Projection = ipTextProjection{Title: facts.title, SortTitle: facts.sortTitle,
			Language: facts.language, SortKey: facts.sortKey, SortHead: facts.sortHead,
			SortKeyKeepArticles: facts.keepKey, SortHeadKeepArticles: facts.keepHead}
	}
	edit := ipTextEditRequest{Kind: IPFieldEdit, Source: source, Fields: make([]ipTextEditField, len(fields))}
	for j, f := range c.request.Root.Fields {
		state, present, e := ipTextEditFieldState(ctx, turn, source, f.Field)
		if e != nil {
			return out, e
		}
		catalog, e := ipTextEditCatalogState(ctx, turn, source, f.Field)
		if e != nil {
			return out, e
		}
		if !present {
			state = fieldmeta.TextState{Effective: catalog}
		}
		edit.Fields[j] = ipTextEditField{Field: f.Field, Before: state, Edit: fields[j]}
	}
	childIdentity := CommandIdentity{Incarnation: childIncarnation, Sequence: sequence,
		OperationID: ipEditBulkTargetOperation(c.identity, target.Ordinal)}
	child, err := newIPTextEditCommand(childIdentity, edit)
	if err != nil {
		// The captured bundle cannot be validated against this target as it now
		// stands (for example a lock over an absent value). Record the explicit
		// stale failure and continue the page; a retry is then identical.
		return out, ipEditBulkPageMarkStale(ctx, turn, r.Operation, target)
	}
	ownerFacts := receiptOwnerFacts{exists: true, incarnation: childIncarnation}
	if owner.found {
		ownerFacts.lastSequence, ownerFacts.acknowledgedSequence = owner.last, owner.ack
		copy(ownerFacts.incarnation[:], owner.incarnation)
	}
	decision, err := classifyReceipt(receiptIdentity{childIdentity, child.digest}, IPFieldEdit, ownerFacts, receiptFacts{})
	if err != nil || decision != receiptFresh {
		return out, errors.Join(ErrResolveRequired, err)
	}
	entry := &ipCommandEntry{
		request: ipTextEditEntryRequest(child), decision: decision, ownerID: owner.id, owner: ownerFacts,
	}
	result, err := ipTextEditWriteFresh(ctx, turn, child, entry, appliedMS)
	if err != nil {
		return out, err
	}
	if err = ipBulkExpectOne(ctx, turn, ipBulkPageTargetApplied, result.value.SubjectRevision,
		result.value.OwnerID, childIncarnation[:], sequence, r.Operation, target.Ordinal,
		target.EntityID, target.Incarnation[:], target.Revision); err != nil {
		return out, err
	}
	out.applied = true
	return out, nil
}

// applyIPEditBulkPage is the closed page body. Current authority precedes
// historical receipt disclosure; a saved page returns its exact original result
// with zero new effects.
func applyIPEditBulkPage(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipEntryBudget, c *ipEditBulkPageCommand) (out ipEditBulkPageCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipEditBulkPageCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IPEditBulkPage {
		return zero, ErrInvalid
	}
	q := c.request
	actual, err := canonicalRequestDigest(IPEditBulkPage, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPEntryOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipEditBulkPageLookupReceipt(ctx, turn, c)
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
		out.Value, out.Digest = lookup.applied.value, lookup.applied.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.recheck(ctx, turn); err != nil {
		return zero, err
	}
	f := lookup.owner
	cursor, err := ipEditBulkCursor(f.after)
	if err != nil {
		return zero, err
	}
	if f.revision != q.Revision || f.revision == math.MaxInt64 {
		return zero, ErrConflict
	}
	if cursor != q.AfterOrdinal || f.processed+f.failed != cursor {
		return zero, ErrResolveRequired
	}
	if f.status != "ready" && f.status != "running" {
		return zero, ErrConflict
	}
	fields, err := ipEditBulkPageFields(ctx, turn, q.Root)
	if err != nil {
		return zero, err
	}
	raw, err := ipEditBulkPageRaw(ctx, turn, q.Root.Operation, q.AfterOrdinal)
	if err != nil {
		return zero, err
	}
	// A captured row beyond the immutable declared count is a corrupted cohort,
	// never a free extra edit.
	if len(raw) > 0 && raw[len(raw)-1].Ordinal >= q.Root.ExpectedCount {
		return zero, ErrResolveRequired
	}
	if len(q.Targets) == 0 {
		if q.AfterOrdinal != q.Root.ExpectedCount || len(raw) != 0 {
			return zero, ErrConflict
		}
	} else {
		if q.AfterOrdinal >= q.Root.ExpectedCount || len(raw) < len(q.Targets) {
			return zero, ErrConflict
		}
		for j, want := range q.Targets {
			if !ipEditBulkPageTargetMatches(raw[j], want) {
				return zero, ErrConflict
			}
			if raw[j].Ordinal != raw[0].Ordinal+int64(j) {
				return zero, ErrResolveRequired
			}
		}
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	processed, failed, next := f.processed, f.failed, q.AfterOrdinal
	for _, target := range q.Targets {
		outcome, e := ipEditBulkPageTarget(ctx, turn, c, fields, target, appliedMS)
		if e != nil {
			return zero, e
		}
		if outcome.applied {
			processed++
		} else {
			failed++
		}
		next++
	}
	if next != processed+failed {
		return zero, ErrResolveRequired
	}
	revision := f.revision + 1
	if err = ipBulkExpectOne(ctx, turn, ipBulkPageAdvance, processed, failed,
		ipEditBulkCursorText(next), c.identity.Sequence, q.Root.Operation,
		c.identity.Incarnation[:], f.revision, f.last, f.ack, f.after, f.processed, f.failed); err != nil {
		return zero, err
	}
	value := ipEditBulkPageResult{OwnerID: q.Root.Operation, Revision: revision, AfterOrdinal: next,
		Captured: f.expected.Int64, Processed: processed, Failed: failed,
		NoNext: next == f.expected.Int64, AppliedMS: appliedMS, Manifest: q.Root.Manifest}
	digest, err := ipEditBulkPageResultDigest(c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	if err = ipBulkExpectOne(ctx, turn, ipBulkPageReceiptWrite, q.Root.Operation,
		c.identity.Incarnation[:], c.identity.Sequence, c.identity.OperationID[:], c.digest[:],
		appliedMS, digest[:], revision, next, f.expected.Int64, processed, failed,
		ipEditBulkManifest, q.Root.Manifest[:]); err != nil {
		return zero, err
	}
	saved, err := ipEditBulkPageReadSaved(ctx, turn, c)
	if err != nil || saved.digest != digest || saved.value != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.value, saved.digest, Applied
	return out, nil
}

func ipEditBulkPageResultDigest(identity CommandIdentity, request RequestDigest, value ipEditBulkPageResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipEditBulkPageSavedResult{IPEditBulkPage, value})
}

func preflightIPEditBulkPageResult(v ipEditBulkPageSavedResult) error {
	if v.Kind != IPEditBulkPage {
		return ErrInvalid
	}
	r := v.Value
	if !ipEditBulkValidOperation(r.OwnerID) || r.Revision <= 0 || r.AfterOrdinal < 0 ||
		r.Captured <= 0 || r.AfterOrdinal > r.Captured || r.Processed < 0 || r.Failed < 0 ||
		r.Processed+r.Failed != r.AfterOrdinal || r.AppliedMS < 0 || r.Manifest == [32]byte{} ||
		r.NoNext != (r.AfterOrdinal == r.Captured) {
		return ErrInvalid
	}
	return nil
}

func writeIPEditBulkPageResult(e *requestEncoder, identity CommandIdentity, request RequestDigest, v ipEditBulkPageSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_edit_bulk")
	e.text("page")
	e.text("applied")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	r := v.Value
	e.text(r.OwnerID)
	for _, n := range []int64{r.Revision, r.AfterOrdinal, r.Captured, r.Processed, r.Failed} {
		e.number(uint64(n))
	}
	e.flag(r.NoNext)
	e.bytes(r.Manifest[:])
	e.number(uint64(r.AppliedMS))
	return e.err
}

func ipEditBulkPageResultEqual(a, b ipEditBulkPageResult) bool { return a == b }
