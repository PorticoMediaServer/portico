package writer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IA-15 is the static reading variant of the IA accepting class. Its private
// outcome and history carry the exact typed result; no public facade, worker or
// producer is wired, so the accepting body stays uncalled outside this package.
type iaReadingAcceptOutcome struct {
	decision receiptDecision
	result   iaReadingInventoryResult
	digest   ResultDigest
}
type iaReadingHistory struct {
	decision receiptDecision
	result   iaReadingInventoryResult
	digest   ResultDigest
}

// Same-turn saved header, never a disclosure capability. The complete typed
// member cohort is decoded only after current authority and the receipt floor.
type iaReadingSavedHeader struct {
	facts                                               receiptFacts
	acceptedRevision, appliedMS, memberBytes            int64
	sourceID, sourceIncarnation                         string
	objectID, objectRevision, cursorKind                string
	directoryPath, directoryIdentity, directoryRevision []byte
	expectedPayload, nextPayload                        []byte
}

type iaReadingNode struct {
	key, title      string
	kind            int64
	publicationType string
	subjectID       int64
	revision        int64
	incarnation     [16]byte
	created         bool
	pathHead        bool
	pathSequence    int64
}

type iaReadingEntityFacts struct {
	kind, revision        int64
	publicationType       string
	publicationTypeSource string
	retired               bool
	incarnation           [16]byte
}

// One governed reading accept or original replay. Current owner authority is
// established BEFORE any retained owner/header read; fresh source, job,
// directory, asset and publication witnesses are compared only after the receipt
// is Fresh. A saved result is decoded from the immutable receipt/child cohort,
// never a live row.
func acceptIAReadingInventory(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, r iaReadingInventoryRequest) (out iaReadingAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAAcceptReadingInventory {
		return out, ErrInvalid
	}
	command, err := newIAReadingInventoryCommand(reference.Identity, r)
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
	owner, header, err := iaReadingReceiptFacts(ctx, turn, r.scope.runID, reference)
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(receiptIdentity{reference.Identity, reference.RequestDigest}, IAAcceptReadingInventory, owner, header.facts)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	switch decision {
	case receiptRetired:
		return iaReadingAcceptOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIAReadingInventory(ctx, turn, authority, command, header)
		if err != nil {
			return out, err
		}
		return iaReadingAcceptOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIAReadingInventory(ctx, turn, authority, command, reference)
	default:
		return out, ErrResolveRequired
	}
}

// Owner and header facts are decoded by finite points scoped to the exact
// original run/incarnation/sequence. No current source/lease/asset row filters
// history; a kind mismatch is a conflict, never a fresh admission.
func iaReadingReceiptFacts(ctx context.Context, turn *commandTurn, runID string, reference ReceiptReference) (owner receiptOwnerFacts, header iaReadingSavedHeader, err error) {
	row, err := wc008DraftQuery(ctx, turn, iaReadingOwner, runID)
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

	row, err = wc008DraftQuery(ctx, turn, iaReadingHeader, runID, reference.Identity.Incarnation[:], reference.Identity.Sequence)
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
	if kind == iaReadingReceiptKind {
		header.facts.kind = IAAcceptReadingInventory
	}
	return owner, header, nil
}

