package playback

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

type replanFixture struct {
	t    *testing.T
	db   *sql.DB
	s    *Service
	h    *HLS
	p    identity.Principal
	item string
}

// newReplanFixture has one 1080p title in a container that is repackaged
// rather than played as it is. bitrate is the whole file's.
func newReplanFixture(t *testing.T, bitrate int64) *replanFixture {
	t.Helper()
	db, s, p, _, item, _ := activityFixture(t)
	h := &HLS{db: db}
	s.ConfigureHLS(h)
	updateCatalogAsset(t, db, firstCatalogAssetToken(t, db), func(a *compactcatalog.Asset) {
		a.Container, a.Width, a.Height, a.Duration, a.Size = "mkv", 1920, 1080, 100, bitrate*100/8
	})
	return &replanFixture{t: t, db: db, s: s, h: h, p: p, item: item}
}

// A copy admitted under the canonical 2 Mbit/s preset must fit its 720p
// height as well as its bitrate. Other fixtures retain their 1080p source.
func new720pReplanFixture(t *testing.T, bitrate int64) *replanFixture {
	t.Helper()
	f := newReplanFixture(t, bitrate)
	updateCatalogAsset(t, f.db, firstCatalogAssetToken(t, f.db), func(a *compactcatalog.Asset) {
		a.Width, a.Height = 1280, 720
	})
	return f
}

func (f *replanFixture) configure(cfg DeliveryConfiguration) {
	f.s.ConfigureDelivery(fixedSettings{cfg}, nil, nil, "")
	f.h.ConfigureSettings(fixedSettings{cfg})
}

