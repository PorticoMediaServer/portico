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

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/bookseries"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const iaBookRequestVersion uint16 = 1

// IA-13 is the audiobook/book variant of the IA accepting class. Its private
// outcome and history carry the exact typed result; no public facade, worker or
// producer is wired, so the accepting body stays uncalled outside this package.
type iaBookAcceptOutcome struct {
	decision receiptDecision
	result   iaAudioInventoryResult
	digest   ResultDigest
}
type iaBookHistory struct {
	decision receiptDecision
	result   iaAudioInventoryResult
	digest   ResultDigest
}

// Same-turn saved header, never a disclosure capability. The complete typed
// member cohort is decoded only after current authority and the receipt floor.
type iaBookSavedHeader struct {
	facts                                               receiptFacts
	acceptedRevision, appliedMS, memberBytes            int64
	sourceID, sourceIncarnation                         string
	objectID, objectRevision, cursorKind                string
	directoryPath, directoryIdentity, directoryRevision []byte
	expectedPayload, nextPayload                        []byte
}

// Prepared observations only. The book/file identity source keys are caller
// naming or scan evidence. The author value is exact accepted bytes: it is
// never trimmed, normalized or case-folded here, and its catalogue key is the
// canonical AuthorIdentityKey ("author:"+Unicode lowercase). No current
// authority, asset row, entity ID or search generation is captured.
type iaBookInventoryRequest struct {
	scope     inventoryScope
	source    inventorySource
	run       inventoryRun
	directory inventoryDirectory
	files     []inventoryFile // exactly one original audiobook media observation
	complete  bool            // complete cohort assertion, not source proof

	bookKey, bookTitle, fileKey, fileTitle   string
	author, narrator, series, seriesPosition string
	year                                     int64
}

type iaBookInventoryCommand struct {
	identity CommandIdentity
	request  iaBookInventoryRequest
	digest   RequestDigest
	valid    bool
}

