package administration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
)

func selectionDeleteTestTarget(t *testing.T, db *sql.DB, item string) selectionactions.Target {
	t.Helper()
	var target selectionactions.Target
	var parent, added sql.NullInt64
	if err := db.QueryRow(`SELECT e.id,pid(e.public_id),cl.library_id,k.name,e.title,e.parent_id,e.added_ms FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?)`, item).Scan(&target.EntityID, &target.ItemID, &target.LibraryID, &target.Kind, &target.Title, &parent, &added); err != nil {
		t.Fatal(err)
	}
	var p any
	if parent.Valid {
		p = parent.Int64
	}
	if added.Valid {
		target.Lifetime = strconv.FormatInt(added.Int64, 10)
	}
	target.Identity = selectionactions.TargetIdentity(target.ItemID, target.LibraryID, target.Kind, p)
	return target
}
func selectionDeleteTestCapture(t *testing.T, s *Service, target selectionactions.Target) []selectionactions.File {
	t.Helper()
	cursor := NewSelectionDeleteCursor(target)
	var files []selectionactions.File
	for i := 0; i < 10000; i++ {
		before := *cursor
		next, records, done, err := s.StepSelectionDelete(context.Background(), cursor)
		if err != nil {
			t.Fatal(err)
		}
		if next.units > selectionDeleteUnits {
			t.Fatalf("raw work %d exceeds %d", next.units, selectionDeleteUnits)
		}
		if cursor.afterAsset != before.afterAsset || cursor.phase != before.phase || cursor.file != before.file {
			t.Fatal("input cursor mutated")
		}
		if next.Bytes() > selectionactions.MaxSourceBytes {
			t.Fatalf("retained bytes %d", next.Bytes())
		}
		for _, record := range records {
			if record.Target != nil {
				t.Fatal("cursor repeated source target")
			}
			if record.File != nil {
				files = append(files, *record.File)
			}
			if record.Finish != 0 && (!done || record.Finish != target.EntityID) {
				t.Fatalf("invalid finish %+v", record)
			}
		}
		if done {
			if len(records) != 1 || records[0].Finish != target.EntityID {
				t.Fatal("missing final Finish record")
			}
			again, records, done, err := s.StepSelectionDelete(context.Background(), next)
			if err != nil || !done || len(records) != 0 || again == nil {
				t.Fatalf("terminal cursor re-emitted records: %v", err)
			}
			return files
		}
		cursor = next
	}
	t.Fatal("cursor did not finish")
	return nil
}
func selectionDeleteTestAllow(context.Context, *sql.Tx, identity.Principal, string, string, *selectionactions.Target) error {
	return nil
}
func selectionDeleteTestOptions(remove bool) selectionactions.Options {
	return selectionactions.Options{DeleteFiles: &remove}
}
func selectionDeleteTestCheckpoint(t *testing.T, effects *[]selectionactions.Effect) func(*sql.Tx, selectionactions.Effect) error {
	t.Helper()
	return func(tx *sql.Tx, e selectionactions.Effect) error {
		// This is the real caller-owned journal/checkpoint seam, in the same writer.
		if _, err := tx.Exec(`INSERT INTO admin_documents(scope,revision,body,updated_ms) VALUES(?,1,'{}',0)`, fmt.Sprintf("selection-test-checkpoint-%d", len(*effects))); err != nil {
			return err
		}
		*effects = append(*effects, e)
		return nil
	}
}
func selectionDeleteTestOther(t *testing.T, db *sql.DB, library string) int64 {
	t.Helper()
	var id int64
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		handle, err := compactcatalog.LibraryTx(context.Background(), tx, library)
		if err != nil {
			return err
		}
		id, _, err = compactcatalog.EnsureEntityTx(context.Background(), tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Movie, Key: "selection-other", Title: "Other"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSelectionDeleteCursorManyFilesBoundedAndReadOnly(t *testing.T) {
	s, db, root, item, paths := deletionFixture(t)
	target := selectionDeleteTestTarget(t, db, item)
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for i := 2; i < 257; i++ {
			asset, _, err := compactcatalog.UpsertAssetTx(context.Background(), tx, compactcatalog.Asset{Path: filepath.Join(root, "media", fmt.Sprintf("absent-%d.mkv", i)), Size: 11})
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(context.Background(), tx, target.EntityID, asset, compactcatalog.Link{Part: i}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	files := selectionDeleteTestCapture(t, s, target)
	if len(files) != 257 {
		t.Fatalf("captured %d files", len(files))
	}
	for i, file := range files {
		if i > 0 && file.AssetID <= files[i-1].AssetID {
			t.Fatal("not canonical asset key order")
		}
		if file.SourceID == "" || file.SourceIncarnation == "" || file.SourceGeneration == 0 || file.RootIdentity == "" {
			t.Fatalf("missing root/source proof %+v", file)
		}
	}
	var links int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=?`, target.EntityID).Scan(&links); err != nil || links != 257 {
		t.Fatalf("read cursor changed links: %d %v", links, err)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSelectionDeleteCursorReferenceFanoutResumes(t *testing.T) {
	s, db, _, item, _ := deletionFixture(t)
	target := selectionDeleteTestTarget(t, db, item)
	var token string
	if err := db.QueryRow(`SELECT token FROM catalog_assets ORDER BY id LIMIT 1`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 35; i++ {
		_, err := db.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES(?, 'session','a','p',?,?,1,'playing',?,'token','2099-01-01',?,100)`, fmt.Sprint(i), target.EntityID, token, fmt.Sprint(i), fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	files := selectionDeleteTestCapture(t, s, target)
	if len(files) != 2 || files[0].Shared {
		t.Fatalf("own reference fanout changed sharing %+v", files)
	}
	var effects []selectionactions.Effect
	err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), target, &files[0], selectionDeleteTestAllow, selectionDeleteTestCheckpoint(t, &effects))
	var changed *SelectionDeleteError
	if !errors.As(err, &changed) || changed.Code != "file_changed" || len(effects) != 0 {
		t.Fatalf("dense live proof must conservatively refuse: %v %+v", err, effects)
	}
	if _, err = os.Stat(files[0].Path); err != nil {
		t.Fatal("unprovable file touched", err)
	}
}

func TestSelectionDeleteApplyFilesThenFinalTarget(t *testing.T) {
	s, db, _, item, paths := deletionFixture(t)
	target := selectionDeleteTestTarget(t, db, item)
	files := selectionDeleteTestCapture(t, s, target)
	var effects []selectionactions.Effect
	calls := 0
	auth := func(ctx context.Context, tx *sql.Tx, p identity.Principal, a, library string, target *selectionactions.Target) error {
		calls++
		if a != "delete" || library != "lib-1" || target == nil {
			t.Fatal("incorrect authority seam")
		}
		return nil
	}
	checkpoint := selectionDeleteTestCheckpoint(t, &effects)
	for i := range files {
		files[i].Started = true
		if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), target, &files[i], auth, checkpoint); err != nil {
			t.Fatal(err)
		}
		var exists int
		if err := db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE id=?`, target.EntityID).Scan(&exists); err != nil || exists != 1 {
			t.Fatal("target settled before final command", exists, err)
		}
	}
	if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), target, nil, auth, checkpoint); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE id=?`, target.EntityID).Scan(&count); err != nil || count != 0 {
		t.Fatal("target not removed", count, err)
	}
	if calls != 5 || len(effects) != 3 || effects[0].Files+effects[1].Files != 2 || effects[0].Bytes+effects[1].Bytes != 22 {
		t.Fatalf("calls=%d effects=%+v", calls, effects)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("file remains", err)
		}
	}
}

