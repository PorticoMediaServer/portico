package playback

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func TestPublishedOriginalLanguageReachesAuthorizedPreviewAndCreate(t *testing.T) {
	db, s, principal, _, item, entity := activityFixture(t)
	ctx := context.Background()
	s.ConfigureHLS(&HLS{})
	asset := firstCatalogAssetToken(t, db)
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) { a.Container = "mkv" })
	storeStreams(t, s, []assets.Stream{
		{Index: 0, Type: "video", Codec: "h264", Detail: assets.StreamDetail{Profile: "High", BitDepth: 8, Width: 640, Height: 360, DynamicRange: assets.RangeSDR}},
		{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Language: "eng", Default: true},
		{Index: 2, Type: "audio", Codec: "aac", Channels: 2, Language: "jpn"},
		{Index: 3, Type: "audio", Codec: "aac", Channels: 2, Language: "fra"},
	})
	// Use the actual normalized preference writer/reader, not an injected
	// DeliveryInput. Metadata rows below are accepted provider fixture facts.
	_, err := operations.New(db).ApplyPreferences(ctx, principal, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{Scope: operations.ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "original-context", Values: operations.PreferencePatch{"playback.preferredAudioLanguages": []string{"@original", "fr"}, "playback.subtitleMode": "foreign", "playback.preferredSubtitleLanguages": []string{"en"}}})
	if err != nil {
		t.Fatal(err)
	}
	var authorizedTx *sql.Tx
	denied := false
	s.AuthorityTx = func(ctx context.Context, tx *sql.Tx, p identity.Principal, target string) (identity.Principal, error) {
		if denied || p.AccountID != principal.AccountID || target != item {
			return p, identity.ErrUnauthorized
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE public_id=pid_blob(?) AND retired=0)`, target).Scan(&exists); err != nil {
			return p, err
		}
		if !exists {
			return p, identity.ErrUnauthorized
		}
		authorizedTx = tx
		return p, nil
	}
	s.ConfigureDelivery(nil, func(tx *sql.Tx, v identity.Viewer, device string) (DeliveryPreferenceReader, error) {
		if tx != authorizedTx {
			return nil, errors.New("planning escaped authorized snapshot")
		}
		values, _, _, err := operations.EffectivePreferences(tx, v, device)
		return values, err
	}, nil, "")
	if _, err = db.Exec(`INSERT OR IGNORE INTO screen_metadata_work(target_kind,target_id,library_id,title_seed) VALUES('item',?,'lib','Film')`, entity); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO screen_metadata_publications(id,target_kind,target_id,provider,provider_type,provider_id,payload,confidence,reasons,source_kind,input_digest,query_digest,observed_at,language,region,selection_revision,generation,decision) VALUES('original-context','item',?,'tmdb','movie','42','{}',1,'[]','provider','fixture','fixture','now','en','US',1,1,'accepted')`, entity); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE screen_metadata_work SET accepted_publication='original-context' WHERE target_kind='item' AND target_id=?`, entity); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO screen_metadata_fields(target_kind,target_id,field,value,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision,publication_id) VALUES('item',?,'language','"ja"','tmdb','provider','',1,'now','en','US',1,'original-context')`, entity); err != nil {
		t.Fatal(err)
	}
	for n, tc := range []struct {
		language string
		choice   V1Choice
		want     int
		reason   string
		foreign  bool
	}{
		{"ja", V1Choice{}, 2, "original_language", true}, {"xx", V1Choice{}, 1, "original_default", true}, {"ko", V1Choice{}, 3, "preferred_language", false}, {"ja", V1Choice{AudioStream: ptrAudioIndex(3)}, 3, "explicit", false}, {"ja", V1Choice{CarriedAudioLanguage: "eng"}, 1, "carried", true},
	} {
		if _, err = db.Exec(`UPDATE screen_metadata_fields SET value=? WHERE target_kind='item' AND target_id=? AND field='language'`, fmt.Sprintf("%q", tc.language), entity); err != nil {
			t.Fatal(err)
		}
		preview, err := s.PlanV1(ctx, principal, item, tc.choice)
		if err != nil || preview.AudioStream != tc.want || preview.Trace.Audio.ChosenBy != tc.reason {
			t.Fatalf("preview %s: stream=%d trace=%+v err=%v", tc.language, preview.AudioStream, preview.Trace, err)
		}
		values, _, _, err := operations.EffectivePreferences(db, principal.Viewer, "web")
		if err != nil {
			t.Fatal(err)
		}
		sub := subtitles.ChooseAutomatic(automaticSubtitles(values, preview.Trace, "en-US"), []subtitles.AutoCandidate{{ID: "english", Language: "eng"}})
		// The actual saved list declares French familiar. @original does not
		// declare a familiar language, and app locale does not add one to it.
		if (sub != nil) != tc.foreign {
			t.Fatalf("foreign subtitles ignored actual selected audio: stream=%d subtitle=%+v", tc.want, sub)
		}
		session, err := s.CreateV1(ctx, principal, item, fmt.Sprintf("original-%d", n), tc.choice, nil)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := loadDeliveryPlan(ctx, db, session.ID)
		if err != nil || actual.AudioStream != tc.want || actual.Trace.Audio.ChosenBy != tc.reason {
			t.Fatalf("create %s: plan=%+v err=%v", tc.language, actual, err)
		}
	}
	if _, err = db.Exec(`UPDATE screen_metadata_publications SET decision='superseded' WHERE id='original-context'`); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PlanV1(ctx, principal, item, V1Choice{})
	if err != nil || preview.AudioStream != 1 || preview.Trace.Audio.ChosenBy != "original_default" {
		t.Fatalf("stale publication selected original: %+v %v", preview.Trace, err)
	}
	denied = true
	if _, err = s.PlanV1(ctx, principal, item, V1Choice{}); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("preview bypassed current title authority: %v", err)
	}
}
func ptrAudioIndex(v int) *int { return &v }