func (f *replanFixture) start(request string, choice V1Choice) Session {
	f.t.Helper()
	session, err := f.s.CreateV1(context.Background(), f.p, f.item, request, choice, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return session
}

func (f *replanFixture) plan(session string) *DeliveryPlan {
	f.t.Helper()
	plan, err := loadDeliveryPlan(context.Background(), f.db, session)
	if err != nil || plan == nil {
		f.t.Fatal(plan, err)
	}
	return plan
}

// subtitle re-plans for a subtitle change the way a request does: ctx carries
// what the caller's admission allows on the connection it arrived on. A ctx
// without it is given a LAN request with no member ceiling.
func (f *replanFixture) subtitle(ctx context.Context, session string, resource *subtitles.Resource) error {
	if _, ok := replanCeilingsFrom(ctx); !ok {
		ctx = WithReplanCeilings(ctx, ReplanCeilings{})
	}
	return f.bareSubtitle(ctx, session, resource)
}

func (f *replanFixture) bareSubtitle(ctx context.Context, session string, resource *subtitles.Resource) error {
	ctx, err := f.s.prepareDelivery(ctx)
	if err != nil {
		return err
	}
	return dbwork.WithWriteTx(ctx, f.db, dbwork.ClassEstablishedPlayback, func(tx *sql.Tx) error {
		return f.s.ChangeSubtitleDeliveryTx(ctx, tx, f.p, session, resource)
	})
}

func remoteRequest(memberBitrate int) context.Context {
	return WithReplanCeilings(context.Background(), ReplanCeilings{Remote: true, MaxVideoBitrateBPS: memberBitrate})
}

// asStoredBefore rewrites a presentation's stored plan to the shape plans had
// before they kept their target, with a field a later build might add, and
// records the v1 session it belongs to as the v1 layer does.
func (f *replanFixture) asStoredBefore(session string) {
	f.t.Helper()
	mustAudioExec(f.t, f.db, `UPDATE playback_delivery_plans SET policy_json=json_set(json_remove(policy_json,'$.sessionTarget'),'$.addedLater',json('{"a":1}')) WHERE session_id=?`, session)
	mustAudioExec(f.t, f.db, `INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,media_session_id,lease_expires_ms,created_ms,updated_ms) VALUES(?,'device',?,?,'local',?,'','vod','viewer','playing','{"quality":{"mode":"original"}}',1,1,?,0,0,0)`, "v1-"+session, f.p.AccountID, f.p.ProfileID, session, session)
	if plan := f.plan(session); plan.Target != nil || plan.Policy == nil {
		f.t.Fatalf("the rewritten plan is not the earlier shape: %+v", plan)
	}
}

var styledSubtitle = &subtitles.Resource{ID: "styled", Revision: 1, Format: "ass"}

// within reports whether a planned value respects a ceiling (zero: none).
func within(value, ceiling int) bool { return ceiling <= 0 || value <= ceiling }

// A v1 presentation stored before plans kept their ceilings cannot be planned
// again: what it started under is not recoverable, and it must not be planned
// under no ceiling at all. A presentation started by a named quality stores
// its ceilings like any other.
func TestReplanRefusesAV1PresentationWhoseCeilingsWereNotStored(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, test := range []struct {
		name   string
		choice V1Choice
	}{
		{"no limit under the member's remote cap", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 2_000_000}},
		{"the viewer's limit", V1Choice{Limit: true, MaxVideoBitrateBPS: 1_500_000, MaxVideoHeight: 480}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newReplanFixture(t, 40_000_000)
			session := f.start("earlier", test.choice)
			started := f.plan(session.ID)
			f.asStoredBefore(session.ID)
			for _, resource := range []*subtitles.Resource{styledSubtitle, nil} {
				if err := f.subtitle(context.Background(), session.ID, resource); !errors.Is(err, ErrQualityUnavailable) {
					t.Fatalf("a subtitle re-plan (burn-in %v) of a presentation with no stored ceilings: %v", resource != nil, err)
				}
			}
			if _, err := f.h.downgradeCopyPlan(context.Background(), session.ID, f.plan(session.ID)); !errors.Is(err, ErrQualityUnavailable) {
				t.Fatalf("a conversion fallback of a presentation with no stored ceilings: %v", err)
			}
			if after := f.plan(session.ID); after.VideoBitrateBPS != started.VideoBitrateBPS || after.TargetHeight != started.TargetHeight || after.BurnIn != nil {
				t.Fatalf("a refused re-plan changed the stored plan: %+v", after)
			}
		})
	}
	t.Run("a legacy session", func(t *testing.T) {
		f := newReplanFixture(t, 40_000_000)
		session, err := f.s.Create(f.p, f.item, "automatic", "legacy")
		if err != nil {
			t.Fatal(err)
		}
		if err = f.subtitle(context.Background(), session.ID, styledSubtitle); err != nil {
			t.Fatalf("a legacy session's burn-in re-plan: %v", err)
		}
		if plan := f.plan(session.ID); plan.BurnIn == nil || plan.Target == nil || plan.QualityID != "automatic" {
			t.Fatalf("legacy re-plan: %+v", plan)
		}
	})
}

// A copied stream that has to be converted after all is converted under the
// presentation's ceilings, and so is every subtitle change after it.
func TestConversionFallbackKeepsThePresentationCeilings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, test := range []struct {
		name    string
		choice  V1Choice
		bitrate int
	}{
		{"the member's remote cap", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 2_000_000}, 2_000_000},
		{"the viewer's limit", V1Choice{Limit: true, MaxVideoBitrateBPS: 2_000_000, MaxVideoHeight: 1080}, 2_000_000},
	} {
		t.Run(test.name, func(t *testing.T) {
			// The remote preset requires 720p; the explicit viewer limit keeps
			// its original 1080p source and independently supplied height.
			var f *replanFixture
			if test.choice.Remote {
				f = new720pReplanFixture(t, 1_600_000)
			} else {
				f = newReplanFixture(t, 1_600_000)
			}
			session := f.start("copied", test.choice)
			started := f.plan(session.ID)
			if started.VideoAction != "copy" || started.Target == nil {
				t.Fatalf("the presentation did not start as a copy under a stored target: %+v", started)
			}
			converted, err := f.h.downgradeCopyPlan(context.Background(), session.ID, started)
			if err != nil {
				t.Fatalf("conversion fallback: %v", err)
			}
			if converted.VideoAction != "convert" || converted.VideoBitrateBPS < 1 || converted.VideoBitrateBPS > test.bitrate || converted.QualityID != started.QualityID {
				t.Fatalf("the fallback converts at %d bit/s (%s) under a %d bit/s ceiling", converted.VideoBitrateBPS, converted.QualityID, test.bitrate)
			}
			for _, resource := range []*subtitles.Resource{styledSubtitle, nil} {
				if err = f.subtitle(context.Background(), session.ID, resource); err != nil {
					t.Fatal(err)
				}
				if plan := f.plan(session.ID); plan.VideoBitrateBPS > test.bitrate || plan.Target == nil || (resource != nil) != (plan.BurnIn != nil) {
					t.Fatalf("after the fallback a subtitle change (burn-in %v) planned %d bit/s: %+v", resource != nil, plan.VideoBitrateBPS, plan)
				}
			}
		})
	}
	t.Run("canonical remote cap converts a taller source at admission", func(t *testing.T) {
		f := newReplanFixture(t, 1_600_000)
		session := f.start("taller-than-preset", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 2_000_000})
		plan := f.plan(session.ID)
		if plan.VideoAction != "convert" || plan.TargetHeight != 720 || plan.VideoBitrateBPS < 1 || plan.VideoBitrateBPS > 2_000_000 || plan.Target == nil {
			t.Fatalf("1080p source bypassed the canonical 2 Mbit/s 720p cap: %+v", plan)
		}
	})

}