func TestSelectionDeleteLiveAuthorityBeforeEveryFile(t *testing.T) {
	s, db, _, item, _ := deletionFixture(t)
	target := selectionDeleteTestTarget(t, db, item)
	files := selectionDeleteTestCapture(t, s, target)
	var effects []selectionactions.Effect
	calls := 0
	auth := func(context.Context, *sql.Tx, identity.Principal, string, string, *selectionactions.Target) error {
		calls++
		if calls >= 3 {
			return ErrDenied
		}
		return nil
	}
	checkpoint := selectionDeleteTestCheckpoint(t, &effects)
	if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), target, &files[0], auth, checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), target, &files[1], auth, checkpoint); !errors.Is(err, ErrDenied) {
		t.Fatalf("revoked file: %v", err)
	}
	if _, err := os.Stat(files[1].Path); err != nil || len(effects) != 1 {
		t.Fatalf("revoked unlink had effects %+v %v", effects, err)
	}
}

func TestSelectionDeleteLiveDriftPreventsUnlink(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{"stat", "source", "new-owner", "lifetime", "policy", "link", "root"} {
		t.Run(change, func(t *testing.T) {
			s, db, root, item, _ := deletionFixture(t)
			target := selectionDeleteTestTarget(t, db, item)
			files := selectionDeleteTestCapture(t, s, target)
			file := files[0]
			var err error
			switch change {
			case "stat":
				err = os.WriteFile(file.Path, []byte("replacement bytes with different size"), 0600)
			case "source":
				_, err = db.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, file.SourceID)
			case "new-owner":
				other := selectionDeleteTestOther(t, db, target.LibraryID)
				err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
					return compactcatalog.LinkAssetTx(context.Background(), tx, other, file.AssetID, compactcatalog.Link{})
				})
			case "lifetime":
				_, err = db.Exec(`UPDATE catalog_entities SET added_ms=coalesce(added_ms,0)+1 WHERE id=?`, target.EntityID)
			case "policy":
				_, err = db.Exec(`UPDATE admin_documents SET body='{"allowMediaDeletion":false}' WHERE scope=?`, libraryScope(target.LibraryID))
			case "link":
				_, err = db.Exec(`UPDATE catalog_asset_links SET edition='new edition' WHERE entity_id=? AND asset_id=?`, target.EntityID, file.AssetID)
			case "root":
				err = os.Rename(file.Root, filepath.Join(root, "old-media"))
				if err == nil {
					err = os.Mkdir(file.Root, 0700)
				}
				if err == nil {
					info, e := os.Stat(filepath.Join(root, "old-media", filepath.Base(file.Path)))
					err = e
					if err == nil {
						err = os.WriteFile(file.Path, []byte("00123456789"), 0600)
						if err == nil {
							err = os.Chtimes(file.Path, info.ModTime(), info.ModTime())
						}
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			var effects []selectionactions.Effect
			err = s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), target, &file, selectionDeleteTestAllow, selectionDeleteTestCheckpoint(t, &effects))
			if err == nil || len(effects) != 0 {
				t.Fatalf("drift authorized: err=%v effects=%+v", err, effects)
			}
			if _, err = os.Stat(file.Path); err != nil {
				t.Fatalf("drift file removed: %v", err)
			}
		})
	}
}

