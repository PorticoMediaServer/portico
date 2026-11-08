package read

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readingidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// mpReadingResumeSeedVersion pins the reviewed resume seed shape independently
// of the test source text, so unrelated assertions do not invalidate the golden.
const mpReadingResumeSeedVersion = "mp-reading-resume-seed-v1"

// Group F reading children are not embedded. The exact draft DDL subset is
// loaded into each fixture database; no canonical schema file is edited.
const mpReadingResumeDraft = "testdata/mp-reading-resume-draft.sql"

const (
	mpReadingResumePersonalA int64 = 9001
	mpReadingResumePersonalB int64 = 9002
	mpReadingResumeAssetV2   int64 = 205
)

var mpReadingResumeConversionRevision = strings.Repeat("ab", 32)

func mpReadingResumeHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(foregroundFixtureHash(t)+mpReadingResumeSeedVersion)))
}

func mpReadingResumeNativeRevision(t *testing.T, item, asset int64, token, format, relative, object string, source string, incarnation string, root string, size, modified int64) string {
	t.Helper()
	revision, evidence := mpReadingTestEvidence(t, source, incarnation, object, size, modified)
	got := readingidentity.Revision(item, asset, format, token, size, modified, source, 1, incarnation, root, relative, object, revision, evidence)
	if got == "" {
		t.Fatal("fixture source revision")
	}
	return got
}

// mpReadingResumeSeed extends the MP-01 source fixture with the exact reading
// child DDL, one second source version, and two personal titles. The personal
// rows deliberately carry timed-media state to prove it cannot become a Reading
// location.
func mpReadingResumeSeed(t *testing.T) *browseFixture {
	t.Helper()
	f := mpReadingSourceSeed(t)
	ddl, err := os.ReadFile(mpReadingResumeDraft)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, string(ddl))
	// Second current source version of item 1101.
	v2Revision, v2Evidence := mpReadingTestEvidence(t, "source-book", "inc-book", "ino:21", 1000, 1)
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,?,?,'','','',0,0,0,1)`, mpReadingResumeAssetV2, "book-asset-v2", "/media/books/one-v2.epub", 1000, 1)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id,available) VALUES(1101,?,1,1)`, mpReadingResumeAssetV2)
	f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(1101,?,'epub')`, mpReadingResumeAssetV2)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,retired,state,unsupported_reason) VALUES('book-object-v2','source-book',?,'inc-book','books/one-v2.epub','ino:21',?,?,1000,1,0,'available','')`, "book-asset-v2", v2Revision, v2Evidence)
	// Timed-media resume columns are populated on purpose: Reading never reads them.
	for _, personal := range []struct {
		id, item, revision, reading int64
	}{{mpReadingResumePersonalA, 1101, 3, 7}, {mpReadingResumePersonalB, 1103, 1, 2}} {
		f.exec(t, `INSERT INTO personal_items(id,profile_id,item_public_id,item_id,library_id,resume_position,resume_unit,resume_completed,watched,revision,reading_revision) SELECT ?,'p',p.public_id,p.id,p.library_id,500,0,1,1,?,? FROM catalog_subjects AS p WHERE p.id=?`, personal.id, personal.revision, personal.reading, personal.item)
	}
	rev201 := mpReadingResumeNativeRevision(t, 1101, 201, "book-asset", "epub", "books/one.epub", "ino:1", "source-book", "inc-book", "dev:1:ino:9:fsid:a", 1000, 1)
	rev205 := mpReadingResumeNativeRevision(t, 1101, mpReadingResumeAssetV2, "book-asset-v2", "epub", "books/one-v2.epub", "ino:21", "source-book", "inc-book", "dev:1:ino:9:fsid:a", 1000, 1)
	for _, child := range []struct {
		personal, asset, revision, observed int64
		token, sourceRevision, cfi          string
		progression                         float64
	}{
		{mpReadingResumePersonalA, 201, 2, 100, "book-asset", rev201, "epubcfi(/6/4!/4/2:0)", 0.2},
		{mpReadingResumePersonalA, mpReadingResumeAssetV2, 1, 200, "book-asset-v2", rev205, "epubcfi(/6/4!/4/2:2)", 0.4},
		{mpReadingResumePersonalB, 203, 1, 300, "book-mobi", mpReadingResumeConversionRevision, "epubcfi(/6/4!/4/2:4)", 0.3},
	} {
		f.exec(t, `INSERT INTO personal_reading_resumes(personal_item_id,asset_id,source_token,source_revision,format,cfi,section,page_index,progression,revision,observed_ms) VALUES(?,?,?,?,'epub',?,'EPUB/chapter.xhtml',NULL,?,?,?)`, child.personal, child.asset, child.token, child.sourceRevision, child.cfi, child.progression, child.revision, child.observed)
	}
	return f
}

