package read

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"github.com/PorticoMediaServer/portico/server/internal/readingidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// mpReadingSourceSeedVersion pins the reviewed seed shape independently of the
// test source text, so unrelated assertions do not invalidate the plan golden.
const mpReadingSourceSeedVersion = "mp-reading-source-seed-v1"

func mpReadingSourceHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(foregroundFixtureHash(t)+mpReadingSourceSeedVersion)))
}

type mpReadingSeedObject struct {
	id, source, token, relative, object string
	size, modified                      int64
	retired                             int64
	state, unsupported                  string
}

func mpReadingTestEvidence(t *testing.T, source, incarnation, object string, size, modified int64) (string, string) {
	t.Helper()
	evidence := mediasource.InventoryEvidence{Kind: "local", Scope: source + ":" + incarnation, Object: object, Size: size, ModifiedNS: modified, ChangeToken: fmt.Sprintf("fp1:ino:%d:size:%d:mtime:%d", len(object), size, modified)}
	revision, err := evidence.Revision()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	return revision, string(raw)
}

func mpReadingSourceSeed(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, `UPDATE libraries SET kind='book' WHERE id='movies'`)
	f.exec(t, `UPDATE libraries SET kind='comic' WHERE id='other'`)
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(20,'ebook',0,0,1,1,0),(21,'comic',0,0,1,1,0)`)
	for _, source := range []struct{ id, library, incarnation, rootIdentity string }{
		{"source-book", "movies", "inc-book", "dev:1:ino:9:fsid:a"},
		{"source-comic", "other", "inc-comic", "dev:1:ino:10:fsid:a"},
	} {
		f.exec(t, `INSERT INTO library_sources(id,library_id,kind,root,configured_root,generation,incarnation,root_identity,health,enabled) VALUES(? ,?,'local','/media','/media',1,?,?,'ready',1)`, source.id, source.library, source.incarnation, source.rootIdentity)
	}
	items := []struct {
		id, kind, library, asset, size, modified int64
		token, format, libraryName, relative     string
	}{
		{1101, 20, 1, 201, 1000, 1, "book-asset", "epub", "movies", "books/one.epub"},
		{1102, 20, 1, 202, 1001, 1, "book-pdf", "pdf", "movies", "books/two.pdf"},
		{1103, 20, 1, 203, 1002, 1, "book-mobi", "mobi", "movies", "books/three.mobi"},
		{1104, 20, 1, 204, 1003, 1, "book-azw3", "azw3", "movies", "books/four.azw3"},
		{1201, 21, 2, 211, 2000, 2, "comic-asset", "cbz", "other", "comics/one.cbz"},
		{1202, 21, 2, 212, 2001, 2, "comic-pdf", "pdf", "other", "comics/two.pdf"},
		{1203, 21, 2, 213, 2002, 2, "comic-archive-asset", "cb7", "other", "comics/three.cb7"},
		{1204, 21, 2, 214, 2003, 2, "comic-rar-asset", "cbr", "other", "comics/four.cbr"},
	}
	for _, item := range items {
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public[8:], uint64(item.id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,?)`, item.id, public, item.library, item.kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(?,?,?,?,?,?,?,?,?,0,?,?)`,
			item.id, public, item.library, item.kind, fmt.Sprintf("Reading %d", item.id), fmt.Sprint(item.id), fmt.Sprint(item.id), fmt.Sprint(item.id), fmt.Sprint(item.id), public, public)
		f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,?,?,'','','',0,0,0,?)`,
			item.asset, item.token, "/media/"+item.relative, item.size, item.modified, item.library)
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id,available) VALUES(?,?,?,1)`, item.id, item.asset, item.library)
		f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,?)`, item.id, item.asset, item.format)
	}
	objects := []mpReadingSeedObject{
		{id: "book-object-0", source: "source-book", token: "book-asset", relative: "books/one.epub", object: "ino:0", size: 1000, modified: 1, retired: 1, state: "available"},
		{id: "book-object-01-foreign", source: "source-comic", token: "book-asset", relative: "books/one.epub", object: "ino:99", size: 1000, modified: 1, state: "available"},
		{id: "book-object-1", source: "source-book", token: "book-asset", relative: "books/one.epub", object: "ino:1", size: 1000, modified: 1, state: "available"},
		{id: "book-object-2", source: "source-book", token: "book-asset", relative: "copies/one.epub", object: "ino:2", size: 1000, modified: 1, state: "available"},
		{id: "book-pdf-object", source: "source-book", token: "book-pdf", relative: "books/two.pdf", object: "ino:3", size: 1001, modified: 1, state: "available"},
		{id: "book-mobi-object", source: "source-book", token: "book-mobi", relative: "books/three.mobi", object: "ino:4", size: 1002, modified: 1, state: "available"},
		{id: "book-azw3-object", source: "source-book", token: "book-azw3", relative: "books/four.azw3", object: "ino:5", size: 1003, modified: 1, state: "available"},
		{id: "comic-object", source: "source-comic", token: "comic-asset", relative: "comics/one.cbz", object: "ino:11", size: 2000, modified: 2, state: "available"},
		{id: "comic-pdf-object", source: "source-comic", token: "comic-pdf", relative: "comics/two.pdf", object: "ino:12", size: 2001, modified: 2, state: "available"},
		{id: "comic-archive-object", source: "source-comic", token: "comic-archive-asset", relative: "comics/three.cb7", object: "ino:13", size: 2002, modified: 2, state: "available"},
		{id: "comic-rar-object", source: "source-comic", token: "comic-rar-asset", relative: "comics/four.cbr", object: "ino:14", size: 2003, modified: 2, state: "available"},
	}
	incarnations := map[string]string{"source-book": "inc-book", "source-comic": "inc-comic"}
	for _, o := range objects {
		revision, evidence := mpReadingTestEvidence(t, o.source, incarnations[o.source], o.object, o.size, o.modified)
		state := o.state
		if state == "" {
			state = "available"
		}
		f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,retired,state,unsupported_reason) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			o.id, o.source, o.token, incarnations[o.source], o.relative, o.object, revision, evidence, o.size, o.modified, o.retired, state, o.unsupported)
	}
	return f
}

