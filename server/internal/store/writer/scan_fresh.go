package writer

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"math"
	"time"
)

// applyInventoryAccept is the closed NEW ordinary N1 effects body. W alone
// invokes it after current disclosure, original history MISS and the fresh
// library gate, on the SAME admitted turn. Every error requires whole-turn
// rollback. Successful preparation deliberately leaves Outcome zero: only W's
// terminal authority/lease/physical COMMIT can publish Applied.
func applyInventoryAccept(ctx context.Context, turn *commandTurn, command *inventoryAcceptCommand) (value InventoryResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || command == nil || !command.valid {
		return value, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return value, err
	}
	// W's complete composed profile must contain EVERY possible branch before
	// any derived observation/argument/result allocation or native effect.
	for _, id := range inventoryFreshIDs() {
		if _, ok := turn.steps[id]; !ok {
			return value, invalid("unregistered_inventory_fresh_step")
		}
	}
	requiredBytes, err := inventoryFreshRequiredBytes(command)
	if err != nil {
		return value, err
	}
	if err = turn.requireInventoryMemory(ctx, requiredBytes); err != nil {
		return value, err
	}
	if len(command.request.files) > 1 {
		if err = validateInventoryFrozenBatch(command); err != nil {
			return value, err
		}
	}
	r, f := command.request, command.request.files[0]
	appliedMS, err := turn.inventoryTimeMS(ctx, r.run.leaseExpiresMS)
	if err != nil {
		return value, err
	}
	addedAt := time.UnixMilli(appliedMS).UTC().Format("2006-01-02T15:04:05.000000000Z")
	if len(addedAt) != 30 {
		return value, ErrInvalid
	}
	members := make([]inventoryResultMember, 0, len(r.files))
	var facts inventoryFreshFacts
	for i := range r.files {
		member, current, e := applyInventoryFreshMember(ctx, turn, command, i, appliedMS, addedAt)
		if e != nil {
			return value, e
		}
		if i == 0 {
			facts = current
		}
		members = append(members, member)
	}
	// Run/job progress advances once for the entire atomic page. Library,
	// membership and age revisions above advance once per actual new entity.
	count := int64(len(members))
	if facts.discovered > math.MaxInt64-count || facts.processed > math.MaxInt64-count {
		return value, ErrConflict
	}
	facts.newDiscovered, facts.newProcessed = facts.discovered+count, facts.processed+count

	row, err := turn.queryRow(ctx, turn.steps[scanFreshDirectoryCAS].statementStep,
		append([]any{r.directory.nextCursor, r.directory.pages + 1}, inventoryDirectoryArgs(command)...)...)
	if err != nil {
		return value, err
	}
	var nextCursor string
	var pages, skipped int64
	if err = row.Scan(&nextCursor, &pages, &skipped); err != nil {
		return value, inventoryFreshReadError(err)
	}
	if nextCursor != r.directory.nextCursor || pages != r.directory.pages+1 || skipped != r.directory.skipped {
		return value, ErrConflict
	}
	if err = execInventoryFreshOne(ctx, turn, scanFreshJobCAS, facts.newProcessed, r.scope.runID, r.scope.libraryKey, facts.processed); err != nil {
		return value, err
	}
	if err = execInventoryFreshOne(ctx, turn, scanFreshProgressCAS, append([]any{addedAt}, inventorySourceArgs(command)...)...); err != nil {
		return value, err
	}
	args := append([]any{facts.newDiscovered, r.run.revision + 1, command.identity.Sequence}, inventoryRunArgs(command)...)
	row, err = turn.queryRow(ctx, turn.steps[scanFreshRunCAS].statementStep, append(args, facts.discovered)...)
	if err != nil {
		return value, err
	}
	var acceptedRevision, discovered, sequence int64
	if err = row.Scan(&acceptedRevision, &discovered, &sequence); err != nil {
		return value, inventoryFreshReadError(err)
	}
	if acceptedRevision != r.run.revision+1 || discovered != facts.newDiscovered || sequence != command.identity.Sequence {
		return value, ErrConflict
	}
	// The three historical revision echoes are native RETURNING facts, with
	// deliberately separate meanings. No Basic/RAW/publication witness is added.
	v := inventoryAcceptResult{RunID: r.scope.runID, AcceptedRunRevision: acceptedRevision, AppliedMS: appliedMS,
		SourceID: r.source.id, SourceIncarnation: r.source.incarnation, SourceRevision: f.revision, NextCursor: nextCursor,
		ResultCount: count, Members: members}
	digest, err := inventoryAcceptResultDigest(command.identity, command.digest, v)
	if err != nil {
		return value, err
	}
	if err = execInventoryFreshOne(ctx, turn, scanReceiptInsert, v.RunID, command.identity.Incarnation[:], command.identity.Sequence,
		command.identity.OperationID[:], command.digest[:], scanInventoryReceiptKind, v.AcceptedRunRevision, v.AppliedMS,
		v.SourceID, v.SourceIncarnation, v.SourceRevision, v.NextCursor, v.ResultCount, digest[:]); err != nil {
		return value, err
	}
	for _, m := range v.Members {
		if err = execInventoryFreshOne(ctx, turn, scanResultInsert, v.RunID, command.identity.Incarnation[:], command.identity.Sequence,
			m.InputOrdinal, m.ChildKind, m.ChildOrdinal, m.ObjectID, m.LibraryID, m.SubjectID, m.AssetID, m.AcceptedAssetRevision); err != nil {
			return value, err
		}
	}
	// Keep the exact file fact in scope: no observation-revision substitution by
	// source generation, directory/run revision, size epoch or entity revision.
	if v.SourceRevision != f.revision {
		return value, ErrConflict
	}
	value = InventoryResult{RunID: v.RunID, AcceptedRunRevision: v.AcceptedRunRevision, AppliedMS: v.AppliedMS,
		SourceID: v.SourceID, SourceIncarnation: v.SourceIncarnation, SourceRevision: v.SourceRevision, NextCursor: v.NextCursor,
		ResultCount: v.ResultCount, Digest: digest, Members: make([]InventoryMember, len(members))}
	for i, m := range members {
		value.Members[i] = InventoryMember(m)
	}
	return value, nil
}