// A re-plan never plans above what the presentation started under, whatever
// the owner's ceilings have become since, and never above those either.
func TestReplanNeverRaisesACeiling(t *testing.T) {
	remote := remoteRequest(0)
	t.Run("the owner's ceilings were lifted after the start", func(t *testing.T) {
		f := newReplanFixture(t, 40_000_000)
		strict := DefaultDeliveryConfiguration()
		strict.MaxVideoBitrateBPS, strict.MaxVideoHeight, strict.MaxAudioBitrateBPS = 3_000_000, 480, 96_000
		f.configure(strict)
		session := f.start("capped", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 2_000_000})
		started := f.plan(session.ID)
		if started.VideoAction != "convert" || started.VideoBitrateBPS > 2_000_000 || started.TargetHeight > 480 {
			t.Fatalf("the presentation did not start under its ceilings: %+v", started)
		}
		f.configure(DefaultDeliveryConfiguration())
		for _, resource := range []*subtitles.Resource{styledSubtitle, nil} {
			if err := f.subtitle(remote, session.ID, resource); err != nil {
				t.Fatal(err)
			}
			plan := f.plan(session.ID)
			if plan.VideoBitrateBPS > started.VideoBitrateBPS || plan.TargetHeight > started.TargetHeight || !within(plan.AudioBitrateBPS, 96_000) {
				t.Fatalf("a re-plan (burn-in %v) rose to %d bit/s at %dp, %d bit/s audio; the presentation started at %d bit/s at %dp", resource != nil, plan.VideoBitrateBPS, plan.TargetHeight, plan.AudioBitrateBPS, started.VideoBitrateBPS, started.TargetHeight)
			}
		}
	})
	t.Run("the owner's ceilings were lowered after the start", func(t *testing.T) {
		f := newReplanFixture(t, 40_000_000)
		session := f.start("lowered", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 2_000_000})
		strict := DefaultDeliveryConfiguration()
		strict.MaxVideoBitrateBPS, strict.MaxVideoHeight = 1_000_000, 360
		f.configure(strict)
		for _, resource := range []*subtitles.Resource{styledSubtitle, nil} {
			if err := f.subtitle(remote, session.ID, resource); err != nil {
				t.Fatal(err)
			}
			if plan := f.plan(session.ID); plan.VideoBitrateBPS > 1_000_000 || plan.TargetHeight > 360 {
				t.Fatalf("a re-plan (burn-in %v) planned %d bit/s at %dp above the owner's 1000000 bit/s at 360p", resource != nil, plan.VideoBitrateBPS, plan.TargetHeight)
			}
		}
		// Lifting them again does not give back what the member's cap never allowed.
		f.configure(DefaultDeliveryConfiguration())
		if err := f.subtitle(remote, session.ID, styledSubtitle); err != nil {
			t.Fatal(err)
		}
		if plan := f.plan(session.ID); plan.VideoBitrateBPS > 2_000_000 {
			t.Fatalf("a later re-plan escaped the member's cap: %d bit/s", plan.VideoBitrateBPS)
		}
	})
	t.Run("every ceiling of a stored target", func(t *testing.T) {
		ceilings := []int{0, 96_000, 480, 2_000_000, 8_000_000}
		for _, remote := range []bool{false, true} {
			for _, stored := range ceilings {
				for _, owner := range ceilings {
					for _, member := range ceilings {
						cfg := DefaultDeliveryConfiguration()
						cfg.MaxVideoBitrateBPS, cfg.MaxAudioBitrateBPS, cfg.MaxVideoHeight = owner, owner, owner
						was := QualityTarget{RungID: "limit", MaxVideoBitrateBPS: stored, MaxAudioBitrateBPS: stored, MaxVideoHeight: stored}
						now := narrowTarget(was, remote, member, cfg)
						for name, pair := range map[string][2]int{"video bitrate": {was.MaxVideoBitrateBPS, now.MaxVideoBitrateBPS}, "audio bitrate": {was.MaxAudioBitrateBPS, now.MaxAudioBitrateBPS}, "height": {was.MaxVideoHeight, now.MaxVideoHeight}} {
							if !within(pair[1], pair[0]) || pair[0] > 0 && pair[1] <= 0 {
								t.Fatalf("%s ceiling %d became %d (owner %d, member %d, remote %v)", name, pair[0], pair[1], owner, member, remote)
							}
							if name != "video bitrate" && owner > 0 && (pair[1] <= 0 || pair[1] > owner) {
								t.Fatalf("%s ceiling %d ignores the owner's %d", name, pair[1], owner)
							}
						}
						if member > 0 && (now.MaxVideoBitrateBPS <= 0 || now.MaxVideoBitrateBPS > member) {
							t.Fatalf("a video bitrate ceiling %d ignores the member's %d", now.MaxVideoBitrateBPS, member)
						}
						if remote && owner > 0 && (now.MaxVideoBitrateBPS <= 0 || now.MaxVideoBitrateBPS > owner) {
							t.Fatalf("a remote video bitrate ceiling %d ignores the owner's %d", now.MaxVideoBitrateBPS, owner)
						}
					}
				}
			}
		}
	})
}

