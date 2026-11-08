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

// IA-12 is the music variant of the IA accepting class. Its private outcome and
// history carry the exact typed result; no public facade, worker or producer is
// wired, so the accepting body stays uncalled outside this package.
type iaMusicAcceptOutcome struct {
	decision receiptDecision
	result   iaAudioInventoryResult
	digest   ResultDigest
}
type iaMusicHistory struct {
	decision receiptDecision
	result   iaAudioInventoryResult
	digest   ResultDigest
}

// Same-turn saved header, never a disclosure capability. The complete typed
// member cohort is decoded only after current authority and the receipt floor.
type iaMusicSavedHeader struct {
	facts                                               receiptFacts
	acceptedRevision, appliedMS, memberBytes            int64
	sourceID, sourceIncarnation                         string
	objectID, objectRevision, cursorKind                string
	directoryPath, directoryIdentity, directoryRevision []byte
	expectedPayload, nextPayload                        []byte
}

// One governed music accept or original replay. Current owner authority is
// established BEFORE any retained owner/header read; fresh source, directory,
// asset and run witnesses are compared only after the receipt is Fresh. A saved
// result is decoded from the immutable receipt/child cohort, never a live row.
func acceptIAMusicInventory(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, r iaMusicInventoryRequest) (out iaMusicAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAAcceptAudioInventory {
		return out, ErrInvalid
	}
	command, err := newIAMusicInventoryCommand(reference.Identity, r)
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
	owner, header, err := iaMusicReceiptFacts(ctx, turn, r.scope.runID, reference)
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
		return iaMusicAcceptOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIAMusicInventory(ctx, turn, authority, command, header)
		if err != nil {
			return out, err
		}
		return iaMusicAcceptOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIAMusicInventory(ctx, turn, authority, command, reference)
	default:
		return out, ErrResolveRequired
	}
}

// Owner and header facts are decoded by finite points scoped to the exact
// original run/incarnation/sequence. No current source/lease/asset row filters
// history; a kind mismatch is a conflict, never a fresh admission.
func iaMusicReceiptFacts(ctx context.Context, turn *commandTurn, runID string, reference ReceiptReference) (owner receiptOwnerFacts, header iaMusicSavedHeader, err error) {
	row, err := wc008DraftQuery(ctx, turn, iaMusicOwner, runID)
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

	row, err = wc008DraftQuery(ctx, turn, iaMusicHeader, runID, reference.Identity.Incarnation[:], reference.Identity.Sequence)
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
	if kind == iaMusicReceiptKind {
		header.facts.kind = IAAcceptAudioInventory
	}
	return owner, header, nil
}

