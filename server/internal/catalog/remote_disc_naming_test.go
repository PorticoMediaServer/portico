package catalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// namingRemote is the source producer stub for the naming-only publication: it
// records the reference the commit writes and refuses every byte-level call, so a
// test fails loudly if naming ever reaches for the image.
type namingRemote struct {
	references   []string
	wantRevision string
}

func (r *namingRemote) Handles(path string) bool { return strings.HasPrefix(path, "/remote") }
func (r *namingRemote) InspectRoot(context.Context, string) (storage.Snapshot, error) {
	return storage.Snapshot{}, storage.ErrRemoteConfig
}
func (r *namingRemote) Stat(context.Context, string) (storage.Snapshot, error) {
	return storage.Snapshot{}, storage.ErrRemoteConfig
}
func (r *namingRemote) ListPage(context.Context, string, string, string) (storage.RemotePage, error) {
	return storage.RemotePage{}, storage.ErrRemoteConfig
}
func (r *namingRemote) CommitPage(context.Context, *sql.Tx, storage.RemotePage) error {
	return storage.ErrRemoteConfig
}
func (r *namingRemote) CheckScan(context.Context, string, string) error {
	return storage.ErrRemoteConfig
}
func (r *namingRemote) ValidateScan(context.Context, *sql.Tx, string, string) error {
	return storage.ErrRemoteConfig
}
func (r *namingRemote) RecordReference(_ context.Context, _ *sql.Tx, path, revision string) error {
	// The caller must forward the original observed revision, never an empty proof.
	if revision == "" || revision != r.wantRevision {
		return storage.ErrRemoteChanged
	}
	r.references = append(r.references, path)
	return nil
}
func (r *namingRemote) Acquire(context.Context, string, string) (storage.RemoteObject, error) {
	return nil, storage.ErrRemoteConfig
}

func (f namingFixture) observed() assets.Facts {
	return assets.Facts{ObservedSize: 734003200, ObservedModifiedNS: 1700000000000000000, ObservedRevision: f.remote.wantRevision}
}

type namingFixture struct {
	service  *Service
	db       *sql.DB
	remote   *namingRemote
	library  string
	source   string
	job      string
	object   string
	revision string
	policy   int64
}

// namingFixture builds one running remote scan with one inventoried disc image
// and its queued analysis row, without any inventory page walk.
func newNamingFixture(t *testing.T, kind, relative string) namingFixture {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := New(db)
	local := filepath.Join(root, "local")
	if err = os.Mkdir(local, 0700); err != nil {
		t.Fatal(err)
	}
	library, err := service.Create("Titles", kind, local)
	if err != nil {
		t.Fatal(err)
	}
	remote := &namingRemote{}
	service.storage = &storage.Client{Remote: remote}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = db.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation,state) VALUES('remote','webdav','Remote','/remote',1,'ready')`); err != nil {
		t.Fatal(err)
	}
	sourceID := library.ID + "-remote"
	if _, err = db.Exec(`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root,classification,generation,incarnation,root_identity,identity_confirmed,enabled,remote_source_id) VALUES(?,?,'remote','Remote','/remote','/remote','network',1,'incarnation','identity',1,1,'remote')`, sourceID, library.ID); err != nil {
		t.Fatal(err)
	}
	var policy int64
	if err = db.QueryRow(`SELECT revision FROM library_scan_policies WHERE library_id=?`, library.ID).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	job := "naming-job"
	if _, err = db.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running',?)`, job, library.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode) VALUES(?,?,1,'incarnation','identity',?,'analysis','incremental')`, job, sourceID, policy); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO remote_scan_generations(job_id,source_id,generation) VALUES(?,'remote','1')`, job); err != nil {
		t.Fatal(err)
	}
	asset := "naming-asset"
	if _, err = db.Exec(`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available) VALUES(?,?,734003200,1700000000000000000,'iso','','',0,0,0,1)`, asset, filepath.Join("/remote", relative)); err != nil {
		t.Fatal(err)
	}
	object, revision := "naming-object", "revision-1"
	// The provider observation and catalogue revision are distinct witnesses.
	remote.wantRevision = "1:original-provider-observation"
	if _, err = db.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,unsupported_reason,directory_path) VALUES(?,?,?,'incarnation',?,'identity',?,'{}',734003200,1700000000000000000,?,'',?)`, object, sourceID, asset, relative, revision, job, filepath.Dir(relative)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision) VALUES(?,?,?,?)`, job, object, revision, policy); err != nil {
		t.Fatal(err)
	}
	return namingFixture{service: service, db: db, remote: remote, library: library.ID, source: sourceID, job: job, object: object, revision: revision, policy: policy}
}

