package playback

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"strings"
	"testing"
	"time"
)

func TestOrdinaryBurnSelectionReplansRotatesGrantAndReturnsToCopy(t *testing.T) {
	ctx, s, h, session, grant, _ := copyFixture(t, 96, 40)
	// The selections below arrive as a LAN request with no member ceiling.
	ctx = WithReplanCeilings(ctx, ReplanCeilings{})
	facts, e := decoder.ProbeToolchain(ctx, h.binary)
	if e != nil {
		t.Fatal(e)
	}
	if !facts.Filters["ass"] {
		decodertest.Unavailable(t, "qualified libass FFmpeg required for ordinary burn-in runtime")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	helper, _ := os.Executable()
	sub, e := subtitles.New(subtitles.Options{DB: h.db, Directory: root, Storage: h.SourceStorage, HelperBinary: helper, Authorize: func(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (identity.Principal, error) {
		return p, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer sub.Close()
	s.ConfigureSubtitleDelivery(sub)
	p := identity.Principal{Hash: "login", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}, Epoch: 1}
	var item, asset string
	if e = h.db.QueryRow(`SELECT pid(e.public_id),ps.asset_id FROM playback_sessions ps JOIN catalog_entities e ON e.id=ps.item_id WHERE ps.id=?`, session.ID).Scan(&item, &asset); e != nil {
		t.Fatal(e)
	}
	body := "[Script Info]\nScriptType: v4.00+\nPlayResX: 160\nPlayResY: 90\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Arial,18,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:00.00,0:00:39.00,Default,,0,0,0,,{\\pos(80,45)}Burn test\n"
	pub, e := sub.Mutate(ctx, p, item, subtitles.Mutation{OperationID: "styled-upload", SourceID: asset, Scope: "personal", Format: "ass", Language: "en", Title: "Styled", Rights: "Fixture", OffsetUS: "0", Content: &body, Render: "styled"})
	if e != nil {
		t.Fatal(e)
	}
	plan, e := sub.Plan(ctx, p, item, session.ID)
	if e != nil {
		t.Fatal(e)
	}
	selected, e := sub.Select(ctx, p, item, session.ID, subtitles.SelectRequest{OperationID: "burn", Generation: session.Generation, ExpectedRevision: plan.Revision, Mode: "track", ResourceID: pub.ResourceID, ResourceRevision: 1, OffsetUS: "0", PositionUS: "18000000"})
	if e != nil {
		t.Fatal(e)
	}
	if selected.Presentation == nil || selected.Generation != 2 || selected.Renderer != "burn_in" || strings.Contains(selected.Presentation.StreamURL, "subtitle-video") {
		t.Fatalf("%+v", selected)
	}
	if _, _, _, e = s.ResolveGrant(grant); e == nil {
		t.Fatal("old grant survived")
	}
	delivery, e := loadDeliveryPlan(context.Background(), h.db, session.ID)
	if e != nil || delivery.VideoAction != "convert" || delivery.BurnIn == nil {
		t.Fatal(delivery, e)
	}
	newGrant := strings.TrimSuffix(strings.TrimPrefix(selected.Presentation.StreamURL, "/v1/media/"), "/master.m3u8")
	for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline); {
		_, e = s.HLSFileContext(ctx, newGrant, "segment-000003.ts")
		if !errors.Is(e, ErrSegmentPreparing) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if e != nil {
		t.Fatal(e)
	}
	off, e := sub.Select(ctx, p, item, session.ID, subtitles.SelectRequest{OperationID: "off", Generation: 2, ExpectedRevision: selected.Revision, Mode: "off", OffsetUS: "0", PositionUS: "18000000"})
	if e != nil {
		t.Fatal(e)
	}
	delivery, e = loadDeliveryPlan(context.Background(), h.db, session.ID)
	if e != nil || delivery.BurnIn != nil || delivery.VideoAction != "copy" || off.Generation != 3 {
		t.Fatal(delivery, off, e)
	}
}
