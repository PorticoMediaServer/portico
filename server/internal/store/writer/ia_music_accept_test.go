package writer

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

const (
	iaMusicTestLibraryID      int64 = 7
	iaMusicTestLibraryKey           = "music-library"
	iaMusicTestSourceID             = "music-source"
	iaMusicTestRunID                = "music-run"
	iaMusicTestAssetID        int64 = 41
	iaMusicTestRoot                 = "/media/music"
	iaMusicTestObjectID             = "object:music:1"
	iaMusicTestObjectRevision       = "object-revision:1"
	iaMusicTestDirectory            = "Album"
)

// One private W frame, the frozen clock and the exact group C draft subset. The
// fixture seeds only prepared source/run/asset/search witnesses; it is not a
// qualified population, admission profile or product wiring.
func iaMusicAcceptFixture(t *testing.T) (*executor, *iaEntryRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, proof := iaEntrySecurityFixture(t)
	ddl, err := os.ReadFile("testdata/ia-music-accept-draft.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.db.ExecContext(t.Context(), string(ddl)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(5,'artist',0,0,1,1,0),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1)`, nil},
		{`INSERT INTO libraries(id,name,kind,root,configuration_present,configuration_revision,configuration_metadata_revision) VALUES('music-library','Music','music','/media/music',1,1,1)`, nil},
		{`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'music-library','/media/music')`, nil},
		{`INSERT INTO library_sources(id,library_id,name,configured_root,root,classification,root_identity,follow_symlinks,interval_seconds,next_scan_at,identity_confirmed,incarnation,generation,health,enabled,kind) VALUES('music-source','music-library','Music','/media/music','/media/music','local','music-root-identity',0,0,'',1,'music-incarnation',3,'ready',1,'local')`, nil},
		{`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('music-run','music-library','running',0,'')`, nil},
		{`INSERT INTO inventory_runs(job_id,original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence) VALUES('music-run',7,'music-library','/media/music',0,?,?,'music-source',3,'music-incarnation','music-root-identity',4,'inventory','first-import',11,13,?,5,?,1,1)`,
			[]any{iaEpisodeBytes(3), make([]byte, 32), w.now().Add(time.Hour).UnixMilli(), iaEpisodeBytes(1)}},
		{`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,cursor,state,pages,skipped) VALUES('music-run','Album','music-directory-identity','music-directory-revision','','pending',2,0)`, nil},
		{`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(41,'music-token','/media/music/Album/01 Song.flac',42,7,'','','',0,0,0,7)`, nil},
		{`INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'raw-active',?,?,?,'active',1000)`,
			[]any{searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint}},
		{`UPDATE search_revision SET revision=1,active_generation_id=1,building_generation_id=NULL,input_sequence=0 WHERE singleton=1`, nil},
	} {
		if _, err = w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	registry, err := stmttest.New(t, iaMusicDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	return w, &iaEntryRegistry{registry, keeper}, proof
}

func iaMusicAcceptRequest(w *executor) iaMusicInventoryRequest {
	return iaMusicInventoryRequest{
		scope:  inventoryScope{runID: iaMusicTestRunID, libraryKey: iaMusicTestLibraryKey, libraryID: iaMusicTestLibraryID},
		source: inventorySource{id: iaMusicTestSourceID, incarnation: "music-incarnation", rootIdentity: "music-root-identity", resolvedRoot: []byte(iaMusicTestRoot), generation: 3},
		run: inventoryRun{revision: 5, policyRevision: 4, leaseOwner: 11, leaseGeneration: 13,
			leaseExpiresMS: w.now().Add(time.Hour).UnixMilli(), lastSequence: 1, acknowledgedSequence: 1, phase: "inventory", scanMode: "first-import"},
		directory: inventoryDirectory{path: iaMusicTestDirectory, identity: "music-directory-identity", revision: "music-directory-revision",
			cursorKind: inventoryLinuxCursor, expectedCursor: "", nextCursor: "42", pages: 2, skipped: 0, rawNames: 1, complete: true},
		files: []inventoryFile{{sourceKey: "asset-source-key", relativePath: "Album/01 Song.flac", assetPath: iaMusicTestRoot + "/Album/01 Song.flac",
			objectIdentity: iaMusicTestObjectID, revision: iaMusicTestObjectRevision, size: 42, modifiedNS: 7, title: "01 Song",
			sortTitle: "", sortKey: "song", sortHead: "s", sortKeyKeepArticles: "song", sortHeadKeepArticles: "s",
			metadataLanguage: "en-US", year: 2020}},
		complete:       true,
		albumKey:       "album-key",
		albumTitle:     "Album",
		albumArtistKey: "artist-key",
		artists:        []iaMusicArtist{{key: "artist-key", title: "Artist"}},
		songArtistKeys: []string{"artist-key"},
		songs:          []iaMusicSong{{key: "song-key", title: "Song", part: 0, cueOrdinal: 0, start: 0}},
	}
}

func iaMusicAcceptReference(t *testing.T, r iaMusicInventoryRequest, sequence int64, operation byte) ReceiptReference {
	t.Helper()
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: sequence, OperationID: [16]byte{operation}}
	command, err := newIAMusicInventoryCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	return ReceiptReference{Identity: id, RequestDigest: command.digest}
}

func iaMusicAcceptTurn(t *testing.T, w *executor, r *iaEntryRegistry, q iaMusicInventoryRequest) (*commandTurn, iaEntryBudget) {
	t.Helper()
	return iaEntryTurn(t, w, r, iaEntryRequest{purpose: IAAcceptAudioInventory, action: iaEntryApply, libraryID: q.scope.libraryID, libraryKey: q.scope.libraryKey})
}

func iaMusicAcceptRun(t *testing.T, w *executor, r *iaEntryRegistry, p *usOwnerAuthorityProof, q iaMusicInventoryRequest, ref ReceiptReference) (*commandTurn, iaMusicAcceptOutcome, error) {
	t.Helper()
	turn, budget := iaMusicAcceptTurn(t, w, r, q)
	out, err := acceptIAMusicInventory(t.Context(), turn, p, t.Context(), budget, ref, q)
	return turn, out, err
}

func iaMusicCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

func iaMusicNoEffects(t *testing.T, w *executor) {
	t.Helper()
	for _, table := range []string{"catalog_identities", "catalog_entities", "catalog_asset_links", "catalog_asset_contributions",
		"catalog_song_artists", "scan_command_receipts", "scan_media_result_members", "search_publish_jobs"} {
		if got := iaMusicCount(t, w, `SELECT count(*) FROM `+table); got != 0 {
			t.Fatal("refused request wrote", table, got)
		}
	}
	if got := iaMusicCount(t, w, `SELECT last_command_sequence FROM inventory_runs WHERE job_id=?`, iaMusicTestRunID); got != 1 {
		t.Fatal("refused request advanced the run", got)
	}
	if got := iaMusicCount(t, w, `SELECT input_sequence FROM search_revision WHERE singleton=1`); got != 0 {
		t.Fatal("refused request advanced search input", got)
	}
}

// Fresh birth writes one complete unit: artist/album/song identities, the music
// hierarchy, the song-artist edge, the asset link/contribution, search intent,
// the directory/run advance and the typed receipt/child cohort. Replay returns
// the saved result after live source/lease changes without any new effect.
func TestIAMusicAcceptFreshReplayAndLiveCohort(t *testing.T) {
	w, reg, p := iaMusicAcceptFixture(t)
	q := iaMusicAcceptRequest(w)
	ref := iaMusicAcceptReference(t, q, 2, 2)
	turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.digest == (ResultDigest{}) {
		t.Fatal("fresh music accept", out, err)
	}
	if out.result.facts.acceptedRevision != 6 || out.result.facts.appliedMS != w.now().UnixMilli() ||
		out.result.facts.resultCount != 5 || out.result.facts.memberBytes <= 0 ||
		out.result.facts.objectID != iaMusicTestObjectID || string(out.result.facts.nextPayload) != "42" {
		t.Fatal("fresh music facts", out.result.facts)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		args  []any
		want  int64
	}{
		{`SELECT count(*) FROM catalog_identities WHERE library_id=7`, nil, 3},
		{`SELECT count(*) FROM catalog_entities WHERE library_id=7`, nil, 3},
		{`SELECT count(*) FROM catalog_entities WHERE kind=5 AND artist_id IS NULL AND album_id IS NULL`, nil, 1},
		{`SELECT count(*) FROM catalog_entities WHERE kind=6 AND artist_id=(SELECT id FROM catalog_identities WHERE source_key='artist-key')`, nil, 1},
		{`SELECT count(*) FROM catalog_entities WHERE kind=7 AND album_id=(SELECT id FROM catalog_identities WHERE source_key='album-key') AND artist_id=(SELECT id FROM catalog_identities WHERE source_key='artist-key')`, nil, 1},
		{`SELECT count(*) FROM catalog_entities WHERE kind IN(5,6,7) AND music_incarnation IS NOT NULL AND music_revision=1 AND music_membership_revision=1`, nil, 3},
		{`SELECT count(*) FROM catalog_song_artists`, nil, 1},
		{`SELECT count(*) FROM catalog_asset_links WHERE asset_id=41 AND part_index=0 AND cue_ordinal IS NULL AND start_seconds=0 AND end_seconds IS NULL`, nil, 1},
		{`SELECT count(*) FROM catalog_asset_contributions WHERE asset_id=41 AND membership_refs=1 AND file_size=42`, nil, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='audio_inventory_accept' AND result_count=5 AND accepted_revision=6`, nil, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='audio_inventory_accept' AND ordinal BETWEEN 0 AND 4`, nil, 5},
		{`SELECT count(*) FROM search_publish_jobs`, nil, 3},
		{`SELECT count(*) FROM search_generations WHERE pending_subjects=3`, nil, 1},
		{`SELECT count(*) FROM search_revision WHERE input_sequence=1`, nil, 1},
		{`SELECT count(*) FROM inventory_directories WHERE cursor='42' AND pages=3`, nil, 1},
		{`SELECT count(*) FROM inventory_runs WHERE job_revision=6 AND last_command_sequence=2 AND acknowledged_command_sequence=1`, nil, 1},
	} {
		if got := iaMusicCount(t, w, check.query, check.args...); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
	// Live source/lease changes never edit or filter the saved result.
	wc008NativeExec(t, w, `UPDATE library_sources SET generation=9,root='/moved',configured_root='/moved' WHERE id='music-source'`)
	wc008NativeExec(t, w, `UPDATE inventory_runs SET job_revision=99,job_lease_expires_ms=1 WHERE job_id='music-run'`)
	turn, replay, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
	if err != nil || replay.decision != receiptReplay || replay.digest != out.digest || replay.result.facts.resultCount != 5 ||
		replay.result.facts.acceptedRevision != out.result.facts.acceptedRevision {
		t.Fatal("saved music replay", replay, err)
	}
	if got := iaMusicCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 1 {
		t.Fatal("replay minted a receipt", got)
	}
	turn.transaction.rollback(nil)
}

// Multiple independent song artists and a distinct album artist are separate
// facts: the album and song carry the album artist, while the song-artist table
// carries the complete accepted edge cohort.
func TestIAMusicAcceptMultipleArtistsAndAlbumArtist(t *testing.T) {
	w, reg, p := iaMusicAcceptFixture(t)
	q := iaMusicAcceptRequest(w)
	q.albumArtistKey = "album-artist-key"
	q.artists = []iaMusicArtist{{key: "album-artist-key", title: "Album Artist"}, {key: "featured-key", title: "Featured"}}
	q.songArtistKeys = []string{"album-artist-key", "featured-key"}
	ref := iaMusicAcceptReference(t, q, 2, 2)
	turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 7 {
		t.Fatal("multiple artists", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	albumArtist := iaMusicCount(t, w, `SELECT subject_id FROM catalog_identities WHERE source_key='album-artist-key'`)
	featured := iaMusicCount(t, w, `SELECT subject_id FROM catalog_identities WHERE source_key='featured-key'`)
	if got := iaMusicCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=6 AND artist_id=?`, albumArtist); got != 1 {
		t.Fatal("album artist", got)
	}
	if got := iaMusicCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=7 AND artist_id=?`, albumArtist); got != 1 {
		t.Fatal("song album-artist", got)
	}
	if got := iaMusicCount(t, w, `SELECT count(*) FROM catalog_song_artists WHERE artist_id IN (?,?)`, albumArtist, featured); got != 2 {
		t.Fatal("song artists", got)
	}
}

// A complete CUE cohort accepts one song per cue child with its own range and
// cue ordinal; the cue sheet itself is a witness only and never a linked asset.
func TestIAMusicAcceptCompleteCueCohort(t *testing.T) {
	w, reg, p := iaMusicAcceptFixture(t)
	q := iaMusicAcceptRequest(w)
	q.cue = &iaMusicCue{complete: true, file: inventoryFile{sourceKey: "cue-source-key", relativePath: "Album/album.cue",
		assetPath: iaMusicTestRoot + "/Album/album.cue", objectIdentity: "object:cue:1", revision: "cue-revision", size: 1, modifiedNS: 2,
		metadataLanguage: "en-US", title: "album"}}
	q.songs = nil
	for i := 0; i < 3; i++ {
		start := float64(i * 20)
		end := start + 20
		q.songs = append(q.songs, iaMusicSong{key: "song-" + string(rune('a'+i)), title: "Track " + string(rune('1'+i)),
			part: int64(i), cueOrdinal: int64(i + 1), start: start, end: &end})
	}
	ref := iaMusicAcceptReference(t, q, 2, 2)
	turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh || out.result.facts.resultCount != 11 {
		t.Fatal("cue cohort", out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_entities WHERE kind=7`, 3},
		{`SELECT count(*) FROM catalog_song_artists`, 3},
		{`SELECT count(*) FROM catalog_asset_links WHERE asset_id=41 AND cue_ordinal BETWEEN 1 AND 3 AND end_seconds IS NOT NULL`, 3},
		{`SELECT count(*) FROM catalog_asset_links WHERE asset_id=41 AND part_index=2 AND cue_ordinal=3 AND start_seconds=40 AND end_seconds=60`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='audio_inventory_accept'`, 11},
		{`SELECT count(*) FROM catalog_assets WHERE path='/media/music/Album/album.cue'`, 0},
	} {
		if got := iaMusicCount(t, w, check.query); got != check.want {
			t.Fatal(check.query, got, check.want)
		}
	}
}

// Every bound is enforced before the first effect: an empty cohort, a seventeenth
// song and a sixty-fifth artist all refuse the whole unit.
func TestIAMusicAcceptBounds(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		w, reg, p := iaMusicAcceptFixture(t)
		q := iaMusicAcceptRequest(w)
		q.songs = nil
		ref := ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}, RequestDigest: RequestDigest{1}}
		turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrInvalid) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("empty", out, err)
		}
		turn.transaction.rollback(nil)
		iaMusicNoEffects(t, w)
	})
	t.Run("seventeen_songs", func(t *testing.T) {
		w, reg, p := iaMusicAcceptFixture(t)
		q := iaMusicAcceptRequest(w)
		q.cue = &iaMusicCue{complete: true, file: inventoryFile{sourceKey: "cue-source-key", relativePath: "Album/album.cue",
			assetPath: iaMusicTestRoot + "/Album/album.cue", objectIdentity: "object:cue:1", revision: "cue-revision", size: 1, modifiedNS: 2,
			metadataLanguage: "en-US", title: "album"}}
		q.songs = nil
		for i := 0; i < 17; i++ {
			start := float64(i * 20)
			end := start + 20
			q.songs = append(q.songs, iaMusicSong{key: "song-" + string(rune('a'+i)), title: "Track",
				part: int64(i), cueOrdinal: int64(i + 1), start: start, end: &end})
		}
		ref := ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}, RequestDigest: RequestDigest{1}}
		turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("seventeen songs", out, err)
		}
		turn.transaction.rollback(nil)
		iaMusicNoEffects(t, w)
	})
	t.Run("sixty_five_artists", func(t *testing.T) {
		w, reg, p := iaMusicAcceptFixture(t)
		q := iaMusicAcceptRequest(w)
		q.artists = nil
		for i := 0; i < 65; i++ {
			q.artists = append(q.artists, iaMusicArtist{key: "artist-" + string(rune('a'+i)), title: "Artist"})
		}
		ref := ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 2, OperationID: [16]byte{2}}, RequestDigest: RequestDigest{1}}
		turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
		if err != nil || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("sixty five artists", out, err)
		}
		turn.transaction.rollback(nil)
		iaMusicNoEffects(t, w)
	})
}

