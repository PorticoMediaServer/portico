package playback

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// Burning a subtitle in, and turning it off again, re-plans a presentation
// under the ceilings it was started with: the viewer's own quality limit, and
// the member's remote cap on a presentation that asked for no limit.
func TestSubtitleReplanKeepsThePresentationCeilings(t *testing.T) {
	db, s, p, _, item, _ := activityFixture(t)
	s.ConfigureHLS(&HLS{})
	asset := firstCatalogAssetToken(t, db)
	// A 1080p source at about 40 Mbit/s.
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) {
		a.Container, a.Width, a.Height, a.Size, a.Duration = "mkv", 1920, 1080, 500_000_000, 100
	})
	ctx := context.Background()
	replan := func(t *testing.T, session string, resource *subtitles.Resource) *DeliveryPlan {
		t.Helper()
		request := WithReplanCeilings(ctx, ReplanCeilings{})
		request, err := s.prepareDelivery(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := dbwork.WithWriteTx(request, db, dbwork.ClassEstablishedPlayback, func(tx *sql.Tx) error {
			return s.ChangeSubtitleDeliveryTx(request, tx, p, session, resource)
		}); err != nil {
			t.Fatalf("subtitle re-plan: %v", err)
		}
		plan, err := loadDeliveryPlan(ctx, db, session)
		if err != nil || plan == nil {
			t.Fatal(plan, err)
		}
		return plan
	}
	styled := &subtitles.Resource{ID: "styled", Revision: 1, Format: "ass"}
	for _, test := range []struct {
		name    string
		choice  V1Choice
		bitrate int
		height  int
	}{
		{"the viewer's limit", V1Choice{Limit: true, MaxVideoBitrateBPS: 1_500_000, MaxVideoHeight: 480}, 1_500_000, 480},
		{"the member's remote cap", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 2_000_000}, 2_000_000, 1080},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, err := s.CreateV1(ctx, p, item, test.name, test.choice, nil)
			if err != nil {
				t.Fatal(err)
			}
			started, err := loadDeliveryPlan(ctx, db, session.ID)
			if err != nil || started == nil || started.VideoAction != "convert" || started.VideoBitrateBPS < 1 || started.VideoBitrateBPS > test.bitrate || started.TargetHeight > test.height {
				t.Fatalf("the presentation did not start under its ceilings: %+v %v", started, err)
			}
			burned := replan(t, session.ID, styled)
			if burned.BurnIn == nil || burned.VideoAction != "convert" {
				t.Fatalf("burn-in was not planned: %+v", burned)
			}
			if burned.VideoBitrateBPS != started.VideoBitrateBPS || burned.TargetHeight != started.TargetHeight || burned.QualityID != started.QualityID {
				t.Fatalf("burn-in planned %d bit/s at %dp (%s), the presentation started at %d bit/s at %dp (%s)", burned.VideoBitrateBPS, burned.TargetHeight, burned.QualityID, started.VideoBitrateBPS, started.TargetHeight, started.QualityID)
			}
			off := replan(t, session.ID, nil)
			if off.BurnIn != nil || off.VideoBitrateBPS != started.VideoBitrateBPS || off.TargetHeight != started.TargetHeight {
				t.Fatalf("turning burn-in off planned %d bit/s at %dp, the presentation started at %d bit/s at %dp", off.VideoBitrateBPS, off.TargetHeight, started.VideoBitrateBPS, started.TargetHeight)
			}
		})
	}
}
