package playback

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
)

func writeMemberAwayQuality(t *testing.T, db *sql.DB, account string, quality streamlimits.AwayQuality) {
	t.Helper()
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassEstablishedPlayback, func(tx *sql.Tx) error {
		return streamlimits.WriteAwayQualityTx(context.Background(), tx, account, quality)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMemberAwayQualityNarrowsTheActualPlanAndPreview(t *testing.T) {
	f := newReplanFixture(t, 40_000_000)
	cfg := DefaultDeliveryConfiguration()
	cfg.MaxVideoBitrateBPS = 3_000_000 // Deliberately not a named ladder bitrate.
	f.configure(cfg)
	writeMemberAwayQuality(t, f.db, f.p.AccountID, "720p-high")
	// This rung is same-as-source and normalizes back to original; the member's
	// independent 720p height and the tighter server bitrate must survive it.
	choice, _ := namedQualityChoice("2160p")
	choice.Remote = true
	preview, err := f.s.PlanV1(context.Background(), f.p, f.item, choice)
	if err != nil {
		t.Fatal(err)
	}
	session := f.start("member-away", choice)
	started := f.plan(session.ID)
	for _, plan := range []*DeliveryPlan{&preview, started} {
		if plan.VideoAction != "convert" || plan.TargetHeight < 1 || plan.TargetHeight > 720 || plan.VideoBitrateBPS < 1 || plan.VideoBitrateBPS > 3_000_000 {
			t.Fatalf("away ceiling escaped actual planning: %+v", plan)
		}
	}
	if started.Target == nil || started.Target.MaxVideoHeight != 720 || started.Target.MaxVideoBitrateBPS != 3_000_000 {
		t.Fatalf("lost admitted ceilings: %+v", started.Target)
	}
	// The same source on the home connection retains its uncapped picture.
	home := f.start("member-home", V1Choice{QualityID: "original"})
	if plan := f.plan(home.ID); plan.VideoAction != "copy" || plan.Target.MaxVideoHeight != 0 {
		t.Fatalf("away ceiling applied at home: %+v", plan)
	}
}

func TestMemberAwayQualityIsRereadForReplansAndFallback(t *testing.T) {
	f := newReplanFixture(t, 1_600_000)
	writeMemberAwayQuality(t, f.db, f.p.AccountID, "1080p-high")
	session := f.start("member-lowered", V1Choice{Remote: true})
	started := f.plan(session.ID)
	if started.VideoAction != "copy" {
		t.Fatalf("source should fit original ceiling: %+v", started)
	}
	writeMemberAwayQuality(t, f.db, f.p.AccountID, "480p")
	fallback, err := f.h.downgradeCopyPlan(context.Background(), session.ID, started)
	if err != nil {
		t.Fatal(err)
	}
	if fallback.TargetHeight > 480 || fallback.VideoBitrateBPS > 1_500_000 {
		t.Fatalf("fallback ignored current member ceiling: %+v", fallback)
	}
	if err = f.subtitle(remoteRequest(0), session.ID, styledSubtitle); err != nil {
		t.Fatal(err)
	}
	if plan := f.plan(session.ID); plan.TargetHeight > 480 || plan.VideoBitrateBPS > 1_500_000 {
		t.Fatalf("subtitle replan widened current ceiling: %+v", plan)
	}
	writeMemberAwayQuality(t, f.db, f.p.AccountID, streamlimits.AwayQualityServer)
	if err = f.subtitle(remoteRequest(0), session.ID, nil); err != nil {
		t.Fatal(err)
	}
	if plan := f.plan(session.ID); plan.Target.MaxVideoHeight > 480 || plan.Target.MaxVideoBitrateBPS > 1_500_000 {
		t.Fatalf("later replan raised an admitted ceiling: %+v", plan.Target)
	}
}

func TestMemberChannelCeilingKeepsTheOriginalCapabilities(t *testing.T) {
	original := ClientProfile{Video: []ClientVideoCodec{{Codec: "h264", MaxHeight: 1080, Limits: []ClientVideoLimit{{MaxHeight: 1080}}}}}
	narrowed := memberChannelProfile(original, ReplanCeilings{Remote: true, MaxVideoBitrateBPS: 4_000_000, MaxVideoHeight: 720})
	if narrowed.Video[0].MaxHeight != 720 || narrowed.Video[0].Limits[0].MaxHeight != 720 || narrowed.Video[0].MaxBitrateBPS != 4_000_000 {
		t.Fatalf("channel ceiling incomplete: %+v", narrowed)
	}
	if original.Video[0].MaxHeight != 1080 || original.Video[0].Limits[0].MaxHeight != 1080 || original.Video[0].MaxBitrateBPS != 0 {
		t.Fatalf("shared device capabilities mutated: %+v", original)
	}
}

func TestMemberAwayQualityReadFailsClosedAndLeavesOtherAccountsAlone(t *testing.T) {
	f := newReplanFixture(t, 1_600_000)
	writeMemberAwayQuality(t, f.db, f.p.AccountID, "720p-high")
	if err := dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassEstablishedPlayback, func(tx *sql.Tx) error {
		home, err := memberAwayCeilingsTx(context.Background(), tx, f.p.AccountID, false)
		if err != nil || home.MaxVideoBitrateBPS != 0 || home.MaxVideoHeight != 0 {
			t.Fatalf("home ceiling: %+v %v", home, err)
		}
		remote, err := memberAwayCeilingsTx(context.Background(), tx, f.p.AccountID, true)
		if err != nil || remote.MaxVideoBitrateBPS != 4_000_000 || remote.MaxVideoHeight != 720 {
			t.Fatalf("remote ceiling: %+v %v", remote, err)
		}
		if _, err = memberAwayCeilingsTx(context.Background(), tx, "missing-account", true); err == nil {
			t.Fatal("an unreadable account became uncapped")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