// Fresh effects are one complete unit: artist/album/song identities, the
// music hierarchy, song-artist edges, asset links and contributions, search
// intent, the directory/run advance and the typed receipt/child cohort. Every
// gate is re-proven before the first write and the caller's authority is
// rechecked before any fresh result is released.
func applyIAMusicInventory(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaMusicInventoryCommand, reference ReceiptReference) (out iaMusicAcceptOutcome, err error) {
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
	if id, err := wc008DraftInteger(ctx, turn, iaMusicLibrary, r.scope.libraryID, r.scope.libraryKey); err != nil || id != r.scope.libraryID {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := wc008DraftQuery(ctx, turn, iaMusicSource, r.source.id, r.scope.libraryKey, r.source.generation, r.source.incarnation, r.source.rootIdentity, string(r.source.resolvedRoot), r.source.followSymlinks)
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
	if err = iaMusicCheckDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	asset, err := iaMusicCheckAsset(ctx, turn, r)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	nodes, err := iaMusicPlanNodes(ctx, turn, r)
	if err != nil {
		return out, err
	}
	allocated, err := iaMusicAllocateNodes(ctx, turn, r, nodes, appliedMS)
	if err != nil {
		return out, err
	}
	// Links/contributions bind the exact observed asset. A repeated song link is
	// a conflict; no silent upsert, reparent or album rewrite exists.
	file := r.files[0]
	links := make([]iaMusicLinkFacts, len(r.songs))
	for i, song := range r.songs {
		node := allocated.songs[i]
		var playback [16]byte
		if _, err = rand.Read(playback[:]); err != nil {
			return out, err
		}
		var cue any
		if song.cueOrdinal > 0 {
			cue = song.cueOrdinal
		}
		if err = wc008DraftChanged(ctx, turn, iaMusicLinkInsert, node.subjectID, asset.id, song.part, int64(1), song.start, iaEpisodeNullableReal(song.end), "", playback[:], int64(1), cue, r.scope.libraryID); err != nil {
			return out, err
		}
		if err = wc008DraftChanged(ctx, turn, iaMusicContributionInsert, r.scope.libraryID, node.subjectID, asset.id, int64(1), int64(0), file.size, asset.fileSizeRevision); err != nil {
			return out, err
		}
		for _, key := range r.songArtistKeys {
			if err = wc008DraftChanged(ctx, turn, iaMusicSongArtistInsert, node.subjectID, allocated.artistIDs[key]); err != nil {
				return out, err
			}
		}
		links[i] = iaMusicLinkFacts{song: node, link: song, assetID: asset.id, infoEpoch: asset.infoEpoch, playback: playback}
	}
	if err = applyIAMediaSearchInput(ctx, turn, allocated.newEntities, appliedMS, 5, 7); err != nil {
		return out, err
	}
	if err = iaMusicAdvanceDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	runRevision, err := iaMusicAdvanceRun(ctx, turn, command.identity, r)
	if err != nil {
		return out, err
	}
	facts, err := iaMusicFacts(r, allocated, links, runRevision, appliedMS)
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
	if err = iaMusicWriteReceipt(ctx, turn, command.identity, command.digest, facts, digest); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaMusicAcceptOutcome{decision: receiptFresh, result: iaAudioInventoryResult{facts}, digest: digest}, nil
}

func iaMusicCheckDirectory(ctx context.Context, turn *commandTurn, r iaMusicInventoryRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaMusicDirectory, r.scope.runID, r.directory.path, r.directory.identity, r.directory.revision, r.directory.expectedCursor, r.directory.pages, r.directory.skipped)
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

type iaMusicAssetFacts struct {
	id, infoEpoch, fileSizeRevision int64
}

func iaMusicCheckAsset(ctx context.Context, turn *commandTurn, r iaMusicInventoryRequest) (out iaMusicAssetFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	f := r.files[0]
	row, err := wc008DraftQuery(ctx, turn, iaMusicAsset, r.scope.libraryID, f.assetPath, f.size, f.modifiedNS)
	if err != nil {
		return out, err
	}
	var token string
	if err = row.Scan(&out.id, &token, &out.infoEpoch, &out.fileSizeRevision); errors.Is(err, sql.ErrNoRows) {
		return iaMusicAssetFacts{}, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.id <= 0 || token == "" || out.infoEpoch <= 0 || out.fileSizeRevision <= 0 {
		return out, ErrResolveRequired
	}
	return out, nil
}

// One prepared node per identity source key. Reused nodes are read (and shape
// checked) before any write; new nodes are allocated only after every gate.
type iaMusicNode struct {
	key                   string
	kind                  int64
	title                 string
	subjectID, revision   int64
	incarnation           [16]byte
	created               bool
	parent, artist, album int64
	disc, track           int64
}

type iaMusicEntityFacts struct {
	kind, parent, artist, album, revision int64
	retired                               bool
	incarnation                           [16]byte
}

func iaMusicReadEntity(ctx context.Context, turn *commandTurn, libraryID, subjectID int64) (value iaMusicEntityFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaMusicEntity, subjectID, libraryID)
	if err != nil {
		return value, err
	}
	var parent, artist, album sql.NullInt64
	var retired int64
	var incarnation []byte
	if err = row.Scan(&value.kind, &parent, &artist, &album, &retired, &value.revision, &incarnation); err != nil {
		return value, errors.Join(ErrConflict, err)
	}
	if len(incarnation) != 16 || value.revision <= 0 {
		return value, ErrResolveRequired
	}
	value.parent, value.artist, value.album = parent.Int64, artist.Int64, album.Int64
	value.retired = retired != 0
	copy(value.incarnation[:], incarnation)
	return value, nil
}

// A reused identity must be exactly the prepared music shape. A retired or
// differently parented subject is a conflict, never a silent reparent.
func iaMusicResolveNode(ctx context.Context, turn *commandTurn, libraryID int64, key, title string, kind, parent, artist, album, disc, track int64) (value iaMusicNode, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaMusicIdentity, libraryID, key)
	if err != nil {
		return value, err
	}
	var subjectID int64
	err = row.Scan(&subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return iaMusicNode{key: key, kind: kind, title: title, created: true, parent: parent, artist: artist, album: album, disc: disc, track: track}, nil
	}
	if err != nil {
		return value, err
	}
	entity, err := iaMusicReadEntity(ctx, turn, libraryID, subjectID)
	if err != nil {
		return value, err
	}
	if entity.retired || entity.kind != kind {
		return value, ErrConflict
	}
	switch kind {
	case 5:
		if entity.parent != 0 || entity.artist != 0 || entity.album != 0 {
			return value, ErrConflict
		}
	case 6:
		if entity.parent != artist || entity.artist != artist || entity.album != 0 {
			return value, ErrConflict
		}
	case 7:
		if entity.parent != album || entity.album != album || entity.artist != artist {
			return value, ErrConflict
		}
	}
	return iaMusicNode{key: key, kind: kind, title: title, subjectID: subjectID, revision: entity.revision, incarnation: entity.incarnation,
		parent: parent, artist: artist, album: album, disc: disc, track: track}, nil
}