func mpReadingSourceFixture(t *testing.T) (*browseFixture, *MPReadingSourceReader) {
	t.Helper()
	f := composeReadFixture(t, mpReadingSourceSeed(t), append(browseDefinitions(), MPReadingSourceDefinitions()...), readfixture.LiveConfig(time.Now), true, mpReadingSourceHash(t))
	r, err := NewMPReadingSourceReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func mpReadingSelect(t *testing.T, r *MPReadingSourceReader, f *browseFixture, item, asset, kind, library int64) (MPReadingSource, error) {
	t.Helper()
	view := f.input
	view.Library, view.Kind = library, kind
	got, _, err := r.selectSource(t.Context(), foregroundRequest(), time.Second, f.output, MPReadingSourceInput{View: view, Item: item, Asset: asset})
	return got, err
}

func TestMPReadingSourceNativeVectors(t *testing.T) {
	f, r := mpReadingSourceFixture(t)
	for _, vector := range []struct {
		name                 string
		item, asset, kind    int64
		library              int64
		token, format        string
		size, modified       int64
		source, incarnation  string
		rootIdentity         string
		object, objectRow    string
		relative, evidenceOf string
	}{
		{"epub", 1101, 201, 20, 1, "book-asset", "epub", 1000, 1, "source-book", "inc-book", "dev:1:ino:9:fsid:a", "ino:1", "book-object-1", "books/one.epub", "ino:1"},
		{"pdf", 1102, 202, 20, 1, "book-pdf", "pdf", 1001, 1, "source-book", "inc-book", "dev:1:ino:9:fsid:a", "ino:3", "book-pdf-object", "books/two.pdf", "ino:3"},
		{"comic", 1201, 211, 21, 2, "comic-asset", "cbz", 2000, 2, "source-comic", "inc-comic", "dev:1:ino:10:fsid:a", "ino:11", "comic-object", "comics/one.cbz", "ino:11"},
	} {
		t.Run(vector.name, func(t *testing.T) {
			got, err := mpReadingSelect(t, r, f, vector.item, vector.asset, vector.kind, vector.library)
			if err != nil {
				t.Fatal(err)
			}
			if got.Item != vector.item || got.Asset != vector.asset || got.ItemKind != vector.kind || got.Token != vector.token || got.Format != vector.format || got.PresentationFormat != vector.format || got.Converted || got.Size != vector.size || got.ModifiedNS != vector.modified || got.SourceID != vector.source || got.SourceIncarnation != vector.incarnation || got.RootIdentity != vector.rootIdentity || got.ObjectIdentity != vector.object || got.InventoryObject != vector.objectRow || got.RelativePath != vector.relative {
				t.Fatalf("decoded %+v", got)
			}
			if got.PublicID[15] == 0 && got.PublicID[8] == 0 {
				t.Fatal("missing public id")
			}
			want := readingidentity.Revision(got.Item, got.Asset, got.Format, got.Token, got.Size, got.ModifiedNS, got.SourceID, got.SourceGeneration, got.SourceIncarnation, got.RootIdentity, got.RelativePath, got.ObjectIdentity, got.InventoryRevision, got.Evidence)
			if want == "" || got.Revision() != want || len(got.Revision()) != 64 {
				t.Fatalf("revision %q want %q", got.Revision(), want)
			}
			var evidence mediasource.InventoryEvidence
			if err := json.Unmarshal([]byte(got.Evidence), &evidence); err != nil || evidence.Object != vector.evidenceOf {
				t.Fatalf("opaque evidence not preserved: %q %v", got.Evidence, err)
			}
		})
	}
}

func TestMPReadingSourceConversionProvenance(t *testing.T) {
	f, r := mpReadingSourceFixture(t)
	converted, err := newMPReadingSourceReaderWithConversion(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	for _, vector := range []struct {
		item, asset     int64
		token, format   string
		size            int64
		object, relPath string
	}{{1103, 203, "book-mobi", "mobi", 1002, "ino:4", "books/three.mobi"}, {1104, 204, "book-azw3", "azw3", 1003, "ino:5", "books/four.azw3"}} {
		got, err := mpReadingSelect(t, converted, f, vector.item, vector.asset, 20, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Converted || got.Format != vector.format || got.PresentationFormat != "epub" || got.Token != vector.token || got.Size != vector.size || got.ObjectIdentity != vector.object || got.RelativePath != vector.relPath {
			t.Fatalf("conversion provenance %+v", got)
		}
		native := readingidentity.Revision(got.Item, got.Asset, got.Format, got.Token, got.Size, got.ModifiedNS, got.SourceID, got.SourceGeneration, got.SourceIncarnation, got.RootIdentity, got.RelativePath, got.ObjectIdentity, got.InventoryRevision, got.Evidence)
		pretended := readingidentity.Revision(got.Item, got.Asset, "epub", got.Token, got.Size, got.ModifiedNS, got.SourceID, got.SourceGeneration, got.SourceIncarnation, got.RootIdentity, got.RelativePath, got.ObjectIdentity, got.InventoryRevision, got.Evidence)
		if got.Revision() != native || got.Revision() == pretended {
			t.Fatal("converted source revision must stay the native original, not the derived presentation")
		}
		if _, err := mpReadingSelect(t, r, f, vector.item, vector.asset, 20, 1); !errors.Is(err, ErrDenied) {
			t.Fatalf("unconfigured conversion admitted: %v", err)
		}
	}
}

func TestMPReadingSourceCanonicalLowestObject(t *testing.T) {
	f, r := mpReadingSourceFixture(t)
	first, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.InventoryObject != "book-object-1" || first.ObjectIdentity != "ino:1" {
		t.Fatalf("canonical object %+v", first)
	}
	f.exec(t, `UPDATE inventory_objects SET retired=1 WHERE id='book-object-1'`)
	second, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.InventoryObject != "book-object-2" || second.ObjectIdentity != "ino:2" || second.Revision() == first.Revision() {
		t.Fatalf("next canonical object %+v", second)
	}
	f.exec(t, `UPDATE inventory_objects SET retired=1 WHERE id='book-object-2'`)
	if got, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1); !errors.Is(err, ErrDenied) || got != (MPReadingSource{}) {
		t.Fatalf("foreign or retired object admitted: %+v %v", got, err)
	}
}

func TestMPReadingSourceRowIDIsNotPhysicalIdentity(t *testing.T) {
	f, r := mpReadingSourceFixture(t)
	original, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Swap the order so the previously second physical object becomes lowest.
	f.exec(t, `UPDATE inventory_objects SET id='z-book-object-1' WHERE id='book-object-1'`)
	f.exec(t, `UPDATE inventory_objects SET id='a-book-object-2' WHERE id='book-object-2'`)
	swapped, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if swapped.InventoryObject != "a-book-object-2" || swapped.ObjectIdentity != "ino:2" || swapped.Revision() == original.Revision() {
		t.Fatalf("row id was used as identity: %+v", swapped)
	}
	// A row whose physical identity was replaced by its own row id must refuse,
	// never silently become a newer source.
	f.exec(t, `UPDATE inventory_objects SET retired=1 WHERE id='z-book-object-1'`)
	revision, evidence := mpReadingTestEvidence(t, "source-book", "inc-book", "a-book-object-2", 1000, 1)
	f.exec(t, `UPDATE inventory_objects SET object_identity='a-book-object-2',revision=?,evidence_json=? WHERE id='a-book-object-2'`, revision, evidence)
	if got, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1); !errors.Is(err, ErrUnqualified) || got != (MPReadingSource{}) {
		t.Fatalf("row id substituted for physical identity: %+v %v", got, err)
	}
}

func TestMPReadingSourceEvidenceStatRootChanges(t *testing.T) {
	f, r := mpReadingSourceFixture(t)
	baseline, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	revision, evidence := mpReadingTestEvidence(t, "source-book", "inc-book", "ino:1", 1000, 1)
	var changed mediasource.InventoryEvidence
	if err := json.Unmarshal([]byte(evidence), &changed); err != nil {
		t.Fatal(err)
	}
	changed.ChangeToken = "fp1:ino:other:size:1000:mtime:1"
	raw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	changedRevision, err := changed.Revision()
	if err != nil {
		t.Fatal(err)
	}
	if changedRevision == revision {
		t.Fatal("fixture evidence did not change")
	}
	f.exec(t, `UPDATE inventory_objects SET revision=?,evidence_json=? WHERE id='book-object-1'`, changedRevision, string(raw))
	afterEvidence, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if afterEvidence.Revision() == baseline.Revision() || afterEvidence.Evidence == baseline.Evidence {
		t.Fatal("evidence change did not move the revision")
	}
	f.exec(t, `UPDATE inventory_objects SET revision=?,evidence_json=? WHERE id='book-object-1'`, revision, evidence)
	// A stat change is a new physical observation only when the object and the
	// asset agree; a half-updated pair is not a source.
	f.exec(t, `UPDATE catalog_assets SET size=1004,modified_ns=9 WHERE id=201`)
	if got, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1); !errors.Is(err, ErrDenied) || got != (MPReadingSource{}) {
		t.Fatalf("stat mismatch admitted: %+v %v", got, err)
	}
	statRevision, statEvidence := mpReadingTestEvidence(t, "source-book", "inc-book", "ino:1", 1004, 9)
	f.exec(t, `UPDATE inventory_objects SET size=1004,modified_ns=9,revision=?,evidence_json=? WHERE id='book-object-1'`, statRevision, statEvidence)
	afterStat, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1)
	if err != nil || afterStat.Revision() == baseline.Revision() || afterStat.Size != 1004 {
		t.Fatalf("stat change %+v %v", afterStat, err)
	}
	f.exec(t, `UPDATE catalog_assets SET size=1000,modified_ns=1 WHERE id=201`)
	f.exec(t, `UPDATE inventory_objects SET size=1000,modified_ns=1,revision=?,evidence_json=? WHERE id='book-object-1'`, revision, evidence)
	f.exec(t, `UPDATE library_sources SET root_identity='dev:1:ino:77:fsid:a' WHERE id='source-book'`)
	afterRoot, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1)
	if err != nil || afterRoot.Revision() == baseline.Revision() || afterRoot.RootIdentity != "dev:1:ino:77:fsid:a" {
		t.Fatalf("root identity change %+v %v", afterRoot, err)
	}
	f.exec(t, `UPDATE library_sources SET root_identity='dev:1:ino:9:fsid:a',incarnation='inc-next' WHERE id='source-book'`)
	if got, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1); !errors.Is(err, ErrDenied) || got != (MPReadingSource{}) {
		t.Fatalf("stale root incarnation admitted: %+v %v", got, err)
	}
}