// The view is derived from the already frozen original; it carries no receipt
// or admission of its own. Only the outer command advances cursor/receipt.
func applyInventoryFreshMember(ctx context.Context, turn *commandTurn, original *inventoryAcceptCommand, ordinal int, appliedMS int64, addedAt string) (member inventoryResultMember, facts inventoryFreshFacts, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			Member inventoryResultMember
			Facts  inventoryFreshFacts
		}{member, facts}, err)
		member, facts, err = v.Member, v.Facts, e
	}()
	command, err := inventoryMemberCommand(original, ordinal)
	if err != nil {
		return member, facts, err
	}
	observation, err := prepareInventoryFreshObservation(command)
	if err != nil {
		return member, facts, err
	}
	facts, err = readInventoryFreshFacts(ctx, turn, command)
	if err != nil {
		return member, facts, err
	}
	r, f := command.request, command.request.files[0]
	allocated, err := insertInventoryFreshCatalogue(ctx, turn, command, addedAt)
	if err != nil {
		return member, facts, err
	}
	observation.objectID, observation.assetToken = allocated.objectID, allocated.assetToken
	actual, err := insertInventoryObject(ctx, turn, observation)
	if err != nil {
		return member, facts, err
	}
	link, err := turn.queryRow(ctx, turn.steps[scanInventoryLink].statementStep, r.scope.libraryID, allocated.subjectID, allocated.assetID, f.part)
	if err != nil {
		return member, facts, err
	}
	var actualPart int64
	if err = link.Scan(&actualPart); err != nil {
		return member, facts, err
	}
	if actualPart != f.part {
		return member, facts, ErrConflict
	}
	if err = applyInventoryFreshAggregates(ctx, turn, command, &facts, &allocated); err != nil {
		return member, facts, err
	}
	if err = applySearchInputIntent(ctx, turn, &searchInputChange{AcceptedAtMS: appliedMS, NewEntities: []newOrdinaryEntitySource{{
		SubjectID: allocated.subjectID, LibraryID: r.scope.libraryID, Kind: scanNewMovieKind,
		MetadataIncarnation: allocated.metadata, EntityRevision: allocated.entityRevision,
	}}}); err != nil {
		return member, facts, err
	}
	if err = validateInventoryFreshUnion(ctx, turn, command, allocated); err != nil {
		return member, facts, err
	}
	member = inventoryResultMember{InputOrdinal: int64(ordinal), ChildKind: scanOrdinaryChildKind, ChildOrdinal: scanOrdinaryChildOrdinal,
		ObjectID: actual.objectID, LibraryID: r.scope.libraryID, SubjectID: allocated.subjectID, AssetID: allocated.assetID, AcceptedAssetRevision: allocated.infoEpoch}
	if actual.revision != f.revision {
		return member, facts, ErrConflict
	}
	return member, facts, nil
}