type iaMusicAllocation struct {
	artists     []iaMusicNode
	album       iaMusicNode
	songs       []iaMusicNode
	artistIDs   map[string]int64
	newEntities []newOrdinaryEntitySource
}

// Node order is parent-first: each artist, then the album, then songs in
// complete cohort order. This is the hierarchy chronology every edge relies on.
func iaMusicPlanNodes(ctx context.Context, turn *commandTurn, r iaMusicInventoryRequest) (out iaMusicAllocation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	out.artists = make([]iaMusicNode, len(r.artists))
	for i, artist := range r.artists {
		node, err := iaMusicResolveNode(ctx, turn, r.scope.libraryID, artist.key, artist.title, 5, 0, 0, 0, 0, 0)
		if err != nil {
			return out, err
		}
		out.artists[i] = node
	}
	albumArtistID := out.artists[iaMusicArtistIndex(r.artists, r.albumArtistKey)].subjectID
	out.album, err = iaMusicResolveNode(ctx, turn, r.scope.libraryID, r.albumKey, r.albumTitle, 6, albumArtistID, albumArtistID, 0, 0, 0)
	if err != nil {
		return out, err
	}
	out.songs = make([]iaMusicNode, len(r.songs))
	for i, song := range r.songs {
		node, err := iaMusicResolveNode(ctx, turn, r.scope.libraryID, song.key, song.title, 7, out.album.subjectID, albumArtistID, out.album.subjectID, song.disc, song.track)
		if err != nil {
			return out, err
		}
		out.songs[i] = node
	}
	return out, nil
}

func iaMusicArtistIndex(artists []iaMusicArtist, key string) int {
	for i := range artists {
		if artists[i].key == key {
			return i
		}
	}
	return -1
}

// Allocation runs only after every fresh gate. Parent IDs are assigned before
// children, so each entity insert carries a real same-library parent.
func iaMusicAllocateNodes(ctx context.Context, turn *commandTurn, r iaMusicInventoryRequest, nodes iaMusicAllocation, appliedMS int64) (out iaMusicAllocation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	addedAt := time.UnixMilli(appliedMS).UTC().Format("2006-01-02T15:04:05.000000000Z")
	libraryID := r.scope.libraryID
	language := r.files[0].metadataLanguage
	year := r.files[0].year
	out.artists = make([]iaMusicNode, len(nodes.artists))
	out.artistIDs = make(map[string]int64, len(nodes.artists))
	for i, artist := range nodes.artists {
		if artist.created {
			if artist, err = iaMusicCreateNode(ctx, turn, libraryID, artist, year, language, addedAt); err != nil {
				return out, err
			}
			out.newEntities = append(out.newEntities, iaMusicSearchEntity(artist, libraryID))
		}
		out.artists[i] = artist
		out.artistIDs[artist.key] = artist.subjectID
	}
	albumArtistID := out.artistIDs[r.albumArtistKey]
	out.album = nodes.album
	if out.album.created {
		out.album.parent, out.album.artist = albumArtistID, albumArtistID
		if out.album, err = iaMusicCreateNode(ctx, turn, libraryID, out.album, year, language, addedAt); err != nil {
			return out, err
		}
		out.newEntities = append(out.newEntities, iaMusicSearchEntity(out.album, libraryID))
	}
	out.songs = make([]iaMusicNode, len(nodes.songs))
	for i, song := range nodes.songs {
		if song.created {
			song.parent, song.artist, song.album = out.album.subjectID, albumArtistID, out.album.subjectID
			if song, err = iaMusicCreateNode(ctx, turn, libraryID, song, year, language, addedAt); err != nil {
				return out, err
			}
			out.newEntities = append(out.newEntities, iaMusicSearchEntity(song, libraryID))
		}
		out.songs[i] = song
	}
	return out, nil
}

