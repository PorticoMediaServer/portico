package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// FIRST ordinary stream-only input (families1..3). This freezes accepted-pending
// technical input; it does not activate the asset or declare Basic ready. W's
// complete command supplies current disclosure/history/run+source custody,
// funded preparation and ONE trusted time value before invoking these effects.
type basicStreamAccept struct {
	source                  basicStreamFactStage
	changeRevision, shapeID int64
	rawShape                ScanShape
}

type basicOrdinaryInputAccept struct {
	stream   basicStreamAccept
	counts   [9]int64          // original manifest, not a count inferred from a LIMIT prefix
	snapshot basicProbeEffects // actual current scalar/counter capture after staging
}

type basicAcceptedPending struct {
	changeID, inputRevision, expectedEpoch, changeRevision int64
	upperEntity, targetAvailable, shapeID                  int64
}

const (
	basicAcceptTargets    stmt.ID = "scan.basic_accept.targets"
	basicAcceptFamilies   stmt.ID = "scan.basic_accept.families"
	basicAcceptStreams    stmt.ID = "scan.basic_accept.streams"
	basicAcceptBreadth    stmt.ID = "scan.basic_accept.breadth"
	basicAcceptOwners     stmt.ID = "scan.basic_accept.owners"
	basicAcceptEntity     stmt.ID = "scan.basic_accept.entity"
	basicAcceptSubject    stmt.ID = "scan.basic_accept.subject"
	basicAcceptVersions   stmt.ID = "scan.basic_accept.versions"
	basicAcceptSuccessor  stmt.ID = "scan.basic_accept.successor"
	basicAcceptAssetShape stmt.ID = "scan.basic_accept.asset_shape"
	basicAcceptShape      stmt.ID = "scan.basic_accept.shape"
	basicAcceptFreeze     stmt.ID = "scan.basic_accept.freeze"
)

func basicStreamAcceptDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: basicAcceptTargets, Kind: stmt.Read, SQL: `SELECT job_revision,family_mask,target_shape_id,target_available,target_owner_object,target_owner_revision,copy_family,created_ms,
job_lease_owner,job_lease_generation,job_lease_expires_ms FROM catalog_info_changes WHERE id=?`, Params: []stmt.Param{i("change_id")}, Cohorts: []stmt.Cohort{{Name: "staged_targets", Bindings: []any{int64(51)}}}},
		{ID: basicAcceptFamilies, Kind: stmt.Read, SQL: `SELECT family,witness_ordinal,rows_expected,rows_staged,sealed FROM catalog_info_input_families
WHERE change_id=? ORDER BY family LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")}, Cohorts: []stmt.Cohort{{Name: "complete_three_families", Bindings: []any{int64(51), int64(19)}}}},
		{ID: basicAcceptStreams, Kind: stmt.Read, SQL: `SELECT asset_id,stream_index,type FROM catalog_info_new_streams
WHERE change_id=? ORDER BY asset_id,stream_index LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")}, Cohorts: []stmt.Cohort{{Name: "complete_stream_set", Bindings: []any{int64(51), int64(766)}}}},
		{ID: basicAcceptBreadth, Kind: stmt.Read, SQL: `SELECT asset_id,stream_index FROM catalog_info_new_stream_breadth
WHERE change_id=? ORDER BY asset_id,stream_index LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")}, Cohorts: []stmt.Cohort{{Name: "complete_breadth_set", Bindings: []any{int64(51), int64(766)}}}},
		{ID: basicAcceptOwners, Kind: stmt.Read, SQL: `SELECT entity_id,library_id,cue_ordinal FROM catalog_asset_links INDEXED BY catalog_asset_links_asset
WHERE asset_id=? ORDER BY entity_id LIMIT ?`, Params: []stmt.Param{i("asset_id"), i("row_limit")}, Cohorts: []stmt.Cohort{{Name: "one_ordinary_owner", Bindings: []any{int64(41), int64(2)}}}},
		{ID: basicAcceptEntity, Kind: stmt.Read, SQL: `SELECT library_id,kind,retired FROM catalog_entities WHERE id=?`, Params: []stmt.Param{i("entity_id")}, Cohorts: []stmt.Cohort{{Name: "current_movie_owner", Bindings: []any{int64(31)}}}},
		{ID: basicAcceptSubject, Kind: stmt.Read, SQL: `SELECT id,library_id,kind FROM catalog_subjects WHERE id=?`, Params: []stmt.Param{i("subject_id")}, Cohorts: []stmt.Cohort{{Name: "retained_movie_owner", Bindings: []any{int64(31)}}}},
		{ID: basicAcceptVersions, Kind: stmt.Read, SQL: `SELECT asset_id,library_id FROM catalog_asset_links INDEXED BY catalog_asset_links_order WHERE entity_id=? AND part_index=0 ORDER BY asset_id LIMIT ?`, Params: []stmt.Param{i("entity_id"), i("row_limit")}, Cohorts: []stmt.Cohort{{Name: "final_version_union", Bindings: []any{int64(31), int64(17)}}}},
		{ID: basicAcceptSuccessor, Kind: stmt.Read, SQL: `SELECT id FROM catalog_info_changes WHERE asset_id=? AND asset_token=? AND slot=?`, Params: []stmt.Param{i("asset_id"), s("asset_token"), i("slot")}, Cohorts: []stmt.Cohort{{Name: "successor_absent", Bindings: []any{int64(41), "basic-asset", int64(1)}}}},
		{ID: basicAcceptAssetShape, Kind: stmt.Read, SQL: `SELECT available,container,video_codec,height,info_owner_object,info_owner_revision FROM catalog_assets WHERE id=?`, Params: []stmt.Param{i("asset_id")}, Cohorts: []stmt.Cohort{{Name: "ordinary_asset_target", Bindings: []any{int64(41)}}}},
		{ID: basicAcceptShape, Kind: stmt.Read, SQL: `SELECT container,video_codec,height,ref_count FROM catalog_info_shapes WHERE id=?`, Params: []stmt.Param{i("shape_id")}, Cohorts: []stmt.Cohort{{Name: "held_shape_target", Bindings: []any{int64(61)}}}},
		{ID: basicAcceptFreeze, Kind: stmt.Write, SQL: `UPDATE catalog_info_changes SET phase=?,has_after=?,after_entity=?,upper_entity=?,target_available=?,job_revision=?,job_next_attempt_ms=?
WHERE id=? AND asset_id=? AND asset_token=? AND slot=? AND phase=? AND expected_epoch=? AND input_revision=? AND family_mask=? AND fingerprint=?
AND job_revision=? AND target_shape_id=? AND target_available IS NULL AND target_owner_object IS NULL AND target_owner_revision IS NULL
AND copy_family=? AND copy_aux=? AND fault=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=?
RETURNING id,input_revision,expected_epoch,job_revision,upper_entity,target_available,target_shape_id`,
			Params: []stmt.Param{i("next_phase"), i("has_after"), i("after_entity"), i("upper_entity"), i("target_available"), i("next_job_revision"), i("next_attempt_ms"),
				i("change_id"), i("asset_id"), s("asset_token"), i("slot"), i("expected_phase"), i("expected_epoch"), i("input_revision"), i("family_mask"), blob("fingerprint"),
				i("expected_job_revision"), i("target_shape_id"), i("copy_family"), i("copy_aux"), s("fault"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms")},
			Cohorts: []stmt.Cohort{{Name: "freeze_accepted_pending", Bindings: []any{int64(1), int64(0), int64(0), int64(31), int64(1), int64(14), int64(2000),
				int64(51), int64(41), "basic-asset", int64(0), int64(0), int64(17), int64(7), int64(7), make([]byte, 32), int64(13), int64(61), int64(1), int64(0), "", int64(0), int64(0), int64(0)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_accept.go"}
		defs[n].Plan.Hot = true
		var table, seek string
		switch defs[n].ID {
		case basicAcceptTargets, basicAcceptFreeze:
			table, seek = "catalog_info_changes", "rowid=?"
		case basicAcceptAssetShape:
			table, seek = "catalog_assets", "rowid=?"
		case basicAcceptShape:
			table, seek = "catalog_info_shapes", "rowid=?"
		case basicAcceptEntity:
			table, seek = "catalog_entities", "rowid=?"
		case basicAcceptSubject:
			table, seek = "catalog_subjects", "rowid=?"
		case basicAcceptFamilies:
			table, seek = "catalog_info_input_families", "change_id=?"
		case basicAcceptStreams:
			table, seek = "catalog_info_new_streams", "change_id=?"
		case basicAcceptBreadth:
			table, seek = "catalog_info_new_stream_breadth", "change_id=?"
		case basicAcceptVersions:
			defs[n].Plan.Indexes = []string{"catalog_asset_links_order"}
			seek = "entity_id=? AND part_index=?"
		case basicAcceptOwners:
			defs[n].Plan.Indexes = []string{"catalog_asset_links_asset"}
			seek = "asset_id=?"
		case basicAcceptSuccessor:
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_catalog_info_changes_1"}
			seek = "asset_id=? AND asset_token=? AND slot=?"
		}
		if table != "" {
			defs[n].Plan.PrimaryKeys = []string{table}
		}
		defs[n].Plan.Seeks = []string{seek}
	}
	return defs
}

// readBasicStreamCaps reads complete bounded native sets, never LIMIT-success
// prefixes. Version/owner IDs come from native same-library associations, not
// the prepared IDs alone. FIRST requires one ordinary movie owner, no CUE or
// episodes; later command variants must explicitly widen that contract.
func readBasicStreamCaps(ctx context.Context, turn *commandTurn, in basicStreamAccept) (shape ScanShape, owner int64, err error) {
	shape = ScanShape{LibraryID: in.source.libraryID, OrdinaryOwners: 1}
	rawStreams := make(map[int]StreamClass, len(in.rawShape.Streams))
	for _, stream := range in.rawShape.Streams {
		rawStreams[stream.Index] = stream.Class
	}
	rows, err := basicRows(ctx, turn, basicAcceptOwners, in.source.assetID, int64(2))
	if err != nil {
		return shape, 0, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	var library int64
	var cue sql.NullInt64
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return shape, 0, err
		}
		return shape, 0, ErrConflict
	}
	if err = rows.Scan(&owner, &library, &cue); err != nil {
		return shape, 0, err
	}
	if rows.Next() || library != shape.LibraryID || cue.Valid {
		return shape, 0, ErrConflict
	}
	if err = rows.Err(); err != nil {
		return shape, 0, err
	}
	row, err := basicStageRead(ctx, turn, basicAcceptEntity, owner)
	if err != nil {
		return shape, 0, err
	}
	var kind, retired int64
	if err = row.Scan(&library, &kind, &retired); err != nil {
		return shape, 0, basicStageError(err)
	}
	if library != shape.LibraryID || kind != 1 || retired != 0 {
		return shape, 0, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicAcceptSubject, owner)
	if err != nil {
		return shape, 0, err
	}
	var subject int64
	if err = row.Scan(&subject, &library, &kind); err != nil {
		return shape, 0, basicStageError(err)
	}
	if subject != owner || library != shape.LibraryID || kind != 1 {
		return shape, 0, ErrConflict
	}
	shape.AuthoredParentIDs = []int64{owner}
	versions, err := basicRows(ctx, turn, basicAcceptVersions, owner, int64(MaxVersions+1))
	if err != nil {
		return shape, 0, err
	}
	defer func() { err = errors.Join(err, versions.Close()) }()
	for versions.Next() {
		var id int64
		if err = versions.Scan(&id, &library); err != nil {
			return shape, 0, err
		}
		if library != shape.LibraryID {
			return shape, 0, ErrConflict
		}
		shape.VersionAssetIDs = append(shape.VersionAssetIDs, id)
	}
	if err = versions.Err(); err != nil {
		return shape, 0, err
	}
	streams, err := basicRows(ctx, turn, basicAcceptStreams, in.source.changeID, int64(3*MaxStreamsPerClass+1))
	if err != nil {
		return shape, 0, err
	}
	defer func() { err = errors.Join(err, streams.Close()) }()
	for streams.Next() {
		var token, class string
		var index int
		if err = streams.Scan(&token, &index, &class); err != nil {
			return shape, 0, err
		}
		if token != in.source.assetToken {
			return shape, 0, ErrConflict
		}
		var c StreamClass
		switch class {
		case "audio":
			c = AudioStream
		case "video":
			c = VideoStream
		case "subtitle":
			c = SubtitleStream
		default:
			return shape, 0, ErrInvalid
		}
		shape.Streams = append(shape.Streams, Stream{Index: index, Class: c})
	}
	if err = streams.Err(); err != nil {
		return shape, 0, err
	}
	if err = ValidateScanShape(shape); err != nil {
		return shape, 0, err
	}
	if len(shape.Streams) != len(in.rawShape.Streams) || len(shape.VersionAssetIDs) != len(in.rawShape.VersionAssetIDs) || len(in.rawShape.AuthoredParentIDs) != 1 || in.rawShape.AuthoredParentIDs[0] != owner {
		return shape, 0, ErrConflict
	}
	for _, s := range shape.Streams {
		if rawStreams[s.Index] != s.Class {
			return shape, 0, ErrConflict
		}
	}
	for _, id := range shape.VersionAssetIDs {
		found := false
		for _, want := range in.rawShape.VersionAssetIDs {
			if id == want {
				found = true
				break
			}
		}
		if !found {
			return shape, 0, ErrConflict
		}
	}
	breadth, err := basicRows(ctx, turn, basicAcceptBreadth, in.source.changeID, int64(3*MaxStreamsPerClass+1))
	if err != nil {
		return shape, 0, err
	}
	defer func() { err = errors.Join(err, breadth.Close()) }()
	n := 0
	for breadth.Next() {
		var token string
		var index int
		if err = breadth.Scan(&token, &index); err != nil {
			return shape, 0, err
		}
		if n >= len(shape.Streams) || token != in.source.assetToken || index != shape.Streams[n].Index {
			return shape, 0, ErrConflict
		}
		n++
	}
	if err = breadth.Err(); err != nil {
		return shape, 0, err
	}
	if n != len(shape.Streams) {
		return shape, 0, ErrConflict
	}
	return shape, owner, nil
}

// acceptBasicStreamInput is the source effect body only. Its ONE phase0→1 CAS
// is atomic with the caller's original durable BasicSeal receipt/run cursor.
// It leaves info_epoch/availability/canonical children untouched and returns no
// Outcome. The caller's ONE trusted W time is a private effect value, not a
// request field, public clock or permission to extend a turn.
func acceptBasicStreamInput(ctx context.Context, turn *commandTurn, in basicStreamAccept, appliedMS int64) (value basicAcceptedPending, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	return freezeBasicInput(ctx, turn, in, 7, [9]int64{1, int64(len(in.rawShape.Streams)), int64(len(in.rawShape.Streams))}, appliedMS)
}

// The complete ordinary path freezes all nine sealed media families. This is
// a BasicSeal effect, never the final BasicAccept availability/result boundary.
func acceptBasicOrdinaryInput(ctx context.Context, turn *commandTurn, in basicOrdinaryInputAccept, appliedMS int64) (value basicAcceptedPending, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || appliedMS < 0 {
		return value, ErrInvalid
	}
	if err = validateBasicOrdinaryCounts(in.stream.source.libraryID, in.stream.rawShape, in.counts); err != nil {
		return value, err
	}
	if in.snapshot.source != in.stream.source || in.snapshot.changeRevision != in.stream.changeRevision || in.snapshot.before != in.snapshot.after {
		return value, ErrInvalid
	}
	if err = validateBasicProbeEffects(in.snapshot); err != nil {
		return value, err
	}
	row, e := basicStageRead(ctx, turn, basicProbeAsset, basicProbeBeforeArgs(in.snapshot)...)
	if e != nil {
		return value, e
	}
	var actual int64
	if e = row.Scan(&actual); e != nil {
		return value, basicStageError(e)
	}
	if actual != in.stream.source.assetID {
		return value, ErrConflict
	}
	return freezeBasicInput(ctx, turn, in.stream, 511, in.counts, appliedMS)
}

func freezeBasicInput(ctx context.Context, turn *commandTurn, in basicStreamAccept, familyMask int64, counts [9]int64, appliedMS int64) (value basicAcceptedPending, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || appliedMS < 0 || in.changeRevision <= 0 || in.changeRevision == math.MaxInt64 || in.shapeID <= 0 {
		return value, ErrInvalid
	}
	if err = ValidateScanShape(in.rawShape); err != nil {
		return value, err
	}
	if in.rawShape.LibraryID != in.source.libraryID || in.rawShape.OrdinaryOwners != 1 || len(in.rawShape.CUEOrdinals) != 0 || len(in.rawShape.EpisodeIDs) != 0 || (familyMask == 7 && in.rawShape.RawChapterCount != 0) || (familyMask != 7 && familyMask != 511) {
		return value, ErrInvalid
	}
	if err = checkBasicStreamInput(ctx, turn, in.source); err != nil {
		return value, err
	}
	if err = checkBasicStreamFact(ctx, turn, in.source); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicAcceptTargets, in.source.changeID)
	if err != nil {
		return value, err
	}
	var revision, mask, copyFamily, created, leaseOwner, leaseGeneration, leaseExpires int64
	var target, available sql.NullInt64
	var object, objectRevision sql.NullString
	if err = row.Scan(&revision, &mask, &target, &available, &object, &objectRevision, &copyFamily, &created, &leaseOwner, &leaseGeneration, &leaseExpires); err != nil {
		return value, basicStageError(err)
	}
	if revision != in.changeRevision || mask != familyMask || !target.Valid || target.Int64 != in.shapeID || available.Valid || object.Valid || objectRevision.Valid || copyFamily != 1 || created > appliedMS || leaseOwner != 0 || leaseGeneration != 0 || leaseExpires != 0 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicAcceptSuccessor, in.source.assetID, in.source.assetToken, int64(1))
	if err != nil {
		return value, err
	}
	var successor int64
	if err = row.Scan(&successor); !errors.Is(err, sql.ErrNoRows) {
		if err == nil {
			return value, ErrConflict
		}
		return value, err
	}
	_, owner, err := readBasicStreamCaps(ctx, turn, in)
	if err != nil {
		return value, err
	}
	families, err := basicRows(ctx, turn, basicAcceptFamilies, in.source.changeID, int64(19))
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, families.Close()) }()
	familyCount := int64(0)
	for families.Next() {
		var family, witness, expected, staged, sealed int64
		if err = families.Scan(&family, &witness, &expected, &staged, &sealed); err != nil {
			return value, err
		}
		familyCount++
		lastFamily := int64(3)
		if familyMask == 511 {
			lastFamily = 9
		}
		if family != familyCount || family < 1 || family > lastFamily {
			return value, ErrConflict
		}
		want := counts[family-1]
		if witness != 0 || expected != want || staged != want || sealed != 1 {
			return value, ErrConflict
		}
	}
	if err = families.Err(); err != nil {
		return value, err
	}
	wantFamilies := int64(3)
	if familyMask == 511 {
		wantFamilies = 9
	}
	if familyCount != wantFamilies {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicAcceptAssetShape, in.source.assetID)
	if err != nil {
		return value, err
	}
	var nativeAvailable, height int64
	var container, codec string
	if err = row.Scan(&nativeAvailable, &container, &codec, &height, &object, &objectRevision); err != nil {
		return value, basicStageError(err)
	}
	if object.Valid || objectRevision.Valid || (nativeAvailable != 0 && nativeAvailable != 1) {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicAcceptShape, in.shapeID)
	if err != nil {
		return value, err
	}
	var targetContainer, targetCodec string
	var targetHeight, refs int64
	if err = row.Scan(&targetContainer, &targetCodec, &targetHeight, &refs); err != nil {
		return value, basicStageError(err)
	}
	if container != targetContainer || codec != targetCodec || height != targetHeight || refs < 1 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicAcceptFreeze, int64(1), int64(0), int64(0), owner, nativeAvailable, in.changeRevision+1, appliedMS,
		in.source.changeID, in.source.assetID, in.source.assetToken, int64(0), int64(0), in.source.expectedEpoch, in.source.inputRevision, familyMask, in.source.witnessFingerprint[:],
		in.changeRevision, in.shapeID, int64(1), int64(0), "", int64(0), int64(0), int64(0))
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.changeID, &value.inputRevision, &value.expectedEpoch, &value.changeRevision, &value.upperEntity, &value.targetAvailable, &value.shapeID); err != nil {
		return value, basicStageError(err)
	}
	if value != (basicAcceptedPending{in.source.changeID, in.source.inputRevision, in.source.expectedEpoch, in.changeRevision + 1, owner, nativeAvailable, in.shapeID}) {
		return value, ErrConflict
	}
	return value, nil
}