// A subtitle change that makes a presentation produce new bytes is planned
// under what the member is allowed now, on the connection the request arrives
// on. No request gets a stream above that: not one that says nothing about its
// limits, not one that claims to be local, not one for another subtitle, not
// one from a different connection than the presentation started on.
func TestSubtitleReplanAppliesTheMembersCurrentCeilings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	another := &subtitles.Resource{ID: "another", Revision: 3, Format: "ass"}
	t.Run("a request that carries no ceilings is refused", func(t *testing.T) {
		f := newReplanFixture(t, 40_000_000)
		session := f.start("bare", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 2_000_000})
		started := f.plan(session.ID)
		for _, resource := range []*subtitles.Resource{styledSubtitle, another, nil} {
			if err := f.bareSubtitle(context.Background(), session.ID, resource); !errors.Is(err, ErrQualityUnavailable) {
				t.Fatalf("a re-plan with no current ceilings (burn-in %v): %v", resource != nil, err)
			}
		}
		if after := f.plan(session.ID); after.VideoBitrateBPS != started.VideoBitrateBPS || after.BurnIn != nil {
			t.Fatalf("a refused re-plan changed the stored plan: %+v", after)
		}
	})
	t.Run("started on the LAN, changed from outside it", func(t *testing.T) {
		f := newReplanFixture(t, 40_000_000)
		session := f.start("moved", V1Choice{})
		if started := f.plan(session.ID); started.VideoAction != "copy" {
			t.Fatalf("the LAN presentation did not start as a copy: %+v", started)
		}
		// A request that claims a local network is still what admission says it is.
		claimsLocal := WithDeliveryContext(remoteRequest(2_000_000), DeliveryContext{NetworkClass: NetworkLocal, ServerLocality: "local", TransportClass: "ethernet"})
		for _, resource := range []*subtitles.Resource{styledSubtitle, another, nil} {
			if err := f.subtitle(claimsLocal, session.ID, resource); err != nil {
				t.Fatal(err)
			}
			if plan := f.plan(session.ID); plan.VideoAction != "convert" || plan.VideoBitrateBPS > 2_000_000 {
				t.Fatalf("from outside the LAN a re-plan (burn-in %v) planned %s at %d bit/s above the member's 2000000", resource != nil, plan.VideoAction, plan.VideoBitrateBPS)
			}
		}
	})
	t.Run("the member's cap was lowered after the start", func(t *testing.T) {
		f := newReplanFixture(t, 40_000_000)
		session := f.start("lowered-member", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 4_000_000})
		if err := f.subtitle(remoteRequest(1_000_000), session.ID, styledSubtitle); err != nil {
			t.Fatal(err)
		}
		if plan := f.plan(session.ID); plan.VideoBitrateBPS > 1_000_000 {
			t.Fatalf("a re-plan planned %d bit/s above the member's lowered 1000000", plan.VideoBitrateBPS)
		}
		// Neither a lifted cap nor a move back to the LAN gives back more than
		// the presentation has been planned under.
		for _, request := range []context.Context{remoteRequest(0), remoteRequest(8_000_000), context.Background()} {
			for _, resource := range []*subtitles.Resource{nil, another} {
				if err := f.subtitle(request, session.ID, resource); err != nil {
					t.Fatal(err)
				}
				if plan := f.plan(session.ID); plan.VideoBitrateBPS > 1_000_000 || plan.VideoAction != "convert" {
					t.Fatalf("a later re-plan (burn-in %v) planned %s at %d bit/s", resource != nil, plan.VideoAction, plan.VideoBitrateBPS)
				}
			}
		}
	})
	t.Run("the owner's remote ceiling follows admission's view of the connection", func(t *testing.T) {
		f := newReplanFixture(t, 40_000_000)
		session := f.start("owner-remote", V1Choice{})
		strict := DefaultDeliveryConfiguration()
		strict.MaxVideoBitrateBPS = 1_000_000
		f.configure(strict)
		claimsLocal := WithDeliveryContext(remoteRequest(0), DeliveryContext{NetworkClass: NetworkLocal})
		if err := f.subtitle(claimsLocal, session.ID, styledSubtitle); err != nil {
			t.Fatal(err)
		}
		if plan := f.plan(session.ID); plan.VideoBitrateBPS > 1_000_000 {
			t.Fatalf("a remote re-plan planned %d bit/s above the owner's remote 1000000", plan.VideoBitrateBPS)
		}
	})
}