// composeDraftReadFixture composes the unchanged read protocol over the exact
// draft-DDL fixture. Group F is not embedded, so plan.Capture refuses the live
// catalogue (see TestMPReadingResumeEmbeddedGoldenBlocker); the registry uses
// stmttest's explicitly unqualified test receipt, as the IA base draft work did.
func composeDraftReadFixture(t *testing.T, f *browseFixture, definitions []stmt.Definition, gc governor.Config) *browseFixture {
	t.Helper()
	registry, err := stmttest.New(t, definitions)
	if err != nil {
		t.Fatal(err)
	}
	reader := browseReaderConfig()
	connector, err := sqliteconn.Connector(f.path, reader)
	if err != nil {
		t.Fatal(err)
	}
	g, a, err := governor.New(gc)
	if err != nil {
		t.Fatal(err)
	}
	f.governor, f.authority = g, a
	f.snapshots, err = governor.NewSnapshots(connector, reader, g, a, registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.snapshots.Close(); err != nil {
			t.Error(err)
		}
	})
	outputRequest := governor.Request{Key: governor.JobKey{Domain: 72, ID: "browse-output"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.MemoryBytes: 8 << 20}, Deadline: time.Now().Add(time.Hour)}
	decision := g.Try(outputRequest)
	if decision.Refusal != nil {
		t.Fatal("retained-output admission", decision.Refusal)
	}
	f.output = BrowseOutput{Authority: a, Grant: decision.Grant, Request: outputRequest}
	t.Cleanup(func() {
		g.Cancel(decision.Grant)
		if err := a.Retired(decision.Grant); err != nil {
			t.Error(err)
		}
	})
	return f
}

