package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// detailRefreshFixture is a real catalogue fixture: one library, one linked
// movie and one exact inventory object under a confirmed local source root. The
// asset deliberately has no stream facts, which is the state the first-play
// refresh exists for. The file on disk is the same size and modified time the
// catalogue and inventory rows name, so the descriptor binding and the
// inventory evidence agree without a fabricated custody row.
type detailRefreshFixture struct {
	catalog *catalogtest.Catalog
	service *Service
	token   string
}

func newDetailRefreshFixture(t *testing.T) *detailRefreshFixture {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "movie.mkv")
	if err := os.WriteFile(path, make([]byte, 4096), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.Open(t)
	lib := c.Library("refresh", "Refresh", "movie", root)
	movie := c.Movie(lib, path, "Refresh", 2026)
	// catalogtest.File writes its own size/mtime; align the row with the real
	// file so the descriptor binding and the inventory stat can both exist.
	updateCatalogAsset(t, c.DB, movie.Token, func(a *compactcatalog.Asset) {
		a.Size = info.Size()
		a.ModifiedNS = info.ModTime().UnixNano()
	})
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := storage.FileObjectIdentity(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	rootIdentity, err := storage.FileObjectIdentity(dir)
	dir.Close()
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE library_sources SET root_identity=?,identity_confirmed=1,health='ready' WHERE library_id='refresh'`, rootIdentity)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns)
 SELECT 'refresh-object',id,?,incarnation,'movie.mkv',?,'r1','{}',?,? FROM library_sources WHERE library_id='refresh'`,
		movie.Token, storage.StableObjectIdentity(identity, rootIdentity), info.Size(), info.ModTime().UnixNano())
	return &detailRefreshFixture{catalog: c, service: New(c.DB), token: movie.Token}
}

func (f *detailRefreshFixture) observe(t *testing.T) playbackDetailObservation {
	t.Helper()
	o, ok, err := f.service.observeDetailRefresh(context.Background(), f.token)
	if err != nil || !ok {
		t.Fatalf("descriptor-bound observation: ok=%v err=%v", ok, err)
	}
	if err = o.bindDescriptor(); err != nil {
		t.Fatalf("descriptor binding: %v", err)
	}
	return o
}

func (f *detailRefreshFixture) streamFacts() assets.Facts {
	return assets.Facts{
		Container: "matroska", VideoCodec: "hevc", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 100,
		Streams: []assets.Stream{
			{Index: 0, Type: "video", Codec: "hevc", Default: true, Detail: assets.StreamDetail{
				Width: 1920, Height: 1080, BitDepth: 10, DolbyVisionProfile: 8,
				StreamBreadth: assets.StreamBreadth{DolbyVisionRPU: true, DolbyVisionBaseLayer: true, DolbyVisionMetadataKnown: true, DolbyVisionEnhancementType: "mel", DolbyVisionCMVersion: "2.9"},
			}},
			{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Default: true, Detail: assets.StreamDetail{SampleRate: 48000}},
		},
	}
}

func TestDetailRefreshAcceptsPendingAndAppliesExactlyOnce(t *testing.T) {
	f := newDetailRefreshFixture(t)
	o := f.observe(t)
	receipt, err := f.service.publishDetailRefresh(context.Background(), o, f.streamFacts())
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatalf("producer claimed a state before the source owner applied it: %+v", receipt)
	}
	if receipt.ChangeID == 0 || receipt.Slot != 0 || receipt.FamiliesMasked != 3 || receipt.RowsStaged == 0 {
		t.Fatalf("pending receipt does not name the staged families: %+v", receipt)
	}
	var kind int
	var witness []byte
	if err = f.catalog.DB.QueryRow(`SELECT kind,fingerprint FROM catalog_info_input_witnesses WHERE change_id=? AND ordinal=0`, receipt.ChangeID).Scan(&kind, &witness); err != nil {
		t.Fatal(err)
	}
	zero := true
	for _, b := range witness {
		zero = zero && b == 0
	}
	if kind != compactcatalog.InfoWitnessPlayback || len(witness) != 32 || zero {
		t.Fatalf("witness kind=%d fingerprint=%x", kind, witness)
	}
	var canonical int
	if err = f.catalog.DB.QueryRow(`SELECT count(*) FROM asset_streams WHERE asset_id=?`, f.token).Scan(&canonical); err != nil || canonical != 0 {
		t.Fatalf("unaccepted playback input reached canonical streams: %d %v", canonical, err)
	}

	f.catalog.Drain()
	var revision int64
	var detailVersion int
	if err = f.catalog.DB.QueryRow(`SELECT revision,detail_version FROM asset_stream_facts WHERE asset_id=?`, f.token).Scan(&revision, &detailVersion); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || detailVersion != assets.StreamDetailVersion {
		t.Fatalf("applied stream fact revision=%d detail=%d", revision, detailVersion)
	}
	if err = f.catalog.DB.QueryRow(`SELECT count(*) FROM asset_streams WHERE asset_id=?`, f.token).Scan(&canonical); err != nil || canonical != 2 {
		t.Fatalf("applied stream rows=%d %v", canonical, err)
	}
	var breadth int
	if err = f.catalog.DB.QueryRow(`SELECT count(*) FROM asset_stream_breadth WHERE asset_id=? AND stream_index=0 AND dovi_rpu=1 AND dovi_base_layer=1`, f.token).Scan(&breadth); err != nil || breadth != 1 {
		t.Fatalf("applied breadth rows=%d %v", breadth, err)
	}

	// The same observation cannot apply twice: the probe revision has moved, so
	// the producer fails closed instead of staging a second body.
	var witnessesBefore int
	if err = f.catalog.DB.QueryRow(`SELECT count(*) FROM catalog_info_input_witnesses WHERE kind=?`, compactcatalog.InfoWitnessPlayback).Scan(&witnessesBefore); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.publishDetailRefresh(context.Background(), o, f.streamFacts()); !errors.Is(err, compactcatalog.ErrInfoInputDrift) {
		t.Fatalf("second apply was not refused: %v", err)
	}
	var witnessesAfter int
	if err = f.catalog.DB.QueryRow(`SELECT count(*) FROM catalog_info_input_witnesses WHERE kind=?`, compactcatalog.InfoWitnessPlayback).Scan(&witnessesAfter); err != nil || witnessesAfter != witnessesBefore {
		t.Fatalf("refused retry claimed another playback witness: %d -> %d %v", witnessesBefore, witnessesAfter, err)
	}
	if err = f.catalog.DB.QueryRow(`SELECT revision FROM asset_stream_facts WHERE asset_id=?`, f.token).Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("refused retry moved the applied revision: %d %v", revision, err)
	}
}