// The conversion fallback has no request to ask. It uses the view of the
// connection admission took for the presentation, not the delivery hint
// stored with the plan, so the owner's remote ceiling applies to a
// presentation admission called remote whatever that hint says.
func TestConversionFallbackUsesAdmissionsViewOfTheConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	hintedLocal := WithDeliveryContext(context.Background(), DeliveryContext{NetworkClass: NetworkLocal})
	lower := func(f *replanFixture) {
		strict := DefaultDeliveryConfiguration()
		strict.MaxVideoBitrateBPS = 1_000_000
		f.configure(strict)
	}
	t.Run("admitted as remote, hinted local", func(t *testing.T) {
		f := newReplanFixture(t, 1_600_000)
		session, err := f.s.CreateV1(hintedLocal, f.p, f.item, "divergent", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 2_000_000}, nil)
		if err != nil {
			t.Fatal(err)
		}
		lower(f)
		started := f.plan(session.ID)
		converted, err := f.h.downgradeCopyPlan(context.Background(), session.ID, started)
		if err != nil {
			t.Fatal(err)
		}
		if converted.VideoBitrateBPS < 1 || converted.VideoBitrateBPS > 1_000_000 {
			t.Fatalf("the fallback converts at %d bit/s above the owner's remote 1000000; stored network hint %s", converted.VideoBitrateBPS, started.Policy.NetworkClass)
		}
	})
	t.Run("stored before plans kept admission's view", func(t *testing.T) {
		f := newReplanFixture(t, 1_600_000)
		session := f.start("unknown-reach", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 2_000_000})
		mustAudioExec(t, f.db, `UPDATE playback_delivery_plans SET policy_json=json_remove(policy_json,'$.sessionReach') WHERE session_id=?`, session.ID)
		lower(f)
		converted, err := f.h.downgradeCopyPlan(context.Background(), session.ID, f.plan(session.ID))
		if err != nil {
			t.Fatal(err)
		}
		if converted.VideoBitrateBPS > 1_000_000 {
			t.Fatalf("a presentation whose reach is unknown converts at %d bit/s above the owner's remote 1000000", converted.VideoBitrateBPS)
		}
	})
	t.Run("admitted on the LAN", func(t *testing.T) {
		f := newReplanFixture(t, 1_600_000)
		session := f.start("lan", V1Choice{})
		lower(f)
		converted, err := f.h.downgradeCopyPlan(context.Background(), session.ID, f.plan(session.ID))
		if err != nil {
			t.Fatal(err)
		}
		if converted.VideoBitrateBPS != 0 || converted.ConversionTarget == nil || converted.ConversionTarget.RateControl != "quality" {
			t.Fatalf("a LAN presentation was held to the owner's remote ceiling: %d bit/s", converted.VideoBitrateBPS)
		}
	})
}