func iaMusicSearchEntity(node iaMusicNode, libraryID int64) newOrdinaryEntitySource {
	return newOrdinaryEntitySource{SubjectID: node.subjectID, LibraryID: libraryID, Kind: node.kind, MetadataIncarnation: node.incarnation, EntityRevision: node.revision}
}

// One subject, its exact source identity and one music entity. The album carries
// its album artist, the song carries that same album and album artist plus its
// own disc/track position; independent song artists are separate edges. No
// provider ID, enrichment, artwork or playback state is claimed.
func iaMusicCreateNode(ctx context.Context, turn *commandTurn, libraryID int64, node iaMusicNode, year int64, language, addedAt string) (out iaMusicNode, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	var public, metadata, artwork, playback, music [16]byte
	var random [4]byte
	for _, b := range [][]byte{public[:], metadata[:], artwork[:], playback[:], music[:], random[:]} {
		if _, err = rand.Read(b); err != nil {
			return out, err
		}
	}
	projection, err := ProjectFilenameMetadata(node.title, "", language)
	if err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, iaMusicSubjectInsert, public[:], libraryID, node.kind)
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
	if err = wc008DraftChanged(ctx, turn, iaMusicIdentityInsert, libraryID, node.key, subjectID); err != nil {
		return out, err
	}
	var nullableParent, nullableArtist, nullableAlbum, nullableDisc, nullableTrack any
	if node.parent > 0 {
		nullableParent = node.parent
	}
	if node.artist > 0 {
		nullableArtist = node.artist
	}
	if node.album > 0 {
		nullableAlbum = node.album
	}
	if node.disc > 0 {
		nullableDisc = node.disc
	}
	if node.track > 0 {
		nullableTrack = node.track
	}
	row, err = wc008DraftQuery(ctx, turn, iaMusicEntityInsert, subjectID, public[:], libraryID, node.kind, nullableParent,
		projection.Title, projection.SortTitle, projection.SortKey, projection.SortHead, projection.SortKeyKeepArticles, projection.SortHeadKeepArticles,
		int64(binary.BigEndian.Uint32(random[:])), year, addedAt, language, nullableArtist, nullableAlbum, nullableDisc, nullableTrack, nil, "",
		metadata[:], artwork[:], playback[:], int64(1), music[:], int64(1), int64(1))
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

