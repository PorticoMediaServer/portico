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
	for _, preview := range []bool{false, true} {
		t.Run(map[bool]string{false: "selected", true: "preview"}[preview], func(t *testing.T) {
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
					t.Errorf("cold artwork connection bypassed snapshot admission: held=%d, want %d", got, held+1)
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > dbwork.ForegroundSnapshotLifetime {
					t.Error("admitted artwork reader lost its foreground lifetime")
				}
				return nil, stopped
			}})
			defer db.Close()
			s := &Service{db: db}
			var file *os.File
			if preview {
				file, _, err = s.PreviewArtwork(context.Background(), RepairTarget{"item", "fixture"}, strings.Repeat("a", 64), ArtworkSmall)
			} else {
				file, _, err = s.EntityArtworkVariant(context.Background(), RepairTarget{"item", "fixture"}, "poster", "", ArtworkSmall, "")
			}
			if file != nil || !errors.Is(err, stopped) || calls != 1 {
				t.Fatalf("connection refusal: file=%v err=%v opens=%d", file, err, calls)
			}
			if got := dbwork.Snapshots().Held; got != held {
				t.Fatalf("failed artwork open leaked snapshot admission: %d, want %d", got, held)
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
