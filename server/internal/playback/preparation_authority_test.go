package playback

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

type preparationInspector struct {
	inspect func(context.Context, string, string) (subtitles.SourceMediaInfo, error)
}

func (i preparationInspector) InspectSubtitleSource(ctx context.Context, item, source string) (subtitles.SourceMediaInfo, error) {
	return i.inspect(ctx, item, source)
}

func TestPlaybackPreparationAuthorityPrecedesRemoteInspection(t *testing.T) {
	for _, v1 := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "v1"}[v1], func(t *testing.T) {
			db, err := persistence.Open(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, item, asset := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "film", "Film", compactcatalog.Asset{Path: "/test/film.strm", Size: 100, ModifiedNS: 1, Container: "strm"})
			s := New(db)
			inspections := 0
			s.ConfigureRemote(&Remote{db: db, SubtitleInspector: preparationInspector{inspect: func(context.Context, string, string) (subtitles.SourceMediaInfo, error) {
				inspections++
				return subtitles.SourceMediaInfo{}, errors.New("unauthorized source was inspected")
			}}})
			ctx := context.WithValue(context.Background(), struct{}{}, "original request")
			authorizations := 0
			s.AuthorityTx = func(check context.Context, tx *sql.Tx, p identity.Principal, target string) (identity.Principal, error) {
				authorizations++
				var token string
				if check != ctx || target != item {
					t.Fatal("preparation escaped the original invocation", check, target)
				}
				if err := tx.QueryRowContext(check, `SELECT token FROM catalog_assets WHERE token=?`, asset).Scan(&token); err != nil || token != asset {
					t.Fatal("authority did not use the current DB snapshot", err)
				}
				return p, identity.ErrUnauthorized
			}
			if v1 {
				_, err = s.CreateV1(ctx, identity.Principal{}, item, "rejected-before-io", V1Choice{QualityID: "original"}, nil)
			} else {
				_, err = s.CreateContext(ctx, identity.Principal{}, item, "original", "rejected-before-io")
			}
			if !errors.Is(err, identity.ErrUnauthorized) || authorizations != 1 || inspections != 0 {
				t.Fatal("rejected command reached source preparation", err, authorizations, inspections)
			}
			var sessions int
			if err = db.QueryRow(`SELECT COUNT(*) FROM playback_sessions`).Scan(&sessions); err != nil || sessions != 0 {
				t.Fatal("rejected preparation created a playback session", sessions, err)
			}
		})
	}
}

func TestPlaybackPreparationAuthorityStillRechecksBeforeAccepting(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, item, asset := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "film", "Film", compactcatalog.Asset{Path: "/test/film.strm", Size: 100, ModifiedNS: 1, Container: "strm"})
	s := New(db)
	authorizations, inspections := 0, 0
	s.AuthorityTx = func(_ context.Context, _ *sql.Tx, p identity.Principal, target string) (identity.Principal, error) {
		authorizations++
		if target != item {
			t.Fatal("foreign item authorized")
		}
		if authorizations == 1 {
			return p, nil
		}
		return p, identity.ErrUnauthorized
	}
	s.ConfigureRemote(&Remote{db: db, SubtitleInspector: preparationInspector{inspect: func(_ context.Context, target, source string) (subtitles.SourceMediaInfo, error) {
		if authorizations != 1 || target != item || source != asset {
			t.Fatal("inspection did not follow authorized selection", authorizations, target, source)
		}
		inspections++
		return subtitles.SourceMediaInfo{Container: "mov", VideoCodec: "h264", AudioCodec: "aac", DurationUS: 2_000_000, Evidence: "observed:fixture",
			Probe: []byte(`{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"2.000000","start_time":"0.000000"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":160,"height":90},{"index":1,"codec_type":"audio","codec_name":"aac","channels":2}]}`)}, nil
	}}})
	_, err = s.CreateV1(context.Background(), identity.Principal{}, item, "revoked-during-preparation", V1Choice{QualityID: "original"}, nil)
	if !errors.Is(err, identity.ErrUnauthorized) || authorizations != 2 || inspections != 1 {
		t.Fatal("early authority replaced accepting-transaction authority", err, authorizations, inspections)
	}
	var sessions int
	if err = db.QueryRow(`SELECT COUNT(*) FROM playback_sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("revoked preparation was accepted", sessions, err)
	}
}
