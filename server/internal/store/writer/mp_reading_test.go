package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"strings"
	"testing"
)

func mpReadingIntentFixture(t *testing.T) (*executor, *mpReadingPublication) {
	t.Helper()
	w := wc008NativeExecutor(t)
	exec := func(q string, args ...any) { wc008NativeExec(t, w, q, args...) }
	exec(`INSERT INTO libraries(id,name,kind,root) VALUES('library-one','Books','book','/fixture')`)
	exec(`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(20,'ebook',0,0,1,1,0)`)
	public := []byte{3, 128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	exec(`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'library-one','/fixture')`)
	exec(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2111,?,7,20)`, public)
	exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(2111,?,7,20,'Book','book','b','book','b',0,?,?)`, public, public, public)
	exec(`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity) VALUES('reading-source','library-one','/fixture','/fixture','inc-reading','dev:1:ino:1')`)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(201,'book-asset','/fixture/book.epub',1000,1,'','','',0,0,0,7)`)
	exec(`INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(2111,201,7)`)
	exec(`INSERT INTO reading_publications(item_id,asset_id,format) VALUES(2111,201,'epub')`)
	evidence := mediasource.InventoryEvidence{Kind: "local", Scope: "reading-source:inc-reading", Object: "ino:5", Size: 1000, ModifiedNS: 1, ChangeToken: "fp1:ino:5:size:1000:mtime:1"}
	rev, err := evidence.Revision()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(evidence)
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns) VALUES('reading-object','reading-source','book-asset','inc-reading','book.epub','ino:5',?,?,1000,1)`, rev, string(raw))
	v := read.MPReadingSourceValues{Item: 2111, ItemKind: 20, Asset: 201, Library: 7, LibraryName: "library-one", LibraryKind: "book", PublicID: []byte{3, 128, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, Format: "epub", Token: "book-asset", Size: 1000, ModifiedNS: 1, LinkAvailable: 1, AssetAvailable: 1, AssetLibrary: 7, ObjectID: "reading-object", ObjectAssetID: "book-asset", ObjectSourceID: "reading-source", ObjectRootIncarnation: "inc-reading", RelativePath: "book.epub", ObjectIdentity: "ino:5", InventoryRevision: rev, Evidence: string(raw), ObjectSize: 1000, ObjectModifiedNS: 1, ObjectState: "available", SourceID: "reading-source", SourceLibraryID: "library-one", SourceKind: "local", SourceGeneration: 1, SourceIncarnation: "inc-reading", RootIdentity: "dev:1:ino:1", SourceEnabled: 1, SourceHealth: "ready"}
	p, err := holdMPReadingPublication(t.Context(), v, false, []string{"chapter.xhtml"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return w, p
}
func mpReadingInput(p *mpReadingPublication) mpReadingIntentInput {
	return mpReadingIntentInput{publication: p, profile: "profile-one", action: "location", location: mpReadingLocation{Format: "epub", CFI: "epubcfi(/6/2)", Section: "chapter.xhtml", Progression: .4}}
}
func TestMPReadingIntentSourceCASPausedAndMetadata(t *testing.T) {
	for _, cell := range []string{"fresh", "paused", "cas", "metadata", "source-generation", "physical", "evidence", "clear", "reread", "copied", "revoked"} {
		t.Run(cell, func(t *testing.T) {
			w, p := mpReadingIntentFixture(t)
			q := mpReadingInput(p)
			purpose := CommandID(770)
			switch cell {
			case "paused":
				q.historyPaused = true
			case "cas":
				q.expectedReadingRevision = 1
			case "metadata":
				q.action = "reread"
				q.expectedMetadataRevision = 9
				purpose = 771
			case "source-generation":
				wc008NativeExec(t, w, `UPDATE library_sources SET generation=2 WHERE id='reading-source'`)
			case "physical":
				wc008NativeExec(t, w, `UPDATE library_sources SET root_identity='different-root' WHERE id='reading-source'`)
			case "evidence":
				wc008NativeExec(t, w, `UPDATE inventory_objects SET evidence_json='{}' WHERE id='reading-object'`)
			case "clear":
				q.action = "clear"
				purpose = 771
			case "reread":
				q.action = "reread"
				q.expectedMetadataRevision = 1
				purpose = 771
			case "copied":
				copy := *p
				q.publication = &copy
			case "revoked":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				p.life = ctx
			}
			turn := wc008NativeTurn(t, w, purpose, mpReadingIntentDefinitions())
			got, err := mpReadingIntent(t.Context(), turn, q)
			bad := cell == "cas" || cell == "metadata" || cell == "evidence" || cell == "copied" || cell == "revoked"
			if bad {
				if err == nil || got != (mpReadingIntentValue{}) {
					t.Fatal(got, err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if cell == "paused" {
				if got.Changed || got.Reason != "history_paused" {
					t.Fatal(got)
				}
			} else if cell == "source-generation" || cell == "physical" {
				if !got.Stale || got.Changed || got.Reason != "source_changed" {
					t.Fatal(got)
				}
			} else if !got.Changed || got.SelectedBytes <= 0 || got.Source.Revision() != p.source.Revision() {
				t.Fatal(got)
			}
			_, _ = turn.transaction.rollback(nil)
			var count int
			if err = w.db.QueryRow(`SELECT count(*) FROM personal_items`).Scan(&count); err != nil || count != 0 {
				t.Fatal("MP wrote personal state", count, err)
			}
		})
	}
}
func TestMPReadingIntentLocationBounds(t *testing.T) {
	_, p := mpReadingIntentFixture(t)
	for _, l := range []mpReadingLocation{{Format: "epub", CFI: "epubcfi(/6/2)", Section: "wrong"}, {Format: "epub", CFI: "epubcfi(/6/2)", Page: sql.NullInt64{Valid: true}}, {Format: "epub", CFI: "bad"}, {Format: "epub", CFI: "epubcfi(/6/2)", Progression: 2}} {
		if validateMPReadingLocation(p, l) == nil {
			t.Fatal(l)
		}
	}
	p.source.PresentationFormat = "pdf"
	p.pages = 2
	if validateMPReadingLocation(p, mpReadingLocation{Format: "pdf", Page: sql.NullInt64{Int64: 1, Valid: true}}) != nil {
		t.Fatal("valid PDF")
	}
	if validateMPReadingLocation(p, mpReadingLocation{Format: "pdf", Page: sql.NullInt64{Int64: 2, Valid: true}}) == nil {
		t.Fatal("out of range PDF")
	}
	p.source.PresentationFormat = "cbz"
	p.pages = 0
	if validateMPReadingLocation(p, mpReadingLocation{Format: "cbz", Page: sql.NullInt64{Valid: true}}) == nil {
		t.Fatal("comic unknown count")
	}
}
func TestMPReadingTypedOriginalSurvivesAssetDeletion(t *testing.T) {
	w, p := mpReadingIntentFixture(t)
	key := "p251_0000000000001_" + strings.Repeat("a", 32)
	wc008NativeExec(t, w, `INSERT INTO user_mutation_accounts(id,authority,account_id,charged_stop) VALUES(1,'local','account',0)`)
	wc008NativeExec(t, w, `INSERT INTO user_mutation_receipts(key,scope_id,profile_id,family_id,operation,digest,is_stop,issued_ms,status) VALUES(?,1,'profile-one','family','reading',?,0,1,'applied')`, key, strings.Repeat("a", 64))
	wc008NativeExec(t, w, `INSERT INTO user_mutation_reading_results(receipt_key,command_kind,result_version,item_public_id,asset_id,source_token,source_revision,format,cfi,section,progression,reading_revision,personal_revision,observed_ms,accepted,stale,cleared,reason) VALUES(?,770,1,?,201,'book-asset',?,'epub','epubcfi(/6/2)','chapter.xhtml',0.4,2,3,100,1,0,0,'')`, key, p.source.PublicID[:], p.source.Revision())
	turn := wc008NativeTurn(t, w, 770, mpReadingIntentDefinitions())
	before, err := readMPReadingOriginal(t.Context(), turn, key, 770)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = turn.transaction.rollback(nil)
	wc008NativeExec(t, w, `DELETE FROM inventory_objects WHERE id='reading-object'`)
	wc008NativeExec(t, w, `DELETE FROM catalog_asset_links WHERE asset_id=201`)
	wc008NativeExec(t, w, `DELETE FROM reading_publications WHERE asset_id=201`)
	wc008NativeExec(t, w, `DELETE FROM catalog_assets WHERE id=201`)
	turn = wc008NativeTurn(t, w, 770, mpReadingIntentDefinitions())
	after, err := readMPReadingOriginal(t.Context(), turn, key, 770)
	if err != nil || after != before || after.ReadingRevision != 2 || after.Progression.Float64 != .4 {
		t.Fatal(after, err)
	}
	if bad, err := readMPReadingOriginal(t.Context(), turn, key, 771); !errors.Is(err, ErrResolveRequired) || bad != (mpReadingOriginal{}) {
		t.Fatal(bad, err)
	}
}
func TestMPReadingDefinitionsFullF(t *testing.T) {
	w, _ := mpReadingIntentFixture(t)
	wc008NativePrepare(t, w, mpReadingIntentDefinitions())
}

func TestMPReadingPerSourceStalePositionsShareRevision(t *testing.T) {
	w, p := mpReadingIntentFixture(t)
	wc008NativeExec(t, w, `INSERT INTO personal_items(id,profile_id,item_public_id,item_id,library_id,reading_revision) VALUES(1,'profile-one',?,2111,7,4)`, p.source.PublicID[:])
	for _, asset := range []int64{201, 202} {
		wc008NativeExec(t, w, `INSERT INTO personal_reading_resumes(personal_item_id,asset_id,source_token,source_revision,format,cfi,progression,revision,observed_ms) VALUES(1,?,'old-asset',?,'epub','epubcfi(/6/2)',0.4,3,100)`, asset, strings.Repeat("a", 64))
	}
	turn := wc008NativeTurn(t, w, 770, mpReadingIntentDefinitions())
	q := mpReadingInput(p)
	q.expectedReadingRevision = 4
	got, err := mpReadingIntent(t.Context(), turn, q)
	if err != nil || !got.SavedStale || !got.Saved.Present || got.Saved.ChildRevision != 3 || got.ReadingRevision != 4 {
		t.Fatal(got, err)
	}
	other, err := readMPReadingSaved(t.Context(), turn, 1, 202)
	if err != nil || other != got.Saved {
		t.Fatal(other, err)
	}
	_, _ = turn.transaction.rollback(nil)
	wc008NativeExec(t, w, `UPDATE personal_items SET reading_revision=5 WHERE id=1`)
	turn = wc008NativeTurn(t, w, 770, mpReadingIntentDefinitions())
	got, err = mpReadingIntent(t.Context(), turn, q)
	if !errors.Is(err, ErrConflict) || got != (mpReadingIntentValue{}) {
		t.Fatal(got, err)
	}
}