// Everything a plan keeps beside its resolved policy survives a reload and a
// re-plan together, with a field a later build might add.
func TestStoredPlanExtrasSurviveReloadAndReplan(t *testing.T) {
	f := newReplanFixture(t, 40_000_000)
	asset := firstCatalogAssetToken(t, f.db)
	updateCatalogAsset(t, f.db, asset, func(a *compactcatalog.Asset) { a.AudioCodec = "truehd" })
	mustAudioExec(t, f.db, `INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint,detail_version) SELECT token,1,size,modified_ns,'test',99 FROM catalog_assets WHERE token=?`, asset)
	mustAudioExec(t, f.db, `INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,detail_json) VALUES(?,0,'video','h264','','',0,'',1,0,'{"width":1920,"height":1080,"bitDepth":8}'),(?,1,'audio','truehd','eng','',6,'5.1',1,0,'{"sampleRate":96000}')`, asset, asset)
	session := f.start("extras", V1Choice{Limit: true, MaxVideoBitrateBPS: 1_500_000, MaxVideoHeight: 480})
	mustAudioExec(t, f.db, `UPDATE playback_delivery_plans SET policy_json=json_set(policy_json,'$.addedLater',json('{"a":1}')) WHERE session_id=?`, session.ID)
	stored := f.plan(session.ID)
	if stored.Target == nil || stored.Target.MaxVideoHeight != 480 || stored.AudioSampleRate != 48000 || stored.Policy == nil || stored.Policy.NetworkClass != NetworkUnknown {
		t.Fatalf("reloaded plan: target %+v, audio rate %d, policy %+v", stored.Target, stored.AudioSampleRate, stored.Policy)
	}
	if err := f.subtitle(context.Background(), session.ID, styledSubtitle); err != nil {
		t.Fatal(err)
	}
	if plan := f.plan(session.ID); plan.BurnIn == nil || plan.Target == nil || plan.Target.MaxVideoHeight != 480 || plan.AudioSampleRate != 48000 || plan.VideoBitrateBPS > 1_500_000 {
		t.Fatalf("re-planned plan: target %+v, audio rate %d, %d bit/s", plan.Target, plan.AudioSampleRate, plan.VideoBitrateBPS)
	}
}