func TestSelectionDeleteCrashReplayUndercountsMissingFile(t *testing.T) {
	s, db, root, item, _ := deletionFixture(t)
	target := selectionDeleteTestTarget(t, db, item)
	file := selectionDeleteTestCapture(t, s, target)[0]
	// Simulate process loss after unlink, before catalogue/journal checkpoint.
	if err := os.Remove(file.Path); err != nil {
		t.Fatal(err)
	}
	file.Started = true
	// A new service and real database reopen resume only concrete file facts.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	s = NewAt(reopened, root)
	var effects []selectionactions.Effect
	if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), target, &file, selectionDeleteTestAllow, selectionDeleteTestCheckpoint(t, &effects)); err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Files != 0 || effects[0].Bytes != 0 {
		t.Fatalf("invented attribution %+v", effects)
	}
}

func TestSelectionDeleteFinalRejectsNewLink(t *testing.T) {
	s, db, root, item, _ := deletionFixture(t)
	target := selectionDeleteTestTarget(t, db, item)
	files := selectionDeleteTestCapture(t, s, target)
	var effects []selectionactions.Effect
	checkpoint := selectionDeleteTestCheckpoint(t, &effects)
	for i := range files {
		if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(false), target, &files[i], selectionDeleteTestAllow, checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		asset, _, err := compactcatalog.UpsertAssetTx(context.Background(), tx, compactcatalog.Asset{Path: filepath.Join(root, "media", "new.mkv"), Size: 100})
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(context.Background(), tx, target.EntityID, asset, compactcatalog.Link{})
	}); err != nil {
		t.Fatal(err)
	}
	err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(false), target, nil, selectionDeleteTestAllow, checkpoint)
	if !errors.Is(err, ErrConflict) || len(effects) != 2 {
		t.Fatalf("new version inherited old final command: %v %+v", err, effects)
	}
}

