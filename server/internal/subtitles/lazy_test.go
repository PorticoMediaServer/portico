package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type cacheTestExtractor struct {
	entered, release chan struct{}
	calls            atomic.Int32
	body             []byte
}

func (e *cacheTestExtractor) Probe(context.Context, string, string) (*Inventory, string, error) {
	return nil, "", ErrUnsupported
}
func (e *cacheTestExtractor) Extract(ctx context.Context, item, source string, index int, format string) ([]byte, string, error) {
	e.calls.Add(1)
	if e.entered != nil {
		close(e.entered)
	}
	if e.release != nil {
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	raw, err := CanonicalAsset(e.body, nil, format, 60000000)
	return raw, "", err
}
func persistDiscovered(t *testing.T, f *subtitleFixture, tracks []Track) {
	t.Helper()
	tx, e := f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = Persist(tx, f.item.Token, 1000, 1, &Inventory{Status: "known", TimingKnown: true, Tracks: tracks}); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	f.s.ffmpeg = "fixture"
}
func TestDiscoveredTracksAreImmediatelySelectableAndAutoSelectable(t *testing.T) {
	f := newSubtitleFixture(t)
	persistDiscovered(t, f, []Track{{Origin: "embedded", StreamIndex: 3, Format: "srt", Language: "en", Default: true}})
	catalog, e := f.s.List(context.Background(), f.p, f.item.Public)
	if e != nil || len(catalog.Resources) != 1 || !catalog.Resources[0].Enabled {
		t.Fatal(catalog, e)
	}
	if catalog.Resources[0].size != 0 {
		t.Fatal("discovery extracted bytes")
	}
	tx, e := f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	e = AutoSelectTx(context.Background(), tx, "session", 1, f.item.Public, f.item.Token, actor(f.p), AutoChoice{Enabled: true, Languages: []string{"en"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	plan, e := f.s.Plan(context.Background(), f.p, f.item.Public, "session")
	if e != nil || plan.Selected == nil || plan.Selected.ID != catalog.Resources[0].ID || plan.DocumentURL == "" {
		t.Fatal(plan, e)
	}
	// A viewer can populate the shared byte cache without shared-edit authority.
	viewer := f.p
	viewer.Role = "viewer"
	extractor := &cacheTestExtractor{body: []byte(plainSubtitle)}
	f.s.Extractor = extractor
	reader, _, e := f.s.OpenManifestDocument(context.Background(), viewer, f.item.Public, "session", plan.Selected.ID, plan.Selected.Revision)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := ManifestWebVTT(reader, 0, -1)
	reader.Close()
	if e != nil || !strings.Contains(string(raw), "Hello") {
		t.Fatal(string(raw), e)
	}
	if extractor.calls.Load() != 1 {
		t.Fatal("wrong extraction count")
	}
	f.allowed = false
	if _, _, e = f.s.OpenManifestDocument(context.Background(), viewer, f.item.Public, "session", plan.Selected.ID, plan.Selected.Revision); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("revoked reader", e)
	}
}
func TestRemoteDiscoveryAutomaticChoiceAndManifestRequireSessionEvidence(t *testing.T) {
	for _, expected := range []string{"representation", "replaced"} {
		t.Run(expected, func(t *testing.T) {
			f := newSubtitleFixture(t)
			persistDiscovered(t, f, []Track{{Origin: "embedded", StreamIndex: 3, Format: "srt", Language: "en"}})
			catalog, e := f.s.List(context.Background(), f.p, f.item.Public)
			if e != nil {
				t.Fatal(e)
			}
			tx, e := f.db.Begin()
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			if _, e = tx.Exec(`UPDATE subtitle_render_revisions SET source_evidence='representation'; INSERT INTO subtitle_remote_sessions VALUES('session',?)`, expected); e != nil {
				t.Fatal(e)
			}
			if e = AutoSelectTx(context.Background(), tx, "session", 1, f.item.Public, f.item.Token, actor(f.p), AutoChoice{Enabled: true, Languages: []string{"en"}}); e != nil {
				t.Fatal(e)
			}
			var selected string
			if e = tx.QueryRow(`SELECT resource_id FROM playback_subtitle_state WHERE session_id='session'`).Scan(&selected); e != nil {
				t.Fatal(e)
			}
			matched := expected == "representation"
			if (selected != "") != matched {
				t.Fatal("incorrect automatic evidence match", selected)
			}
			// Even a retained manifest pin must not bypass representation fencing.
			if _, e = tx.Exec(`INSERT OR IGNORE INTO playback_subtitle_pins VALUES('session',?,1)`, catalog.Resources[0].ID); e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			e = f.s.CheckManifestDocument(context.Background(), f.p, f.item.Public, "session", catalog.Resources[0].ID, 1)
			if matched && e != nil || !matched && !errors.Is(e, identity.ErrUnauthorized) {
				t.Fatal("incorrect manifest evidence match", e)
			}
		})
	}
}
func TestCacheFillSurvivesFirstReaderCancellation(t *testing.T) {
	f := newSubtitleFixture(t)
	persistDiscovered(t, f, []Track{{Origin: "embedded", StreamIndex: 3, Format: "srt", Language: "en"}})
	catalog, e := f.s.List(context.Background(), f.p, f.item.Public)
	if e != nil {
		t.Fatal(e)
	}
	r := catalog.Resources[0]
	extractor := &cacheTestExtractor{entered: make(chan struct{}), release: make(chan struct{}), body: []byte(plainSubtitle)}
	f.s.Extractor = extractor
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.s.ensureCached(ctx, f.p, f.item.Public, r.ID, r.Revision) }()
	<-extractor.entered
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	close(extractor.release)
	if e = f.s.ensureCached(context.Background(), f.p, f.item.Public, r.ID, r.Revision); e != nil {
		t.Fatal(e)
	}
	if extractor.calls.Load() != 1 {
		t.Fatal("retry restarted extraction")
	}
}
func TestOriginalASSIsAuthoredAndSelectedWithoutVideoConversion(t *testing.T) {
	f := newSubtitleFixture(t)
	body := assHeader + "Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\\pos(100,120)}Styled\r\n"
	m := uploadMutation("styled", body, f.item.Token)
	m.Format = "ass"
	receipt, e := f.s.Mutate(context.Background(), f.p, f.item.Public, m)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := f.s.Select(context.Background(), f.p, f.item.Public, "session", SelectRequest{OperationID: "client-ass", Generation: 1, ExpectedRevision: 1, Mode: "track", ResourceID: receipt.ResourceID, ResourceRevision: 1, Rendering: "ass", OffsetUS: "0"})
	if e != nil || plan.Renderer != "client" || plan.Generation != 1 || plan.DocumentURL != "" {
		t.Fatal(plan, e)
	}
	original, e := f.s.OriginalAsset(context.Background(), f.p, f.item.Public, "session", receipt.ResourceID, 1)
	if e != nil || string(original.Data) != body {
		t.Fatalf("authored script changed: %v", e)
	}
	if len(plan.Selected.Deliveries) != 2 || !strings.HasSuffix(plan.Selected.Deliveries[1].URL, "/original.ass") {
		t.Fatal(plan.Selected.Deliveries)
	}
	var selectedRenderer string
	_ = f.db.QueryRow(`SELECT renderer FROM playback_subtitle_state WHERE session_id='session'`).Scan(&selectedRenderer)
	if selectedRenderer != "client" {
		t.Fatal(selectedRenderer)
	}
	if _, e = f.db.Exec(`UPDATE catalog_assets SET size=1001 WHERE token=?`, f.item.Token); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.OriginalAsset(context.Background(), f.p, f.item.Public, "session", receipt.ResourceID, 1); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("replaced source exposed original", e)
	}
}
func TestRemovedDiscoveryRetiresOnlyItsInventoryResource(t *testing.T) {
	f := newSubtitleFixture(t)
	persistDiscovered(t, f, []Track{{Origin: "embedded", StreamIndex: 3, Format: "srt", Language: "en"}})
	if _, e := f.s.Mutate(context.Background(), f.p, f.item.Public, uploadMutation("personal", plainSubtitle, f.item.Token)); e != nil {
		t.Fatal(e)
	}
	persistDiscovered(t, f, nil)
	catalog, e := f.s.List(context.Background(), f.p, f.item.Public)
	if e != nil || len(catalog.Resources) != 1 || catalog.Resources[0].Origin != "upload" {
		t.Fatal(catalog, e)
	}
}
func TestTolerantTextDocumentReportsSkippedCues(t *testing.T) {
	raw, e := CanonicalAsset([]byte(plainSubtitle+"\n2\nbad --> timing\nBad\n"), nil, "srt", 60000000)
	if e != nil {
		t.Fatal(e)
	}
	var doc Document
	if e = json.Unmarshal(raw, &doc); e != nil || doc.SkippedCues != 1 || len(doc.Cues) != 1 {
		t.Fatal(doc, e)
	}
}

func TestASSInlineAttachmentsKeepTextAndOriginalWithoutBurnAdmission(t *testing.T) {
	f := newSubtitleFixture(t)
	body := assHeader + "Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Good\n[Fonts]\nfontname: Example.ttf\nodd font data\n"
	m := uploadMutation("inline-ass", body, f.item.Token)
	m.Format = "ass"
	receipt, e := f.s.Mutate(context.Background(), f.p, f.item.Public, m)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := f.s.Select(context.Background(), f.p, f.item.Public, "session", SelectRequest{OperationID: "inline-original", Generation: 1, ExpectedRevision: 1, Mode: "track", ResourceID: receipt.ResourceID, ResourceRevision: 1, Rendering: "ass", OffsetUS: "0"})
	if e != nil || plan.Renderer != "client" {
		t.Fatal(plan, e)
	}
	original, e := f.s.OriginalAsset(context.Background(), f.p, f.item.Public, "session", receipt.ResourceID, 1)
	if e != nil || string(original.Data) != body {
		t.Fatal(original, e)
	}
	canonical, e := CanonicalAsset([]byte(body), nil, "ass", 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = DecodeRenderAsset(canonical); !errors.Is(e, ErrUnsupported) {
		t.Fatal("unsafe styled asset was admitted to burn-in", e)
	}
}

func TestStyledResourceCanDeliverPlainTextAndRetainOriginalAfterMetadataEdit(t *testing.T) {
	f := newSubtitleFixture(t)
	body := assHeader + "Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,Good\n"
	m := uploadMutation("stored-style", body, f.item.Token)
	m.Format = "ass"
	m.Render = "styled"
	receipt, e := f.s.Mutate(context.Background(), f.p, f.item.Public, m)
	if e != nil {
		t.Fatal(e)
	}
	edit := m
	edit.OperationID = "edit-style-label"
	edit.ResourceID = receipt.ResourceID
	edit.ExpectedRevision = 1
	edit.Content = nil
	edit.Title = "Changed label"
	second, e := f.s.Mutate(context.Background(), f.p, f.item.Public, edit)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := f.s.Select(context.Background(), f.p, f.item.Public, "session", SelectRequest{OperationID: "plain-style", Generation: 1, ExpectedRevision: 1, Mode: "track", ResourceID: receipt.ResourceID, ResourceRevision: second.Revision, Rendering: "text", OffsetUS: "0"})
	if e != nil || plan.Renderer != "external_text" {
		t.Fatal(plan, e)
	}
	reader, e := f.s.OpenDocument(context.Background(), f.p, f.item.Public, "session", receipt.ResourceID, second.Revision)
	if e != nil {
		t.Fatal(e)
	}
	var d Document
	e = json.NewDecoder(reader).Decode(&d)
	reader.Close()
	if e != nil || len(d.Cues) != 1 || d.Cues[0].Text != "Good" {
		t.Fatal(d, e)
	}
	original, e := f.s.OriginalAsset(context.Background(), f.p, f.item.Public, "session", receipt.ResourceID, second.Revision)
	if e != nil || string(original.Data) != body {
		t.Fatal(original, e)
	}
}

func TestDiscoveredASSCachePreservesStyleAndOriginal(t *testing.T) {
	f := newSubtitleFixture(t)
	persistDiscovered(t, f, []Track{{Origin: "embedded", StreamIndex: 3, Format: "ass", Language: "en"}})
	catalog, err := f.s.List(context.Background(), f.p, f.item.Public)
	if err != nil || len(catalog.Resources) != 1 {
		t.Fatal(catalog, err)
	}
	resource := catalog.Resources[0]
	if resource.Renderer != "burn_in" {
		t.Fatalf("discovered styled track advertised as %q", resource.Renderer)
	}
	body := assHeader + "Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,{\\pos(100,120)}Styled\r\n"
	f.s.Extractor = &cacheTestExtractor{body: []byte(body)}
	if err = f.s.ensureCached(context.Background(), f.p, f.item.Public, resource.ID, resource.Revision); err != nil {
		t.Fatal(err)
	}
	catalog, err = f.s.List(context.Background(), f.p, f.item.Public)
	if err != nil || catalog.Resources[0].Renderer != "burn_in" || catalog.Resources[0].Revision != resource.Revision {
		t.Fatalf("cache changed the discovery's rendering or revision: %+v %v", catalog, err)
	}
	plan, err := f.s.Select(context.Background(), f.p, f.item.Public, "session", SelectRequest{OperationID: "draw-discovered-ass", Generation: 1, ExpectedRevision: 1, Mode: "track", ResourceID: resource.ID, ResourceRevision: resource.Revision, Rendering: "ass", OffsetUS: "0"})
	if err != nil || plan.Renderer != "client" || plan.Generation != 1 {
		t.Fatal(plan, err)
	}
	original, err := f.s.OriginalAsset(context.Background(), f.p, f.item.Public, "session", resource.ID, resource.Revision)
	if err != nil || string(original.Data) != body {
		t.Fatalf("cache stripped the authored style: %v", err)
	}
}