type inventoryFreshAllocation struct {
	subjectID, assetID, infoEpoch, fileSizeRevision                      int64
	entityRevision, scalarRevision, aggregateRevision, admissionRevision int64
	metadata                                                             [16]byte
	assetToken, objectID                                                 string
}

func insertInventoryFreshCatalogue(ctx context.Context, turn *commandTurn, c *inventoryAcceptCommand, addedAt string) (out inventoryFreshAllocation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	// One fixed bounded entropy buffer, allocated only after every fresh gate.
	var entropy [100]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return out, err
	}
	public, art, playback := entropy[:16], entropy[32:48], entropy[48:64]
	copy(out.metadata[:], entropy[16:32])
	out.assetToken, out.objectID = hex.EncodeToString(entropy[64:80]), hex.EncodeToString(entropy[80:96])
	r, f := c.request, c.request.files[0]
	row, err := turn.queryRow(ctx, turn.steps[scanInventorySubject].statementStep, public, r.scope.libraryID, scanNewMovieKind)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.subjectID); err != nil {
		return out, err
	}
	if out.subjectID <= 0 {
		return out, ErrConflict
	}
	if err = execInventoryFreshOne(ctx, turn, scanInventoryAlias, r.scope.libraryID, f.sourceKey, out.subjectID); err != nil {
		return out, err
	}
	row, err = turn.queryRow(ctx, turn.steps[scanInventoryNewAsset].statementStep, out.assetToken, r.scope.libraryID, f.assetPath, f.size, f.modifiedNS,
		"", "", "", int64(0), int64(0), float64(0))
	if err != nil {
		return out, err
	}
	var token string
	if err = row.Scan(&out.assetID, &token, &out.infoEpoch); err != nil {
		return out, err
	}
	if out.assetID <= 0 || token != out.assetToken || out.infoEpoch <= 0 {
		return out, ErrConflict
	}
	row, err = turn.queryRow(ctx, turn.steps[scanInventoryEntity].statementStep, out.subjectID, public, r.scope.libraryID, scanNewMovieKind,
		f.title, f.sortTitle, f.sortKey, f.sortHead, f.sortKeyKeepArticles, f.sortHeadKeepArticles,
		int64(binary.BigEndian.Uint32(entropy[96:100])), f.year, addedAt, f.metadataLanguage, out.metadata[:], art, playback, scanInitialPlaybackOriginRevision)
	if err != nil {
		return out, err
	}
	var id, playbackRevision int64
	var metadataActual, playbackActual []byte
	if err = row.Scan(&id, &metadataActual, &out.entityRevision, &playbackActual, &playbackRevision); err != nil {
		return out, err
	}
	if id != out.subjectID || !bytes.Equal(metadataActual, out.metadata[:]) || !bytes.Equal(playbackActual, playback) ||
		out.entityRevision != 1 || playbackRevision != scanInitialPlaybackOriginRevision {
		return out, ErrConflict
	}
	return out, nil
}

func validateInventoryFreshUnion(ctx context.Context, turn *commandTurn, c *inventoryAcceptCommand, a inventoryFreshAllocation) error {
	rows, err := turn.query(ctx, turn.steps[scanFreshFinalLinks].statementStep, a.subjectID, int64(2))
	if err != nil {
		return err
	}
	defer rows.Close()
	var lib, entity, asset int64
	var part, available int64
	var cue, shape, pending sql.NullInt64
	var start, end sql.NullFloat64
	var infoDuration float64
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return err
		}
		return ErrConflict
	}
	if err = rows.Scan(&lib, &entity, &asset, &cue, &start, &end, &part, &available, &shape, &pending, &infoDuration); err != nil {
		return err
	}
	if lib != c.request.scope.libraryID || entity != a.subjectID || asset != a.assetID || cue.Valid || !start.Valid || start.Float64 != 0 || end.Valid ||
		part != c.request.files[0].part || available != 1 || shape.Valid || pending.Valid || infoDuration != 0 || rows.Next() {
		return ErrConflict
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	rows, err = turn.query(ctx, turn.steps[scanFreshFinalOwners].statementStep, a.assetID, int64(2))
	if err != nil {
		return err
	}
	defer rows.Close()
	var owner int64
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return err
		}
		return ErrConflict
	}
	if err = rows.Scan(&owner); err != nil {
		return err
	}
	if owner != a.subjectID || rows.Next() {
		return ErrConflict
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	// Only whole sources count toward the version cap; multipart links are
	// independently stored part indexes, as in the legacy catalogue.
	var versions []int64
	if part == 0 {
		versions = []int64{asset}
	}
	return ValidateScanShape(ScanShape{LibraryID: lib, OrdinaryOwners: 1, VersionAssetIDs: versions})
}