// associateNamingPositive reproduces the original discovery association before
// the already-queued naming phase. Refusal fixtures remain raw and unlinked.
func associateNamingPositive(t *testing.T, f namingFixture) {
	t.Helper()
	writer, err := dbwork.Begin(context.Background(), f.db, dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	ctx, tx := writer.Context(), writer.Tx()
	source, err := InventoryFenceTx(ctx, tx, f.job)
	if err != nil {
		t.Fatal(err)
	}
	if source.ID != f.source || source.LibraryID != f.library {
		t.Fatal("discovery association changed the original source", source)
	}
	var asset, relative string
	if err = tx.QueryRowContext(ctx, `SELECT asset_id,relative_path FROM inventory_objects WHERE id=? AND source_id=? AND revision=? AND root_incarnation=? AND retired=0 AND asset_id='naming-asset'`, f.object, source.ID, f.revision, source.Incarnation).Scan(&asset, &relative); err != nil {
		t.Fatal(err)
	}
	if err = f.service.associateInventoryTx(ctx, tx, source, asset, filepath.Join(source.ResolvedPath, relative)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Root64: discovery associates the remote disc image with its named title;
// naming completes with technical work deferred and no byte of the image read.
func TestCommitInventoryNamingPublishesRemoteDiscImageFromItsName(t *testing.T) {
	f := newNamingFixture(t, "movie", "Heat (1995).iso")
	associateNamingPositive(t, f)
	if err := f.service.CommitInventoryNaming(context.Background(), f.job, f.object, f.revision, f.policy, f.observed()); err != nil {
		t.Fatal(err)
	}
	var title, kind string
	var year int
	if err := f.db.QueryRow(`SELECT e.title,k.name,e.year FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE a.token='naming-asset'`).Scan(&title, &kind, &year); err != nil {
		t.Fatalf("the disc image did not become a titled item: %v", err)
	}
	if title != "Heat" || kind != "movie" || year != 1995 {
		t.Fatalf("published %q %q %d, want the file name's title", title, kind, year)
	}
	var state, analysisRevision string
	var queue string
	if err := f.db.QueryRow(`SELECT analysis_state,analysis_revision FROM inventory_objects WHERE id=?`, f.object).Scan(&state, &analysisRevision); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT state FROM inventory_analysis_queue WHERE job_id=? AND object_id=?`, f.job, f.object).Scan(&queue); err != nil {
		t.Fatal(err)
	}
	if state != "deferred" || analysisRevision != f.revision || queue != "done" {
		t.Fatalf("naming recorded %q/%q/%q, want deferred technical analysis", state, analysisRevision, queue)
	}
	// No technical fact is invented for the title it published.
	for _, table := range []string{"asset_stream_facts", "asset_chapter_facts", "inventory_disc_facts"} {
		var rows int
		if err := f.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 0 {
			t.Fatalf("%s carries %d rows after naming-only publication", table, rows)
		}
	}
	var video, audio string
	var duration float64
	if err := f.db.QueryRow(`SELECT video_codec,audio_codec,duration FROM catalog_assets WHERE token='naming-asset'`).Scan(&video, &audio, &duration); err != nil {
		t.Fatal(err)
	}
	if video != "" || audio != "" || duration != 0 {
		t.Fatalf("naming wrote technical facts %q %q %v", video, audio, duration)
	}
	var issues int
	if err := f.db.QueryRow(`SELECT count(*) FROM inventory_scan_issues WHERE job_id=? AND reason='unsupported_remote_disc'`, f.job).Scan(&issues); err != nil {
		t.Fatal(err)
	}
	if issues != 0 {
		t.Fatal("the ordinary title still carries the bespoke remote-disc issue")
	}
	if len(f.remote.references) != 1 || f.remote.references[0] != "/remote/Heat (1995).iso" {
		t.Fatalf("recorded references %v, want the exact image path once", f.remote.references)
	}
}

// The library kind decides the association: a TV name supplies show and
// position; absent descriptive metadata, the fresh episode keeps Episode N.
func TestCommitInventoryNamingFollowsTheLibraryKind(t *testing.T) {
	f := newNamingFixture(t, "tv", "Show Name/Season 01/Show Name - s01e02 - Episode Title.iso")
	associateNamingPositive(t, f)
	if err := f.service.CommitInventoryNaming(context.Background(), f.job, f.object, f.revision, f.policy, f.observed()); err != nil {
		t.Fatal(err)
	}
	var show, episode, kind string
	var season, number int
	if err := f.db.QueryRow(`SELECT show.title,ep.number,sd.number,item.title,k.name FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id JOIN catalog_seasons sd ON sd.entity_id=ep.season_id AND sd.show_id=ep.show_id JOIN catalog_entities sea ON sea.id=sd.entity_id AND item.parent_id=sea.id JOIN catalog_entities show ON show.id=ep.show_id AND sea.parent_id=show.id JOIN catalog_kinds k ON k.id=item.kind JOIN catalog_asset_links link ON link.entity_id=item.id JOIN catalog_assets asset ON asset.id=link.asset_id JOIN catalog_libraries library ON library.id=item.library_id WHERE asset.token='naming-asset' AND library.library_id=? AND show.library_id=item.library_id AND sea.library_id=item.library_id AND k.name='episode'`, f.library).Scan(&show, &number, &season, &episode, &kind); err != nil {
		t.Fatalf("the TV disc image did not become an episode from its name: %v", err)
	}
	if show != "Show Name" || season != 1 || number != 2 || kind != "episode" || episode != "Episode 2" {
		t.Fatalf("published %q s%d e%d %q %q, want the named episode with its no-metadata title", show, season, number, kind, episode)
	}
}

// The naming-only commit refuses anything that is not a remote disc image in a
// video library, so it cannot become a general unprobed publication path.
func TestCommitInventoryNamingRefusesOtherObjects(t *testing.T) {
	for _, tc := range []struct{ name, kind, relative string }{
		{"local library", "movie", "Heat (1995).iso"},
		{"book library", "book", "Heat (1995).iso"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newNamingFixture(t, tc.kind, tc.relative)
			if tc.name == "local library" {
				if _, err := f.db.Exec(`UPDATE library_sources SET kind='local',remote_source_id=NULL WHERE id=?`, f.source); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.CommitInventoryNaming(context.Background(), f.job, f.object, f.revision, f.policy, f.observed()); err == nil {
				t.Fatal("naming-only publication accepted an object it must refuse")
			}
		})
	}
	t.Run("ordinary file", func(t *testing.T) {
		f := newNamingFixture(t, "movie", "Heat (1995).mkv")
		if err := f.service.CommitInventoryNaming(context.Background(), f.job, f.object, f.revision, f.policy, f.observed()); err == nil {
			t.Fatal("naming-only publication accepted a non-disc file")
		}
	})
}

// (3) The naming commit reproves the accepted queue invocation and the exact
// observation it read, so it can never publish an unqueued, changed or
// unavailable object.
func TestCommitInventoryNamingReprovesQueueAndObservation(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(t *testing.T, f namingFixture)
	}{
		{"missing queue row", func(t *testing.T, f namingFixture) {
			if _, err := f.db.Exec(`DELETE FROM inventory_analysis_queue WHERE job_id=? AND object_id=?`, f.job, f.object); err != nil {
				t.Fatal(err)
			}
		}},
		{"queue no longer pending", func(t *testing.T, f namingFixture) {
			if _, err := f.db.Exec(`UPDATE inventory_analysis_queue SET state='done' WHERE job_id=? AND object_id=?`, f.job, f.object); err != nil {
				t.Fatal(err)
			}
		}},
		{"changed observation", func(t *testing.T, f namingFixture) {}},
		{"unsupported reason", func(t *testing.T, f namingFixture) {
			if _, err := f.db.Exec(`UPDATE inventory_objects SET unsupported_reason='unsupported_remote_disc' WHERE id=?`, f.object); err != nil {
				t.Fatal(err)
			}
		}},
		{"unavailable asset", func(t *testing.T, f namingFixture) {
			if _, err := f.db.Exec(`UPDATE catalog_assets SET available=0 WHERE token='naming-asset'`); err != nil {
				t.Fatal(err)
			}
		}},
		{"asset names another path", func(t *testing.T, f namingFixture) {
			if _, err := f.db.Exec(`UPDATE catalog_assets SET path='/remote/Other.iso' WHERE token='naming-asset'`); err != nil {
				t.Fatal(err)
			}
		}},
		{"asset size disagrees with object", func(t *testing.T, f namingFixture) {
			if _, err := f.db.Exec(`UPDATE catalog_assets SET size=size-1 WHERE token='naming-asset'`); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newNamingFixture(t, "movie", "Heat (1995).iso")
			tc.break_(t, f)
			observed := f.observed()
			if tc.name == "changed observation" {
				observed.ObservedSize--
			}
			if err := f.service.CommitInventoryNaming(context.Background(), f.job, f.object, f.revision, f.policy, observed); err == nil {
				t.Fatal("naming-only publication accepted an unqueued, changed or unavailable object")
			}
			var linked int
			if err := f.db.QueryRow(`SELECT count(*) FROM catalog_asset_links`).Scan(&linked); err != nil {
				t.Fatal(err)
			}
			if linked != 0 {
				t.Fatal("a refused naming publication still linked a title")
			}
		})
	}
}

func (r *namingRemote) ListInventoryPage(ctx context.Context, job, directory, after string, limit int) (storage.RemotePage, error) {
	return r.ListPage(ctx, job, directory, after)
}
