package writer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IA-11 is the first accepting IA media class. Its private outcome and history
// carry the exact typed result; no public facade, worker or producer is wired.
type iaEpisodeAcceptOutcome struct {
	decision receiptDecision
	result   iaEpisodeInventoryResult
	digest   ResultDigest
}
type iaEpisodeHistory struct {
	decision receiptDecision
	result   iaEpisodeInventoryResult
	digest   ResultDigest
}

// Same-turn saved header, never a disclosure capability. The complete typed
// member cohort is decoded only after current authority and the receipt floor.
type iaEpisodeSavedHeader struct {
	facts                                               receiptFacts
	acceptedRevision, appliedMS, memberBytes            int64
	sourceID, sourceIncarnation                         string
	objectID, objectRevision, cursorKind                string
	directoryPath, directoryIdentity, directoryRevision []byte
	expectedPayload, nextPayload                        []byte
}

// One governed episode accept or original replay. Current owner authority is
// established BEFORE any retained owner/header read; fresh source, directory,
// asset and run witnesses are compared only after the receipt is Fresh. A saved
// result is decoded from the immutable receipt/child cohort, never a live row.
func acceptIAEpisodeInventory(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, budget iaEntryBudget, reference ReceiptReference, r iaEpisodeInventoryRequest) (out iaEpisodeAcceptOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IAAcceptEpisodeInventory {
		return out, ErrInvalid
	}
	command, err := newIAEpisodeInventoryCommand(reference.Identity, r)
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
	owner, header, err := iaEpisodeReceiptFacts(ctx, turn, r.scope.runID, reference)
	if err != nil {
		return out, err
	}
	decision, err := classifyReceipt(receiptIdentity{reference.Identity, reference.RequestDigest}, IAAcceptEpisodeInventory, owner, header.facts)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	switch decision {
	case receiptRetired:
		return iaEpisodeAcceptOutcome{decision: receiptRetired}, nil
	case receiptReplay:
		history, err := lookupIAEpisodeInventory(ctx, turn, authority, command, header)
		if err != nil {
			return out, err
		}
		return iaEpisodeAcceptOutcome{decision: history.decision, result: history.result, digest: history.digest}, nil
	case receiptFresh:
		return applyIAEpisodeInventory(ctx, turn, authority, command, reference)
	default:
		return out, ErrResolveRequired
	}
}

// Owner and header facts are decoded by finite points scoped to the exact
// original run/incarnation/sequence. No current source/lease/asset row filters
// history; a kind mismatch is a conflict, never a fresh admission.
func iaEpisodeReceiptFacts(ctx context.Context, turn *commandTurn, runID string, reference ReceiptReference) (owner receiptOwnerFacts, header iaEpisodeSavedHeader, err error) {
	row, err := wc008DraftQuery(ctx, turn, iaEpisodeOwner, runID)
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

	row, err = wc008DraftQuery(ctx, turn, iaEpisodeHeader, runID, reference.Identity.Incarnation[:], reference.Identity.Sequence)
	if err != nil {
		return owner, header, err
	}
	var operation, request, digest []byte
	var kind string
	var resultCount, acceptedRevision, appliedMS, version, memberBytes sql.NullInt64
	var sourceID, sourceIncarnation, objectID, objectRevision, cursorKind sql.NullString
	err = row.Scan(&operation, &request, &kind, &resultCount, &digest, &acceptedRevision, &appliedMS,
		&sourceID, &sourceIncarnation, &version, &memberBytes, &objectID, &objectRevision,
		&cursorKind, &header.directoryPath, &header.directoryIdentity, &header.directoryRevision,
		&header.expectedPayload, &header.nextPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, header, nil
	}
	if err != nil {
		return owner, header, err
	}
	if len(operation) != 16 || len(request) != 32 || len(digest) != 32 {
		return owner, header, ErrResolveRequired
	}
	header.facts.exists = true
	header.facts.identity.command = reference.Identity
	copy(header.facts.identity.command.OperationID[:], operation)
	copy(header.facts.identity.digest[:], request)
	copy(header.facts.resultDigest[:], digest)
	if kind != iaEpisodeReceiptKind {
		// A receipt at this exact key belongs to another command purpose. Its IA
		// media projection is intentionally NULL, so it is left undecoded and
		// classifyReceipt answers the kind mismatch with ErrConflict rather than
		// a raw NULL scan error.
		return owner, header, nil
	}
	if !resultCount.Valid || !acceptedRevision.Valid || !appliedMS.Valid || !version.Valid || version.Int64 != 1 ||
		!memberBytes.Valid || !sourceID.Valid || !sourceIncarnation.Valid || !objectID.Valid || !objectRevision.Valid || !cursorKind.Valid {
		return owner, header, ErrResolveRequired
	}
	header.facts.resultCount = resultCount.Int64
	header.acceptedRevision = acceptedRevision.Int64
	header.appliedMS = appliedMS.Int64
	header.memberBytes = memberBytes.Int64
	header.sourceID, header.sourceIncarnation = sourceID.String, sourceIncarnation.String
	header.objectID, header.objectRevision, header.cursorKind = objectID.String, objectRevision.String, cursorKind.String
	header.facts.kind = IAAcceptEpisodeInventory
	return owner, header, nil
}

