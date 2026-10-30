package playbackv1

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

func TestConvertingSeekRestartsOnlyAfterRevisionCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, withOffset := range []bool{false, true} {
		name := "seek"
		if withOffset {
			name = "seek-with-subtitle-offset"
		}
		t.Run(name, func(t *testing.T) {
			for _, outcome := range []string{"accepted", "revision-race", "publication-failure"} {
				t.Run(outcome, func(t *testing.T) {
					ctx := context.Background()
					db := pausedTestDB(t)
					now := time.Now()
					insertPausedSession(t, db, "seek-session", "vod", "playing", now.UnixMilli(), 0)
					if _, err := db.Exec(`UPDATE playback_v1_sessions SET media_session_id='seek-media',presentation='{"mode":"stream","url":"/fixture","decision":{"video":{"action":"transcode","reasons":[]}}}' WHERE id='seek-session'`); err != nil {
						t.Fatal(err)
					}
					root := t.TempDir()
					dir := filepath.Join(root, "seek-media")
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					output := filepath.Join(dir, "generation")
					if err := os.WriteFile(output, []byte("original-generation"), 0600); err != nil {
						t.Fatal(err)
					}
					binary, err := os.Executable()
					if err != nil {
						t.Fatal(err)
					}
					// Exercise real restart cleanup. There is deliberately no legacy
					// media row, so its final start check launches no decoder process.
					hls, err := playback.NewHLS(ctx, db, root, binary)
					if err != nil {
						t.Fatal(err)
					}
					player := playback.New(db)
					player.ConfigureHLS(hls)
					service := &Service{DB: db, Playback: player}
					if outcome == "publication-failure" {
						if _, err = db.Exec(`CREATE TRIGGER reject_seek_event BEFORE INSERT ON api_events WHEN NEW.resource_id='seek-session' BEGIN SELECT RAISE(ABORT,'fixture rejects seek publication'); END`); err != nil {
							t.Fatal(err)
						}
					}
					raced := false
					service.Now = func() time.Time {
						// session() already read revision 1 when it checks the lease.
						// Model a concurrent accepted report before PATCH's CAS.
						if outcome == "revision-race" && !raced {
							raced = true
							if _, err := db.Exec(`UPDATE playback_v1_sessions SET revision=2,state='paused',position_ms=1234 WHERE id='seek-session'`); err != nil {
								t.Fatal(err)
							}
						}
						return now
					}
					change := Change{Seek: &SeekRequest{PositionMs: 12000}}
					if withOffset {
						offset := int64(500)
						change.SubtitleOffsetMs = &offset
					}
					view, err := service.Patch(ctx, pausedCaller(), "seek-session", "1", change)
					if outcome != "accepted" {
						wantRevision, wantPosition, wantState := 1, int64(0), "playing"
						if outcome == "revision-race" {
							var conflict *RevisionError
							if !errors.As(err, &conflict) || conflict.Revision != 2 {
								t.Fatalf("lost CAS: view=%+v error=%v", view, err)
							}
							wantRevision, wantPosition, wantState = 2, 1234, "paused"
						} else if err == nil || !strings.Contains(err.Error(), "fixture rejects seek publication") {
							t.Fatalf("publication failure: view=%+v error=%v", view, err)
						}
						data, readErr := os.ReadFile(output)
						if readErr != nil || string(data) != "original-generation" {
							t.Fatalf("losing seek discarded producer output: %q %v", data, readErr)
						}
						current, readErr := loadRow(ctx, db, "seek-session")
						if readErr != nil || current.generation != 1 || current.revision != wantRevision || current.position != wantPosition || current.state != wantState {
							t.Fatalf("losing seek changed the winner: %+v %v", current, readErr)
						}
					} else {
						if err != nil || view.Revision != "2" || view.Presentation.Generation != 2 || view.Presentation.StartPositionMs != 12000 {
							t.Fatalf("accepted seek: %+v %v", view, err)
						}
						if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
							t.Fatalf("accepted seek did not restart output: %v", err)
						}
					}
				})
			}
		})
	}
}