func iaMusicAdvanceDirectory(ctx context.Context, turn *commandTurn, r iaMusicInventoryRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if r.directory.pages == math.MaxInt64 {
		return ErrCap
	}
	next := r.directory.pages + 1
	row, err := wc008DraftQuery(ctx, turn, iaMusicDirectoryCAS, r.directory.nextCursor, next,
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
func iaMusicAdvanceRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, r iaMusicInventoryRequest) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if r.run.revision == math.MaxInt64 || identity.Sequence <= 0 || r.run.lastSequence != identity.Sequence-1 {
		return 0, ErrCap
	}
	row, err := wc008DraftQuery(ctx, turn, iaMusicRunCAS, r.run.revision+1, identity.Sequence,
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

type iaMusicLinkFacts struct {
	song      iaMusicNode
	link      iaMusicSong
	assetID   int64
	infoEpoch int64
	playback  [16]byte
}

// The complete typed cohort: every artist identity, the album identity, each
// song identity, each independent song-artist edge, then each song membership.
// Ordinals are contiguous from zero.
func iaMusicFacts(r iaMusicInventoryRequest, nodes iaMusicAllocation, links []iaMusicLinkFacts, runRevision, appliedMS int64) (iaMediaResult, error) {
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
	for _, artist := range nodes.artists {
		add(iaMediaMember{kind: "identity", libraryID: r.scope.libraryID, subjectID: artist.subjectID, subjectKind: 5,
			subjectRevision: artist.revision, metadataIncarnation: artist.incarnation})
	}
	add(iaMediaMember{kind: "identity", libraryID: r.scope.libraryID, subjectID: nodes.album.subjectID, subjectKind: 6,
		subjectRevision: nodes.album.revision, metadataIncarnation: nodes.album.incarnation,
		parentID: iaEpisodePtr(nodes.artistIDs[r.albumArtistKey]), parentKind: iaEpisodePtr(int64(5))})
	for _, link := range links {
		add(iaMediaMember{kind: "identity", libraryID: r.scope.libraryID, subjectID: link.song.subjectID, subjectKind: 7,
			subjectRevision: link.song.revision, metadataIncarnation: link.song.incarnation,
			parentID: iaEpisodePtr(nodes.album.subjectID), parentKind: iaEpisodePtr(int64(6))})
	}
	for _, link := range links {
		for _, key := range r.songArtistKeys {
			add(iaMediaMember{kind: "artist", libraryID: r.scope.libraryID, subjectID: link.song.subjectID, subjectKind: 7,
				subjectRevision: link.song.revision, metadataIncarnation: link.song.incarnation,
				parentID: iaEpisodePtr(nodes.artistIDs[key]), parentKind: iaEpisodePtr(int64(5))})
		}
	}
	for _, link := range links {
		edition := ""
		present := "present"
		start := link.link.start
		playback := link.playback
		var cue *int64
		if link.link.cueOrdinal > 0 {
			cue = iaEpisodePtr(link.link.cueOrdinal)
		}
		add(iaMediaMember{kind: "membership", libraryID: r.scope.libraryID, subjectID: link.song.subjectID, subjectKind: 7,
			subjectRevision: link.song.revision, metadataIncarnation: link.song.incarnation,
			assetID: iaEpisodePtr(link.assetID), assetInfoEpoch: iaEpisodePtr(link.infoEpoch), partIndex: iaEpisodePtr(link.link.part),
			linkOriginRevision: iaEpisodePtr(int64(1)), linkOriginIncarnation: &playback, edition: &edition, linkEffect: &present,
			cueOrdinal: cue, startSeconds: &start, endSeconds: link.link.end})
	}
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

func iaMusicWriteReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, facts iaMediaResult, digest ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = wc008DraftChanged(ctx, turn, iaMusicReceiptInsert, facts.runID, identity.Incarnation[:], identity.Sequence, identity.OperationID[:], request[:],
		iaMusicReceiptKind, facts.acceptedRevision, facts.appliedMS, facts.sourceID, facts.sourceIncarnation, nil, nil, "", facts.resultCount, digest[:],
		facts.resultVersion, facts.memberBytes, facts.objectID, facts.objectRevision, facts.cursorKind,
		facts.directoryPath, facts.directoryIdentity, facts.directoryRevision, facts.expectedPayload, facts.nextPayload); err != nil {
		return err
	}
	for _, m := range facts.members {
		if err = wc008DraftChanged(ctx, turn, iaMusicMemberInsert, facts.runID, identity.Incarnation[:], identity.Sequence, iaMusicReceiptKind,
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
func lookupIAMusicInventory(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaMusicInventoryCommand, header iaMusicSavedHeader) (out iaMusicHistory, err error) {
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
	for _, id := range []stmt.ID{iaMusicMemberBytes, iaMusicMembers} {
		if _, ok := turn.steps[id]; !ok {
			return out, invalid("ia_music_member_steps")
		}
	}
	limit := header.facts.resultCount + 1
	rows, err := turn.query(ctx, turn.steps[iaMusicMemberBytes].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
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
	rows, err = turn.query(ctx, turn.steps[iaMusicMembers].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
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
	return iaMusicHistory{decision: receiptReplay, result: iaAudioInventoryResult{facts}, digest: digest}, nil
}
