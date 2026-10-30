package catalog

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// SoundSimilar keeps the selection basis explicit. Callers use the approved
// existing genre line on fallback, never describe unavailable vectors as sound.
type SoundSimilar struct {
	Items   ListeningSimilar
	BySound bool
}

type sonicGroup struct {
	id     int64
	public string
	vector sonic.Embedding
}

const currentSonicEligibleSQL = `EXISTS(SELECT 1 FROM catalog_asset_links source_link
 JOIN catalog_assets source_asset ON source_asset.id=source_link.asset_id
 JOIN inventory_objects source_object INDEXED BY inventory_objects_asset ON source_object.asset_id=source_asset.token
 JOIN library_sources source_config ON source_config.id=source_object.source_id
 WHERE source_link.entity_id=item.id AND source_link.available=1 AND source_asset.available=1
 AND source_asset.audio_codec<>'' AND source_asset.duration>0
 AND source_object.retired=0 AND source_object.state='available' AND source_object.unsupported_reason=''
 AND source_object.size=source_asset.size AND source_object.modified_ns=source_asset.modified_ns
 AND source_config.enabled=1 AND source_config.library_id=library.library_id AND source_object.root_incarnation=source_config.incarnation)`

// currentSonicVectorSQL reads only the current source/version/range. The outer
// item must already be permitted by the current viewer. A newer copy wins
// deterministically; no global album/artist means can include hidden songs.
const currentSonicVectorSQL = `(SELECT embedding.vector` + currentSonicResultBodySQL
const currentSonicResultIDSQL = `(SELECT embedding.result_id` + currentSonicResultBodySQL
const currentSonicResultBodySQL = ` FROM music_sonic_embeddings embedding
 JOIN analysis_results r ON r.id=embedding.result_id
 JOIN analysis_heads h ON h.result_id=r.id AND h.stage='sonic'
 JOIN inventory_objects o ON o.id=h.object_id AND o.revision=h.source_revision
 JOIN library_sources src ON src.id=o.source_id
 JOIN catalog_assets a ON a.token=o.asset_id
 JOIN catalog_asset_links link ON link.asset_id=a.id AND link.entity_id=embedding.entity_id
 LEFT JOIN music_virtual_tracks cue ON cue.asset_id=a.token AND cue.entity_id=embedding.entity_id
 WHERE embedding.entity_id=item.id AND embedding.model_id=? AND embedding.model_version=? AND embedding.model_sha256=?
 AND src.library_id=library.library_id AND src.enabled=1 AND src.incarnation=o.root_incarnation
 AND o.state='available' AND o.retired=0 AND o.unsupported_reason='' AND o.size=a.size AND o.modified_ns=a.modified_ns
 AND a.available=1 AND link.available=1 AND r.retired_ms=0 AND r.stage='sonic'
 AND r.object_id=o.id AND r.asset_id=a.token AND r.source_revision=o.revision
 AND r.root_incarnation=src.incarnation AND r.configuration_generation=src.generation
 AND r.algorithm GLOB 'portico-analysis-3:sonic:*'
 AND (a.container<>'strm' OR EXISTS(SELECT 1 FROM analysis_probe_bindings probe WHERE probe.object_id=o.id AND probe.source_revision=o.revision AND probe.evidence=r.evidence))
 AND embedding.source_start_us=CAST(round(COALESCE(cue.start_seconds,0)*1000000) AS INTEGER)
 AND embedding.source_end_us=CAST(round(COALESCE(cue.end_seconds,a.duration)*1000000) AS INTEGER)
 ORDER BY r.created_ms DESC,r.id DESC LIMIT 1)`