// Every fresh witness is re-proven after the receipt is Fresh. A changed
// library, source, directory, asset or parent refuses the complete unit before
// any catalogue/receipt row exists.
func TestIAMusicAcceptFreshGates(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *executor, *iaMusicInventoryRequest){
		"retired_library": func(t *testing.T, w *executor, _ *iaMusicInventoryRequest) {
			wc008NativeExec(t, w, `UPDATE catalog_libraries SET retired=1 WHERE id=7`)
		},
		"stale_source":    func(_ *testing.T, _ *executor, q *iaMusicInventoryRequest) { q.source.generation++ },
		"stale_directory": func(_ *testing.T, _ *executor, q *iaMusicInventoryRequest) { q.directory.expectedCursor = "9" },
		"changed_asset":   func(_ *testing.T, _ *executor, q *iaMusicInventoryRequest) { q.files[0].size++ },
		"changed_lease": func(t *testing.T, w *executor, q *iaMusicInventoryRequest) {
			q.run.leaseExpiresMS = w.now().UnixMilli()
		},
		"stale_run_revision": func(_ *testing.T, _ *executor, q *iaMusicInventoryRequest) { q.run.revision++ },
		"foreign_parent": func(t *testing.T, w *executor, _ *iaMusicInventoryRequest) {
			wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(99,X'99000000000000000000000000000000',7,5),(100,X'9a000000000000000000000000000000',7,6)`)
			wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id) VALUES(99,X'99000000000000000000000000000000',7,5,'Other','other','o','other','o',1,zeroblob(16),zeroblob(16),NULL),(100,X'9a000000000000000000000000000000',7,6,'Other Album','other album','o','other album','o',1,zeroblob(16),zeroblob(16),99)`)
			wc008NativeExec(t, w, `INSERT INTO catalog_identities(library_id,source_key,subject_id) VALUES(7,'album-key',100)`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaMusicAcceptFixture(t)
			q := iaMusicAcceptRequest(w)
			mutate(t, w, &q)
			ref := iaMusicAcceptReference(t, q, 2, 2)
			turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
				t.Fatal("fresh gate", out, err)
			}
			turn.transaction.rollback(nil)
			if got := iaMusicCount(t, w, `SELECT count(*) FROM catalog_entities WHERE kind=7`); got != 0 {
				t.Fatal("gate wrote a song entity", got)
			}
			if got := iaMusicCount(t, w, `SELECT count(*) FROM catalog_asset_links`); got != 0 {
				t.Fatal("gate wrote a link", got)
			}
			if got := iaMusicCount(t, w, `SELECT count(*) FROM scan_command_receipts`); got != 0 {
				t.Fatal("gate wrote a receipt", got)
			}
		})
	}
}