// Fresh effects are one complete unit: the reading item identity, its exact
// publication attachment (link + reading_publications row), search intent, the
// directory/run advance and the typed receipt/child cohort. Every gate is
// re-proven before the first write and the caller's authority is rechecked
// before any fresh result is released.
func applyIAReadingInventory(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaReadingInventoryCommand, reference ReceiptReference) (out iaReadingAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	r := command.request
	if turn == nil || turn.transaction == nil || turn.transaction.writer == nil || turn.transaction.writer.now == nil {
		return out, ErrInvalid
	}
	appliedMS := turn.transaction.writer.now().UnixMilli()
	if appliedMS < 0 || appliedMS >= r.run.leaseExpiresMS {
		return out, ErrConflict
	}
	// Fresh eligibility: live library of the exact reading kind, exact current
	// source, exact reading job, pending directory and the exact observed asset
	// row. Any changed witness refuses the whole unit before a single catalogue,
	// publication or receipt row exists.
	if id, err := wc008DraftInteger(ctx, turn, iaReadingLibrary, r.scope.libraryID, r.scope.libraryKey); err != nil || id != r.scope.libraryID {
		return out, errors.Join(ErrConflict, err)
	}
	libraryKind, err := wc008DraftQuery(ctx, turn, iaReadingLibraryKind, r.scope.libraryKey)
	if err != nil {
		return out, err
	}
	var kindName string
	if err = libraryKind.Scan(&kindName); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if kindName != iaReadingLibraryKindName(r.itemKind) {
		return out, ErrConflict
	}
	row, err := wc008DraftQuery(ctx, turn, iaReadingSource, r.source.id, r.scope.libraryKey, r.source.generation, r.source.incarnation, r.source.rootIdentity, string(r.source.resolvedRoot), r.source.followSymlinks)
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
	if err = iaReadingCheckJob(ctx, turn, r); err != nil {
		return out, err
	}
	if err = iaReadingCheckDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	asset, err := iaReadingCheckAsset(ctx, turn, r)
	if err != nil {
		return out, err
	}
	if err = iaReadingCheckUnattached(ctx, turn, asset.id); err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	node, err := iaReadingPlanNode(ctx, turn, r)
	if err != nil {
		return out, err
	}
	node, err = iaReadingAllocateNode(ctx, turn, r, node, appliedMS)
	if err != nil {
		return out, err
	}
	// The complete publication membership binds the exact observed asset. A
	// repeated link is never a silent upsert or a reparent.
	var playback [16]byte
	if _, err = rand.Read(playback[:]); err != nil {
		return out, err
	}
	if err = wc008DraftChanged(ctx, turn, iaReadingLinkInsert, node.subjectID, asset.id, int64(0), int64(1), float64(0), nil, "", playback[:], int64(1), nil, r.scope.libraryID); err != nil {
		return out, err
	}
	if err = wc008DraftChanged(ctx, turn, iaReadingContributionInsert, r.scope.libraryID, node.subjectID, asset.id, int64(1), int64(0), r.files[0].size, asset.fileSizeRevision); err != nil {
		return out, err
	}
	if err = iaReadingAttachPublication(ctx, turn, r, node, asset); err != nil {
		return out, err
	}
	// Only a newly created reading item needs search intent. A reused item's
	// intent was captured with its first publication and is never re-inserted.
	var search []newOrdinaryEntitySource
	if node.created {
		search = append(search, iaReadingSearchEntity(node, r.scope.libraryID))
	}
	if err = applyIAMediaSearchInput(ctx, turn, search, appliedMS, 20, 21); err != nil {
		return out, err
	}
	if err = iaReadingAdvanceDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	runRevision, err := iaReadingAdvanceRun(ctx, turn, command.identity, r)
	if err != nil {
		return out, err
	}
	facts, err := iaReadingFacts(r, node, asset, playback, runRevision, appliedMS)
	if err != nil {
		return out, err
	}
	if err = preflightIAMediaResult(IAAcceptReadingInventory, command.identity, command.digest, facts); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaReadingInventoryResult{facts})
	if err != nil {
		return out, err
	}
	if err = iaReadingWriteReceipt(ctx, turn, command.identity, command.digest, facts, digest); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaReadingAcceptOutcome{decision: receiptFresh, result: iaReadingInventoryResult{facts}, digest: digest}, nil
}

// A reading item kind and the library kind are one closed pair: an ebook item is
// never accepted into a comic library or the reverse.
func iaReadingLibraryKindName(kind int64) string {
	switch kind {
	case 20:
		return "book"
	case 21:
		return "comic"
	}
	return ""
}

// The reading job is the scan lifecycle row for this exact source. A page cannot
// be accepted into a job that does not exist or that names another source
// incarnation/generation/root.
func iaReadingCheckJob(ctx context.Context, turn *commandTurn, r iaReadingInventoryRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaReadingJob, r.scope.runID)
	if err != nil {
		return err
	}
	var sourceID, incarnation, rootIdentity, root string
	var generation int64
	if err = row.Scan(&sourceID, &incarnation, &generation, &rootIdentity, &root); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return errors.Join(ErrConflict, err)
	}
	if sourceID != r.source.id || incarnation != r.source.incarnation || generation != r.source.generation ||
		rootIdentity != r.source.rootIdentity || root != string(r.source.resolvedRoot) {
		return ErrConflict
	}
	return nil
}

func iaReadingCheckDirectory(ctx context.Context, turn *commandTurn, r iaReadingInventoryRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaReadingDirectory, r.scope.runID, r.directory.path, r.directory.identity, r.directory.revision, r.directory.expectedCursor, r.directory.pages, r.directory.skipped)
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

type iaReadingAssetFacts struct {
	id, infoEpoch, fileSizeRevision int64
}

