package playback

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestRealMultiAudioFactsPublicationOffersAndRevision(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	probe := decodertest.QualifiedFFprobe(t)
	root := t.TempDir()
	path := filepath.Join(root, "Two Languages (2026).mp4")
	cmd := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=160x90:d=1", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-f", "lavfi", "-i", "sine=frequency=880:duration=1", "-map", "0:v", "-map", "1:a", "-map", "2:a", "-c:v", "libx264", "-c:a", "aac", "-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=fra", "-disposition:a:0", "0", "-disposition:a:1", "default", "-shortest", path)
	if raw, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(raw))
	}
	facts, e := (assets.Probe{Binary: probe}).Inspect(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	if len(facts.Streams) != 3 || facts.Streams[1].Language != "eng" || facts.Streams[2].Language != "fra" || facts.Streams[1].Default || !facts.Streams[2].Default {
		t.Fatal("actual streams", facts.Streams)
	}
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	_, item, token := catalogFixture(t, db, "lib", root, compactcatalog.Movie, "two-languages", "Two Languages (2026)", compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height, Duration: facts.Duration})
	cat := catalogtest.New(t, db)
	writeStreams := func(streams []assets.Stream) {
		t.Helper()
		cat.Write(func(_ context.Context, tx *sql.Tx) error {
			return assets.PersistStreams(tx, token, info.Size(), info.ModTime().UnixNano(), streams)
		})
	}
	writeStreams(facts.Streams)
	p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	service := New(db)
	scope := OffersScope{"server", "lib", item, "fence"}
	first, e := service.Offers(context.Background(), p, scope, "", "")
	if e != nil || len(first.Sources) != 1 || first.Sources[0].FactsRevision != 1 || len(first.Sources[0].Streams) != 3 {
		t.Fatal(first, e)
	}
	for _, st := range first.Sources[0].Streams {
		// Audio tracks are selectable (by stream index on the play command);
		// nothing else is offered as a choice it cannot honour.
		if st.Type == "audio" && (!st.Enabled || st.Reason != "") {
			t.Fatal("audio track not offered", st)
		}
		if st.Type != "audio" && (st.Enabled || st.Reason == "") {
			t.Fatal("fake selection offer")
		}
	}
	session, e := service.Create(p, item, "automatic", "play")
	if e != nil {
		t.Fatal(e)
	}
	current, e := service.Offers(context.Background(), p, scope, session.ID, first.Revision)
	if e != nil || current.Current.AudioSelection != "platform_default" {
		t.Fatal(current, e)
	}
	other := p
	other.ProfileID = "other"
	if _, e = service.Offers(context.Background(), other, scope, session.ID, ""); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("session leaked", e)
	}
	writeStreams(facts.Streams)
	unchanged, e := service.Offers(context.Background(), p, scope, "", first.Revision)
	if e != nil || unchanged.Sources[0].FactsRevision != 1 {
		t.Fatal("unchanged scan churn", e)
	}
	facts.Streams[2].Title = "French audio"
	writeStreams(facts.Streams)
	if _, e = service.Offers(context.Background(), p, scope, "", first.Revision); !errors.Is(e, ErrStaleOffer) {
		t.Fatal("changed facts fence", e)
	}
	var aid string
	if e = db.QueryRow(`SELECT a.token FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE e.public_id=pid_blob(?) ORDER BY l.part_index,a.id LIMIT 1`, item).Scan(&aid); e != nil {
		t.Fatal(e)
	}
	_, siblingItem := linkExistingCatalogAsset(t, db, "lib", compactcatalog.Movie, "sibling", "Sibling", aid)
	if _, e = db.Exec(`DELETE FROM playback_sessions WHERE id=?`, session.ID); e != nil {
		t.Fatal(e)
	}
	deleteCatalogEntity(t, db, item)
	settlePlaybackCatalog(t, db)
	scope.ItemID = siblingItem
	siblingOffers, e := service.Offers(context.Background(), p, scope, "", "")
	if e != nil || len(siblingOffers.Sources[0].Streams) != 3 {
		t.Fatal("shared facts removed", e)
	}
	updateCatalogAsset(t, db, aid, func(a *compactcatalog.Asset) { a.ModifiedNS++ })
	settlePlaybackCatalog(t, db)
	stale, e := service.Offers(context.Background(), p, scope, "", "")
	if e != nil || stale.Sources[0].FactsStatus != "stale" || len(stale.Sources[0].Streams) != 0 {
		t.Fatal("stale facts represented as current", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = service.Offers(ctx, p, scope, "", ""); !errors.Is(e, context.Canceled) {
		t.Fatal("cancellation", e)
	}
}
func TestOffersCapacityAndLegacyUnknown(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	id, item, _ := catalogFixture(t, db, "l", "/source", compactcatalog.Movie, "i", "I", compactcatalog.Asset{Path: "/source/0", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1, Height: 1, Duration: 10})
	for n := 1; n < 33; n++ {
		path := fmt.Sprintf("/source/%d", n)
		linkCatalogAsset(t, db, id, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1, Height: 1, Duration: 10})
	}
	s := New(db)
	settlePlaybackCatalog(t, db)
	scope := OffersScope{"server", "l", item, "fence"}
	complete, e := s.Offers(context.Background(), identity.Principal{}, scope, "", "")
	if e != nil || len(complete.Sources) != 33 {
		t.Fatalf("sources: %d, %v", len(complete.Sources), e)
	}
	ctx := context.Background()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT asset_id FROM catalog_asset_links WHERE entity_id=? AND asset_id!=(SELECT id FROM catalog_assets WHERE path=?)`, id, "/source/0")
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	var assetIDs []int64
	for rows.Next() {
		var assetID int64
		if err = rows.Scan(&assetID); err != nil {
			rows.Close()
			tx.Rollback()
			t.Fatal(err)
		}
		assetIDs = append(assetIDs, assetID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		tx.Rollback()
		t.Fatal(err)
	}
	rows.Close()
	for _, assetID := range assetIDs {
		if err = compactcatalog.UnlinkAssetTx(ctx, tx, id, assetID); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	settlePlaybackCatalog(t, db)
	o, e := s.Offers(context.Background(), identity.Principal{}, scope, "", "")
	if e != nil || o.Sources[0].FactsStatus != "unavailable" || o.Sources[0].FactsRevision != 0 {
		t.Fatal(o, e)
	}
	if _, e = db.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) VALUES(?,1,1,1,'test')`, o.Sources[0].ID); e != nil {
		t.Fatal(e)
	}
	tx, e = db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 129; n++ {
		if _, e = tx.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES(?,?,'audio','aac','eng','',2,'stereo',0,0)`, o.Sources[0].ID, n); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	o, e = s.Offers(context.Background(), identity.Principal{}, scope, "", "")
	if e != nil || len(o.Sources[0].Streams) != 129 {
		t.Fatalf("streams: %d, %v", len(o.Sources[0].Streams), e)
	}
}

func TestStreamOverflowRollsBackPhysicalPublication(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	token := addCatalogAsset(t, db, compactcatalog.Asset{Path: "/fixture", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1, Height: 1, Duration: 10})
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	updateCatalogAssetTx(t, context.Background(), tx, token, func(a *compactcatalog.Asset) { a.AudioCodec = "changed" })
	streams := make([]assets.Stream, 129)
	for i := range streams {
		streams[i] = assets.Stream{Index: i, Type: "audio", Codec: "aac"}
	}
	if e = assets.PersistStreams(tx, token, 1, 1, streams); e == nil {
		t.Fatal("overflow accepted")
	}
	tx.Rollback()
	var codec string
	db.QueryRow(`SELECT audio_codec FROM catalog_assets WHERE token=?`, token).Scan(&codec)
	if codec != "aac" {
		t.Fatal("physical publication not atomic")
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM asset_stream_facts`).Scan(&n)
	if n != 0 {
		t.Fatal("partial stream facts published")
	}
}