// Current authority precedes every retained read: a forged issuer, a demoted
// owner and a cancelled caller lifetime all deny with zero public output.
func TestIAMusicAcceptAuthorityCells(t *testing.T) {
	t.Run("forged_issuer", func(t *testing.T) {
		w, reg, p := iaMusicAcceptFixture(t)
		q := iaMusicAcceptRequest(w)
		ref := iaMusicAcceptReference(t, q, 2, 2)
		fake := *p
		fake.witness.issuer.family = "forged"
		turn, out, err := iaMusicAcceptRun(t, w, reg, &fake, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("forged issuer", out, err)
		}
		turn.transaction.rollback(nil)
		iaMusicNoEffects(t, w)
	})
	t.Run("stale_authority", func(t *testing.T) {
		for name, mutation := range map[string]string{
			"demoted_owner":  `UPDATE direct_memberships SET role='member'`,
			"retired_token":  `UPDATE authorization_family_tokens SET retired=1`,
			"denied_device":  `UPDATE identity_security_fences SET denied=1`,
			"profile_reset":  `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
			"reset_session":  `INSERT INTO reset_file_sessions(family_id,kind) VALUES('issuer-family','owner')`,
			"narrowed_grant": `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES('issuer-family','cast_target')`,
		} {
			t.Run(name, func(t *testing.T) {
				w, reg, p := iaMusicAcceptFixture(t)
				wc008NativeExec(t, w, mutation)
				q := iaMusicAcceptRequest(w)
				ref := iaMusicAcceptReference(t, q, 2, 2)
				turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
				if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
					t.Fatal("stale authority", out, err)
				}
				turn.transaction.rollback(nil)
				iaMusicNoEffects(t, w)
			})
		}
	})
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, reg, p := iaMusicAcceptFixture(t)
		q := iaMusicAcceptRequest(w)
		ref := iaMusicAcceptReference(t, q, 2, 2)
		turn, budget := iaMusicAcceptTurn(t, w, reg, q)
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		out, err := acceptIAMusicInventory(cancelled, turn, p, cancelled, budget, ref, q)
		if err == nil || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("cancelled lifetime", out, err)
		}
		turn.transaction.rollback(nil)
		iaMusicNoEffects(t, w)
	})
}

// Current authority is re-observed after the fresh gates and again after the
// writes. A same-turn revocation injected by the last read or by a catalogue
// write must deny the fresh result and leave no committed effect.
func TestIAMusicAcceptLateAuthorityRechecks(t *testing.T) {
	t.Run("before_writes", func(t *testing.T) {
		w, reg, p := iaMusicAcceptFixture(t)
		state := installIAEntryQueryRevocation(t, w, "FROM catalog_assets")
		q := iaMusicAcceptRequest(w)
		ref := iaMusicAcceptReference(t, q, 2, 2)
		turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
		if !state.fired || state.err != nil {
			t.Fatal("asset-close mutation did not run", state.err)
		}
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("pre-write recheck released a stale result", out, err)
		}
		turn.transaction.rollback(nil)
		iaMusicNoEffects(t, w)
	})
	t.Run("after_writes", func(t *testing.T) {
		w, reg, p := iaMusicAcceptFixture(t)
		wc008NativeExec(t, w, `CREATE TRIGGER ia_music_late_revocation AFTER INSERT ON catalog_asset_links BEGIN UPDATE identity_security_fences SET denied=1; END`)
		q := iaMusicAcceptRequest(w)
		ref := iaMusicAcceptReference(t, q, 2, 2)
		turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
		if !errors.Is(err, ErrDisclosureDenied) || out.decision != 0 || len(out.result.facts.members) != 0 {
			t.Fatal("post-write recheck released a stale result", out, err)
		}
		var links int64
		if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_asset_links`).Scan(&links); err != nil || links != 1 {
			t.Fatal("link write not reached before the final recheck", links, err)
		}
		turn.transaction.rollback(nil)
		iaMusicNoEffects(t, w)
	})
}