func TestMPReadingSourceUnavailableAndUnsupported(t *testing.T) {
	f, r := mpReadingSourceFixture(t)
	// Each negative owns a fresh fixture so earlier mutations cannot mask it.
	for _, tc := range []struct {
		name                   string
		want                   error
		item, asset, kind, lib int64
		mutate                 func(*testing.T, *browseFixture)
	}{
		{"asset-unavailable", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE catalog_assets SET available=0 WHERE id=201`)
		}},
		{"link-unavailable", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE catalog_asset_links SET available=0 WHERE entity_id=1101`)
		}},
		{"publication-removed", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `DELETE FROM reading_publications WHERE item_id=1101`)
		}},
		{"item-retired", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=1101`)
		}},
		{"dvr-retired", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `INSERT INTO dvr_catalog_retirements(item_id,requested_ms) VALUES(1101,1)`)
		}},
		{"source-disabled", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE library_sources SET enabled=0 WHERE id='source-book'`)
		}},
		{"source-offline", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE library_sources SET health='offline' WHERE id='source-book'`)
		}},
		{"source-root-changed", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE library_sources SET health='root_changed' WHERE id='source-book'`)
		}},
		{"source-removing", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE library_sources SET health='removing' WHERE id='source-book'`)
		}},
		{"object-retired", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE inventory_objects SET retired=1 WHERE asset_id='book-asset'`)
		}},
		{"object-missing", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE inventory_objects SET state='missing' WHERE asset_id='book-asset'`)
		}},
		{"object-unsupported", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE inventory_objects SET unsupported_reason='unsupported' WHERE asset_id='book-asset'`)
		}},
		{"unsupported-book-format", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE reading_publications SET format='cbz' WHERE item_id=1101`)
		}},
		{"unsupported-comic-format", ErrDenied, 1201, 211, 21, 2, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE reading_publications SET format='epub' WHERE item_id=1201`)
		}},
		{"mixed-library-kind", ErrUnqualified, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `UPDATE libraries SET kind='comic' WHERE id='movies'`)
		}},
		{"no-physical-object", ErrDenied, 0, 0, 0, 0, func(t *testing.T, f *browseFixture) {
			f.exec(t, `DELETE FROM inventory_objects WHERE asset_id='book-asset'`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := mpReadingSourceFixture(t)
			tc.mutate(t, f)
			item, asset, kind, library := tc.item, tc.asset, tc.kind, tc.lib
			if item == 0 {
				item, asset, kind, library = 1101, 201, 20, 1
			}
			if got, err := mpReadingSelect(t, r, f, item, asset, kind, library); !errors.Is(err, tc.want) || got != (MPReadingSource{}) {
				t.Fatalf("%s admitted: %+v %v", tc.name, got, err)
			}
		})
	}
	// Archive builds expose CB7/CBR only when their own capability is compiled;
	// the ordinary build must refuse both even with a current local object.
	for _, tc := range []struct {
		name              string
		item, asset, size int64
		token             string
		enabled           bool
	}{{"cb7", 1203, 213, 2002, "comic-archive-asset", readingcap.CB7()}, {"cbr", 1204, 214, 2003, "comic-rar-asset", readingcap.CBR()}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mpReadingSelect(t, r, f, tc.item, tc.asset, 21, 2)
			if !tc.enabled {
				if !errors.Is(err, ErrDenied) || got != (MPReadingSource{}) {
					t.Fatalf("%s admitted without capability: %+v %v", tc.name, got, err)
				}
				return
			}
			if err != nil || got.Format != tc.name || got.PresentationFormat != tc.name || got.Converted || got.Token != tc.token || got.Size != tc.size || got.ItemKind != 21 {
				t.Fatalf("%s build %+v %v", tc.name, got, err)
			}
		})
	}
	// An archive source must be current and local; a remote kind is refused
	// even when the build compiles its decoder.
	t.Run("archive-remote-source", func(t *testing.T) {
		remote, rr := mpReadingSourceFixture(t)
		remote.exec(t, `UPDATE library_sources SET kind='smb' WHERE id='source-comic'`)
		if got, err := mpReadingSelect(t, rr, remote, 1203, 213, 21, 2); !errors.Is(err, ErrDenied) || got != (MPReadingSource{}) {
			t.Fatalf("remote archive source admitted: %+v %v", got, err)
		}
	})
}