func TestDetailRefreshRefusesMovedCustodyBeforeAnyBody(t *testing.T) {
	f := newDetailRefreshFixture(t)
	o := f.observe(t)
	f.catalog.Exec(`UPDATE inventory_objects SET state='missing' WHERE id='refresh-object'`)
	var before, after int
	if err := f.catalog.DB.QueryRow(`SELECT count(*) FROM catalog_info_input_witnesses WHERE kind=?`, compactcatalog.InfoWitnessPlayback).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.publishDetailRefresh(context.Background(), o, f.streamFacts()); !errors.Is(err, compactcatalog.ErrInfoInputDrift) {
		t.Fatalf("moved custody was not refused: %v", err)
	}
	if err := f.catalog.DB.QueryRow(`SELECT count(*) FROM catalog_info_input_witnesses WHERE kind=?`, compactcatalog.InfoWitnessPlayback).Scan(&after); err != nil || after != before {
		t.Fatalf("refused custody check claimed a body: %d -> %d %v", before, after, err)
	}
}

func TestDetailRefreshRefusesMovedAssetStatBeforeAnyBody(t *testing.T) {
	f := newDetailRefreshFixture(t)
	o := f.observe(t)
	updateCatalogAsset(t, f.catalog.DB, f.token, func(a *compactcatalog.Asset) { a.ModifiedNS++ })
	if _, err := f.service.publishDetailRefresh(context.Background(), o, f.streamFacts()); !errors.Is(err, compactcatalog.ErrInfoInputDrift) {
		t.Fatalf("moved asset stat was not refused: %v", err)
	}
	var witnesses int
	if err := f.catalog.DB.QueryRow(`SELECT count(*) FROM catalog_info_input_witnesses WHERE kind=?`, compactcatalog.InfoWitnessPlayback).Scan(&witnesses); err != nil || witnesses != 0 {
		t.Fatalf("refused stat check staged a playback witness: %d %v", witnesses, err)
	}
}

func TestDetailRefreshNeedsExactInventoryCustody(t *testing.T) {
	f := newDetailRefreshFixture(t)
	f.catalog.Exec(`DELETE FROM inventory_objects WHERE id='refresh-object'`)
	if _, ok, err := f.service.observeDetailRefresh(context.Background(), f.token); err != nil || ok {
		t.Fatalf("unbound file was observed: ok=%v err=%v", ok, err)
	}
}

func TestPlaybackDetailWitnessBindsDescriptorSnapshotAndFacts(t *testing.T) {
	o := playbackDetailObservation{
		AssetID: 7, Token: "token", Path: "/films/one.mkv", Size: 10, ModifiedNS: 20,
		Container: "matroska", DetailVersion: 0, ObjectID: "object", ObjectRevision: "r1",
		ObjectIdentity: "ino:1", SourceID: "source", SourceGeneration: 3,
		RootIncarnation: "inc", RootIdentity: "dev:1:ino:2", Root: "/films", RelativePath: "one.mkv",
	}
	input, err := playbackDetailInputFingerprint(assets.Facts{Container: "matroska", Streams: []assets.Stream{{Index: 0, Type: "video", Codec: "hevc", Detail: assets.StreamDetail{StreamBreadth: assets.StreamBreadth{DolbyVisionRPU: true}}}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := playbackDetailWitnessFingerprint(o, input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := playbackDetailWitnessFingerprint(o, input)
	if err != nil || again != first {
		t.Fatalf("identical observation did not reuse its witness: %v", err)
	}
	moved := o
	moved.ObjectIdentity = "ino:2"
	other, err := playbackDetailWitnessFingerprint(moved, input)
	if err != nil || other == first {
		t.Fatalf("descriptor identity is not in the witness: %v", err)
	}
	changed, err := playbackDetailInputFingerprint(assets.Facts{Container: "matroska", Streams: []assets.Stream{{Index: 0, Type: "video", Codec: "hevc"}}})
	if err != nil || changed == input {
		t.Fatalf("technical child change is not in the input fingerprint: %v", err)
	}
}
