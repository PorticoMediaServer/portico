package writer

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const iaExtraAcceptRequestVersion uint16 = 1

const (
	iaExtraAcceptMaxTitleBytes = 4096
	iaExtraAcceptMaxPathBytes  = 16384
)

// IA-16 is the extras/trailers variant of the IA accepting class. Its private
// outcome and history carry the exact typed result; no public facade, worker or
// producer is wired, so the accepting body stays uncalled outside this package.
type iaExtraAcceptOutcome struct {
	decision receiptDecision
	result   iaExtraInventoryResult
	digest   ResultDigest
}
type iaExtraHistory struct {
	decision receiptDecision
	result   iaExtraInventoryResult
	digest   ResultDigest
}

// Same-turn saved header, never a disclosure capability. The complete typed
// member cohort is decoded only after current authority and the receipt floor.
type iaExtraSavedHeader struct {
	facts                                               receiptFacts
	acceptedRevision, appliedMS, memberBytes            int64
	sourceID, sourceIncarnation                         string
	objectID, objectRevision, cursorKind                string
	directoryPath, directoryIdentity, directoryRevision []byte
	expectedPayload, nextPayload                        []byte
}

// Prepared observations only. The item and owner source keys are caller naming
// or scan evidence and are never derived, normalized or allocated here. An
// empty ownerKey is an explicitly unresolved owner and is persisted as SQL NULL,
// never replaced by the parent path or a guessed identity. No current
// authority, asset row, entity ID or search generation is captured.
type iaExtraAcceptRequest struct {
	scope     inventoryScope
	source    inventorySource
	run       inventoryRun
	directory inventoryDirectory
	files     []inventoryFile // exactly one observed extra/trailer file
	complete  bool            // complete cohort assertion, not source proof

	itemKey, itemTitle string
	extraKind          string
	parentPath         string // exact native relative parent path evidence
	ownerKey           string // empty is the explicit unresolved owner
	ownerKind          int64  // expected owner kind 1..4 when ownerKey is set
	part               int64
	edition            string
	start              float64
	end                *float64
}

type iaExtraAcceptCommand struct {
	identity CommandIdentity
	request  iaExtraAcceptRequest
	digest   RequestDigest
	valid    bool
}