func TestSelectionDeleteKeepsOriginallySharedFileForBothOwners(t *testing.T) {
	s, db, _, item, _ := deletionFixture(t)
	first := selectionDeleteTestTarget(t, db, item)
	other := selectionDeleteTestOther(t, db, first.LibraryID)
	var public string
	if err := db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, other).Scan(&public); err != nil {
		t.Fatal(err)
	}
	var asset int64
	if err := db.QueryRow(`SELECT asset_id FROM catalog_asset_links WHERE entity_id=? ORDER BY asset_id LIMIT 1`, first.EntityID).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(context.Background(), tx, other, asset, compactcatalog.Link{})
	}); err != nil {
		t.Fatal(err)
	}
	second := selectionDeleteTestTarget(t, db, public)
	a := selectionDeleteTestCapture(t, s, first)[0]
	b := selectionDeleteTestCapture(t, s, second)[0]
	if !a.Shared || !b.Shared {
		t.Fatal("shared file not classified per target")
	}
	var effects []selectionactions.Effect
	checkpoint := selectionDeleteTestCheckpoint(t, &effects)
	for _, command := range []struct {
		target selectionactions.Target
		file   selectionactions.File
	}{{first, a}, {second, b}} {
		if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), command.target, &command.file, selectionDeleteTestAllow, checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(a.Path); err != nil {
		t.Fatal("both-selected shared file deleted", err)
	}
	for _, effect := range effects {
		if effect.Files != 0 || effect.Bytes != 0 {
			t.Fatalf("shared effects %+v", effects)
		}
	}
}

func TestSelectionDeleteCursorOutsideReferences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, table := range []string{"catalog_asset_links", "item_extras", "playback_sessions", "lyric_resources", "lyric_candidates", "subtitle_resources", "dvr_catalog_provenance"} {
		t.Run(table, func(t *testing.T) {
			s, db, _, item, _ := deletionFixture(t)
			target := selectionDeleteTestTarget(t, db, item)
			other := selectionDeleteTestOther(t, db, target.LibraryID)
			var asset int64
			var token string
			if err := db.QueryRow(`SELECT a.id,a.token FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? ORDER BY l.asset_id LIMIT 1`, target.EntityID).Scan(&asset, &token); err != nil {
				t.Fatal(err)
			}
			var err error
			switch table {
			case "catalog_asset_links":
				err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
					return compactcatalog.LinkAssetTx(context.Background(), tx, other, asset, compactcatalog.Link{})
				})
			case "item_extras":
				_, err = db.Exec(`INSERT INTO item_extras(item_id,library_id,parent_path,kind,title,asset_id,relative_path) VALUES(?,'lib-1','/','trailer','Other',?,'other')`, other, token)
			case "playback_sessions":
				_, err = db.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('outside','session','a','p',?,?,1,'playing','outside','token','2099-01-01','outside',100)`, other, token)
			case "lyric_resources":
				_, err = db.Exec(`INSERT INTO lyric_resources(id,item_id,asset_id,source_version,scope,authority,account_id,profile_id,revision) VALUES('outside',?,?,'v','library','owner','a','p',1)`, other, token)
			case "lyric_candidates":
				_, err = db.Exec(`INSERT INTO lyric_candidates(id,item_id,asset_id,source_version,actor,target_id,expected_revision,document,provenance,language,expires_at,provider_revision) VALUES('outside',?,?,'v','owner','target',1,'{}','{}','en','2099-01-01',1)`, other, token)
			case "subtitle_resources":
				_, err = db.Exec(`INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision) VALUES('outside',?,?,'shared','',1)`, other, token)
			case "dvr_catalog_provenance":
				_, err = db.Exec(`INSERT INTO dvr_catalog_provenance(item_id,recording_id,asset_id,source_id,channel_id,programme_id,guide_generation,artifact_digest,captured_start_ms,captured_end_ms,state) VALUES(?,'outside',?,'source','channel','programme','guide','digest',0,1,'completed')`, other, token)
			}
			if err != nil {
				t.Fatal(err)
			}
			files := selectionDeleteTestCapture(t, s, target)
			if len(files) != 2 || !files[0].Shared {
				t.Fatalf("outside %s reference lost: %+v", table, files)
			}
			var effects []selectionactions.Effect
			if err = s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), target, &files[0], selectionDeleteTestAllow, selectionDeleteTestCheckpoint(t, &effects)); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(files[0].Path); err != nil || len(effects) != 1 || effects[0].Files != 0 {
				t.Fatalf("outside shared file touched: %+v %v", effects, err)
			}
		})
	}
}

func TestSelectionDeleteObservedEffectSurvivesPostUnlinkRevocation(t *testing.T) {
	s, db, _, item, _ := deletionFixture(t)
	target := selectionDeleteTestTarget(t, db, item)
	file := selectionDeleteTestCapture(t, s, target)[0]
	calls := 0
	auth := func(context.Context, *sql.Tx, identity.Principal, string, string, *selectionactions.Target) error {
		calls++
		if calls == 2 {
			return ErrDenied
		}
		return nil
	}
	var effects []selectionactions.Effect
	err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), target, &file, auth, selectionDeleteTestCheckpoint(t, &effects))
	if !errors.Is(err, ErrDenied) || len(effects) != 1 || effects[0].Bytes != file.Size || effects[0].Files != 1 || effects[0].Code != "authority_revoked" {
		t.Fatalf("observed fact lost: %+v %v", effects, err)
	}
	var linked, available int
	if err = db.QueryRow(`SELECT count(*),coalesce(max(available),-1) FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, target.EntityID, file.AssetID).Scan(&linked, &available); err != nil || linked != 1 || available != 0 {
		t.Fatalf("revocation must preserve link but reconcile disk absence: %d/%d %v", linked, available, err)
	}
	var checkpoints int
	if err = db.QueryRow(`SELECT count(*) FROM admin_documents WHERE scope LIKE 'selection-test-checkpoint-%'`).Scan(&checkpoints); err != nil || checkpoints != 1 {
		t.Fatalf("observed checkpoint not committed: %d %v", checkpoints, err)
	}
}

