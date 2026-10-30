package compactcatalog_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestInfoSourceResumesOnlyCommittedRawPrefix(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("input", "Input", "movie", "/input")
	movie := c.Movie(lib, "/input/one.mkv", "One", 2026)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	stopped := false
	interrupted := errors.New("producer interrupted")
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
	prepare := func(ctx context.Context, tx *sql.Tx) (compactcatalog.InfoInputSpec, []compactcatalog.InfoSourceFamily, error) {
		var epoch int64
		if err := tx.QueryRowContext(ctx, `SELECT info_epoch FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&epoch); err != nil {
			return compactcatalog.InfoInputSpec{}, nil, err
		}
		spec := compactcatalog.InfoInputSpec{AssetID: movie.Asset, Token: movie.Token, ExpectedEpoch: epoch, FamilyMask: 3, Fingerprint: [32]byte{1}, Witnesses: []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: [32]byte{1}}}}
		families := []compactcatalog.InfoSourceFamily{
			{Number: 1, Count: 1, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
				return compactcatalog.InfoStreamFactRow{InputOrdinal: i, AssetID: movie.Token, Revision: 1, Size: size, ModifiedNS: modified, Fingerprint: "test-input"}, nil
			}},
			{Number: 2, Count: 129, Row: func(_ context.Context, _ *sql.Tx, i int64) (compactcatalog.InfoStageRow, error) {
				if i == 17 && !stopped {
					stopped = true
					return nil, interrupted
				}
				return compactcatalog.InfoStreamRow{InputOrdinal: i, AssetID: movie.Token, StreamIndex: i, Type: "audio", Codec: "aac"}, nil
			}},
		}
		return spec, families, nil
	}
	_, err := compactcatalog.PublishInfoSource(context.Background(), c.DB, check, prepare)
	if !errors.Is(err, interrupted) {
		t.Fatal("missing interruption", err)
	}
	var staged, phase int64
	if err = c.DB.QueryRow(`SELECT f.rows_staged,h.phase FROM catalog_info_input_families f JOIN catalog_info_changes h ON h.id=f.change_id WHERE f.family=2`).Scan(&staged, &phase); err != nil || staged != 16 || phase != 0 {
		t.Fatal("uncommitted input prefix was accepted", staged, phase, err)
	}
	var canonical int
	if err = c.DB.QueryRow(`SELECT count(*) FROM asset_streams WHERE asset_id=?`, movie.Token).Scan(&canonical); err != nil || canonical != 0 {
		t.Fatal("unaccepted input reached canonical facts", canonical, err)
	}
	receipt, err := compactcatalog.PublishInfoSource(context.Background(), c.DB, check, prepare)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending || receipt.RowsStaged != 130 {
		t.Fatal("retry receipt", receipt, err)
	}
	c.Drain()
	if err = c.DB.QueryRow(`SELECT count(*) FROM asset_streams WHERE asset_id=?`, movie.Token).Scan(&canonical); err != nil || canonical != 129 {
		t.Fatal("lost or duplicated resumed input", canonical, err)
	}
	var ready bool
	if err = c.DB.QueryRow(`SELECT info_applied_epoch=info_desired_epoch AND info_source_blockers=0 AND info_availability_pending=0 FROM catalog_entities WHERE id=?`, movie.ID).Scan(&ready); err != nil || !ready {
		t.Fatal("source holder not released", ready, err)
	}
}