func newIAExtraAcceptCommand(id CommandIdentity, r iaExtraAcceptRequest) (*iaExtraAcceptCommand, error) {
	// Counts precede all text traversal, copying and hashing.
	if len(r.files) != 1 || !iaEpisodeCounts(reflect.ValueOf(r)) {
		return nil, invalid("ia_extra_count")
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaEpisodeWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_extra_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !iaEpisodeValidText(reflect.ValueOf(r)) || !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return nil, invalid("ia_extra_text")
	}
	if err := validateIAExtraAcceptOriginal(id, r); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IAAcceptExtraInventory, iaExtraAcceptRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.files = slices.Clone(r.files)
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	if r.end != nil {
		v := *r.end
		r.end = &v
	}
	for _, s := range iaExtraAcceptTexts(&r) {
		*s = strings.Clone(*s)
	}
	return &iaExtraAcceptCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

// Every accepting gate is a closed witness comparison. The closed extra kind,
// exact title/path bytes and the optional exact owner are prepared evidence.
func validateIAExtraAcceptOriginal(id CommandIdentity, r iaExtraAcceptRequest) error {
	s, u, d := r.source, r.run, r.directory
	if id.Sequence <= 0 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) ||
		r.scope.runID == "" || r.scope.libraryKey == "" || r.scope.libraryID <= 0 ||
		s.id == "" || s.incarnation == "" || s.rootIdentity == "" || s.generation <= 0 || len(s.resolvedRoot) == 0 || s.resolvedRoot[0] != '/' ||
		u.revision <= 0 || u.policyRevision <= 0 || u.leaseOwner <= 0 || u.leaseGeneration <= 0 || u.leaseExpiresMS <= 0 || u.lastSequence != id.Sequence-1 || u.acknowledgedSequence != u.lastSequence || u.phase != "inventory" || !inventoryScanMode(u.scanMode) ||
		d.identity == "" || d.revision == "" || d.cursorKind != inventoryLinuxCursor || !inventoryPath(d.path, true) || d.pages < 0 || d.skipped < 0 || d.rawNames != 1 || d.unstable {
		return invalid("ia_extra_original")
	}
	root := string(s.resolvedRoot)
	f := r.files[0]
	if !r.complete || strings.ContainsAny(root, `\`) || path.Clean(root) != root || f.sourceKey == "" || f.objectIdentity == "" || f.revision == "" ||
		f.directory || f.size < 0 || f.part < 0 || f.year < 0 || f.metadataLanguage == "" || !inventoryPath(f.relativePath, false) ||
		path.Dir(f.relativePath) != d.path || strings.ContainsAny(f.assetPath, `\`) || path.Clean(f.assetPath) != f.assetPath || f.assetPath != path.Join(root, f.relativePath) {
		return invalid("ia_extra_file")
	}
	if !iaEpisodeKey(r.itemKey) || !iaExtraAcceptText(r.itemTitle, iaExtraAcceptMaxTitleBytes) || !iaExtraKind(r.extraKind) ||
		!iaExtraAcceptParentPath(r.parentPath) || r.part < 0 || !iaExtraAcceptText(r.edition, iaExtraAcceptMaxTitleBytes) &&
		r.edition != "" || math.IsNaN(r.start) || math.IsInf(r.start, 0) || r.start < 0 ||
		r.end != nil && (math.IsNaN(*r.end) || math.IsInf(*r.end, 0) || *r.end <= r.start) {
		return invalid("ia_extra_evidence")
	}
	// A resolved owner must name its exact expected kind. An unresolved owner
	// carries no kind at all: the two states are never merged.
	if r.ownerKey == "" {
		if r.ownerKind != 0 {
			return invalid("ia_extra_owner")
		}
	} else if !iaEpisodeKey(r.ownerKey) || r.ownerKind < 1 || r.ownerKind > 4 {
		return invalid("ia_extra_owner")
	}
	if err := ValidateMetadataShape(r.itemTitle, "", 0, 0, 0); err != nil {
		return err
	}
	return nil
}

// iaExtraKind is the closed item_extras.kind domain. "other" is the explicit
// unknown type, never a silent fallback for a missing value.
func iaExtraKind(value string) bool {
	switch value {
	case "trailer", "featurette", "deleted_scene", "behind_the_scenes", "interview", "scene", "short", "other":
		return true
	default:
		return false
	}
}

// iaExtraAcceptText bounds one retained identifier without cleaning, folding or
// trimming it.
func iaExtraAcceptText(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// iaExtraAcceptParentPath keeps the exact native relative parent path bytes.
func iaExtraAcceptParentPath(value string) bool {
	if !iaExtraAcceptText(value, iaExtraAcceptMaxPathBytes) || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\") || value == "." {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (c *iaExtraAcceptCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_extra_command")
	}
	checked, err := newIAExtraAcceptCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}

func iaExtraAcceptTexts(r *iaExtraAcceptRequest) []*string {
	base := inventoryAcceptRequest{scope: r.scope, source: r.source, run: r.run, directory: r.directory, files: r.files}
	texts := []*string{&r.scope.runID, &r.scope.libraryKey, &r.source.id, &r.source.incarnation, &r.source.rootIdentity, &r.run.phase, &r.run.scanMode, &r.directory.path, &r.directory.identity, &r.directory.revision, &r.directory.expectedCursor, &r.directory.nextCursor, &r.directory.cursorKind}
	texts = append(texts, inventoryRequestTexts(&base)[14:]...)
	return append(texts, &r.itemKey, &r.itemTitle, &r.extraKind, &r.parentPath, &r.ownerKey, &r.edition)
}

// One governed extra accept or original replay. Current owner authority is
// established BEFORE any retained owner/header read; fresh source, directory,
// asset and run witnesses are compared only after the receipt is Fresh. A saved
// result is decoded from the immutable receipt/child cohort, never a live row.
func acceptIAExtraInventory(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, r iaExtraAcceptRequest) (out iaExtraAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAAcceptExtraInventory {
		return out, ErrInvalid
	}
	command, err := newIAExtraAcceptCommand(reference.Identity, r)
	if err != nil {
		return out, err
	}
	// The canonical request digest is recomputed by this writer. A caller's
	// mismatched digest is a conflicting original, never a fresh admission.
	if command.digest != reference.RequestDigest {
		return out, ErrConflict
	}
	authority, err := authorizeIAEntryOwner(ctx, turn, p, lifetime, budget)
	if err != nil {
		return out, err
	}
	owner, header, err := iaExtraReceiptFacts(ctx, turn, r.scope.runID, reference)
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(receiptIdentity{reference.Identity, reference.RequestDigest}, IAAcceptExtraInventory, owner, header.facts)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	switch decision {
	case receiptRetired:
		return iaExtraAcceptOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIAExtraInventory(ctx, turn, authority, command, header)
		if err != nil {
			return out, err
		}
		return iaExtraAcceptOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIAExtraInventory(ctx, turn, authority, command, reference)
	default:
		return out, ErrResolveRequired
	}
}

// Owner and header facts are decoded by finite points scoped to the exact
// original run/incarnation/sequence. No current source/lease/asset row filters
// history; a kind mismatch is a conflict, never a fresh admission.
func iaExtraReceiptFacts(ctx context.Context, turn *commandTurn, runID string, reference ReceiptReference) (owner receiptOwnerFacts, header iaExtraSavedHeader, err error) {
	row, err := wc008DraftQuery(ctx, turn, iaExtraAcceptOwner, runID)
	if err != nil {
		return owner, header, err
	}
	var incarnation []byte
	err = row.Scan(&incarnation, &owner.lastSequence, &owner.acknowledgedSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, header, nil
	}
	if err != nil {
		return owner, header, err
	}
	if len(incarnation) != 16 {
		return owner, header, ErrResolveRequired
	}
	owner.exists = true
	copy(owner.incarnation[:], incarnation)

	row, err = wc008DraftQuery(ctx, turn, iaExtraAcceptHeader, runID, reference.Identity.Incarnation[:], reference.Identity.Sequence)
	if err != nil {
		return owner, header, err
	}
	var operation, request, digest []byte
	var kind string
	var version int64
	err = row.Scan(&operation, &request, &kind, &header.facts.resultCount, &digest, &header.acceptedRevision, &header.appliedMS,
		&header.sourceID, &header.sourceIncarnation, &version, &header.memberBytes, &header.objectID, &header.objectRevision,
		&header.cursorKind, &header.directoryPath, &header.directoryIdentity, &header.directoryRevision,
		&header.expectedPayload, &header.nextPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, header, nil
	}
	if err != nil {
		return owner, header, err
	}
	if len(operation) != 16 || len(request) != 32 || len(digest) != 32 || version != 1 {
		return owner, header, ErrResolveRequired
	}
	header.facts.exists = true
	header.facts.identity.command = reference.Identity
	copy(header.facts.identity.command.OperationID[:], operation)
	copy(header.facts.identity.digest[:], request)
	copy(header.facts.resultDigest[:], digest)
	if kind == iaExtraAcceptReceiptKind {
		header.facts.kind = IAAcceptExtraInventory
	}
	return owner, header, nil
}

// Fresh effects are one complete unit: the extra identity (with its exact
// item_extras row), the asset link/contribution, search intent, the
// directory/run advance and the typed receipt/child cohort. Every gate is
// re-proven before the first write and the caller's authority is rechecked
// before any fresh result is released.
func applyIAExtraInventory(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaExtraAcceptCommand, reference ReceiptReference) (out iaExtraAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	r := command.request
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return out, ErrInvalid
	}
	appliedMS := turn.transaction.writer.now().UnixMilli()
	if appliedMS < 0 || appliedMS >= r.run.leaseExpiresMS {
		return out, ErrConflict
	}
	// Fresh eligibility: live library, exact current source, pending directory
	// and the exact observed asset row. Any changed witness refuses the whole
	// unit before a single catalogue or receipt row exists.
	if id, err := wc008DraftInteger(ctx, turn, iaExtraAcceptLibrary, r.scope.libraryID, r.scope.libraryKey); err != nil || id != r.scope.libraryID {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := wc008DraftQuery(ctx, turn, iaExtraAcceptSource, r.source.id, r.scope.libraryKey, r.source.generation, r.source.incarnation, r.source.rootIdentity, string(r.source.resolvedRoot), r.source.followSymlinks)
	if err != nil {
		return out, err
	}
	var sourceID string
	if err = row.Scan(&sourceID); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if sourceID != r.source.id {
		return out, ErrConflict
	}
	if err = iaExtraCheckDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	asset, err := iaExtraCheckAsset(ctx, turn, r)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	owner, err := iaExtraResolveOwner(ctx, turn, r)
	if err != nil {
		return out, err
	}
	item, created, err := iaExtraResolveItem(ctx, turn, r, owner.subjectID, asset.token, appliedMS)
	if err != nil {
		return out, err
	}
	// Links/contributions bind the exact observed asset. A repeated item+asset
	// link is a conflict; no silent upsert, reparent or edition rewrite exists.
	var playback [16]byte
	if _, err = rand.Read(playback[:]); err != nil {
		return out, err
	}
	var nullOwner any
	if owner.subjectID > 0 {
		nullOwner = owner.subjectID
	}
	if created {
		if err = wc008DraftChanged(ctx, turn, iaExtraAcceptItemInsert, item.subjectID, r.scope.libraryKey, r.parentPath, nullOwner,
			r.extraKind, r.itemTitle, asset.token, r.files[0].relativePath); err != nil {
			return out, err
		}
	}
	if err = wc008DraftChanged(ctx, turn, iaExtraAcceptLinkInsert, nil, item.subjectID, asset.id, r.part, int64(1), r.start, iaEpisodeNullableReal(r.end), r.edition,
		playback[:], int64(1), r.scope.libraryID); err != nil {
		return out, err
	}
	if err = wc008DraftChanged(ctx, turn, iaExtraAcceptContributionInsert, r.scope.libraryID, item.subjectID, asset.id, int64(1), int64(0), r.files[0].size, asset.fileSizeRevision); err != nil {
		return out, err
	}
	if created {
		if err = applyIAMediaSearchInput(ctx, turn, []newOrdinaryEntitySource{iaExtraSearchEntity(item, r.scope.libraryID)}, appliedMS, 11, 11); err != nil {
			return out, err
		}
	}
	if err = iaExtraAdvanceDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	runRevision, err := iaExtraAdvanceRun(ctx, turn, command.identity, r)
	if err != nil {
		return out, err
	}
	facts, err := iaExtraFacts(r, item, owner, asset, playback, runRevision, appliedMS)
	if err != nil {
		return out, err
	}
	if err = preflightIAMediaResult(IAAcceptExtraInventory, command.identity, command.digest, facts); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaExtraInventoryResult{facts})
	if err != nil {
		return out, err
	}
	if err = iaExtraWriteReceipt(ctx, turn, command.identity, command.digest, facts, digest); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaExtraAcceptOutcome{decision: receiptFresh, result: iaExtraInventoryResult{facts}, digest: digest}, nil
}

func iaExtraCheckDirectory(ctx context.Context, turn *commandTurn, r iaExtraAcceptRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaExtraAcceptDirectory, r.scope.runID, r.directory.path, r.directory.identity, r.directory.revision, r.directory.expectedCursor, r.directory.pages, r.directory.skipped)
	if err != nil {
		return err
	}
	var pages, skipped int64
	if err = row.Scan(&pages, &skipped); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if pages != r.directory.pages || skipped != r.directory.skipped {
		return ErrConflict
	}
	return nil
}

type iaExtraAssetFacts struct {
	id, infoEpoch, fileSizeRevision int64
	token                           string
}

func iaExtraCheckAsset(ctx context.Context, turn *commandTurn, r iaExtraAcceptRequest) (out iaExtraAssetFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	f := r.files[0]
	row, err := wc008DraftQuery(ctx, turn, iaExtraAcceptAsset, r.scope.libraryID, f.assetPath, f.size, f.modifiedNS)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.id, &out.token, &out.infoEpoch, &out.fileSizeRevision); errors.Is(err, sql.ErrNoRows) {
		return iaExtraAssetFacts{}, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.id <= 0 || out.token == "" || out.infoEpoch <= 0 || out.fileSizeRevision <= 0 {
		return out, ErrResolveRequired
	}
	return out, nil
}

// One retained extra item. Reused items are read (and shape checked) before any
// write; a new item is allocated only after every gate.
type iaExtraItem struct {
	key, title                     string
	subjectID, revision, ownerKind int64
	incarnation                    [16]byte
	created                        bool
}

// The owner is evidence, not authority: it must be the exact current
// same-library entity of the expected kind. An unresolved owner stays nil.
func iaExtraResolveOwner(ctx context.Context, turn *commandTurn, r iaExtraAcceptRequest) (value iaExtraItem, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if r.ownerKey == "" {
		return iaExtraItem{}, nil
	}
	row, err := wc008DraftQuery(ctx, turn, iaExtraAcceptIdentity, r.scope.libraryID, r.ownerKey)
	if err != nil {
		return value, err
	}
	var subjectID int64
	if err = row.Scan(&subjectID); errors.Is(err, sql.ErrNoRows) {
		return value, ErrConflict
	}
	if err != nil {
		return value, err
	}
	entity, err := iaExtraReadEntity(ctx, turn, r.scope.libraryID, subjectID)
	if err != nil {
		return value, err
	}
	if entity.retired || entity.kind != r.ownerKind || entity.kind < 1 || entity.kind > 4 {
		return value, ErrConflict
	}
	return iaExtraItem{subjectID: subjectID, revision: entity.revision, incarnation: entity.incarnation, ownerKind: entity.kind}, nil
}

type iaExtraEntityFacts struct {
	kind, revision int64
	title          string
	retired        bool
	incarnation    [16]byte
}

func iaExtraReadEntity(ctx context.Context, turn *commandTurn, libraryID, subjectID int64) (value iaExtraEntityFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaExtraAcceptEntity, subjectID, libraryID)
	if err != nil {
		return value, err
	}
	var retired int64
	var incarnation []byte
	if err = row.Scan(&value.kind, &value.title, &retired, &value.revision, &incarnation); err != nil {
		return value, errors.Join(ErrConflict, err)
	}
	if len(incarnation) != 16 || value.revision <= 0 {
		return value, ErrResolveRequired
	}
	value.retired = retired != 0
	copy(value.incarnation[:], incarnation)
	return value, nil
}

// The extra's exact item_extras row is part of its identity: a reused key whose
// kind, title, parent path, owner, asset or relative path differs is a conflict,
// never a silent rewrite. A new extra is allocated only after every gate.
func iaExtraResolveItem(ctx context.Context, turn *commandTurn, r iaExtraAcceptRequest, ownerID int64, assetToken string, appliedMS int64) (out iaExtraItem, created bool, err error) {
	node := iaExtraItem{key: r.itemKey, title: r.itemTitle}
	row, err := wc008DraftQuery(ctx, turn, iaExtraAcceptIdentity, r.scope.libraryID, r.itemKey)
	if err != nil {
		return out, false, err
	}
	var subjectID int64
	err = row.Scan(&subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		created = true
		out, err = iaExtraCreateItem(ctx, turn, r, appliedMS)
		return out, created, err
	}
	if err != nil {
		return out, false, err
	}
	entity, err := iaExtraReadEntity(ctx, turn, r.scope.libraryID, subjectID)
	if err != nil {
		return out, false, err
	}
	if entity.retired || entity.kind != 11 || entity.title != r.itemTitle {
		return out, false, ErrConflict
	}
	row, err = wc008DraftQuery(ctx, turn, iaExtraAcceptItem, subjectID)
	if err != nil {
		return out, false, err
	}
	var library, parentPath, kind, title, token, relative string
	var owner sql.NullInt64
	if err = row.Scan(&library, &parentPath, &owner, &kind, &title, &token, &relative); errors.Is(err, sql.ErrNoRows) {
		return out, false, ErrConflict
	}
	if err != nil {
		return out, false, err
	}
	ownerMatches := owner.Valid && ownerID > 0 && owner.Int64 == ownerID || !owner.Valid && ownerID == 0
	if library != r.scope.libraryKey || parentPath != r.parentPath || kind != r.extraKind || title != r.itemTitle ||
		!ownerMatches || token != assetToken || relative != r.files[0].relativePath {
		return out, false, ErrConflict
	}
	node.subjectID, node.revision, node.incarnation = subjectID, entity.revision, entity.incarnation
	return node, false, nil
}

// One subject, its exact source identity, one extra entity and the exact
// item_extras row. The entity carries no title/art fanout beyond its own title.
func iaExtraCreateItem(ctx context.Context, turn *commandTurn, r iaExtraAcceptRequest, appliedMS int64) (out iaExtraItem, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	var public, metadata, artwork, playback, random [16]byte
	for _, b := range [][]byte{public[:], metadata[:], artwork[:], playback[:], random[:]} {
		if _, err = rand.Read(b); err != nil {
			return out, err
		}
	}
	projection, err := ProjectFilenameMetadata(r.itemTitle, "", r.files[0].metadataLanguage)
	if err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, iaExtraAcceptSubjectInsert, public[:], r.scope.libraryID, int64(11))
	if err != nil {
		return out, err
	}
	var subjectID int64
	if err = row.Scan(&subjectID); err != nil {
		return out, err
	}
	if subjectID <= 0 {
		return out, ErrConflict
	}
	if err = wc008DraftChanged(ctx, turn, iaExtraAcceptIdentityInsert, r.scope.libraryID, r.itemKey, subjectID); err != nil {
		return out, err
	}
	addedAt := time.UnixMilli(appliedMS).UTC().Format("2006-01-02T15:04:05.000000000Z")
	row, err = wc008DraftQuery(ctx, turn, iaExtraAcceptEntityInsert, subjectID, public[:], r.scope.libraryID, int64(11),
		projection.Title, projection.SortTitle, projection.SortKey, projection.SortHead, projection.SortKeyKeepArticles, projection.SortHeadKeepArticles,
		int64(binary.BigEndian.Uint32(random[:4])), r.files[0].year, addedAt, r.files[0].metadataLanguage,
		metadata[:], artwork[:], playback[:], int64(1))
	if err != nil {
		return out, err
	}
	var actualID, revision int64
	var actualMetadata []byte
	if err = row.Scan(&actualID, &revision, &actualMetadata); err != nil {
		return out, err
	}
	if actualID != subjectID || revision != 1 || !equalBytes(actualMetadata, metadata[:]) {
		return out, ErrConflict
	}
	out = iaExtraItem{key: r.itemKey, title: r.itemTitle, subjectID: subjectID, revision: revision}
	copy(out.incarnation[:], metadata[:])
	return out, nil
}

func iaExtraSearchEntity(node iaExtraItem, libraryID int64) newOrdinaryEntitySource {
	return newOrdinaryEntitySource{SubjectID: node.subjectID, LibraryID: libraryID, Kind: 11, MetadataIncarnation: node.incarnation, EntityRevision: node.revision}
}

func iaExtraAdvanceDirectory(ctx context.Context, turn *commandTurn, r iaExtraAcceptRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if r.directory.pages == math.MaxInt64 {
		return ErrCap
	}
	next := r.directory.pages + 1
	row, err := wc008DraftQuery(ctx, turn, iaExtraAcceptDirectoryCAS, r.directory.nextCursor, next,
		r.scope.runID, r.directory.path, r.directory.identity, r.directory.revision, r.directory.expectedCursor, r.directory.pages, r.directory.skipped)
	if err != nil {
		return err
	}
	var cursor string
	var pages, skipped int64
	if err = row.Scan(&cursor, &pages, &skipped); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if cursor != r.directory.nextCursor || pages != next || skipped != r.directory.skipped {
		return ErrConflict
	}
	return nil
}

// The accept consumes exactly one run sequence and one run revision. It leaves
// the ACK floor untouched: only a separate durable ACK retires this original.
func iaExtraAdvanceRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, r iaExtraAcceptRequest) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if r.run.revision == math.MaxInt64 || identity.Sequence <= 0 || r.run.lastSequence != identity.Sequence-1 {
		return 0, ErrCap
	}
	row, err := wc008DraftQuery(ctx, turn, iaExtraAcceptRunCAS, r.run.revision+1, identity.Sequence,
		r.scope.runID, identity.Incarnation[:], r.scope.libraryID, r.scope.libraryKey, string(r.source.resolvedRoot), r.source.followSymlinks,
		r.source.id, r.source.generation, r.source.incarnation, r.source.rootIdentity, r.run.policyRevision, r.run.phase, r.run.scanMode,
		r.run.leaseOwner, r.run.leaseGeneration, r.run.leaseExpiresMS, r.run.revision, r.run.lastSequence, r.run.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	var revision, sequence int64
	if err = row.Scan(&revision, &sequence); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if revision != r.run.revision+1 || sequence != identity.Sequence {
		return 0, ErrConflict
	}
	return revision, nil
}

// The complete typed cohort is one extra member: the actual retained item ID and
// revision, its truthful owner (or no parent at all when unresolved), and the
// exact accepted asset/link facts. Titles and paths stay bound by the request
// digest and are never reconstructed from live rows.
func iaExtraFacts(r iaExtraAcceptRequest, item, owner iaExtraItem, asset iaExtraAssetFacts, playback [16]byte, runRevision, appliedMS int64) (iaMediaResult, error) {
	facts := iaMediaResult{
		runID: r.scope.runID, sourceID: r.source.id, sourceIncarnation: r.source.incarnation,
		acceptedRevision: runRevision, appliedMS: appliedMS,
		objectID: r.files[0].objectIdentity, objectRevision: r.files[0].revision,
		cursorKind:        r.directory.cursorKind,
		directoryPath:     []byte(r.directory.path),
		directoryIdentity: []byte(r.directory.identity), directoryRevision: []byte(r.directory.revision),
		expectedPayload: []byte(r.directory.expectedCursor), nextPayload: []byte(r.directory.nextCursor),
		resultVersion: 1,
	}
	m := iaMediaMember{kind: "extra", libraryID: r.scope.libraryID, subjectID: item.subjectID, subjectKind: 11,
		subjectRevision: item.revision, metadataIncarnation: item.incarnation,
		assetID: iaEpisodePtr(asset.id), assetInfoEpoch: iaEpisodePtr(asset.infoEpoch), partIndex: iaEpisodePtr(r.part),
		linkOriginRevision: iaEpisodePtr(int64(1)), linkOriginIncarnation: &playback, edition: &r.edition, linkEffect: iaEpisodePtr("present"),
		startSeconds: &r.start, endSeconds: r.end, extraKind: &r.extraKind}
	if owner.subjectID > 0 {
		m.parentID, m.parentKind = iaEpisodePtr(owner.subjectID), iaEpisodePtr(owner.ownerKind)
	}
	m.ordinal = 0
	facts.members = []iaMediaMember{m}
	facts.resultCount = 1
	n, err := iaMediaMemberRetainedBytes(IAAcceptExtraInventory, facts.runID, m)
	if err != nil {
		return iaMediaResult{}, err
	}
	facts.memberBytes = n
	return facts, nil
}

func iaExtraWriteReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, facts iaMediaResult, digest ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = wc008DraftChanged(ctx, turn, iaExtraAcceptReceiptInsert, facts.runID, identity.Incarnation[:], identity.Sequence, identity.OperationID[:], request[:],
		iaExtraAcceptReceiptKind, facts.acceptedRevision, facts.appliedMS, facts.sourceID, facts.sourceIncarnation, nil, nil, "", facts.resultCount, digest[:],
		facts.resultVersion, facts.memberBytes, facts.objectID, facts.objectRevision, facts.cursorKind,
		facts.directoryPath, facts.directoryIdentity, facts.directoryRevision, facts.expectedPayload, facts.nextPayload); err != nil {
		return err
	}
	for _, m := range facts.members {
		if err = wc008DraftChanged(ctx, turn, iaExtraAcceptMemberInsert, facts.runID, identity.Incarnation[:], identity.Sequence, iaExtraAcceptReceiptKind,
			m.ordinal, m.kind, m.libraryID, m.subjectID, m.subjectKind, m.subjectRevision, m.metadataIncarnation[:],
			iaEpisodeNullableInt(m.parentID), iaEpisodeNullableInt(m.parentKind), iaEpisodeNullableInt(m.ancestorID), iaEpisodeNullableInt(m.ancestorKind),
			iaEpisodeNullableInt(m.assetID), iaEpisodeNullableInt(m.assetInfoEpoch), iaEpisodeNullableInt(m.partIndex),
			iaEpisodeNullableInt(m.linkOriginRevision), iaEpisodeNullableBlob(m.linkOriginIncarnation),
			iaEpisodeNullableText(m.edition), iaEpisodeNullableText(m.linkEffect), iaEpisodeNullableInt(m.cueOrdinal),
			iaEpisodeNullableReal(m.startSeconds), iaEpisodeNullableReal(m.endSeconds),
			iaEpisodeNullableText(m.episodeBoundaryStatus), iaEpisodeNullableText(m.readingFormat),
			iaEpisodeNullableInt(m.readingPathHead), iaEpisodeNullableInt(m.readingPathSequence), iaEpisodeNullableText(m.extraKind)); err != nil {
			return err
		}
	}
	return nil
}

// Replay decodes only the immutable receipt and its complete typed child
// cohort. Current source, directory, asset and lease rows are never consulted.
func lookupIAExtraInventory(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaExtraAcceptCommand, header iaExtraSavedHeader) (out iaExtraHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || authority == nil || command == nil || !command.valid ||
		!header.facts.exists || header.facts.kind != IAAcceptExtraInventory || header.facts.identity != (receiptIdentity{command.identity, command.digest}) ||
		header.facts.resultCount != 1 || header.memberBytes <= 0 ||
		header.acceptedRevision <= 0 || header.appliedMS < 0 || header.sourceID == "" || header.sourceIncarnation == "" ||
		header.objectID == "" || header.objectRevision == "" || len(header.directoryPath) == 0 || len(header.directoryIdentity) == 0 || len(header.directoryRevision) == 0 || len(header.nextPayload) == 0 {
		return out, ErrResolveRequired
	}
	if err = authority.budget.check(ctx, turn, header.memberBytes); err != nil {
		return out, err
	}
	for _, id := range []stmt.ID{iaExtraAcceptMemberBytes, iaExtraAcceptMembers} {
		if _, ok := turn.steps[id]; !ok {
			return out, invalid("ia_extra_member_steps")
		}
	}
	limit := int64(2)
	rows, err := turn.query(ctx, turn.steps[iaExtraAcceptMemberBytes].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
	if err != nil {
		return out, err
	}
	var length int64
	var members int
	readErr := func() error {
		for rows.Next() {
			var ordinal, n int64
			if err := rows.Scan(&ordinal, &n); err != nil {
				return err
			}
			if ordinal != 0 || n <= 0 || members != 0 {
				return ErrResolveRequired
			}
			length, members = n, 1
		}
		return rows.Err()
	}()
	if err := errors.Join(readErr, rows.Close()); err != nil {
		return out, err
	}
	if members != 1 || length != header.memberBytes {
		return out, ErrResolveRequired
	}
	if err = authority.budget.check(ctx, turn, length); err != nil {
		return out, err
	}
	rows, err = turn.query(ctx, turn.steps[iaExtraAcceptMembers].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
	if err != nil {
		return out, err
	}
	facts := iaMediaResult{
		runID: command.request.scope.runID, sourceID: header.sourceID, sourceIncarnation: header.sourceIncarnation,
		acceptedRevision: header.acceptedRevision, appliedMS: header.appliedMS,
		objectID: header.objectID, objectRevision: header.objectRevision, cursorKind: header.cursorKind,
		directoryPath: header.directoryPath, directoryIdentity: header.directoryIdentity, directoryRevision: header.directoryRevision,
		expectedPayload: header.expectedPayload, nextPayload: header.nextPayload, resultVersion: 1,
	}
	readErr = func() error {
		for rows.Next() {
			var m iaMediaMember
			var parent, parentKind, ancestor, ancestorKind, asset, epoch, part, linkRevision, cue sql.NullInt64
			var start, end sql.NullFloat64
			var metadata, linkIncarnation []byte
			var edition, effect, boundary, reading, extra sql.NullString
			var readingHead, readingSequence sql.NullInt64
			if err := rows.Scan(&m.ordinal, &m.kind, &m.libraryID, &m.subjectID, &m.subjectKind, &m.subjectRevision, &metadata,
				&parent, &parentKind, &ancestor, &ancestorKind, &asset, &epoch, &part, &linkRevision, &linkIncarnation,
				&edition, &effect, &cue, &start, &end, &boundary, &reading, &readingHead, &readingSequence, &extra); err != nil {
				return err
			}
			if m.ordinal != int64(len(facts.members)) || len(facts.members) != 0 || len(metadata) != 16 {
				return ErrResolveRequired
			}
			copy(m.metadataIncarnation[:], metadata)
			m.parentID, m.parentKind = iaEpisodePointer(parent), iaEpisodePointer(parentKind)
			m.ancestorID, m.ancestorKind = iaEpisodePointer(ancestor), iaEpisodePointer(ancestorKind)
			m.assetID, m.assetInfoEpoch, m.partIndex, m.linkOriginRevision = iaEpisodePointer(asset), iaEpisodePointer(epoch), iaEpisodePointer(part), iaEpisodePointer(linkRevision)
			if linkIncarnation != nil {
				var v [16]byte
				if len(linkIncarnation) != 16 {
					return ErrResolveRequired
				}
				copy(v[:], linkIncarnation)
				m.linkOriginIncarnation = &v
			}
			m.edition, m.linkEffect = iaEpisodeTextPointer(edition), iaEpisodeTextPointer(effect)
			m.cueOrdinal = iaEpisodePointer(cue)
			m.startSeconds, m.endSeconds = iaEpisodeRealPointer(start), iaEpisodeRealPointer(end)
			m.episodeBoundaryStatus = iaEpisodeTextPointer(boundary)
			m.readingFormat, m.readingPathHead, m.readingPathSequence = iaEpisodeTextPointer(reading), iaEpisodePointer(readingHead), iaEpisodePointer(readingSequence)
			m.extraKind = iaEpisodeTextPointer(extra)
			n, err := iaMediaMemberRetainedBytes(IAAcceptExtraInventory, facts.runID, m)
			if err != nil || n != length {
				return ErrResolveRequired
			}
			facts.members = append(facts.members, m)
		}
		return rows.Err()
	}()
	if err := errors.Join(readErr, rows.Close()); err != nil {
		return out, err
	}
	if len(facts.members) != 1 {
		return out, ErrResolveRequired
	}
	facts.resultCount = 1
	facts.memberBytes = length
	if err = preflightIAMediaResult(IAAcceptExtraInventory, command.identity, command.digest, facts); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaExtraInventoryResult{facts})
	if err != nil || !equalBytes(digest[:], header.facts.resultDigest[:]) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	return iaExtraHistory{decision: receiptReplay, result: iaExtraInventoryResult{facts}, digest: digest}, nil
}