// Fresh effects are one complete unit: hierarchy/identity/asset links,
// contributions, show/season chronology, search intent, the directory/run
// advance and the typed receipt/child cohort. Fresh witnesses are proven after
// the receipt is Fresh and before the first catalogue write; the caller's
// authority is rechecked again before any fresh result is released.
func applyIAEpisodeInventory(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaEpisodeInventoryCommand, reference ReceiptReference) (out iaEpisodeAcceptOutcome, err error) {
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
	if id, err := wc008DraftInteger(ctx, turn, iaEpisodeLibrary, r.scope.libraryID, r.scope.libraryKey); err != nil || id != r.scope.libraryID {
		return out, errors.Join(ErrConflict, err)
	}
	row, err := wc008DraftQuery(ctx, turn, iaEpisodeSource, r.source.id, r.scope.libraryKey, r.source.generation, r.source.incarnation, r.source.rootIdentity, string(r.source.resolvedRoot), r.source.followSymlinks)
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
	if err = iaEpisodeCheckDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	asset, err := iaEpisodeCheckAsset(ctx, turn, r)
	if err != nil {
		return out, err
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	nodes, err := iaEpisodePlanNodes(ctx, turn, r)
	if err != nil {
		return out, err
	}
	allocated, err := iaEpisodeAllocateNodes(ctx, turn, r, nodes, appliedMS)
	if err != nil {
		return out, err
	}
	// Links/contributions bind the exact observed asset. A repeated episode
	// link is a conflict; no silent upsert, reparent or edition rewrite exists.
	file := r.files[0]
	links := make([]iaEpisodeLinkFacts, len(r.episodes))
	for i, link := range r.episodes {
		episode := allocated.episodes[i]
		var playback [16]byte
		if _, err = rand.Read(playback[:]); err != nil {
			return out, err
		}
		if err = wc008DraftChanged(ctx, turn, iaEpisodeLinkInsert, episode.subjectID, asset.id, link.part, int64(1), link.start, iaEpisodeNullableReal(link.end), "", playback[:], int64(1), r.scope.libraryID); err != nil {
			return out, err
		}
		if err = wc008DraftChanged(ctx, turn, iaEpisodeContributionInsert, r.scope.libraryID, episode.subjectID, asset.id, int64(1), int64(0), file.size, asset.fileSizeRevision); err != nil {
			return out, err
		}
		links[i] = iaEpisodeLinkFacts{episode: episode, link: link, assetID: asset.id, infoEpoch: asset.infoEpoch, playback: playback}
	}
	// Every accepted link is a genuinely new child membership for its exact
	// destination container. Each container advances at most once to the same
	// accepted event instant and never backwards.
	acceptedAt := iaEpisodeAddedAt(appliedMS)
	if err = iaEpisodeAdvanceChronology(ctx, turn, r.scope.libraryID, allocated.show.subjectID, 2, acceptedAt); err != nil {
		return out, err
	}
	for _, season := range allocated.seasons {
		if err = iaEpisodeAdvanceChronology(ctx, turn, r.scope.libraryID, season.subjectID, 3, acceptedAt); err != nil {
			return out, err
		}
	}
	if err = applyIAEpisodeSearchInput(ctx, turn, allocated.newEntities, appliedMS); err != nil {
		return out, err
	}
	if err = iaEpisodeAdvanceDirectory(ctx, turn, r); err != nil {
		return out, err
	}
	runRevision, err := iaEpisodeAdvanceRun(ctx, turn, command.identity, r)
	if err != nil {
		return out, err
	}
	facts, err := iaEpisodeFacts(r, allocated, links, runRevision, appliedMS)
	if err != nil {
		return out, err
	}
	if err = preflightIAMediaResult(IAAcceptEpisodeInventory, command.identity, command.digest, facts); err != nil {
		return out, err
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaEpisodeInventoryResult{facts})
	if err != nil {
		return out, err
	}
	if err = iaEpisodeWriteReceipt(ctx, turn, command.identity, command.digest, facts, digest); err != nil {
		return out, err
	}
	// A later same-turn revocation still redacts the fresh result.
	if err = authority.recheck(ctx, turn); err != nil {
		return out, ErrDisclosureDenied
	}
	return iaEpisodeAcceptOutcome{decision: receiptFresh, result: iaEpisodeInventoryResult{facts}, digest: digest}, nil
}

func iaEpisodeCheckDirectory(ctx context.Context, turn *commandTurn, r iaEpisodeInventoryRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaEpisodeDirectory, r.scope.runID, r.directory.path, r.directory.identity, r.directory.revision, r.directory.expectedCursor, r.directory.pages, r.directory.skipped)
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

type iaEpisodeAssetFacts struct {
	id, infoEpoch, fileSizeRevision int64
}

func iaEpisodeCheckAsset(ctx context.Context, turn *commandTurn, r iaEpisodeInventoryRequest) (out iaEpisodeAssetFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	f := r.files[0]
	row, err := wc008DraftQuery(ctx, turn, iaEpisodeAsset, r.scope.libraryID, f.assetPath, f.size, f.modifiedNS)
	if err != nil {
		return out, err
	}
	var token string
	if err = row.Scan(&out.id, &token, &out.infoEpoch, &out.fileSizeRevision); errors.Is(err, sql.ErrNoRows) {
		return iaEpisodeAssetFacts{}, ErrConflict
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
type iaEpisodeNode struct {
	key         string
	kind        int64
	subjectID   int64
	revision    int64
	incarnation [16]byte
	created     bool
	season      int64 // season number for kind 3/4 (0 is the Specials season), zero otherwise
	number      int64 // episode number for kind 4, zero otherwise
	parent      int64 // show for season, season/show for episode
	show        int64
	hasSeason   bool // kind 4 only: an explicit season container (0 = Specials)
}

type iaEpisodeEntityFacts struct {
	kind, parent, show, season, seasonNumber, episodeNumber, revision int64
	seasonSet, episodeSet                                             bool
	retired                                                           bool
	incarnation                                                       [16]byte
}

func iaEpisodeReadEntity(ctx context.Context, turn *commandTurn, libraryID, subjectID int64) (value iaEpisodeEntityFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaEpisodeEntity, subjectID, libraryID)
	if err != nil {
		return value, err
	}
	var parent, show, season, seasonNumber, episodeNumber sql.NullInt64
	var retired int64
	var incarnation []byte
	if err = row.Scan(&value.kind, &parent, &show, &season, &seasonNumber, &episodeNumber, &retired, &value.revision, &incarnation); err != nil {
		return value, errors.Join(ErrConflict, err)
	}
	if len(incarnation) != 16 || value.revision <= 0 {
		return value, ErrResolveRequired
	}
	value.parent, value.show, value.season = parent.Int64, show.Int64, season.Int64
	value.seasonNumber, value.seasonSet = seasonNumber.Int64, seasonNumber.Valid
	value.episodeNumber, value.episodeSet = episodeNumber.Int64, episodeNumber.Valid
	value.retired = retired != 0
	copy(value.incarnation[:], incarnation)
	return value, nil
}

// A reused identity must be exactly the prepared container shape. A retired or
// differently parented subject is a conflict, never a silent reparent.
func iaEpisodeResolveNode(ctx context.Context, turn *commandTurn, libraryID int64, key string, kind, season, episode, parent, show int64, hasSeason bool) (value iaEpisodeNode, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	row, err := wc008DraftQuery(ctx, turn, iaEpisodeIdentity, libraryID, key)
	if err != nil {
		return value, err
	}
	var subjectID int64
	err = row.Scan(&subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return iaEpisodeNode{key: key, kind: kind, created: true, season: season, number: episode, parent: parent, show: show, hasSeason: hasSeason}, nil
	}
	if err != nil {
		return value, err
	}
	entity, err := iaEpisodeReadEntity(ctx, turn, libraryID, subjectID)
	if err != nil {
		return value, err
	}
	if entity.retired || entity.kind != kind {
		return value, ErrConflict
	}
	switch kind {
	case 2:
		if entity.parent != 0 || entity.show != 0 || entity.season != 0 {
			return value, ErrConflict
		}
	case 3:
		if entity.show != show || entity.parent != show || entity.season != 0 || !entity.seasonSet || entity.seasonNumber != season {
			return value, ErrConflict
		}
	case 4:
		if entity.show != show || !entity.episodeSet || entity.episodeNumber != episode {
			return value, ErrConflict
		}
		if hasSeason {
			if entity.season != parent || entity.parent != parent {
				return value, ErrConflict
			}
		} else if entity.season != 0 || entity.parent != show {
			return value, ErrConflict
		}
	}
	return iaEpisodeNode{key: key, kind: kind, subjectID: subjectID, revision: entity.revision, incarnation: entity.incarnation,
		season: season, number: episode, parent: parent, show: show, hasSeason: hasSeason}, nil
}

type iaEpisodeAllocation struct {
	show        iaEpisodeNode
	seasons     []iaEpisodeNode
	episodes    []iaEpisodeNode
	newEntities []newOrdinaryEntitySource
}

// Node order is parent-first: show, then each distinct season, then episodes in
// complete link order. This is the hierarchy chronology every link relies on.
func iaEpisodePlanNodes(ctx context.Context, turn *commandTurn, r iaEpisodeInventoryRequest) (out iaEpisodeAllocation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	show, err := iaEpisodeResolveNode(ctx, turn, r.scope.libraryID, r.showKey, 2, 0, 0, 0, 0, false)
	if err != nil {
		return out, err
	}
	seasonIndex := make(map[int64]int, len(r.episodes))
	for _, link := range r.episodes {
		if !link.hasSeason {
			continue
		}
		if _, ok := seasonIndex[link.season]; ok {
			continue
		}
		seasonIndex[link.season] = len(out.seasons)
		season, err := iaEpisodeResolveNode(ctx, turn, r.scope.libraryID, link.seasonKey, 3, link.season, 0, 0, show.subjectID, false)
		if err != nil {
			return out, err
		}
		season.season, season.show, season.hasSeason = link.season, show.subjectID, true
		out.seasons = append(out.seasons, season)
	}
	out.episodes = make([]iaEpisodeNode, len(r.episodes))
	for i, link := range r.episodes {
		parent := show.subjectID
		if link.hasSeason {
			parent = out.seasons[seasonIndex[link.season]].subjectID
		}
		episode, err := iaEpisodeResolveNode(ctx, turn, r.scope.libraryID, link.episodeKey, 4, link.season, link.episode, parent, show.subjectID, link.hasSeason)
		if err != nil {
			return out, err
		}
		episode.parent, episode.show, episode.season, episode.hasSeason = parent, show.subjectID, link.season, link.hasSeason
		out.episodes[i] = episode
	}
	out.show = show
	return out, nil
}

// Allocation runs only after every fresh gate. Parent IDs are assigned before
// children, so each entity insert carries a real same-library parent.
func iaEpisodeAllocateNodes(ctx context.Context, turn *commandTurn, r iaEpisodeInventoryRequest, nodes iaEpisodeAllocation, appliedMS int64) (out iaEpisodeAllocation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	var entropy [4096]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return out, err
	}
	next := 0
	take := func(n int) []byte {
		if next+n > len(entropy) {
			return nil
		}
		b := entropy[next : next+n]
		next += n
		return b
	}
	addedAt := iaEpisodeAddedAt(appliedMS)
	libraryID := r.scope.libraryID
	language := r.files[0].metadataLanguage
	show := nodes.show
	if show.created {
		title := r.naming.Titles[0].Value
		if show, err = iaEpisodeCreateNode(ctx, turn, libraryID, show, title, 0, 0, 0, 0, 0, 0, language, addedAt, take); err != nil {
			return out, err
		}
		out.newEntities = append(out.newEntities, iaEpisodeSearchEntity(show, libraryID))
	}
	out.show = show
	out.seasons = make([]iaEpisodeNode, len(nodes.seasons))
	seasonIndex := make(map[int64]int, len(nodes.seasons))
	for i, season := range nodes.seasons {
		seasonIndex[season.season] = i
		if season.created {
			title := "Season " + strconv.FormatInt(season.season, 10)
			if season.season == 0 {
				// Legacy names the zero-numbered container "Specials"; the
				// schema allows season_number 0 and the show stream orders it
				// first.
				title = "Specials"
			}
			if season, err = iaEpisodeCreateNode(ctx, turn, libraryID, season, title, show.subjectID, show.subjectID, 0, season.season, 0, 0, language, addedAt, take); err != nil {
				return out, err
			}
			out.newEntities = append(out.newEntities, iaEpisodeSearchEntity(season, libraryID))
		}
		out.seasons[i] = season
	}
	out.episodes = make([]iaEpisodeNode, len(nodes.episodes))
	for i, episode := range nodes.episodes {
		seasonID, seasonNumber := int64(0), int64(0)
		if episode.hasSeason {
			season := out.seasons[seasonIndex[episode.season]]
			seasonID, seasonNumber = season.subjectID, episode.season
		}
		parent := show.subjectID
		if seasonID > 0 {
			parent = seasonID
		}
		if episode.created {
			title := "Episode " + strconv.FormatInt(episode.number, 10)
			episode, err = iaEpisodeCreateNode(ctx, turn, libraryID, episode, title, parent, show.subjectID, seasonID, seasonNumber, episode.number, r.files[0].year, language, addedAt, take)
			if err != nil {
				return out, err
			}
			out.newEntities = append(out.newEntities, iaEpisodeSearchEntity(episode, libraryID))
		}
		out.episodes[i] = episode
	}
	return out, nil
}

func iaEpisodeSearchEntity(node iaEpisodeNode, libraryID int64) newOrdinaryEntitySource {
	return newOrdinaryEntitySource{SubjectID: node.subjectID, LibraryID: libraryID, Kind: node.kind, MetadataIncarnation: node.incarnation, EntityRevision: node.revision}
}

// Fixed-width UTC with all nine fractional digits: the only accepted chronology
// encoding, so lexical order is true instant order.
func iaEpisodeAddedAt(appliedMS int64) string {
	return time.UnixMilli(appliedMS).UTC().Format("2006-01-02T15:04:05.000000000Z")
}

// One subject, its exact source identity and one TV entity. The entity row
// carries the hierarchy columns and four positive TV revisions so the merged
// IA TV read accepts it; no enrichment, artwork or playback state is claimed.
func iaEpisodeCreateNode(ctx context.Context, turn *commandTurn, libraryID int64, node iaEpisodeNode, title string, parent, show, seasonID, seasonNumber, episodeNumber, year int64, language, addedAt string, take func(int) []byte) (out iaEpisodeNode, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	public, metadata, artwork, playback, tv, random := take(16), take(16), take(16), take(16), take(16), take(4)
	if public == nil || metadata == nil || artwork == nil || playback == nil || tv == nil || random == nil {
		return out, ErrCap
	}
	projection, err := ProjectFilenameMetadata(title, "", language)
	if err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, iaEpisodeSubjectInsert, public, libraryID, node.kind)
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
	if err = wc008DraftChanged(ctx, turn, iaEpisodeIdentityInsert, libraryID, node.key, subjectID); err != nil {
		return out, err
	}
	var nullableParent, nullableShow, nullableSeason, nullableSeasonNumber, nullableEpisodeNumber any
	if parent > 0 {
		nullableParent = parent
	}
	if show > 0 {
		nullableShow = show
	}
	if seasonID > 0 {
		nullableSeason = seasonID
	}
	// Zero is a real number for the Specials season, so presence is keyed on
	// the kind/container, never on a positive value.
	if node.kind == 3 || node.kind == 4 && seasonID > 0 {
		nullableSeasonNumber = seasonNumber
	}
	var numbering any
	if node.kind == 4 {
		nullableEpisodeNumber = episodeNumber
		numbering = "seasonal"
		if seasonID == 0 {
			numbering = "absolute"
		}
	}
	row, err = wc008DraftQuery(ctx, turn, iaEpisodeEntityInsert, subjectID, public, libraryID, node.kind, nullableParent,
		projection.Title, projection.SortTitle, projection.SortKey, projection.SortHead, projection.SortKeyKeepArticles, projection.SortHeadKeepArticles,
		int64(binary.BigEndian.Uint32(random)), year, addedAt, language, nullableShow, nullableSeason, nullableSeasonNumber, nullableEpisodeNumber, numbering, "",
		metadata, artwork, playback, int64(1), tv, int64(1), int64(1), int64(1), int64(1))
	if err != nil {
		return out, err
	}
	var actualID, revision int64
	var actualMetadata []byte
	if err = row.Scan(&actualID, &revision, &actualMetadata); err != nil {
		return out, err
	}
	if actualID != subjectID || revision != 1 || !equalBytes(actualMetadata, metadata) {
		return out, ErrConflict
	}
	out = node
	out.subjectID, out.revision = subjectID, revision
	copy(out.incarnation[:], metadata)
	return out, nil
}