func newIABookInventoryCommand(id CommandIdentity, r iaBookInventoryRequest) (*iaBookInventoryCommand, error) {
	// Counts precede all text traversal, copying and hashing.
	if len(r.files) != 1 {
		return nil, invalid("ia_book_count")
	}
	size := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	iaEpisodeWireSize(reflect.ValueOf(r), &size)
	if size > maxCanonicalRequestBytes {
		return nil, &PolicyError{Field: "ia_book_bytes", Limit: maxCanonicalRequestBytes, Cause: ErrCap}
	}
	if !iaEpisodeValidText(reflect.ValueOf(r)) || !utf8.Valid(r.source.resolvedRoot) || bytes.IndexByte(r.source.resolvedRoot, 0) >= 0 {
		return nil, invalid("ia_book_text")
	}
	if err := validateIABookOriginal(id, r); err != nil {
		return nil, err
	}
	digest, err := canonicalRequestDigest(IAAcceptAudioInventory, iaBookRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	r.files = slices.Clone(r.files)
	r.source.resolvedRoot = bytes.Clone(r.source.resolvedRoot)
	for _, s := range iaBookTexts(&r) {
		*s = strings.Clone(*s)
	}
	return &iaBookInventoryCommand{identity: id, request: r, digest: digest, valid: true}, nil
}

// Every accepting gate is a closed witness comparison. The folder title,
// author and file title are prepared evidence. A blank author is still exact
// bytes and still gets its exact key, but it grants no author membership.
func validateIABookOriginal(id CommandIdentity, r iaBookInventoryRequest) error {
	s, u, d := r.source, r.run, r.directory
	if id.Sequence <= 0 || id.OperationID == ([16]byte{}) || id.Incarnation == ([16]byte{}) ||
		r.scope.runID == "" || r.scope.libraryKey == "" || r.scope.libraryID <= 0 ||
		s.id == "" || s.incarnation == "" || s.rootIdentity == "" || s.generation <= 0 || len(s.resolvedRoot) == 0 || s.resolvedRoot[0] != '/' ||
		u.revision <= 0 || u.policyRevision <= 0 || u.leaseOwner <= 0 || u.leaseGeneration <= 0 || u.leaseExpiresMS <= 0 || u.lastSequence != id.Sequence-1 || u.acknowledgedSequence != u.lastSequence || u.phase != "inventory" || !inventoryScanMode(u.scanMode) ||
		d.identity == "" || d.revision == "" || d.cursorKind != inventoryLinuxCursor || !inventoryPath(d.path, true) || d.pages < 0 || d.skipped < 0 || d.rawNames != 1 || d.unstable {
		return invalid("ia_book_original")
	}
	root := string(s.resolvedRoot)
	f := r.files[0]
	if !r.complete || strings.ContainsAny(root, `\`) || path.Clean(root) != root || f.sourceKey == "" || f.objectIdentity == "" || f.revision == "" ||
		f.directory || f.size < 0 || f.part < 0 || f.year < 0 || f.metadataLanguage == "" || !inventoryPath(f.relativePath, false) ||
		path.Dir(f.relativePath) != d.path || strings.ContainsAny(f.assetPath, `\`) || path.Clean(f.assetPath) != f.assetPath || f.assetPath != path.Join(root, f.relativePath) {
		return invalid("ia_book_file")
	}
	// A sequence position without a series would invent a projection key that
	// IA-14 does not have; refuse it here instead of silently dropping it.
	if !iaEpisodeKey(r.bookKey) || strings.TrimSpace(r.bookTitle) == "" || !iaEpisodeKey(r.fileKey) ||
		strings.TrimSpace(r.fileTitle) == "" || r.year < 0 || r.seriesPosition != "" && r.series == "" {
		return invalid("ia_book_metadata")
	}
	// The title policy is the same one ProjectFilenameMetadata enforces, so an
	// overlong author, narrator or title refuses before any gate or write rather
	// than part-way through allocation.
	for _, text := range []string{r.author, r.narrator, r.bookTitle, r.fileTitle} {
		if err := ValidateMetadataShape(text, "", 0, 0, 0); err != nil {
			return err
		}
	}
	// The accepted series cohort is bounded by the same canonical rule IA-14
	// publishes with, so an overlong pair refuses up front instead of rolling
	// back after the author/book writes.
	if err := bookseries.Validate(iaBookSeriesAcceptedPairs(r)); err != nil {
		return iaBookSeriesInputError(err)
	}
	return nil
}

func (c *iaBookInventoryCommand) checkOriginal() error {
	if c == nil || !c.valid {
		return invalid("ia_book_command")
	}
	checked, err := newIABookInventoryCommand(c.identity, c.request)
	if err != nil || checked.digest != c.digest {
		return ErrConflict
	}
	return nil
}

func iaBookTexts(r *iaBookInventoryRequest) []*string {
	base := inventoryAcceptRequest{scope: r.scope, source: r.source, run: r.run, directory: r.directory, files: r.files}
	texts := []*string{&r.scope.runID, &r.scope.libraryKey, &r.source.id, &r.source.incarnation, &r.source.rootIdentity, &r.run.phase, &r.run.scanMode, &r.directory.path, &r.directory.identity, &r.directory.revision, &r.directory.expectedCursor, &r.directory.nextCursor, &r.directory.cursorKind}
	texts = append(texts, inventoryRequestTexts(&base)[14:]...)
	texts = append(texts, &r.bookKey, &r.bookTitle, &r.author, &r.narrator, &r.series, &r.seriesPosition, &r.fileKey, &r.fileTitle)
	return texts
}

// iaBookAuthorIdentityKey is the exact dbwork.AuthorIdentityKey rule. The
// writer package deliberately does not import dbwork: its init registers
// deterministic SQL functions, and adding them to every writer plan capture
// would change pragma_function_list. scan/ia_author_key.go uses dbwork directly
// and pins both forms to the same literals.
func iaBookAuthorIdentityKey(author string) string { return "author:" + strings.ToLower(author) }

// iaBookAuthorMember reports whether the exact author bytes may become a kind-13
// author identity. Empty and whitespace-only values keep their exact bytes and
// exact keys, but no author membership and no rename fanout.
func iaBookAuthorMember(author string) bool { return strings.TrimSpace(author) != "" }

// One governed book accept or original replay. Current owner authority is
// established BEFORE any retained owner/header read; fresh source, directory,
// asset and run witnesses are compared only after the receipt is Fresh. A saved
// result is decoded from the immutable receipt/child cohort, never a live row.
func acceptIABookInventory(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, r iaBookInventoryRequest) (out iaBookAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAAcceptAudioInventory {
		return out, ErrInvalid
	}
	command, err := newIABookInventoryCommand(reference.Identity, r)
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
	owner, header, err := iaBookReceiptFacts(ctx, turn, r.scope.runID, reference)
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(receiptIdentity{reference.Identity, reference.RequestDigest}, IAAcceptAudioInventory, owner, header.facts)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	switch decision {
	case receiptRetired:
		return iaBookAcceptOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIABookInventory(ctx, turn, authority, command, header)
		if err != nil {
			return out, err
		}
		return iaBookAcceptOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIABookInventory(ctx, turn, authority, command, reference)
	default:
		return out, ErrResolveRequired
	}
}

// Owner and header facts are decoded by finite points scoped to the exact
// original run/incarnation/sequence. No current source/lease/asset row filters
// history; a kind mismatch is a conflict, never a fresh admission.
func iaBookReceiptFacts(ctx context.Context, turn *commandTurn, runID string, reference ReceiptReference) (owner receiptOwnerFacts, header iaBookSavedHeader, err error) {
	row, err := wc008DraftQuery(ctx, turn, iaBookOwner, runID)
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

	row, err = wc008DraftQuery(ctx, turn, iaBookHeader, runID, reference.Identity.Incarnation[:], reference.Identity.Sequence)
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
	if kind == iaBookReceiptKind {
		header.facts.kind = IAAcceptAudioInventory
	}
	return owner, header, nil
}

// Fresh effects are one complete unit: the author identity (when the exact
// author grants membership), the book identity with author+key together, the
// audiobook-file identity, the file membership, search intent, the
// directory/run advance and the typed receipt/child cohort. Every gate is
// re-proven before the first write and the caller's authority is rechecked
// before any fresh result is released.
func applyIABookInventory(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaBookInventoryCommand, reference ReceiptReference) (out iaBookAcceptOutcome, err error) {
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
	if id, err := wc008DraftInteger(ctx, turn, iaBookLibrary, r.scope.libraryID, r.scope.libraryKey); err != nil || id != r.scope.libraryID {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := wc008DraftQuery(ctx, turn, iaBookSource, r.source.id, r.scope.libraryKey, r.source.generation, r.source.incarnation, r.source.rootIdentity, string(r.source.resolvedRoot), r.source.followSymlinks)
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
	if err = iaBookCheckDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	asset, err := iaBookCheckAsset(ctx, turn, r)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	nodes, err := iaBookPlanNodes(ctx, turn, r)
	if err != nil {
		return out, err
	}
	allocated, err := iaBookAllocateNodes(ctx, turn, r, nodes, appliedMS)
	if err != nil {
		return out, err
	}
	// The complete file membership binds the exact observed asset. A repeated
	// link is never a silent upsert or a reparent.
	var playback [16]byte
	if _, err = rand.Read(playback[:]); err != nil {
		return out, err
	}
	if err = wc008DraftChanged(ctx, turn, iaBookLinkInsert, allocated.file.subjectID, asset.id, int64(0), int64(1), float64(0), nil, "", playback[:], int64(1), nil, r.scope.libraryID); err != nil {
		return out, err
	}
	if err = wc008DraftChanged(ctx, turn, iaBookContributionInsert, r.scope.libraryID, allocated.file.subjectID, asset.id, int64(1), int64(0), r.files[0].size, asset.fileSizeRevision); err != nil {
		return out, err
	}
	// IA-14: the accepted local series pairs publish with this exact accepted
	// audiobook source and policy revision. The projection and its head commit
	// or roll back with the rest of the unit.
	if err = iaBookSeriesPublish(ctx, turn, r.scope.libraryID, allocated.book.subjectID, asset, r.run.policyRevision, iaBookSeriesAcceptedPairs(r)); err != nil {
		return out, err
	}
	if err = applyIAMediaSearchInput(ctx, turn, allocated.newEntities, appliedMS, 8, 13); err != nil {
		return out, err
	}
	if err = iaBookAdvanceDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	runRevision, err := iaBookAdvanceRun(ctx, turn, command.identity, r)
	if err != nil {
		return out, err
	}
	facts, err := iaBookFacts(r, allocated, asset, playback, runRevision, appliedMS)
	if err != nil {
		return out, err
	}
	if err = preflightIAMediaResult(IAAcceptAudioInventory, command.identity, command.digest, facts); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaAudioInventoryResult{facts})
	if err != nil {
		return out, err
	}
	if err = iaBookWriteReceipt(ctx, turn, command.identity, command.digest, facts, digest); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaBookAcceptOutcome{decision: receiptFresh, result: iaAudioInventoryResult{facts}, digest: digest}, nil
}

func iaBookCheckDirectory(ctx context.Context, turn *commandTurn, r iaBookInventoryRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaBookDirectory, r.scope.runID, r.directory.path, r.directory.identity, r.directory.revision, r.directory.expectedCursor, r.directory.pages, r.directory.skipped)
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

type iaBookAssetFacts struct {
	id, infoEpoch, fileSizeRevision int64
	token                           string
}

func iaBookCheckAsset(ctx context.Context, turn *commandTurn, r iaBookInventoryRequest) (out iaBookAssetFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	f := r.files[0]
	row, err := wc008DraftQuery(ctx, turn, iaBookAsset, r.scope.libraryID, f.assetPath, f.size, f.modifiedNS)
	if err != nil {
		return out, err
	}
	var token string
	if err = row.Scan(&out.id, &token, &out.infoEpoch, &out.fileSizeRevision); errors.Is(err, sql.ErrNoRows) {
		return iaBookAssetFacts{}, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.id <= 0 || token == "" || out.infoEpoch <= 0 || out.fileSizeRevision <= 0 {
		return out, ErrResolveRequired
	}
	out.token = token
	return out, nil
}

// One prepared node per identity source key. Reused nodes are read (and shape
// checked) before any write; new nodes are allocated only after every gate.
type iaBookNode struct {
	key                        string
	kind                       int64
	title                      string
	subjectID, revision        int64
	incarnation                [16]byte
	created                    bool
	parent, book               int64
	author, authorKey          string
	narrator, series, position string
}

type iaBookEntityFacts struct {
	kind, parent, book, revision int64
	parentSet, bookSet           bool
	retired                      bool
	incarnation                  [16]byte
	author, authorKey, narrator  string
	series, seriesPosition       string
}

func iaBookReadEntity(ctx context.Context, turn *commandTurn, libraryID, subjectID int64) (value iaBookEntityFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaBookEntity, subjectID, libraryID)
	if err != nil {
		return value, err
	}
	var parent, book sql.NullInt64
	var authorKey sql.NullString
	var retired int64
	var incarnation []byte
	if err = row.Scan(&value.kind, &parent, &book, &value.author, &authorKey, &value.narrator, &value.series, &value.seriesPosition,
		&retired, &value.revision, &incarnation); err != nil {
		return value, errors.Join(ErrConflict, err)
	}
	if len(incarnation) != 16 || value.revision <= 0 {
		return value, ErrResolveRequired
	}
	value.parent, value.parentSet = parent.Int64, parent.Valid
	value.book, value.bookSet = book.Int64, book.Valid
	value.authorKey = authorKey.String
	value.retired = retired != 0
	copy(value.incarnation[:], incarnation)
	return value, nil
}

// The author identity is the canonical exact key, never a title heuristic. A
// reused author is not renamed: its first accepted display bytes stay put and
// the new book carries its own exact author bytes beside the same key.
func iaBookResolveAuthor(ctx context.Context, turn *commandTurn, libraryID int64, author string) (value iaBookNode, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if !iaBookAuthorMember(author) {
		return iaBookNode{}, nil
	}
	key := iaBookAuthorIdentityKey(author)
	row, err := wc008DraftQuery(ctx, turn, iaBookIdentity, libraryID, key)
	if err != nil {
		return value, err
	}
	var subjectID int64
	err = row.Scan(&subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return iaBookNode{key: key, kind: 13, title: author, created: true}, nil
	}
	if err != nil {
		return value, err
	}
	entity, err := iaBookReadEntity(ctx, turn, libraryID, subjectID)
	if err != nil {
		return value, err
	}
	if entity.retired || entity.kind != 13 || entity.parentSet || entity.bookSet {
		return value, ErrConflict
	}
	return iaBookNode{key: key, kind: 13, title: author, subjectID: subjectID, revision: entity.revision, incarnation: entity.incarnation}, nil
}

// A reused book must agree on the exact author bytes AND the exact key. Writing
// only one of them on reuse would either rename the author or break the key
// invariant, so a mismatch is a conflict rather than an update.
func iaBookResolveBook(ctx context.Context, turn *commandTurn, libraryID int64, key, title, author, authorKey string, parent int64, narrator, series, position string) (value iaBookNode, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	node := iaBookNode{key: key, kind: 8, title: title, parent: parent, author: author, authorKey: authorKey, narrator: narrator, series: series, position: position}
	row, err := wc008DraftQuery(ctx, turn, iaBookIdentity, libraryID, key)
	if err != nil {
		return value, err
	}
	var subjectID int64
	err = row.Scan(&subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		node.created = true
		return node, nil
	}
	if err != nil {
		return value, err
	}
	entity, err := iaBookReadEntity(ctx, turn, libraryID, subjectID)
	if err != nil {
		return value, err
	}
	if entity.retired || entity.kind != 8 || entity.parent != parent || entity.author != author || entity.authorKey != authorKey {
		return value, ErrConflict
	}
	node.subjectID, node.revision, node.incarnation = subjectID, entity.revision, entity.incarnation
	return node, nil
}

func iaBookResolveFile(ctx context.Context, turn *commandTurn, libraryID int64, key, title string, bookID int64) (value iaBookNode, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	node := iaBookNode{key: key, kind: 9, title: title, parent: bookID, book: bookID}
	row, err := wc008DraftQuery(ctx, turn, iaBookIdentity, libraryID, key)
	if err != nil {
		return value, err
	}
	var subjectID int64
	err = row.Scan(&subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		node.created = true
		return node, nil
	}
	if err != nil {
		return value, err
	}
	entity, err := iaBookReadEntity(ctx, turn, libraryID, subjectID)
	if err != nil {
		return value, err
	}
	if entity.retired || entity.kind != 9 || !entity.parentSet || entity.parent != bookID || !entity.bookSet || entity.book != bookID {
		return value, ErrConflict
	}
	node.subjectID, node.revision, node.incarnation = subjectID, entity.revision, entity.incarnation
	return node, nil
}

type iaBookAllocation struct {
	author      iaBookNode
	book        iaBookNode
	file        iaBookNode
	newEntities []newOrdinaryEntitySource
}

// Node order is author-first, then the book, then the audiobook file. This is
// the chronology every parent edge and the receipt cohort rely on.
func iaBookPlanNodes(ctx context.Context, turn *commandTurn, r iaBookInventoryRequest) (out iaBookAllocation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	author, err := iaBookResolveAuthor(ctx, turn, r.scope.libraryID, r.author)
	if err != nil {
		return out, err
	}
	authorKey := iaBookAuthorIdentityKey(r.author)
	book, err := iaBookResolveBook(ctx, turn, r.scope.libraryID, r.bookKey, r.bookTitle, r.author, authorKey, author.subjectID,
		r.narrator, r.series, r.seriesPosition)
	if err != nil {
		return out, err
	}
	file, err := iaBookResolveFile(ctx, turn, r.scope.libraryID, r.fileKey, r.fileTitle, book.subjectID)
	if err != nil {
		return out, err
	}
	out.author, out.book, out.file = author, book, file
	return out, nil
}

// Allocation runs only after every fresh gate. Parent IDs are assigned before
// children, so each entity insert carries a real same-library parent.
func iaBookAllocateNodes(ctx context.Context, turn *commandTurn, r iaBookInventoryRequest, nodes iaBookAllocation, appliedMS int64) (out iaBookAllocation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	addedAt := time.UnixMilli(appliedMS).UTC().Format("2006-01-02T15:04:05.000000000Z")
	libraryID := r.scope.libraryID
	language := r.files[0].metadataLanguage
	out.author = nodes.author
	if out.author.created {
		if out.author, err = iaBookCreateNode(ctx, turn, libraryID, out.author, 0, 0, 0, language, addedAt); err != nil {
			return out, err
		}
		out.newEntities = append(out.newEntities, iaBookSearchEntity(out.author, libraryID))
	}
	out.book = nodes.book
	if out.book.created {
		out.book.parent = out.author.subjectID
		if out.book, err = iaBookCreateNode(ctx, turn, libraryID, out.book, out.book.parent, 0, r.year, language, addedAt); err != nil {
			return out, err
		}
		out.newEntities = append(out.newEntities, iaBookSearchEntity(out.book, libraryID))
	}
	out.file = nodes.file
	if out.file.created {
		out.file.parent, out.file.book = out.book.subjectID, out.book.subjectID
		if out.file, err = iaBookCreateNode(ctx, turn, libraryID, out.file, out.file.parent, out.file.book, r.year, language, addedAt); err != nil {
			return out, err
		}
		out.newEntities = append(out.newEntities, iaBookSearchEntity(out.file, libraryID))
	}
	return out, nil
}

func iaBookSearchEntity(node iaBookNode, libraryID int64) newOrdinaryEntitySource {
	return newOrdinaryEntitySource{SubjectID: node.subjectID, LibraryID: libraryID, Kind: node.kind, MetadataIncarnation: node.incarnation, EntityRevision: node.revision}
}

// One subject, its exact source identity and one catalogue entity. An author
// row carries no book relation. A book row carries the exact author bytes and
// its exact key together. An audiobook file carries its book relation. No
// provider ID, enrichment, artwork or playback state is claimed.
func iaBookCreateNode(ctx context.Context, turn *commandTurn, libraryID int64, node iaBookNode, parent, book, year int64, language, addedAt string) (out iaBookNode, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	var public, metadata, artwork, playback, random [16]byte
	for _, b := range [][]byte{public[:], metadata[:], artwork[:], playback[:], random[:]} {
		if _, err = rand.Read(b); err != nil {
			return out, err
		}
	}
	projection, err := ProjectFilenameMetadata(node.title, "", language)
	if err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, iaBookSubjectInsert, public[:], libraryID, node.kind)
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
	if err = wc008DraftChanged(ctx, turn, iaBookIdentityInsert, libraryID, node.key, subjectID); err != nil {
		return out, err
	}
	var nullableParent, nullableBook, authorKey any
	if parent > 0 {
		nullableParent = parent
	}
	if book > 0 {
		nullableBook = book
	}
	if node.kind == 8 {
		authorKey = node.authorKey
	}
	row, err = wc008DraftQuery(ctx, turn, iaBookEntityInsert, subjectID, public[:], libraryID, node.kind, nullableParent,
		projection.Title, projection.SortTitle, projection.SortKey, projection.SortHead, projection.SortKeyKeepArticles, projection.SortHeadKeepArticles,
		int64(binary.BigEndian.Uint32(random[:4])), year, addedAt, language, nullableBook,
		node.author, node.narrator, node.series, node.position, authorKey,
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
	out = node
	out.subjectID, out.revision = subjectID, revision
	copy(out.incarnation[:], metadata[:])
	return out, nil
}

func iaBookAdvanceDirectory(ctx context.Context, turn *commandTurn, r iaBookInventoryRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if r.directory.pages == math.MaxInt64 {
		return ErrCap
	}
	next := r.directory.pages + 1
	row, err := wc008DraftQuery(ctx, turn, iaBookDirectoryCAS, r.directory.nextCursor, next,
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
func iaBookAdvanceRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, r iaBookInventoryRequest) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if r.run.revision == math.MaxInt64 || identity.Sequence <= 0 || r.run.lastSequence != identity.Sequence-1 {
		return 0, ErrCap
	}
	row, err := wc008DraftQuery(ctx, turn, iaBookRunCAS, r.run.revision+1, identity.Sequence,
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

// The complete typed cohort: the author identity (when the author grants
// membership), the book identity with its author parent, the audiobook-file
// identity with its book parent, then the file membership. Ordinals are
// contiguous from zero.
func iaBookFacts(r iaBookInventoryRequest, nodes iaBookAllocation, asset iaBookAssetFacts, playback [16]byte, runRevision, appliedMS int64) (iaMediaResult, error) {
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
	add := func(m iaMediaMember) {
		m.ordinal = int64(len(facts.members))
		facts.members = append(facts.members, m)
	}
	if nodes.author.subjectID > 0 {
		add(iaMediaMember{kind: "identity", libraryID: r.scope.libraryID, subjectID: nodes.author.subjectID, subjectKind: 13,
			subjectRevision: nodes.author.revision, metadataIncarnation: nodes.author.incarnation})
	}
	book := iaMediaMember{kind: "identity", libraryID: r.scope.libraryID, subjectID: nodes.book.subjectID, subjectKind: 8,
		subjectRevision: nodes.book.revision, metadataIncarnation: nodes.book.incarnation}
	if nodes.author.subjectID > 0 {
		book.parentID, book.parentKind = iaEpisodePtr(nodes.author.subjectID), iaEpisodePtr(int64(13))
	}
	add(book)
	add(iaMediaMember{kind: "identity", libraryID: r.scope.libraryID, subjectID: nodes.file.subjectID, subjectKind: 9,
		subjectRevision: nodes.file.revision, metadataIncarnation: nodes.file.incarnation,
		parentID: iaEpisodePtr(nodes.book.subjectID), parentKind: iaEpisodePtr(int64(8))})
	edition := ""
	present := "present"
	start := float64(0)
	add(iaMediaMember{kind: "membership", libraryID: r.scope.libraryID, subjectID: nodes.file.subjectID, subjectKind: 9,
		subjectRevision: nodes.file.revision, metadataIncarnation: nodes.file.incarnation,
		assetID: iaEpisodePtr(asset.id), assetInfoEpoch: iaEpisodePtr(asset.infoEpoch), partIndex: iaEpisodePtr(int64(0)),
		linkOriginRevision: iaEpisodePtr(int64(1)), linkOriginIncarnation: &playback, edition: &edition, linkEffect: &present,
		startSeconds: &start})
	facts.resultCount = int64(len(facts.members))
	for _, m := range facts.members {
		n, err := iaMediaMemberRetainedBytes(IAAcceptAudioInventory, facts.runID, m)
		if err != nil {
			return iaMediaResult{}, err
		}
		facts.memberBytes += n
	}
	return facts, nil
}

func iaBookWriteReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, facts iaMediaResult, digest ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = wc008DraftChanged(ctx, turn, iaBookReceiptInsert, facts.runID, identity.Incarnation[:], identity.Sequence, identity.OperationID[:], request[:],
		iaBookReceiptKind, facts.acceptedRevision, facts.appliedMS, facts.sourceID, facts.sourceIncarnation, nil, nil, "", facts.resultCount, digest[:],
		facts.resultVersion, facts.memberBytes, facts.objectID, facts.objectRevision, facts.cursorKind,
		facts.directoryPath, facts.directoryIdentity, facts.directoryRevision, facts.expectedPayload, facts.nextPayload); err != nil {
		return err
	}
	for _, m := range facts.members {
		if err = wc008DraftChanged(ctx, turn, iaBookMemberInsert, facts.runID, identity.Incarnation[:], identity.Sequence, iaBookReceiptKind,
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
func lookupIABookInventory(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaBookInventoryCommand, header iaBookSavedHeader) (out iaBookHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || authority == nil || command == nil || !command.valid ||
		!header.facts.exists || header.facts.kind != IAAcceptAudioInventory || header.facts.identity != (receiptIdentity{command.identity, command.digest}) ||
		header.facts.resultCount < 1 || header.facts.resultCount > 4096 || header.memberBytes <= 0 ||
		header.acceptedRevision <= 0 || header.appliedMS < 0 || header.sourceID == "" || header.sourceIncarnation == "" ||
		header.objectID == "" || header.objectRevision == "" || len(header.directoryPath) == 0 || len(header.directoryIdentity) == 0 || len(header.directoryRevision) == 0 || len(header.nextPayload) == 0 {
		return out, ErrResolveRequired
	}
	if err = authority.budget.check(ctx, turn, header.memberBytes); err != nil {
		return out, err
	}
	for _, id := range []stmt.ID{iaBookMemberBytes, iaBookMembers} {
		if _, ok := turn.steps[id]; !ok {
			return out, invalid("ia_book_member_steps")
		}
	}
	limit := header.facts.resultCount + 1
	rows, err := turn.query(ctx, turn.steps[iaBookMemberBytes].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
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
	rows, err = turn.query(ctx, turn.steps[iaBookMembers].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
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
			n, err := iaMediaMemberRetainedBytes(IAAcceptAudioInventory, facts.runID, m)
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
	if err = preflightIAMediaResult(IAAcceptAudioInventory, command.identity, command.digest, facts); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaAudioInventoryResult{facts})
	if err != nil || !equalBytes(digest[:], header.facts.resultDigest[:]) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	return iaBookHistory{decision: receiptReplay, result: iaAudioInventoryResult{facts}, digest: digest}, nil
}