func TestMPReadingSourceMatchesAvailabilityView(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		available bool
	}{
		{"ready", "", true},
		{"object-retired", `UPDATE inventory_objects SET retired=1 WHERE asset_id='book-asset'`, false},
		{"object-missing", `UPDATE inventory_objects SET state='missing' WHERE asset_id='book-asset'`, false},
		{"object-unsupported", `UPDATE inventory_objects SET unsupported_reason='x' WHERE asset_id='book-asset'`, false},
		{"source-disabled", `UPDATE library_sources SET enabled=0 WHERE id='source-book'`, false},
		{"incarnation-drift", `UPDATE library_sources SET incarnation='inc-next' WHERE id='source-book'`, false},
		{"source-removing", `UPDATE library_sources SET health='removing' WHERE id='source-book'`, false},
		{"source-root-changed", `UPDATE library_sources SET health='root_changed' WHERE id='source-book'`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := mpReadingSourceFixture(t)
			if tc.sql != "" {
				f.exec(t, tc.sql)
			}
			var want int64
			if err := f.conn.QueryRowContext(t.Context(), `SELECT available FROM inventory_item_availability WHERE item_id=1101 AND asset_id='book-asset'`).Scan(&want); err != nil {
				t.Fatal(err)
			}
			got, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1)
			if (want != 0) != tc.available {
				t.Fatalf("oracle %d want %t", want, tc.available)
			}
			if tc.available && (err != nil || got.Item != 1101) || !tc.available && (!errors.Is(err, ErrDenied) || got != (MPReadingSource{})) {
				t.Fatalf("availability mismatch %+v %v", got, err)
			}
		})
	}
	// Browse availability is not source custody. The view keeps a title
	// available while its source is offline, the asset flag is clear, the link is
	// cleared or no admitted object exists; the source decoder refuses each.
	for _, tc := range []struct {
		name, sql string
	}{
		{"source-offline", `UPDATE library_sources SET health='offline' WHERE id='source-book'`},
		{"asset-unavailable", `UPDATE catalog_assets SET available=0 WHERE id=201`},
		{"link-unavailable", `UPDATE catalog_asset_links SET available=0 WHERE entity_id=1101`},
		{"no-object-fallback", `DELETE FROM inventory_objects WHERE asset_id='book-asset'`},
	} {
		t.Run("browse-only-"+tc.name, func(t *testing.T) {
			f, r := mpReadingSourceFixture(t)
			f.exec(t, tc.sql)
			var want int64
			if err := f.conn.QueryRowContext(t.Context(), `SELECT available FROM inventory_item_availability WHERE item_id=1101 AND asset_id='book-asset'`).Scan(&want); err != nil || want == 0 {
				t.Fatalf("fallback oracle %d %v", want, err)
			}
			if got, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1); !errors.Is(err, ErrDenied) || got != (MPReadingSource{}) {
				t.Fatalf("browse availability fabricated a source: %+v %v", got, err)
			}
		})
	}
}

func TestMPReadingSourcePublicationDriftAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		want      error
	}{
		{"revoke", `UPDATE authorization_session_families SET revoked=1 WHERE id='f'`, ErrDenied},
		{"evidence", `UPDATE inventory_objects SET evidence_json='{}' WHERE id='book-object-1'`, ErrUnqualified},
		{"object-retire", `UPDATE inventory_objects SET retired=1 WHERE id='book-object-1'`, ErrStale},
		{"source-disable", `UPDATE library_sources SET enabled=0 WHERE id='source-book'`, ErrDenied},
		{"asset-unavailable", `UPDATE catalog_assets SET available=0 WHERE id=201`, ErrDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := mpReadingSourceFixture(t)
			r.snapshots = &rsReadingBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
				if phase == 1 {
					f.exec(t, tc.sql)
				}
			}}
			got, err := mpReadingSelect(t, r, f, 1101, 201, 20, 1)
			if got != (MPReadingSource{}) {
				t.Fatalf("drift leaked a draft: %+v", got)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("%v expected: %v", tc.want, err)
			}
		})
	}
	f, r := mpReadingSourceFixture(t)
	for _, bad := range []MPReadingSourceInput{
		{View: f.input, Item: 0, Asset: 201},
		{View: f.input, Item: 1101, Asset: 0},
	} {
		if got, _, err := r.selectSource(t.Context(), foregroundRequest(), time.Second, f.output, bad); !errors.Is(err, ErrInput) || got != (MPReadingSource{}) {
			t.Fatalf("invalid input %+v %v", bad, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	view := f.input
	view.Kind = 20
	got, _, err := r.selectSource(ctx, foregroundRequest(), time.Second, f.output, MPReadingSourceInput{View: view, Item: 1101, Asset: 201})
	if !errors.Is(err, context.Canceled) || got != (MPReadingSource{}) {
		t.Fatalf("cancel leaked: %+v %v", got, err)
	}
}

func TestMPReadingSourceExportedSelect(t *testing.T) {
	f := composeReadFixture(t, mpReadingSourceSeed(t), append(browseDefinitions(), MPReadingSourceDefinitions()...), readfixture.LiveConfig(time.Now), false)
	r, err := NewMPReadingSourceReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	q, request, output := liveRequests("mp-reading-source")
	in := MPReadingSourceInput{View: f.input, Item: 1101, Asset: 201}
	in.View.Kind, in.View.Library = 20, 1
	consumed := false
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		got, result, err := r.Select(ctx, request, time.Second, out, in)
		if err != nil {
			return err
		}
		if got.Item != 1101 || got.Token != "book-asset" || result.LookupStatements == 0 || result.PublicationStatements == 0 || result.Attempts != 1 {
			t.Fatalf("exported select %+v %+v", got, result)
		}
		consumed = true
		return nil
	})
	if err != nil || !consumed {
		t.Fatalf("live consumer %v", err)
	}
}