func iaReadingCheckAsset(ctx context.Context, turn *commandTurn, r iaReadingInventoryRequest) (out iaReadingAssetFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	f := r.files[0]
	row, err := wc008DraftQuery(ctx, turn, iaReadingAsset, r.scope.libraryID, f.assetPath, f.size, f.modifiedNS)
	if err != nil {
		return out, err
	}
	var token string
	if err = row.Scan(&out.id, &token, &out.infoEpoch, &out.fileSizeRevision); errors.Is(err, sql.ErrNoRows) {
		return iaReadingAssetFacts{}, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.id <= 0 || token == "" || out.infoEpoch <= 0 || out.fileSizeRevision <= 0 {
		return out, ErrResolveRequired
	}
	return out, nil
}

// One asset belongs to exactly one publication. A fresh accept never re-attaches
// an asset that already names an item, even the same item: that is a conflicting
// original, not a silent format update.
func iaReadingCheckUnattached(ctx context.Context, turn *commandTurn, assetID int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaReadingPublicationAsset, assetID)
	if err != nil {
		return err
	}
	var item int64
	if err = row.Scan(&item); errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrConflict
}

func iaReadingReadEntity(ctx context.Context, turn *commandTurn, libraryID, subjectID int64) (value iaReadingEntityFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaReadingEntity, subjectID, libraryID)
	if err != nil {
		return value, err
	}
	var retired int64
	var incarnation []byte
	var publicationType, publicationTypeSource sql.NullString
	if err = row.Scan(&value.kind, &retired, &value.revision, &incarnation, &publicationType, &publicationTypeSource); err != nil {
		return value, errors.Join(ErrConflict, err)
	}
	if len(incarnation) != 16 || value.revision <= 0 {
		return value, ErrResolveRequired
	}
	value.retired = retired != 0
	value.publicationType = publicationType.String
	value.publicationTypeSource = publicationTypeSource.String
	copy(value.incarnation[:], incarnation)
	return value, nil
}

// A reused reading item must agree on the exact kind and the exact descriptive
// publication type/source. A mismatch is a conflict, never an update or a
// rename. The path head is the first accepted publication: a new item must claim
// it, and an item that already has publications must not.
func iaReadingPlanNode(ctx context.Context, turn *commandTurn, r iaReadingInventoryRequest) (value iaReadingNode, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	node := iaReadingNode{key: r.itemKey, title: r.itemTitle, kind: r.itemKind, publicationType: r.publicationType, pathHead: r.pathHead, pathSequence: r.pathSequence}
	row, err := wc008DraftQuery(ctx, turn, iaReadingIdentity, r.scope.libraryID, r.itemKey)
	if err != nil {
		return value, err
	}
	var subjectID int64
	err = row.Scan(&subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		// A brand new item is the first publication, so it must be the head.
		if !r.pathHead {
			return value, ErrConflict
		}
		node.created = true
		return node, nil
	}
	if err != nil {
		return value, err
	}
	entity, err := iaReadingReadEntity(ctx, turn, r.scope.libraryID, subjectID)
	if err != nil {
		return value, err
	}
	if entity.retired || entity.kind != r.itemKind || !iaReadingSamePublicationType(entity, r.publicationType) {
		return value, ErrConflict
	}
	row, err = wc008DraftQuery(ctx, turn, iaReadingPublications, subjectID)
	if err != nil {
		return value, err
	}
	var priorAsset int64
	existing := true
	if err = row.Scan(&priorAsset); errors.Is(err, sql.ErrNoRows) {
		existing = false
	} else if err != nil {
		return value, err
	}
	if existing && r.pathHead || !existing && !r.pathHead {
		return value, ErrConflict
	}
	node.subjectID, node.revision, node.incarnation = subjectID, entity.revision, entity.incarnation
	return node, nil
}

func iaReadingSamePublicationType(entity iaReadingEntityFacts, publicationType string) bool {
	if publicationType == "" {
		return entity.publicationType == "" && entity.publicationTypeSource == ""
	}
	return entity.publicationType == publicationType && entity.publicationTypeSource == "file-metadata"
}

// Allocation runs only after every fresh gate. The item subject is born before
// its publication link and the publication row.
func iaReadingAllocateNode(ctx context.Context, turn *commandTurn, r iaReadingInventoryRequest, node iaReadingNode, appliedMS int64) (out iaReadingNode, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !node.created {
		return node, nil
	}
	addedAt := time.UnixMilli(appliedMS).UTC().Format("2006-01-02T15:04:05.000000000Z")
	return iaReadingCreateNode(ctx, turn, r.scope.libraryID, node, r.files[0].year, r.files[0].metadataLanguage, addedAt)
}

func iaReadingSearchEntity(node iaReadingNode, libraryID int64) newOrdinaryEntitySource {
	return newOrdinaryEntitySource{SubjectID: node.subjectID, LibraryID: libraryID, Kind: node.kind, MetadataIncarnation: node.incarnation, EntityRevision: node.revision}
}