// A saved cohort must be complete and digest-exact before it is served. A
// missing member, a wrong retained-byte ledger and a changed result digest all
// refuse the replay with zero public output.
func TestIAMusicAcceptReplayRejectsTamperedCohort(t *testing.T) {
	for name, mutate := range map[string]string{
		"missing_member": `DELETE FROM scan_media_result_members WHERE ordinal=4`,
		"member_bytes":   `UPDATE scan_command_receipts SET ia_media_member_bytes=1`,
		"result_digest":  `UPDATE scan_command_receipts SET result_digest=zeroblob(32)`,
	} {
		t.Run(name, func(t *testing.T) {
			w, reg, p := iaMusicAcceptFixture(t)
			q := iaMusicAcceptRequest(w)
			ref := iaMusicAcceptReference(t, q, 2, 2)
			turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
			if err != nil || out.decision != receiptFresh {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			wc008NativeExec(t, w, mutate)
			turn, replay, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
			if !errors.Is(err, ErrResolveRequired) || replay.decision != 0 || replay.digest != (ResultDigest{}) || len(replay.result.facts.members) != 0 {
				t.Fatal("tampered cohort served", replay, err)
			}
			turn.transaction.rollback(nil)
		})
	}
}

// A stored search bank/profile that cannot be honored refuses the whole unit
// instead of accepting a catalogue cohort with no search intent.
func TestIAMusicAcceptRefusesUnusableSearchBank(t *testing.T) {
	w, reg, p := iaMusicAcceptFixture(t)
	wc008NativeExec(t, w, `UPDATE search_generations SET unicode_fingerprint='bad'`)
	q := iaMusicAcceptRequest(w)
	ref := iaMusicAcceptReference(t, q, 2, 2)
	turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
	if err == nil || out.decision != 0 || out.digest != (ResultDigest{}) || len(out.result.facts.members) != 0 {
		t.Fatal("unusable search bank", out, err)
	}
	turn.transaction.rollback(nil)
	iaMusicNoEffects(t, w)
}

// A lost COMMIT reply is never success. The committed file is read back through
// a FRESH read-only connection, never the fenced writer pool.
func TestIAMusicAcceptUnknownCommitIsNotSuccess(t *testing.T) {
	w, reg, p := iaMusicAcceptFixture(t)
	path := iaEpisodePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	base, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: base, afterCommit: true, replyError: true})
	q := iaMusicAcceptRequest(w)
	ref := iaMusicAcceptReference(t, q, 2, 2)
	turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal("fresh before commit", out, err)
	}
	result, err := turn.transaction.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	readCfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, readCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, check := range []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM catalog_entities WHERE library_id=7`, 3},
		{`SELECT count(*) FROM catalog_song_artists`, 1},
		{`SELECT count(*) FROM scan_command_receipts WHERE command_kind='audio_inventory_accept' AND result_count=5`, 1},
		{`SELECT count(*) FROM scan_media_result_members WHERE command_kind='audio_inventory_accept'`, 5},
	} {
		var got int64
		if err = db.QueryRowContext(t.Context(), check.query).Scan(&got); err != nil || got != check.want {
			t.Fatal(check.query, got, check.want, err)
		}
	}
	if _, err = w.begin(t.Context(), IAAcceptAudioInventory, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("advanced past unknown commit", err)
	}
}

// At or below the durable ACK floor the original is Retired: no result, no
// digest and no new effect, even while the physical rows still exist.
func TestIAMusicAcceptRetiredAfterAck(t *testing.T) {
	w, reg, p := iaMusicAcceptFixture(t)
	q := iaMusicAcceptRequest(w)
	ref := iaMusicAcceptReference(t, q, 2, 2)
	turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE inventory_runs SET acknowledged_command_sequence=2 WHERE job_id='music-run'`)
	before := map[string]int64{}
	for _, table := range []string{"catalog_entities", "catalog_song_artists", "scan_command_receipts", "scan_media_result_members", "search_publish_jobs"} {
		before[table] = iaMusicCount(t, w, `SELECT count(*) FROM `+table)
	}
	turn, out, err = iaMusicAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptRetired || out.digest != (ResultDigest{}) || out.result.facts.resultCount != 0 || len(out.result.facts.members) != 0 {
		t.Fatal("retired original", out, err)
	}
	turn.transaction.rollback(nil)
	for table, want := range before {
		if got := iaMusicCount(t, w, `SELECT count(*) FROM `+table); got != want {
			t.Fatal("retired replay wrote", table, got, want)
		}
	}
}