func TestMPReadingSourceDecodeRefusesMalformedTuples(t *testing.T) {
	base := MPReadingSourceValues{Item: 1101, ItemKind: 20, Asset: 1, Library: 1, LibraryKind: "book", LibraryName: "movies", Format: "epub", Token: "book-asset", Size: 1000, ModifiedNS: 1, LinkAvailable: 1, AssetAvailable: 1, AssetLibrary: 1, ObjectID: "book-object-1", ObjectAssetID: "book-asset", ObjectSourceID: "source-book", ObjectRootIncarnation: "inc-book", RelativePath: "books/one.epub", ObjectIdentity: "ino:1", ObjectSize: 1000, ObjectModifiedNS: 1, ObjectState: "available", SourceID: "source-book", SourceLibraryID: "movies", SourceKind: "local", SourceGeneration: 1, SourceIncarnation: "inc-book", RootIdentity: "dev:1:ino:9:fsid:a", SourceEnabled: 1, SourceHealth: "ready", PublicID: bytes.Repeat([]byte{1}, 16)}
	base.InventoryRevision, base.Evidence = mpReadingTestEvidence(t, "source-book", "inc-book", "ino:1", 1000, 1)
	if _, err := DecodeMPReadingSource(base, false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*MPReadingSourceValues)
		want   error
	}{
		{"zero-item", func(v *MPReadingSourceValues) { v.Item = 0 }, ErrInput},
		{"short-public-id", func(v *MPReadingSourceValues) { v.PublicID = v.PublicID[:15] }, ErrUnqualified},
		{"overflow-size", func(v *MPReadingSourceValues) { v.Size = mpReadingMaxSource + 1; v.ObjectSize = v.Size }, ErrUnqualified},
		{"zero-generation", func(v *MPReadingSourceValues) { v.SourceGeneration = 0 }, ErrUnqualified},
		{"absolute-relative", func(v *MPReadingSourceValues) { v.RelativePath = "/etc/passwd" }, ErrUnqualified},
		{"traversal-relative", func(v *MPReadingSourceValues) { v.RelativePath = "books/../../etc/passwd" }, ErrUnqualified},
		{"windows-traversal", func(v *MPReadingSourceValues) { v.RelativePath = `books\..\..\secret` }, ErrUnqualified},
		{"identity-is-row-id", func(v *MPReadingSourceValues) { v.ObjectIdentity = v.ObjectID }, ErrUnqualified},
		{"invalid-utf8", func(v *MPReadingSourceValues) { v.ObjectIdentity = "ino:\xff" }, ErrUnqualified},
		{"evidence-not-json", func(v *MPReadingSourceValues) { v.Evidence = "not-json" }, ErrUnqualified},
		{"evidence-object-mismatch", func(v *MPReadingSourceValues) {
			revision, evidence := mpReadingTestEvidence(t, "source-book", "inc-book", "ino:other", 1000, 1)
			v.Evidence, v.InventoryRevision = evidence, revision
		}, ErrUnqualified},
		{"evidence-size-mismatch", func(v *MPReadingSourceValues) {
			revision, evidence := mpReadingTestEvidence(t, "source-book", "inc-book", "ino:1", 999, 1)
			v.Evidence, v.InventoryRevision = evidence, revision
		}, ErrUnqualified},
		{"revision-not-evidence", func(v *MPReadingSourceValues) { v.InventoryRevision = base.InventoryRevision[:63] + "0" }, ErrUnqualified},
		{"source-library-mismatch", func(v *MPReadingSourceValues) { v.SourceLibraryID = "other" }, ErrDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := base
			tc.mutate(&v)
			if got, err := DecodeMPReadingSource(v, false); !errors.Is(err, tc.want) || got != (MPReadingSource{}) {
				t.Fatalf("%s: %+v %v", tc.name, got, err)
			}
		})
	}
}