func iaEpisodeAdvanceDirectory(ctx context.Context, turn *commandTurn, r iaEpisodeInventoryRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if r.directory.pages == math.MaxInt64 {
		return ErrCap
	}
	next := r.directory.pages + 1
	row, err := wc008DraftQuery(ctx, turn, iaEpisodeDirectoryCAS, r.directory.nextCursor, next,
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
func iaEpisodeAdvanceRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, r iaEpisodeInventoryRequest) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if r.run.revision == math.MaxInt64 || identity.Sequence <= 0 || r.run.lastSequence != identity.Sequence-1 {
		return 0, ErrCap
	}
	row, err := wc008DraftQuery(ctx, turn, iaEpisodeRunCAS, r.run.revision+1, identity.Sequence,
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

type iaEpisodeLinkFacts struct {
	episode   iaEpisodeNode
	link      iaEpisodeLink
	assetID   int64
	infoEpoch int64
	playback  [16]byte
}

// Show/season last_content_added_at advances monotonically to the accepted
// content-arrival instant. A NULL container is initialized, an older value is
// replaced by an exact nullable CAS, and an equal or newer value is left alone,
// so replay, clock rollback and an older event never move it backwards. The
// accepted instant is the same server-owned value every child of this unit
// observes.
func iaEpisodeAdvanceChronology(ctx context.Context, turn *commandTurn, libraryID, subjectID, kind int64, acceptedAt string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if subjectID <= 0 || libraryID <= 0 || kind != 2 && kind != 3 {
		return ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, iaEpisodeContainer, subjectID, libraryID, kind)
	if err != nil {
		return err
	}
	var prior sql.NullString
	if err = row.Scan(&prior); errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if prior.Valid && prior.String >= acceptedAt {
		return nil
	}
	// The hot grammar has no IS ? form, so the two closed nullable variants are
	// chosen from the stored value: an absent container matches IS NULL and a
	// present one is an exact CAS against its observed instant.
	id, args := iaEpisodeContainerAdvanceNull, []any{acceptedAt, subjectID, libraryID, kind}
	if prior.Valid {
		id = iaEpisodeContainerAdvance
		args = append(args, prior.String)
	}
	row, err = wc008DraftQuery(ctx, turn, id, args...)
	if err != nil {
		return err
	}
	var advanced string
	if err = row.Scan(&advanced); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if advanced != acceptedAt {
		return ErrResolveRequired
	}
	return nil
}

// The complete typed cohort: show, each distinct season, each episode identity,
// then each episode membership. Ordinals are contiguous from zero.
func iaEpisodeFacts(r iaEpisodeInventoryRequest, nodes iaEpisodeAllocation, links []iaEpisodeLinkFacts, runRevision, appliedMS int64) (iaMediaResult, error) {
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
	add(iaMediaMember{kind: "identity", libraryID: r.scope.libraryID, subjectID: nodes.show.subjectID, subjectKind: 2,
		subjectRevision: nodes.show.revision, metadataIncarnation: nodes.show.incarnation})
	for _, season := range nodes.seasons {
		add(iaMediaMember{kind: "identity", libraryID: r.scope.libraryID, subjectID: season.subjectID, subjectKind: 3,
			subjectRevision: season.revision, metadataIncarnation: season.incarnation,
			parentID: iaEpisodePtr(nodes.show.subjectID), parentKind: iaEpisodePtr(int64(2))})
	}
	for _, link := range links {
		m := iaMediaMember{kind: "identity", libraryID: r.scope.libraryID, subjectID: link.episode.subjectID, subjectKind: 4,
			subjectRevision: link.episode.revision, metadataIncarnation: link.episode.incarnation}
		if link.episode.hasSeason {
			found := false
			for _, candidate := range nodes.seasons {
				if candidate.season == link.episode.season {
					m.parentID, m.parentKind = iaEpisodePtr(candidate.subjectID), iaEpisodePtr(int64(3))
					found = true
					break
				}
			}
			if !found {
				return iaMediaResult{}, invalid("ia_episode_season_missing")
			}
			m.ancestorID, m.ancestorKind = iaEpisodePtr(nodes.show.subjectID), iaEpisodePtr(int64(2))
		}
		// A seasonless episode still has a show parent in the catalogue, but the
		// typed child family can only express a season parent for kind 4, so the
		// saved member carries no invented parent edge.
		add(m)
	}
	// Legacy keys the boundary on the file's accepted number cohort, not on a
	// source range: one observed file for several episodes is always the
	// unknown multi-episode interpretation, whether or not its ranges are known.
	status := "whole_source"
	if len(r.episodes) > 1 {
		status = "unknown_multi_episode"
	}
	for _, link := range links {
		edition := ""
		present := "present"
		start := link.link.start
		playback := link.playback
		add(iaMediaMember{kind: "membership", libraryID: r.scope.libraryID, subjectID: link.episode.subjectID, subjectKind: 4,
			subjectRevision: link.episode.revision, metadataIncarnation: link.episode.incarnation,
			assetID: iaEpisodePtr(link.assetID), assetInfoEpoch: iaEpisodePtr(link.infoEpoch), partIndex: iaEpisodePtr(link.link.part),
			linkOriginRevision: iaEpisodePtr(int64(1)), linkOriginIncarnation: &playback, edition: &edition, linkEffect: &present,
			startSeconds: &start, endSeconds: link.link.end, episodeBoundaryStatus: &status})
	}
	facts.resultCount = int64(len(facts.members))
	for _, m := range facts.members {
		n, err := iaMediaMemberRetainedBytes(IAAcceptEpisodeInventory, facts.runID, m)
		if err != nil {
			return iaMediaResult{}, err
		}
		facts.memberBytes += n
	}
	return facts, nil
}

func iaEpisodeWriteReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, facts iaMediaResult, digest ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = wc008DraftChanged(ctx, turn, iaEpisodeReceiptInsert, facts.runID, identity.Incarnation[:], identity.Sequence, identity.OperationID[:], request[:],
		iaEpisodeReceiptKind, facts.acceptedRevision, facts.appliedMS, facts.sourceID, facts.sourceIncarnation, nil, nil, "", facts.resultCount, digest[:],
		facts.resultVersion, facts.memberBytes, facts.objectID, facts.objectRevision, facts.cursorKind,
		facts.directoryPath, facts.directoryIdentity, facts.directoryRevision, facts.expectedPayload, facts.nextPayload); err != nil {
		return err
	}
	for _, m := range facts.members {
		if err = wc008DraftChanged(ctx, turn, iaEpisodeMemberInsert, facts.runID, identity.Incarnation[:], identity.Sequence, iaEpisodeReceiptKind,
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

// iaEpisodePtr is the local presence-pointer constructor; test fixtures use
// their own helper so no test-only symbol leaks into production code.
func iaEpisodePtr[T any](v T) *T { return &v }

func iaEpisodeNullableInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
func iaEpisodeNullableReal(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
func iaEpisodeNullableText(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
func iaEpisodeNullableBlob(p *[16]byte) any {
	if p == nil {
		return nil
	}
	return p[:]
}

// Replay decodes only the immutable receipt and its complete typed child
// cohort. Current source, directory, asset and lease rows are never consulted.
func lookupIAEpisodeInventory(ctx context.Context, turn *commandTurn, authority *iaEntryAuthorization, command *iaEpisodeInventoryCommand, header iaEpisodeSavedHeader) (out iaEpisodeHistory, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || authority == nil || command == nil || !command.valid ||
		!header.facts.exists || header.facts.kind != IAAcceptEpisodeInventory || header.facts.identity != (receiptIdentity{command.identity, command.digest}) ||
		header.facts.resultCount < 1 || header.facts.resultCount > 4096 || header.memberBytes <= 0 ||
		header.acceptedRevision <= 0 || header.appliedMS < 0 || header.sourceID == "" || header.sourceIncarnation == "" ||
		header.objectID == "" || header.objectRevision == "" || len(header.directoryPath) == 0 || len(header.directoryIdentity) == 0 || len(header.directoryRevision) == 0 || len(header.nextPayload) == 0 {
		return out, ErrResolveRequired
	}
	if err = authority.budget.check(ctx, turn, header.memberBytes); err != nil {
		return out, err
	}
	for _, id := range []stmt.ID{iaEpisodeMemberBytes, iaEpisodeMembers} {
		if _, ok := turn.steps[id]; !ok {
			return out, invalid("ia_episode_member_steps")
		}
	}
	limit := header.facts.resultCount + 1
	rows, err := turn.query(ctx, turn.steps[iaEpisodeMemberBytes].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
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
	rows, err = turn.query(ctx, turn.steps[iaEpisodeMembers].statementStep, command.request.scope.runID, command.identity.Incarnation[:], command.identity.Sequence, limit)
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
			n, err := iaMediaMemberRetainedBytes(IAAcceptEpisodeInventory, facts.runID, m)
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
	if err = preflightIAMediaResult(IAAcceptEpisodeInventory, command.identity, command.digest, facts); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	digest, err := canonicalResultDigest(command.identity, command.digest, iaEpisodeInventoryResult{facts})
	if err != nil || !equalBytes(digest[:], header.facts.resultDigest[:]) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = authority.recheck(ctx, turn); err != nil {
		return out, err
	}
	return iaEpisodeHistory{decision: receiptReplay, result: iaEpisodeInventoryResult{facts}, digest: digest}, nil
}

func iaEpisodePointer(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}
func iaEpisodeRealPointer(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}
func iaEpisodeTextPointer(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

// IA-11's accepted search-intent append, shared by every IA accepting class. It
// advances the ONE shared input clock once for the complete new-entity cohort and
// inserts one capture job per bank. A reused identity has no new search intent; a
// stored bank/profile mismatch refuses the whole unit instead of parking native
// publication silently.
func applyIAEpisodeSearchInput(ctx context.Context, turn *commandTurn, entities []newOrdinaryEntitySource, acceptedAtMS int64) error {
	return applyIAMediaSearchInput(ctx, turn, entities, acceptedAtMS, 2, 4)
}

func applyIAMediaSearchInput(ctx context.Context, turn *commandTurn, entities []newOrdinaryEntitySource, acceptedAtMS, firstKind, lastKind int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || acceptedAtMS < 0 || firstKind < 1 || lastKind > 99 || firstKind > lastKind || len(entities) > 4096 {
		return ErrInvalid
	}
	if len(entities) == 0 {
		return nil
	}
	for _, entity := range entities {
		if entity.SubjectID <= 0 || entity.LibraryID <= 0 || entity.Kind < firstKind || entity.Kind > lastKind || entity.EntityRevision != 1 {
			return ErrInvalid
		}
	}
	step := func(id stmt.ID) statementStep { return turn.steps[id].statementStep }
	row, err := turn.queryRow(ctx, step(iaEpisodeSearchHead))
	if err != nil {
		return err
	}
	var servingRevision, sequence int64
	var active, building sql.NullInt64
	if err := row.Scan(&servingRevision, &active, &building, &sequence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errSearchInputConfiguration
		}
		return err
	}
	if !active.Valid || active.Int64 <= 0 || servingRevision <= 0 || building.Valid && (building.Int64 <= 0 || building.Int64 == active.Int64) {
		return errSearchInputConfiguration
	}
	nextSequence, err := nextSearchInputCounter(sequence, 1)
	if err != nil {
		return errSearchInputConfiguration
	}
	type bankFacts struct {
		id, pending, nextPending               int64
		key, recipe, engine, fingerprint, role string
	}
	banks := []bankFacts{{id: active.Int64, role: "active"}}
	if building.Valid {
		banks = append(banks, bankFacts{id: building.Int64, role: "building"})
	}
	for i := range banks {
		bank := &banks[i]
		wantRole := bank.role
		row, err := turn.queryRow(ctx, step(iaEpisodeSearchBank), bank.id)
		if err != nil {
			return err
		}
		if err := row.Scan(&bank.key, &bank.recipe, &bank.engine, &bank.fingerprint, &bank.role, &bank.pending); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errSearchInputConfiguration
			}
			return err
		}
		if bank.role != wantRole || bank.key == "" || !searchpolicy.StoredWellFormed(searchpolicy.Profile{
			RecipeVersion: bank.recipe, EngineVersion: bank.engine, UnicodeFingerprint: bank.fingerprint,
		}) {
			return errSearchInputConfiguration
		}
		bank.nextPending, err = nextSearchInputCounter(bank.pending, int64(len(entities)))
		if err != nil {
			return errSearchInputConfiguration
		}
	}
	for _, entity := range entities {
		row, err := turn.queryRow(ctx, step(iaEpisodeSearchExisting), entity.SubjectID)
		if err != nil {
			return err
		}
		var prior int64
		if err := row.Scan(&prior); !errors.Is(err, sql.ErrNoRows) {
			if err != nil {
				return err
			}
			return errSearchInputConflict
		}
	}
	exactlyOne := func(id stmt.ID, args ...any) error {
		result, err := turn.exec(ctx, step(id), args...)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errSearchInputConflict
		}
		return nil
	}
	for _, bank := range banks {
		for _, entity := range entities {
			if err := exactlyOne(iaEpisodeSearchInsert, entity.SubjectID, bank.id, firstSearchInputGeneration,
				entity.MetadataIncarnation[:], entity.EntityRevision, bank.recipe, firstSearchInputPhase, acceptedAtMS); err != nil {
				return err
			}
		}
		if err := exactlyOne(iaEpisodeSearchPending, bank.nextPending, bank.id, bank.role, bank.key,
			bank.recipe, bank.engine, bank.fingerprint, bank.pending); err != nil {
			return err
		}
	}
	if building.Valid {
		return exactlyOne(iaEpisodeSearchBuilding, nextSequence, servingRevision, active.Int64, building.Int64, sequence)
	}
	return exactlyOne(iaEpisodeSearchSequence, nextSequence, servingRevision, active.Int64, sequence)
}