// sonicGroups scans in group order with bounded memory: one 200-value sum and
// the small ranking result. Every visible, available song must have a valid
// current vector before its album/artist mean is eligible. Querying another
// profile computes another mean over that profile's permitted tracks.
func (s *Service) sonicGroups(viewer Viewer, kind int, only int64, visit func(sonicGroup)) error {
	target := "item.id"
	creditJoin := ""
	switch kind {
	case 5:
		target = "COALESCE(credit.artist_id,album.artist_id)"
		creditJoin = ` LEFT JOIN catalog_song_artists credit ON credit.song_id=item.id `
	case 6:
		target = "song.album_id"
	case 7:
	default:
		return ErrAdminQuery
	}
	visible, args := viewer.itemVisibilitySQL("item.id")
	if only > 0 {
		visible += " AND target.id=?"
		args = append(args, only)
	}
	rows, err := s.read().Query(`SELECT target.id,pid(target.public_id),`+currentSonicVectorSQL+`
 FROM catalog_entities item JOIN catalog_libraries library ON library.id=item.library_id
 JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music'
 JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_albums album ON album.entity_id=song.album_id
 `+creditJoin+` JOIN catalog_entities target ON target.id=`+target+` AND target.retired=0
 WHERE item.kind=7 AND `+visible+`
	 AND `+currentSonicEligibleSQL+`
 AND COALESCE((SELECT enabled FROM library_sonic_settings settings WHERE settings.library_id=library.library_id),?)=1
 AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0)
 ORDER BY target.id,item.id`, append(append([]any{sonic.ModelID, sonic.ModelVersion, sonic.ModelSHA256}, args...), SonicDefaultEnabled())...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var current sonicGroup
	var sum [sonic.Dimensions]float64
	var total, valid int
	finish := func() {
		if total == 0 || total != valid {
			return
		}
		values := make([]float32, sonic.Dimensions)
		for i := range values {
			values[i] = float32(sum[i] / float64(valid))
		}
		if vector, err := sonic.Normalize(values); err == nil {
			current.vector = vector
			visit(current)
		}
	}
	for rows.Next() {
		var group sonicGroup
		var data []byte
		if err = rows.Scan(&group.id, &group.public, &data); err != nil {
			return err
		}
		if current.id != group.id {
			finish()
			current = group
			sum = [sonic.Dimensions]float64{}
			total = 0
			valid = 0
		}
		total++
		vector, err := sonic.FromBytes(data)
		if err != nil {
			continue
		}
		valid++
		for i, v := range vector {
			sum[i] += float64(v)
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	finish()
	return nil
}

// ListeningSimilarBySound is the first page of the sound ranking, or of the
// genre ranking where the seed has no current vector.
func (s *Service) ListeningSimilarBySound(viewer Viewer, id string, limit int) (SoundSimilar, error) {
	return s.ListeningSimilarPage(viewer, id, true, "", limit)
}

// ListeningSimilarPage pages a listening title's similar ranking to its end:
// by sound when asked and the seed has a current vector, else by genre and
// co-play (listeningSimilarRanked). Each page is a slice of one ranking; a
// continuation whose ranking moved under it (a new publication, a basis
// change, new plays or vectors) is an explicit stale continuation.
func (s *Service) ListeningSimilarPage(viewer Viewer, id string, bySound bool, cursor string, limit int) (SoundSimilar, error) {
	out := SoundSimilar{Items: ListeningSimilar{ItemID: id, Entries: []ContentEntry{}}}
	if _, ok := entityid.Decode(id); !ok {
		return out, sql.ErrNoRows
	}
	if dbwork.Snapshot(s.Context()) == nil && s.readTx == nil {
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).ListeningSimilarPage(viewer, id, bySound, cursor, limit)
			return err
		})
		return out, err
	}
	limit = min(pageLimit(limit), 24)
	spec := relationSpec{id: "genre", caching: relationFresh}
	if bySound {
		library, order, found, err := s.sonicSimilarOrder(viewer, id)
		if err != nil {
			return out, err
		}
		if found {
			out.BySound = true
			out.Items.LibraryID = library
			spec = relationSpec{id: "sound", caching: relationContinuation, order: order}
		}
	}
	if !out.BySound {
		library, ranked, err := s.listeningSimilarRanked(viewer, id)
		if err != nil {
			return out, err
		}
		out.Items.LibraryID = library
		spec.order = func(int) (*relationOrdered, error) {
			return &relationOrdered{public: append([]string{}, ranked...), complete: true}, nil
		}
	}
	revision, err := s.ContentRevision(out.Items.LibraryID, viewer.Profile)
	if err != nil {
		return out, err
	}
	out.Items.Revision = revision
	scope := cursorScope{Library: out.Items.LibraryID, Profile: viewer.Profile, Viewer: viewer.Fence, View: "listening_similar", Entity: id, Section: spec.id}
	start, shown := 0, ""
	if cursor != "" {
		value, err := s.decodeDetailCursor(cursor, scope, revision)
		if err != nil {
			return out, err
		}
		if value.Start <= 0 || value.Fingerprint == "" {
			return out, ErrCursor
		}
		start, shown = value.Start, value.Fingerprint
	}
	key := ""
	if spec.caching != relationFresh {
		key = strings.Join([]string{"listening_similar", id, spec.id, viewer.Profile, viewer.Fence, RestrictionFence(viewer.EffectiveRestrictions()), idsJSON(viewer.Libraries), itoa(revision.Catalog), itoa(revision.Viewer)}, "\x00")
	}
	order, err := s.relationOrder(key, spec, cursor == "", start+limit+1)
	if err != nil {
		return out, err
	}
	if cursor != "" && (order.len() < start || order.digest(start) != shown) {
		return out, ErrStaleContinuation
	}
	end := min(order.len(), start+limit)
	if end > start {
		if out.Items.Entries, err = s.homeEngineEntries(viewer.Profile, order.public[start:end]); err != nil {
			return out, err
		}
	}
	for i := range out.Items.Entries {
		out.Items.Entries[i].Playback = nil
	}
	if err = s.EnrichListeningEntries(viewer, out.Items.Entries); err != nil {
		return out, err
	}
	out.Items.HasMore = end < order.len()
	if out.Items.HasMore {
		out.Items.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: scope, Start: end, Fingerprint: order.digest(end), Expires: time.Now().Add(BrowseCursorTTLSecond * time.Second).Unix()}, revision)
		if err != nil {
			return out, err
		}
	}
	after, err := s.ContentRevision(out.Items.LibraryID, viewer.Profile)
	if err != nil {
		return out, err
	}
	if after != revision {
		return out, ErrStaleContinuation
	}
	return out, nil
}