var recordMPReadingSourcePlans = flag.Bool("record-mp-reading-source-plans", false, "record MP reading source native plans")

func TestMPReadingSourcePlans(t *testing.T) {
	f := mpReadingSourceSeed(t)
	definitions := MPReadingSourceDefinitions()
	for _, d := range definitions {
		for _, c := range d.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if err != nil {
				t.Fatalf("%s/%s: %v", d.ID, c.Name, err)
			}
			present := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil || present != (c.Name == "present") {
				t.Fatalf("%s/%s mislabeled: present=%v err=%v", d.ID, c.Name, present, err)
			}
		}
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	cfg := browseReaderConfig()
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	type capture struct {
		Profile string
		Golden  plan.Golden
	}
	var captures []capture
	for _, statistics := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if statistics == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, err := plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, mpReadingSourceHash(t), statistics), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("actual Capture receipt", err)
		}
		if len(golden.Manifest) != 10 || len(golden.Plans) != 19 {
			t.Fatalf("inventory %d/%d", len(golden.Manifest), len(golden.Plans))
		}
		captures = append(captures, capture{string(statistics), golden})
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	const path = "testdata/mp-reading-source-plans.json.gz"
	if *recordMPReadingSourcePlans {
		var buf bytes.Buffer
		z := gzip.NewWriter(&buf)
		if _, err = z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(expected))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}