// A legacy session is not exempt: a subtitle change that makes it produce new
// bytes is planned under what its member and the owner allow now, on the
// connection admission sees, and is refused when the request does not say.
func TestLegacySubtitleReplanAppliesCurrentCeilings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	legacy := func(t *testing.T, f *replanFixture, quality string) Session {
		t.Helper()
		session, err := f.s.Create(f.p, f.item, quality, "legacy-"+quality)
		if err != nil {
			t.Fatal(err)
		}
		if plan := f.plan(session.ID); plan.Target == nil {
			t.Fatalf("a presentation started by a named quality stored no target: %+v", plan)
		}
		return session
	}
	for _, quality := range []string{"automatic", "original"} {
		t.Run(quality+" under the member's cap", func(t *testing.T) {
			f := newReplanFixture(t, 40_000_000)
			session := legacy(t, f, quality)
			claimsLocal := WithDeliveryContext(remoteRequest(1_000_000), DeliveryContext{NetworkClass: NetworkLocal, ServerLocality: "local", TransportClass: "ethernet"})
			for _, resource := range []*subtitles.Resource{styledSubtitle, nil} {
				if err := f.subtitle(claimsLocal, session.ID, resource); err != nil {
					t.Fatal(err)
				}
				if plan := f.plan(session.ID); plan.VideoAction != "convert" || plan.VideoBitrateBPS < 1 || plan.VideoBitrateBPS > 1_000_000 || plan.QualityID != quality || plan.Target == nil {
					t.Fatalf("a legacy re-plan (burn-in %v) planned %s at %d bit/s (%s) above the member's 1000000", resource != nil, plan.VideoAction, plan.VideoBitrateBPS, plan.QualityID)
				}
			}
		})
	}
	t.Run("under the owner's remote ceiling", func(t *testing.T) {
		f := newReplanFixture(t, 40_000_000)
		session := legacy(t, f, "original")
		strict := DefaultDeliveryConfiguration()
		strict.MaxVideoBitrateBPS, strict.MaxVideoHeight = 1_000_000, 480
		f.configure(strict)
		claimsLocal := WithDeliveryContext(remoteRequest(0), DeliveryContext{NetworkClass: NetworkLocal})
		if err := f.subtitle(claimsLocal, session.ID, styledSubtitle); err != nil {
			t.Fatal(err)
		}
		if plan := f.plan(session.ID); plan.VideoBitrateBPS < 1 || plan.VideoBitrateBPS > 1_000_000 || plan.TargetHeight > 480 || plan.TargetHeight < 1 {
			t.Fatalf("a legacy re-plan planned %d bit/s at %dp above the owner's 1000000 at 480p", plan.VideoBitrateBPS, plan.TargetHeight)
		}
	})
	t.Run("a request that carries no ceilings is refused", func(t *testing.T) {
		f := newReplanFixture(t, 40_000_000)
		session := legacy(t, f, "automatic")
		started := f.plan(session.ID)
		for _, resource := range []*subtitles.Resource{styledSubtitle, nil} {
			if err := f.bareSubtitle(context.Background(), session.ID, resource); !errors.Is(err, ErrQualityUnavailable) {
				t.Fatalf("a legacy re-plan with no current ceilings (burn-in %v): %v", resource != nil, err)
			}
		}
		if after := f.plan(session.ID); after.VideoAction != started.VideoAction || after.VideoBitrateBPS != started.VideoBitrateBPS || after.BurnIn != nil {
			t.Fatalf("a refused re-plan changed the stored plan: %+v", after)
		}
	})
	t.Run("on the LAN it is planned as it always was", func(t *testing.T) {
		f := newReplanFixture(t, 40_000_000)
		session := legacy(t, f, "automatic")
		if err := f.subtitle(context.Background(), session.ID, styledSubtitle); err != nil {
			t.Fatal(err)
		}
		// With no ceiling the picture is encoded to a quality, not to a rate:
		// no bit rate at all is planned, which is the opposite of being capped.
		plan := f.plan(session.ID)
		uncapped := plan.VideoBitrateBPS == 0 && plan.VideoAction == "convert"
		if plan.BurnIn == nil || !uncapped && plan.VideoBitrateBPS <= 1_000_000 {
			t.Fatalf("a LAN legacy re-plan was held to a remote ceiling: %d bit/s (%s)", plan.VideoBitrateBPS, plan.VideoAction)
		}
	})
}