// A conflicting digest and a non-contiguous sequence are conflicts, never a
// fresh admission or a new receipt.
func TestIAMusicAcceptRejectsConflictingOriginal(t *testing.T) {
	w, reg, p := iaMusicAcceptFixture(t)
	q := iaMusicAcceptRequest(w)
	ref := iaMusicAcceptReference(t, q, 2, 2)
	changed := q
	changed.files[0].revision += "x"
	turn, out, err := iaMusicAcceptRun(t, w, reg, p, changed, ref)
	if !errors.Is(err, ErrConflict) || out.decision != 0 || out.digest != (ResultDigest{}) {
		t.Fatal("changed request digest", out, err)
	}
	turn.transaction.rollback(nil)
	gap := q
	// The request is internally consistent for sequence 3, but the durable owner
	// is still at sequence 1: a gap, never a fresh admission.
	gap.run.lastSequence, gap.run.acknowledgedSequence = 2, 2
	gapRef := iaMusicAcceptReference(t, gap, 3, 4)
	turn, out, err = iaMusicAcceptRun(t, w, reg, p, gap, gapRef)
	if !errors.Is(err, ErrConflict) || out.decision != 0 {
		t.Fatal("sequence gap", out, err)
	}
	turn.transaction.rollback(nil)
	iaMusicNoEffects(t, w)
}

func TestIAMusicAcceptRollbackLeavesNoEffect(t *testing.T) {
	w, reg, p := iaMusicAcceptFixture(t)
	q := iaMusicAcceptRequest(w)
	ref := iaMusicAcceptReference(t, q, 2, 2)
	turn, out, err := iaMusicAcceptRun(t, w, reg, p, q, ref)
	if err != nil || out.decision != receiptFresh {
		t.Fatal(out, err)
	}
	turn.transaction.rollback(nil)
	iaMusicNoEffects(t, w)
	if got := iaMusicCount(t, w, `SELECT count(*) FROM inventory_directories WHERE cursor='' AND pages=2`); got != 1 {
		t.Fatal("rollback advanced the directory", got)
	}
}
