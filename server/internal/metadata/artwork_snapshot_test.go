package metadata

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

type artworkAdmissionConnector struct {
	driver.Connector
	connect func(context.Context) (driver.Conn, error)
}

func (c artworkAdmissionConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.connect(ctx)
}

// Check admission at physical connection acquisition, before any SQL can run.
// The old reader skipped admission and could open the entire cold pool while
// every queued read was already spending its two-second snapshot lifetime.
func TestArtworkReadsUseSnapshotAdmission(t *testing.T) {
	type readCase struct {
		name string
		read func(*Service, context.Context) error
	}
	cases := []readCase{
		{"selected", func(s *Service, ctx context.Context) error {
			_, _, err := s.EntityArtworkVariant(ctx, RepairTarget{"item", "fixture"}, "poster", "", ArtworkSmall, "")
			return err
		}},
		{"preview", func(s *Service, ctx context.Context) error {
			_, _, err := s.PreviewArtwork(ctx, RepairTarget{"item", "fixture"}, strings.Repeat("a", 64), ArtworkSmall)
			return err
		}},
		{"capture-artwork", func(s *Service, ctx context.Context) error { _, _, err := s.CaptureArtworkContext(ctx); return err }},
		{"repair-state", func(s *Service, ctx context.Context) error {
			_, err := s.RepairState(ctx, RepairTarget{"item", "fixture"}, func(*sql.Tx) error { return nil })
			return err
		}},
		{"repair-preview", func(s *Service, ctx context.Context) error {
			_, err := s.RepairPreview(ctx, RepairTarget{"item", "fixture"}, nil, func(*sql.Tx) error { return nil })
			return err
		}},
		{"attention", func(s *Service, ctx context.Context) error { _, err := s.Attention(ctx, "fixture", "", 1); return err }},
		{"library-agent", func(s *Service, ctx context.Context) error { _, err := s.LibraryAgent(ctx, "fixture"); return err }},
		{"screen-policy", func(s *Service, ctx context.Context) error { _, err := s.ScreenPolicy(ctx, "fixture"); return err }},
		{"screen-state", func(s *Service, ctx context.Context) error {
			_, err := s.ScreenState(ctx, "item", "fixture")
			return err
		}},
		{"music-state", func(s *Service, _ context.Context) error { _, err := s.MusicBrainzState("song", "fixture"); return err }},
		{"settings", func(s *Service, ctx context.Context) error { _, err := s.MetadataSettings(ctx); return err }},
	}
	cases = append(cases,
		readCase{"episode-command", func(s *Service, ctx context.Context) error {
			s.EpisodeThumbnails = thumbnailRuntime(t, s, nil)
			s.EpisodeThumbnails.Sources = &episodeAvailabilitySources{t: t}
			var owner identity.Principal
			owner.Authority, owner.AccountID, owner.ProfileID, owner.Hash = "local", "account", "profile", "hash"
			_, err := s.MakeEpisodeThumbnail(ctx, RepairTarget{"item", "fixture"}, RepairCommand{Action: "make_episode_thumbnail", ExpectedRevision: strings.Repeat("a", 64)}, owner, func(*sql.Tx) error { return nil })
			return err
		}},
		readCase{"episode-scan", func(s *Service, ctx context.Context) error {
			_, _, err := s.ScanEpisodeThumbnails(ctx, "job", "source", "object", "revision", 1)
			return err
		}},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, err := sqlite.NewConnector(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			held := dbwork.Snapshots().Held
			stopped := errors.New("stop before opening a cold connection")
			calls := 0
			db := sql.OpenDB(artworkAdmissionConnector{base, func(ctx context.Context) (driver.Conn, error) {
				calls++
				if got := dbwork.Snapshots().Held; got != held+1 {
					t.Errorf("cold metadata connection bypassed snapshot admission: held=%d, want %d", got, held+1)
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > dbwork.ForegroundSnapshotLifetime {
					t.Error("admitted metadata reader lost its foreground lifetime")
				}
				return nil, stopped
			}})
			defer db.Close()
			if err := tc.read(&Service{db: db}, context.Background()); !errors.Is(err, stopped) || calls != 1 {
				t.Fatalf("connection refusal: err=%v opens=%d", err, calls)
			}
			if got := dbwork.Snapshots().Held; got != held {
				t.Fatalf("failed metadata open leaked snapshot admission: %d, want %d", got, held)
			}
		})
	}
}

func TestArtworkSnapshotClosesFileWhenFinishingFails(t *testing.T) {
	for _, request := range []bool{false, true} {
		t.Run(map[bool]string{false: "private", true: "request"}[request], func(t *testing.T) {
			s, _, _ := repairFixture(t)
			file, err := os.CreateTemp(t.TempDir(), "artwork")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if request {
				var end func()
				ctx, end = dbwork.WithRequestSnapshot(ctx)
				defer end()
			}
			got, _, err := s.withArtworkSnapshot(ctx, func(context.Context) (*os.File, string, error) {
				cancel()
				return file, "image/png", nil
			})
			if got != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled snapshot returned artwork: file=%v err=%v", got, err)
			}
			if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("snapshot failure leaked its opened artwork: %v", err)
			}
		})
	}
}

func TestArtworkReadFailureAbortsCarriedWriter(t *testing.T) {
	for _, preview := range []bool{false, true} {
		t.Run(map[bool]string{false: "selected", true: "preview"}[preview], func(t *testing.T) {
			s, db, target := repairFixture(t)
			write, err := dbwork.Begin(context.Background(), db, dbwork.ClassInteractive)
			if err != nil {
				t.Fatal(err)
			}
			defer write.Rollback()
			if preview {
				_, _, err = s.PreviewArtwork(write.Context(), target, strings.Repeat("a", 64), ArtworkSmall)
			} else {
				_, _, err = s.EntityArtworkVariant(write.Context(), target, "poster", "", ArtworkSmall, "")
			}
			if !errors.Is(err, ErrArtworkPending) {
				t.Fatalf("missing artwork: %v", err)
			}
			if err := write.Commit(); !errors.Is(err, dbwork.ErrAborted) {
				t.Fatalf("failed nested artwork read allowed its writer to commit: %v", err)
			}
		})
	}
}

func TestArtworkCapturedContextKeepsItsCallerAfterSnapshotCloses(t *testing.T) {
	s, _, _ := repairFixture(t)
	type key struct{}
	caller, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "caller"))
	captured, check, err := s.CaptureArtworkContext(caller)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if captured.Err() != nil || dbwork.Snapshot(captured) != nil || captured.Value(key{}) != "caller" || check == nil {
		cancel()
		t.Fatal("captured artwork context retained its finished read instead of its caller")
	}
	cancel()
	if !errors.Is(captured.Err(), context.Canceled) {
		t.Fatal("captured artwork context lost caller cancellation")
	}
}