func mpReadingResumeFixture(t *testing.T) (*browseFixture, *MPReadingResumeReader) {
	t.Helper()
	f := composeDraftReadFixture(t, mpReadingResumeSeed(t), append(append(browseDefinitions(), MPReadingSourceDefinitions()...), MPReadingResumeDefinitions()...), readfixture.LiveConfig(time.Now))
	r, err := NewMPReadingResumeReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func mpReadingResumeRead(t *testing.T, r *MPReadingResumeReader, f *browseFixture, item, kind, library int64) (MPReadingResume, error) {
	t.Helper()
	view := f.input
	view.Library, view.Kind = library, kind
	got, _, err := r.selectResume(t.Context(), foregroundRequest(), time.Second, f.output, MPReadingResumeInput{View: view, Item: item})
	return got, err
}

func TestMPReadingResumeLatestSavedVersion(t *testing.T) {
	f, r := mpReadingResumeFixture(t)
	got, err := mpReadingResumeRead(t, r, f, 1101, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	var public []byte
	if err := f.conn.QueryRowContext(t.Context(), `SELECT public_id FROM catalog_subjects WHERE id=1101`).Scan(&public); err != nil {
		t.Fatal(err)
	}
	if got.Item != 1101 || !bytes.Equal(got.PublicID[:], public) || got.PersonalRevision != 3 || got.ReadingRevision != 7 {
		t.Fatalf("resume facts %+v", got)
	}
	if got.Location == nil || got.Location.AssetID != mpReadingResumeAssetV2 || got.Location.SourceToken != "book-asset-v2" || got.Location.Format != "epub" || got.Location.CFI != "epubcfi(/6/4!/4/2:2)" || got.Location.Section != "EPUB/chapter.xhtml" || got.Location.Progression != 0.4 || got.Location.Revision != 1 || got.Location.ObservedMS != 200 {
		t.Fatalf("latest saved version %+v", got.Location)
	}
	if got.Completed || got.Stale || got.Source == nil || got.Source.Asset != mpReadingResumeAssetV2 || got.Source.Token != "book-asset-v2" || got.Source.Revision() != got.Location.SourceRevision {
		t.Fatalf("current source %+v", got)
	}
	// The older unfinished version exists but must never be returned instead.
	var older int64
	if err := f.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM personal_reading_resumes WHERE personal_item_id=?`, mpReadingResumePersonalA).Scan(&older); err != nil || older != 2 {
		t.Fatalf("two saved versions %d %v", older, err)
	}
}

func TestMPReadingResumeCompletedDoesNotFallBack(t *testing.T) {
	f, r := mpReadingResumeFixture(t)
	f.exec(t, `UPDATE personal_reading_resumes SET progression=1.0,observed_ms=250 WHERE personal_item_id=? AND asset_id=?`, mpReadingResumePersonalA, mpReadingResumeAssetV2)
	got, err := mpReadingResumeRead(t, r, f, 1101, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Location == nil || got.Location.AssetID != mpReadingResumeAssetV2 || !got.Completed || got.Stale {
		t.Fatalf("completed latest %+v", got)
	}
	// Competing completion is chosen before filtering: a newer completed version
	// suppresses the older unfinished one.
	if got.Location.AssetID == 201 && got.Location.Progression < 1 {
		t.Fatal("older unfinished version resurrected")
	}
}

func TestMPReadingResumeStaleLatestDoesNotFallBack(t *testing.T) {
	f, r := mpReadingResumeFixture(t)
	// The latest version's physical source disappears; the older version is
	// still perfectly eligible. The display must report the latest as stale.
	f.exec(t, `UPDATE inventory_objects SET retired=1 WHERE asset_id='book-asset-v2'`)
	got, err := mpReadingResumeRead(t, r, f, 1101, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Location == nil || got.Location.AssetID != mpReadingResumeAssetV2 || !got.Stale || got.Source != nil || got.Completed {
		t.Fatalf("stale latest %+v", got)
	}
	if got.Location.SourceRevision == "" || got.Location.SourceToken != "book-asset-v2" {
		t.Fatal("stale observation lost")
	}
}

func TestMPReadingResumeMissingCurrentSource(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
	}{
		{"asset-unavailable", `UPDATE catalog_assets SET available=0 WHERE id=205`},
		{"link-cleared", `UPDATE catalog_asset_links SET available=0 WHERE entity_id=1101 AND asset_id=205`},
		{"publication-removed", `DELETE FROM reading_publications WHERE item_id=1101 AND asset_id=205`},
		{"object-retired", `DELETE FROM inventory_objects WHERE asset_id='book-asset-v2'`},
		{"source-disabled", `UPDATE library_sources SET enabled=0 WHERE id='source-book'`},
		{"source-offline", `UPDATE library_sources SET health='offline' WHERE id='source-book'`},
		{"stat-changed", `UPDATE catalog_assets SET size=1001 WHERE id=205`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := mpReadingResumeFixture(t)
			f.exec(t, tc.sql)
			got, err := mpReadingResumeRead(t, r, f, 1101, 20, 1)
			if err != nil {
				t.Fatal(err)
			}
			if got.Location == nil || got.Location.AssetID != mpReadingResumeAssetV2 || !got.Stale || got.Source != nil {
				t.Fatalf("%s: %+v", tc.name, got)
			}
		})
	}
}

func TestMPReadingResumeNullLocationIgnoresTimedMedia(t *testing.T) {
	f, r := mpReadingResumeFixture(t)
	f.exec(t, `DELETE FROM personal_reading_resumes WHERE personal_item_id=?`, mpReadingResumePersonalA)
	got, err := mpReadingResumeRead(t, r, f, 1101, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Location != nil || got.Completed || got.Stale {
		t.Fatalf("timed-media state became a Reading location: %+v", got)
	}
	if got.PersonalRevision != 3 || got.ReadingRevision != 7 {
		t.Fatalf("personal watermarks %+v", got)
	}
	// A title with no personal row at all is an empty display, not a denial.
	f2, r2 := mpReadingResumeFixture(t)
	f2.exec(t, `DELETE FROM personal_reading_resumes WHERE personal_item_id=?`, mpReadingResumePersonalA)
	f2.exec(t, `DELETE FROM personal_items WHERE id=?`, mpReadingResumePersonalA)
	none, err := mpReadingResumeRead(t, r2, f2, 1101, 20, 1)
	if err != nil || none.Location != nil || none.PersonalRevision != 0 || none.ReadingRevision != 0 {
		t.Fatalf("no personal row %+v %v", none, err)
	}
}

func TestMPReadingResumeConvertedEPUB(t *testing.T) {
	f, r := mpReadingResumeFixture(t)
	// Without the configured converter the saved EPUB cannot be presented.
	native, err := mpReadingResumeRead(t, r, f, 1103, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if native.Location == nil || native.Location.Format != "epub" || native.Converted || !native.Stale || native.Source != nil {
		t.Fatalf("unconfigured conversion %+v", native)
	}
	convertedReader, err := newMPReadingResumeReaderWithConversion(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mpReadingResumeRead(t, convertedReader, f, 1103, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Location == nil || !got.Converted || got.Stale || got.Source == nil {
		t.Fatalf("converted EPUB %+v", got)
	}
	if got.Source.Format != "mobi" || got.Source.PresentationFormat != "epub" || got.Location.Format != "epub" {
		t.Fatalf("conversion provenance formats %+v", got.Source)
	}
	// The conversion revision is preserved as the saved fact; the native source
	// revision is exposed separately and never substituted for it.
	if got.Location.SourceRevision != mpReadingResumeConversionRevision {
		t.Fatalf("conversion revision %q", got.Location.SourceRevision)
	}
	if got.Source.Revision() == got.Location.SourceRevision {
		t.Fatal("native revision pretended to be the conversion revision")
	}
}

func TestMPReadingResumeRevokeBetweenSnapshots(t *testing.T) {
	f, r := mpReadingResumeFixture(t)
	r.snapshots = &rsReadingBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
		if phase == 1 {
			f.exec(t, `UPDATE authorization_session_families SET revoked=1 WHERE id='f'`)
		}
	}}
	got, err := mpReadingResumeRead(t, r, f, 1101, 20, 1)
	if !errors.Is(err, ErrDenied) || got.Location != nil || got.Source != nil || got.PersonalRevision != 0 {
		t.Fatalf("revoke between snapshots: %+v %v", got, err)
	}
}

func TestMPReadingResumeDriftBetweenSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
	}{
		{"newer-version", `UPDATE personal_reading_resumes SET observed_ms=400 WHERE personal_item_id=9001 AND asset_id=201`},
		{"personal-revision", `UPDATE personal_items SET revision=4 WHERE id=9001`},
		{"saved-location", `UPDATE personal_reading_resumes SET cfi='epubcfi(/6/4!/4/2:8)' WHERE personal_item_id=9001 AND asset_id=205`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, r := mpReadingResumeFixture(t)
			r.snapshots = &rsReadingBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
				if phase == 1 {
					f.exec(t, tc.sql)
				}
			}}
			got, err := mpReadingResumeRead(t, r, f, 1101, 20, 1)
			if got.Location != nil || got.Source != nil || got.PersonalRevision != 0 {
				t.Fatalf("%s leaked a draft: %+v", tc.name, got)
			}
			if !errors.Is(err, ErrStale) {
				t.Fatalf("%s: %v", tc.name, err)
			}
		})
	}
}

func TestMPReadingResumeInputAndCancellation(t *testing.T) {
	f, r := mpReadingResumeFixture(t)
	after := &BrowseCursor{}
	for _, bad := range []MPReadingResumeInput{
		{View: f.input, Item: 0},
		{View: f.input, Item: 1101},
		{View: f.input, Item: 1101},
	} {
		bad.View.Kind = 20
		switch bad.Item {
		case 1101:
			if bad.View.After == nil {
				bad.View.After = after
			} else {
				bad.View.Descending = true
			}
		}
		if got, _, err := r.selectResume(t.Context(), foregroundRequest(), time.Second, f.output, bad); !errors.Is(err, ErrInput) || got.Location != nil {
			t.Fatalf("invalid input %+v %v", bad, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	view := f.input
	view.Kind = 20
	got, _, err := r.selectResume(ctx, foregroundRequest(), time.Second, f.output, MPReadingResumeInput{View: view, Item: 1101})
	if !errors.Is(err, context.Canceled) || got.Location != nil {
		t.Fatalf("cancel leaked: %+v %v", got, err)
	}
	// The wrong library kind refuses even with a saved position.
	wrong, err := mpReadingResumeRead(t, r, f, 1101, 21, 2)
	if !errors.Is(err, ErrDenied) || wrong.Location != nil {
		t.Fatalf("mixed library kind: %+v %v", wrong, err)
	}
}

func TestMPReadingResumeExportedSelect(t *testing.T) {
	f := composeDraftReadFixture(t, mpReadingResumeSeed(t), append(append(browseDefinitions(), MPReadingSourceDefinitions()...), MPReadingResumeDefinitions()...), readfixture.LiveConfig(time.Now))
	r, err := NewMPReadingResumeReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	q, request, output := liveRequests("mp-reading-resume")
	in := MPReadingResumeInput{View: f.input, Item: 1101}
	in.View.Kind, in.View.Library = 20, 1
	consumed := false
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		got, result, err := r.Select(ctx, request, time.Second, out, in)
		if err != nil {
			return err
		}
		if got.Location == nil || got.Location.AssetID != mpReadingResumeAssetV2 || result.LookupStatements == 0 || result.PublicationStatements == 0 || result.Attempts != 1 {
			t.Fatalf("exported select %+v %+v", got, result)
		}
		consumed = true
		return nil
	})
	if err != nil || !consumed {
		t.Fatalf("live consumer %v", err)
	}
}

func TestMPReadingResumeDecodeRefusesMalformedRows(t *testing.T) {
	base := []any{"book-asset", strings.Repeat("0f", 32), "epub", "epubcfi(/6/4!/4/2:0)", "EPUB/chapter.xhtml", nil, 0.5, int64(1), int64(10)}
	if _, err := decodeMPReadingLocation(mpReadingResumeAssetV2, base); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func([]any) []any
	}{
		{"row-length", func(r []any) []any { return r[:8] }},
		{"zero-asset", func(r []any) []any { return r }},
		{"empty-token", func(r []any) []any { r[0] = ""; return r }},
		{"token-control", func(r []any) []any { r[0] = "book\x00asset"; return r }},
		{"token-long", func(r []any) []any { r[0] = strings.Repeat("t", 513); return r }},
		{"revision-short", func(r []any) []any { r[1] = "abcd"; return r }},
		{"revision-uppercase", func(r []any) []any { r[1] = strings.ToUpper(strings.Repeat("0f", 32)); return r }},
		{"revision-nonhex", func(r []any) []any { r[1] = strings.Repeat("zz", 32); return r }},
		{"format", func(r []any) []any { r[2] = "mobi"; return r }},
		{"epub-with-page", func(r []any) []any { r[5] = int64(0); return r }},
		{"epub-no-cfi", func(r []any) []any { r[3] = nil; return r }},
		{"epub-control-cfi", func(r []any) []any { r[3] = "epubcfi(\x01)"; return r }},
		{"section-long", func(r []any) []any { r[4] = strings.Repeat("s", 1025); return r }},
		{"page-negative", func(r []any) []any { r[3], r[4], r[5], r[2] = nil, nil, int64(-1), "pdf"; return r }},
		{"page-missing", func(r []any) []any { r[3], r[4], r[5], r[2] = nil, nil, nil, "pdf"; return r }},
		{"pdf-with-cfi", func(r []any) []any { r[5], r[2] = int64(0), "pdf"; return r }},
		{"progression-nan", func(r []any) []any { r[6] = math.NaN(); return r }},
		{"progression-inf", func(r []any) []any { r[6] = math.Inf(1); return r }},
		{"progression-low", func(r []any) []any { r[6] = -0.1; return r }},
		{"progression-high", func(r []any) []any { r[6] = 1.1; return r }},
		{"revision-zero", func(r []any) []any { r[7] = int64(0); return r }},
		{"observed-negative", func(r []any) []any { r[8] = int64(-1); return r }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := append([]any(nil), base...)
			asset := mpReadingResumeAssetV2
			if tc.name == "zero-asset" {
				asset = 0
			}
			if got, err := decodeMPReadingLocation(asset, tc.mutate(row)); !errors.Is(err, ErrUnqualified) || got != (MPReadingLocation{}) {
				t.Fatalf("%s: %+v %v", tc.name, got, err)
			}
		})
	}
}

// TestMPReadingResumePlans asserts the exact live native plans. A stored
// plan.Golden cannot be minted or compared while group F is not embedded:
// plan.Capture refuses any live catalogue that differs from
// schema.NativeLayoutSHA256. TestMPReadingResumeEmbeddedGoldenBlocker records
// that exact refusal; this test still checks every cohort, key and EQP directly.
func TestMPReadingResumePlans(t *testing.T) {
	f := mpReadingResumeSeed(t)
	definitions := MPReadingResumeDefinitions()
	// The plan registry's native audit cannot run before group F is embedded;
	// prove the same index shape directly on the exact draft DDL.
	indexRows, err := f.conn.QueryContext(t.Context(), `PRAGMA main.index_xinfo(personal_reading_resumes_latest)`)
	if err != nil {
		t.Fatal(err)
	}
	wantIndex := []struct {
		name string
		desc int64
		coll string
		key  int64
	}{{"personal_item_id", 0, "BINARY", 1}, {"observed_ms", 1, "BINARY", 1}, {"asset_id", 1, "BINARY", 1}}
	n := 0
	for indexRows.Next() {
		var seqno, cid, desc, key int64
		var name, coll any
		if err := indexRows.Scan(&seqno, &cid, &name, &desc, &coll, &key); err != nil {
			indexRows.Close()
			t.Fatal(err)
		}
		if key != 1 {
			continue
		}
		if n >= len(wantIndex) || name != wantIndex[n].name || desc != wantIndex[n].desc || coll != wantIndex[n].coll {
			indexRows.Close()
			t.Fatalf("latest index key drift at %d: %v/%v/%v", n, name, desc, coll)
		}
		n++
	}
	indexRows.Close()
	if n != len(wantIndex) {
		t.Fatalf("latest index key incomplete: %d", n)
	}
	personal := statementID("mp.reading.resume.personal")
	latest := statementID("mp.reading.resume.latest")
	child := statementID("mp.reading.resume.child")
	childOctets := statementID("mp.reading.resume.child_octets")
	childDetail := "SEARCH r USING INDEX sqlite_autoindex_personal_reading_resumes_1 (personal_item_id=? AND asset_id=?)"
	want := map[stmt.ID][]string{
		personal:    {"SEARCH i USING INDEX personal_current_live (profile_id=? AND item_id=?)"},
		latest:      {"SEARCH r USING INDEX personal_reading_resumes_latest (personal_item_id=?)"},
		child:       {childDetail},
		childOctets: {childDetail},
	}
	for _, analyze := range []bool{false, true} {
		if analyze {
			f.exec(t, "ANALYZE")
		}
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
			for _, c := range d.Cohorts {
				rows, err := f.conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, c.Bindings...)
				if err != nil {
					t.Fatalf("%s/%s EQP: %v", d.ID, c.Name, err)
				}
				var details []string
				for rows.Next() {
					var id, parent, notUsed int64
					var detail string
					if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
						rows.Close()
						t.Fatalf("%s/%s EQP scan: %v", d.ID, c.Name, err)
					}
					if strings.Contains(detail, "TEMP B-TREE") || strings.HasPrefix(detail, "SCAN") {
						rows.Close()
						t.Fatalf("%s/%s: %s", d.ID, c.Name, detail)
					}
					details = append(details, detail)
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					t.Fatalf("%s/%s EQP: %v", d.ID, c.Name, err)
				}
				rows.Close()
				if expected, ok := want[d.ID]; ok && fmt.Sprint(details) != fmt.Sprint(expected) {
					t.Fatalf("%s/%s: plans %v want %v", d.ID, c.Name, details, expected)
				}
				if len(details) != 1 {
					t.Fatalf("%s/%s: exact one-row point SEARCH required, got %v", d.ID, c.Name, details)
				}
			}
		}
	}
}

// TestMPReadingResumeEmbeddedGoldenBlocker proves the SCHEMA NOTE's condition
// for this task: a native Capture golden cannot be recorded until group F is
// embedded, because the shared harness revalidates the whole live catalogue.
// It must be replaced by a recorded testdata golden at embed.
func TestMPReadingResumeEmbeddedGoldenBlocker(t *testing.T) {
	f := mpReadingResumeSeed(t)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := stmt.Describe(MPReadingResumeDefinitions())
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
	_, err = plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, mpReadingResumeHash(t), plan.Unanalyzed), policy)
	if err == nil || !strings.Contains(err.Error(), "live schema does not match the embedded native layout") {
		t.Fatalf("expected the unembedded-schema refusal, got %v", err)
	}
	t.Logf("group F not embedded: %v", err)
}
