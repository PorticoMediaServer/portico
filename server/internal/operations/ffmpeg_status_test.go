package operations

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFFmpegValidationRefusesAnUnreadableBuildWithoutRestart(t *testing.T) {
	s, p, auth := consoleFixture(t)
	s.FFmpegValidation = FFmpegValidator("ffmpeg")
	doc, err := s.Settings(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	requested := doc.Requested
	requested.FFmpegPath = filepath.Join(t.TempDir(), "missing-ffmpeg")
	out, err := s.ApplySettings(context.Background(), p, auth, SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "ffmpeg-validation", Values: requested})
	if err != nil || out.FFmpegValidation == nil || out.FFmpegValidation.State != "refused" || out.FFmpegValidation.ReasonCode != "ffmpeg.validation.unreadable" || out.FFmpegValidation.InUse {
		t.Fatal(out, err)
	}
}