func TestSelectionDeleteCheckpointFailureReplayDoesNotInventBytes(t *testing.T) {
	s, db, _, item, _ := deletionFixture(t)
	target := selectionDeleteTestTarget(t, db, item)
	file := selectionDeleteTestCapture(t, s, target)[0]
	file.Started = true
	failure := errors.New("checkpoint unavailable")
	err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), target, &file, selectionDeleteTestAllow, func(tx *sql.Tx, effect selectionactions.Effect) error {
		if effect.Files != 1 {
			t.Fatal("unlink not observed")
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("checkpoint failure: %v", err)
	}
	var linked int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, target.EntityID, file.AssetID).Scan(&linked); err != nil || linked != 1 {
		t.Fatal("failed checkpoint committed link deletion", linked, err)
	}
	var effects []selectionactions.Effect
	if err = s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(true), target, &file, selectionDeleteTestAllow, selectionDeleteTestCheckpoint(t, &effects)); err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Files != 0 || effects[0].Bytes != 0 {
		t.Fatalf("replayed ambiguous unlink claimed bytes %+v", effects)
	}
}

func TestSelectionDeleteCursorRejectsUnsafeFactsAndCancelledStep(t *testing.T) {
	for _, scenario := range []string{"outside-root", "mismatched-stat", "oversized-scalar", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			s, db, root, item, paths := deletionFixture(t)
			target := selectionDeleteTestTarget(t, db, item)
			ctx := context.Background()
			switch scenario {
			case "outside-root":
				if _, err := db.Exec(`UPDATE library_sources SET root=? WHERE library_id='lib-1'`, filepath.Join(root, "other")); err != nil {
					t.Fatal(err)
				}
			case "mismatched-stat":
				if err := os.WriteFile(paths[0], []byte("replacement"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE catalog_assets SET modified_ns=0 WHERE path=?`, paths[0]); err != nil {
					t.Fatal(err)
				}
			case "oversized-scalar":
				if _, err := db.Exec(`UPDATE catalog_assets SET path=CAST(zeroblob(65537) AS TEXT) WHERE path=?`, paths[0]); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			cursor := NewSelectionDeleteCursor(target)
			for i := 0; i < 100; i++ {
				next, records, done, err := s.StepSelectionDelete(ctx, cursor)
				if err != nil {
					if next != nil || len(records) != 0 || done {
						t.Fatal("error exposed partial facts")
					}
					if scenario == "oversized-scalar" && !errors.Is(err, selectionactions.ErrCapacity) {
						t.Fatalf("capacity error: %v", err)
					}
					if scenario == "cancelled" && !errors.Is(err, context.Canceled) {
						t.Fatalf("cancelled step: %v", err)
					}
					return
				}
				if done {
					t.Fatal("unsafe facts became ready")
				}
				cursor = next
			}
			t.Fatal("unsafe cursor never refused")
		})
	}
}

func TestSelectionDeleteKeepsDiskWhenDeleteFilesFalse(t *testing.T) {
	s, db, _, item, paths := deletionFixture(t)
	target := selectionDeleteTestTarget(t, db, item)
	files := selectionDeleteTestCapture(t, s, target)
	if _, err := db.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,rating,revision,watched,last_played_at,not_interested) VALUES('p1',?,0,1,9,1,1,'2026-10-01T00:00:00Z',0)`, target.EntityID); err != nil {
		t.Fatal(err)
	}
	var effects []selectionactions.Effect
	checkpoint := selectionDeleteTestCheckpoint(t, &effects)
	for i := range files {
		if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(false), target, &files[i], selectionDeleteTestAllow, checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(false), target, nil, selectionDeleteTestAllow, checkpoint); err != nil {
		t.Fatal(err)
	}
	for _, effect := range effects {
		if effect.Bytes != 0 || effect.Files != 0 {
			t.Fatalf("invented disk effect %+v", effects)
		}
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("preserved file removed", err)
		}
	}
	var favorite bool
	var rating float64
	if err := db.QueryRow(`SELECT favorite,rating FROM retained_title_state WHERE profile_id='p1'`).Scan(&favorite, &rating); err != nil || !favorite || rating != 9 {
		t.Fatalf("profile state not retained: %v %v %v", favorite, rating, err)
	}
}

func TestSelectionDeleteFinalCheckpointAuthorizesExistingTarget(t *testing.T) {
	s, db, _, item, _ := deletionFixture(t)
	target := selectionDeleteTestTarget(t, db, item)
	files := selectionDeleteTestCapture(t, s, target)
	authorize := func(ctx context.Context, tx *sql.Tx, p identity.Principal, action, library string, target *selectionactions.Target) error {
		var visible bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE id=? AND public_id=pid_blob(?))`, target.EntityID, target.ItemID).Scan(&visible); err != nil {
			return err
		}
		if !visible {
			return ErrDenied
		}
		return nil
	}
	var effects []selectionactions.Effect
	record := selectionDeleteTestCheckpoint(t, &effects)
	checkpoint := func(tx *sql.Tx, effect selectionactions.Effect) error {
		// Match the real journal checkpoint's live-target authorization, which must
		// see the target before the atomic final catalogue removal.
		if err := authorize(context.Background(), tx, identity.Principal{}, "delete", target.LibraryID, &target); err != nil {
			return err
		}
		return record(tx, effect)
	}
	for i := range files {
		if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(false), target, &files[i], authorize, checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(false), target, nil, authorize, checkpoint); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE id=?`, target.EntityID).Scan(&remaining); err != nil || remaining != 0 || len(effects) != 3 {
		t.Fatalf("final checkpoint/removal not committed together: target=%d effects=%+v err=%v", remaining, effects, err)
	}
}

func TestSelectionDeleteFinalCheckpointRollbackKeepsTarget(t *testing.T) {
	s, db, _, item, _ := deletionFixture(t)
	target := selectionDeleteTestTarget(t, db, item)
	files := selectionDeleteTestCapture(t, s, target)
	var effects []selectionactions.Effect
	checkpoint := selectionDeleteTestCheckpoint(t, &effects)
	for i := range files {
		if err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(false), target, &files[i], selectionDeleteTestAllow, checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	failure := errors.New("final journal checkpoint unavailable")
	err := s.ApplySelectionDelete(context.Background(), identity.Principal{}, "delete", selectionDeleteTestOptions(false), target, nil, selectionDeleteTestAllow, func(tx *sql.Tx, _ selectionactions.Effect) error {
		if _, err := tx.Exec(`INSERT INTO admin_documents(scope,revision,body,updated_ms) VALUES('selection-final-rollback',1,'{}',0)`); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("final checkpoint error: %v", err)
	}
	var remaining, receipts int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM catalog_entities WHERE id=?),(SELECT count(*) FROM admin_documents WHERE scope='selection-final-rollback')`, target.EntityID).Scan(&remaining, &receipts); err != nil || remaining != 1 || receipts != 0 {
		t.Fatalf("failed checkpoint changed target/receipt: %d/%d %v", remaining, receipts, err)
	}
}