// One subject, its exact source identity and one catalogue entity carrying only
// the accepted static reading facts: the item kind and its descriptive
// publication type. No decoded text, page count, cover, attribution member,
// provider ID or readable-representation state is claimed.
func iaReadingCreateNode(ctx context.Context, turn *commandTurn, libraryID int64, node iaReadingNode, year int64, language, addedAt string) (out iaReadingNode, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	var public, metadata, artwork, random [16]byte
	for _, b := range [][]byte{public[:], metadata[:], artwork[:], random[:]} {
		if _, err = rand.Read(b); err != nil {
			return out, err
		}
	}
	projection, err := ProjectFilenameMetadata(node.title, "", language)
	if err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, iaReadingSubjectInsert, public[:], libraryID, node.kind)
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
	if err = wc008DraftChanged(ctx, turn, iaReadingIdentityInsert, libraryID, node.key, subjectID); err != nil {
		return out, err
	}
	var publicationType, publicationTypeSource any
	if node.publicationType != "" {
		publicationType, publicationTypeSource = node.publicationType, "file-metadata"
	}
	row, err = wc008DraftQuery(ctx, turn, iaReadingEntityInsert, subjectID, public[:], libraryID, node.kind,
		projection.Title, projection.SortTitle, projection.SortKey, projection.SortHead, projection.SortKeyKeepArticles, projection.SortHeadKeepArticles,
		int64(binary.BigEndian.Uint32(random[:4])), year, addedAt, language,
		publicationType, publicationTypeSource, metadata[:], artwork[:])
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
	out = node
	out.subjectID, out.revision = subjectID, revision
	copy(out.incarnation[:], metadata[:])
	return out, nil
}

// The publication attachment is the exact accepted asset and its exact observed
// format. The path head keeps the file's own object identity and revision; an
// alternative publication carries no head facts at all.
func iaReadingAttachPublication(ctx context.Context, turn *commandTurn, r iaReadingInventoryRequest, node iaReadingNode, asset iaReadingAssetFacts) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	var sequence any
	var objectID, sourceRevision any
	head := int64(0)
	if r.pathHead {
		head = 1
		sequence, objectID, sourceRevision = r.pathSequence, r.files[0].objectIdentity, r.files[0].revision
	}
	return wc008DraftChanged(ctx, turn, iaReadingPublicationInsert, node.subjectID, asset.id, r.format, head, sequence, objectID, sourceRevision)
}