// The conversion fallback has no request, so it asks what the presentation's
// member is allowed now. A cap lowered after the presentation was admitted
// applies; a limit that cannot be read stops the fallback rather than passing
// as no limit; and a LAN presentation is not held to a remote cap.
func TestConversionFallbackAppliesTheMembersCurrentCap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	limits := func(f *replanFixture, bitrate int, failure error) *[]string {
		asked := &[]string{}
		f.h.ConfigureMemberCeiling(func(_ context.Context, _ *sql.Tx, account string) (int, error) {
			*asked = append(*asked, account)
			return bitrate, failure
		})
		return asked
	}
	t.Run("admitted at 2 Mbit/s, capped at 1 Mbit/s since", func(t *testing.T) {
		f := new720pReplanFixture(t, 1_600_000)
		session := f.start("tightened", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 2_000_000})
		started := f.plan(session.ID)
		if started.VideoAction != "copy" {
			t.Fatalf("the presentation did not start as a copy: %+v", started)
		}
		asked := limits(f, 1_000_000, nil)
		converted, err := f.h.downgradeCopyPlan(context.Background(), session.ID, started)
		if err != nil {
			t.Fatal(err)
		}
		if converted.VideoAction != "convert" || converted.VideoBitrateBPS < 1 || converted.VideoBitrateBPS > 1_000_000 {
			t.Fatalf("the fallback converts at %d bit/s above the member's current 1000000", converted.VideoBitrateBPS)
		}
		if len(*asked) != 1 || (*asked)[0] != f.p.AccountID {
			t.Fatalf("the member's limit was read for %v, want the presentation's own account %q", *asked, f.p.AccountID)
		}
		// The lower ceiling is what the presentation keeps.
		limits(f, 0, nil)
		if err = f.subtitle(remoteRequest(0), session.ID, styledSubtitle); err != nil {
			t.Fatal(err)
		}
		if plan := f.plan(session.ID); plan.VideoBitrateBPS > 1_000_000 {
			t.Fatalf("a later re-plan planned %d bit/s", plan.VideoBitrateBPS)
		}
	})
	t.Run("a limit that cannot be read", func(t *testing.T) {
		f := new720pReplanFixture(t, 1_600_000)
		session := f.start("unreadable", V1Choice{Remote: true, AdminMaxVideoBitrateBPS: 2_000_000})
		started := f.plan(session.ID)
		unreadable := errors.New("limits unreadable")
		limits(f, 0, unreadable)
		if _, err := f.h.downgradeCopyPlan(context.Background(), session.ID, started); !errors.Is(err, unreadable) {
			t.Fatalf("a fallback whose member limit could not be read: %v", err)
		}
		if after := f.plan(session.ID); after.VideoAction != "copy" {
			t.Fatalf("a refused fallback changed the stored plan: %+v", after)
		}
	})
	t.Run("a LAN presentation", func(t *testing.T) {
		f := newReplanFixture(t, 1_600_000)
		session := f.start("lan-control", V1Choice{})
		asked := limits(f, 1_000_000, nil)
		converted, err := f.h.downgradeCopyPlan(context.Background(), session.ID, f.plan(session.ID))
		if err != nil {
			t.Fatal(err)
		}
		if converted.VideoBitrateBPS != 0 || converted.ConversionTarget == nil || converted.ConversionTarget.RateControl != "quality" || len(*asked) != 0 {
			t.Fatalf("a LAN presentation was held to the member's remote cap: %d bit/s, limit read %d times", converted.VideoBitrateBPS, len(*asked))
		}
	})
	t.Run("a legacy session a request has shown to be remote", func(t *testing.T) {
		f := newReplanFixture(t, 1_600_000)
		session, err := f.s.Create(f.p, f.item, "automatic", "legacy-remote")
		if err != nil {
			t.Fatal(err)
		}
		// Burned in and taken out again from outside the LAN: a copy once more.
		for _, resource := range []*subtitles.Resource{styledSubtitle, nil} {
			if err = f.subtitle(remoteRequest(2_000_000), session.ID, resource); err != nil {
				t.Fatal(err)
			}
		}
		copied := f.plan(session.ID)
		if copied.VideoAction != "copy" {
			t.Fatalf("the legacy session is not a copy again: %+v", copied)
		}
		limits(f, 1_000_000, nil)
		converted, err := f.h.downgradeCopyPlan(context.Background(), session.ID, copied)
		if err != nil {
			t.Fatal(err)
		}
		if converted.VideoBitrateBPS < 1 || converted.VideoBitrateBPS > 1_000_000 {
			t.Fatalf("the fallback converts at %d bit/s above the member's current 1000000", converted.VideoBitrateBPS)
		}
	})
}
