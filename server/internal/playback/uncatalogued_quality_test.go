package playback

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestNamedQualityWithoutSourceKeepsItsCeilings(t *testing.T) {
	f := newReplanFixture(t, 1_000_000)
	ctx, err := f.s.prepareDelivery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	asset := firstCatalogAssetToken(t, f.db)
	for _, automatic := range []bool{false, true} {
		choice, ok := namedQualityChoice("720p-high")
		if !ok {
			t.Fatal("fixture rung is missing")
		}
		if automatic {
			choice.QualityID = "automatic:" + choice.QualityID
		}
		err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassPlaybackStart, func(tx *sql.Tx) error {
			inputs, err := f.s.planInputsTx(ctx, tx, f.p, f.item, asset, "mp4", "h264", "aac", 100, nil, false, nil, &choice)
			if err != nil {
				return err
			}
			if inputs.input.Source != nil || inputs.input.Target.MaxVideoHeight != choice.MaxVideoHeight || inputs.input.Target.MaxVideoBitrateBPS != choice.MaxVideoBitrateBPS {
				t.Fatalf("unmeasured source lost chosen ceilings: %+v", inputs.input)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
