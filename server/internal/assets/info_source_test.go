package assets_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestInfoStreamsQueuedInputsKeepDistinctTechnicalRevisions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(lib, "/m/one.mkv", "One", 2026)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	check := func(ctx context.Context, tx *sql.Tx) error {
		var current bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_assets WHERE id=? AND token=? AND size=? AND modified_ns=?)`, movie.Asset, movie.Token, size, modified).Scan(&current)
		if err != nil {
			return err
		}
		if !current {
			return compactcatalog.ErrInfoInputDrift
		}
		return nil
	}
	publish := func(marker byte, codec string) compactcatalog.InfoAcceptReceipt {
		receipt, err := compactcatalog.PublishInfoSource(context.Background(), c.DB, check, func(ctx context.Context, tx *sql.Tx) (compactcatalog.InfoInputSpec, []compactcatalog.InfoSourceFamily, error) {
			var epoch int64
			if err := tx.QueryRowContext(ctx, `SELECT info_epoch FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&epoch); err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
			families, err := assets.BuildInfoStreams(ctx, tx, movie.Asset, movie.Token, size, modified, []assets.Stream{{Index: 0, Type: "audio", Codec: codec, Default: true, Detail: assets.StreamDetail{StreamBreadth: assets.StreamBreadth{AudioDescription: true}}}})
			if err != nil {
				return compactcatalog.InfoInputSpec{}, nil, err
			}
			var mask int64
			for _, f := range families {
				mask |= 1 << uint(f.Number-1)
			}
			spec := compactcatalog.InfoInputSpec{AssetID: movie.Asset, Token: movie.Token, ExpectedEpoch: epoch, FamilyMask: mask, Fingerprint: [32]byte{marker}, Witnesses: []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: [32]byte{marker}}}}
			return spec, families, nil
		})
		if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
			t.Fatal("publication receipt", receipt, err)
		}
		return receipt
	}
	first := publish(1, "aac")
	second := publish(2, "flac")
	if first.Slot != 0 || second.Slot != 1 {
		t.Fatal("input slots", first, second)
	}
	for _, want := range []struct{ id, revision int64 }{{first.ChangeID, 1}, {second.ChangeID, 2}} {
		var revision int64
		if err := c.DB.QueryRow(`SELECT revision FROM catalog_info_new_stream_facts WHERE change_id=? AND asset_id=?`, want.id, movie.Token).Scan(&revision); err != nil || revision != want.revision {
			t.Fatal("queued technical revision reused", revision, want.revision, err)
		}
	}
	c.Drain()
	var revision int64
	var codec string
	var description bool
	if err := c.DB.QueryRow(`SELECT f.revision,s.codec,b.audio_description FROM asset_stream_facts f JOIN asset_streams s ON s.asset_id=f.asset_id AND s.stream_index=0 JOIN asset_stream_breadth b ON b.asset_id=s.asset_id AND b.stream_index=s.stream_index WHERE f.asset_id=?`, movie.Token).Scan(&revision, &codec, &description); err != nil || revision != 2 || codec != "flac" || !description {
		t.Fatal("queued frame did not publish exactly", revision, codec, description, err)
	}
}