func iaReadingAdvanceDirectory(ctx context.Context, turn *commandTurn, r iaReadingInventoryRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if r.directory.pages == math.MaxInt64 {
		return ErrCap
	}
	next := r.directory.pages + 1
	row, err := wc008DraftQuery(ctx, turn, iaReadingDirectoryCAS, r.directory.nextCursor, next,
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
func iaReadingAdvanceRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, r iaReadingInventoryRequest) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if r.run.revision == math.MaxInt64 || identity.Sequence <= 0 || r.run.lastSequence != identity.Sequence-1 {
		return 0, ErrCap
	}
	row, err := wc008DraftQuery(ctx, turn, iaReadingRunCAS, r.run.revision+1, identity.Sequence,
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

// The complete typed cohort: one reading publication membership with the exact
// accepted format and its exact path head facts. Ordinals are contiguous from
// zero.
func iaReadingFacts(r iaReadingInventoryRequest, node iaReadingNode, asset iaReadingAssetFacts, playback [16]byte, runRevision, appliedMS int64) (iaMediaResult, error) {
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
	edition := ""
	present := "present"
	start := float64(0)
	format := r.format
	head := int64(0)
	var sequence *int64
	if r.pathHead {
		head = 1
		sequence = iaEpisodePtr(r.pathSequence)
	}
	facts.members = append(facts.members, iaMediaMember{
		ordinal: 0, kind: "reading", libraryID: r.scope.libraryID, subjectID: node.subjectID, subjectKind: node.kind,
		subjectRevision: node.revision, metadataIncarnation: node.incarnation,
		assetID: iaEpisodePtr(asset.id), assetInfoEpoch: iaEpisodePtr(asset.infoEpoch), partIndex: iaEpisodePtr(int64(0)),
		linkOriginRevision: iaEpisodePtr(int64(1)), linkOriginIncarnation: &playback, edition: &edition, linkEffect: &present,
		startSeconds: &start, readingFormat: &format, readingPathHead: iaEpisodePtr(head), readingPathSequence: sequence,
	})
	facts.resultCount = int64(len(facts.members))
	for _, m := range facts.members {
		n, err := iaMediaMemberRetainedBytes(IAAcceptReadingInventory, facts.runID, m)
		if err != nil {
			return iaMediaResult{}, err
		}
		facts.memberBytes += n
	}
	return facts, nil
}

func iaReadingWriteReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, facts iaMediaResult, digest ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = wc008DraftChanged(ctx, turn, iaReadingReceiptInsert, facts.runID, identity.Incarnation[:], identity.Sequence, identity.OperationID[:], request[:],
		iaReadingReceiptKind, facts.acceptedRevision, facts.appliedMS, facts.sourceID, facts.sourceIncarnation, nil, nil, "", facts.resultCount, digest[:],
		facts.resultVersion, facts.memberBytes, facts.objectID, facts.objectRevision, facts.cursorKind,
		facts.directoryPath, facts.directoryIdentity, facts.directoryRevision, facts.expectedPayload, facts.nextPayload); err != nil {
		return err
	}
	for _, m := range facts.members {
		if err = wc008DraftChanged(ctx, turn, iaReadingMemberInsert, facts.runID, identity.Incarnation[:], identity.Sequence, iaReadingReceiptKind,
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
// cohort. Current source, job, directory, asset, publication and lease rows are
// never consulted.
func lookupIAReadingInventory(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaReadingInventoryCommand, header iaReadingSavedHeader) (out iaReadingHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || authority == nil || command == nil || !command.valid ||
		!header.facts.exists || header.facts.kind != IAAcceptReadingInventory || header.facts.identity != (receiptIdentity{command.identity, command.digest}) ||
		header.facts.resultCount != 1 || header.memberBytes <= 0 ||
		header.acceptedRevision <= 0 || header.appliedMS < 0 || header.sourceID == "" || header.sourceIncarnation == "" ||
		header.objectID == "" || header.objectRevision == "" || len(header.directoryPath) == 0 || len(header.directoryIdentity) == 0 || len(header.directoryRevision) == 0 || len(header.nextPayload) == 0 {
		return out, ErrResolveRequired
	}
	if err = authority.budget.check(ctx, turn, header.memberBytes); err != nil {
		return out, err
	}
	for _, id := range []stmt.ID{iaReadingMemberBytes, iaReadingMembers} {
		if _, ok := turn.steps[id]; !ok {
			return out, invalid("ia_reading_member_steps")
		}
	}
	limit := header.facts.resultCount + 1
	rows, err := turn.query(ctx, turn.steps[iaReadingMemberBytes].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
	if err != nil {
		return out, err
	}
	var lengths []int64
	readErr := func() error {
		for rows.Next() {
			var ordinal, n int64
			if err := rows.Scan(&ordinal, &n); err != nil {
				return err
			}
			if ordinal != int64(len(lengths)) || n <= 0 || int64(len(lengths)) >= header.facts.resultCount {
				return ErrResolveRequired
			}
			lengths = append(lengths, n)
		}
		return rows.Err()
	}()
	if err := errors.Join(readErr, rows.Close()); err != nil {
		return out, err
	}
	if int64(len(lengths)) != header.facts.resultCount {
		return out, ErrResolveRequired
	}
	var total int64
	for _, n := range lengths {
		if n > math.MaxInt64-total {
			return out, ErrResolveRequired
		}
		total += n
	}
	if total != header.memberBytes {
		return out, ErrResolveRequired
	}
	if err = authority.budget.check(ctx, turn, total); err != nil {
		return out, err
	}
	rows, err = turn.query(ctx, turn.steps[iaReadingMembers].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
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
			if m.ordinal != int64(len(facts.members)) || int64(len(facts.members)) >= header.facts.resultCount || len(metadata) != 16 {
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
			n, err := iaMediaMemberRetainedBytes(IAAcceptReadingInventory, facts.runID, m)
			if err != nil || n != lengths[len(facts.members)] {
				return ErrResolveRequired
			}
			facts.members = append(facts.members, m)
		}
		return rows.Err()
	}()
	if err := errors.Join(readErr, rows.Close()); err != nil {
		return out, err
	}
	if int64(len(facts.members)) != header.facts.resultCount {
		return out, ErrResolveRequired
	}
	facts.resultCount = int64(len(facts.members))
	facts.memberBytes = total
	if err = preflightIAMediaResult(IAAcceptReadingInventory, command.identity, command.digest, facts); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaReadingInventoryResult{facts})
	if err != nil || !equalBytes(digest[:], header.facts.resultDigest[:]) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	return iaReadingHistory{decision: receiptReplay, result: iaReadingInventoryResult{facts}, digest: digest}, nil
}