// sonicSimilarOrder authorizes a sound seed and reads its center; found is
// false where the seed has no current vector. The order scans every permitted
// group's vector, so its continuations reuse the first page's ranking.
func (s *Service) sonicSimilarOrder(viewer Viewer, id string) (string, func(int) (*relationOrdered, error), bool, error) {
	var seed int64
	var kind int
	var library string
	err := s.read().QueryRow(`SELECT item.id,item.kind,library.library_id FROM catalog_entities item JOIN catalog_libraries library ON library.id=item.library_id WHERE item.public_id=pid_blob(?) AND item.retired=0 AND library.retired=0`, id).Scan(&seed, &kind, &library)
	if err != nil {
		return "", nil, false, err
	}
	if !viewer.AllowsLibrary(library) {
		return "", nil, false, sql.ErrNoRows
	}
	if kind != 5 && kind != 6 && kind != 7 {
		return "", nil, false, sql.ErrNoRows
	}
	var center sonic.Embedding
	found := false
	if err = s.sonicGroups(viewer, kind, seed, func(group sonicGroup) {
		if group.id == seed {
			center = group.vector
			found = true
		}
	}); err != nil || !found {
		return library, nil, false, err
	}
	type candidate struct {
		id     int64
		public string
		score  float64
	}
	order := func(int) (*relationOrdered, error) {
		ranked := []candidate{}
		if err := s.sonicGroups(viewer, kind, 0, func(group sonicGroup) {
			if group.id == seed {
				return
			}
			score, e := sonic.Cosine(center, group.vector)
			if e != nil {
				return
			}
			ranked = append(ranked, candidate{group.id, group.public, score})
		}); err != nil {
			return nil, err
		}
		sort.Slice(ranked, func(i, j int) bool {
			if ranked[i].score != ranked[j].score {
				return ranked[i].score > ranked[j].score
			}
			return ranked[i].id < ranked[j].id
		})
		out := &relationOrdered{public: make([]string, 0, len(ranked)), complete: true}
		for _, item := range ranked {
			out.public = append(out.public, item.public)
		}
		return out, nil
	}
	return library, order, true, nil
}

// QueueSonicRefreshTx marks only the existing inventory policy queue. A single
// analyzed song in a shared CUE file cannot hide another missing song/range.
// It does not read recordings, start jobs or grant source authority.
func QueueSonicRefreshTx(ctx context.Context, tx *sql.Tx) error {
	if tx == nil {
		return ErrAdminQuery
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO inventory_policy_pending(library_id,revision)
	 SELECT configured.id,policy.revision FROM libraries configured
	 JOIN library_scan_policies policy ON policy.library_id=configured.id
	 JOIN catalog_libraries library ON library.library_id=configured.id AND library.retired=0
	 WHERE configured.kind='music' AND policy.tier<>'file_list_only'
	 AND COALESCE((SELECT enabled FROM library_sonic_settings settings WHERE settings.library_id=configured.id),?)=1
	 AND EXISTS(SELECT 1 FROM catalog_entities item WHERE item.library_id=library.id AND item.kind=7 AND item.retired=0
	 AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0)
	 AND `+currentSonicEligibleSQL+`
	 AND `+currentSonicVectorSQL+` IS NULL)
	 ON CONFLICT(library_id) DO UPDATE SET revision=excluded.revision`, SonicDefaultEnabled(), sonic.ModelID, sonic.ModelVersion, sonic.ModelSHA256)
	return err
}
