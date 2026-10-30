package operations

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"reflect"
	"testing"
)

func TestRuntimeSettingsSkipsToolValidationAndKeepsAuthority(t *testing.T) {
	s, _, auth := consoleFixture(t)
	calls := 0
	s.FFmpegValidation = func(context.Context, string) FFmpegStatus { calls++; return FFmpegStatus{} }
	runtime, err := s.RuntimeSettings(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || runtime.FFmpegValidation != nil {
		t.Fatal("runtime policy launched console tool validation")
	}
	console, err := s.Settings(context.Background(), auth)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || console.FFmpegValidation == nil {
		t.Fatal("console no longer qualifies its tool")
	}
	if !reflect.DeepEqual(runtime.Effective, console.Effective) || runtime.Revision != console.Revision {
		t.Fatal("runtime policy differs from console policy")
	}
	refused := errors.New("authority refused")
	deny := func(context.Context, *sql.Tx, string) error { return refused }
	if _, err = s.RuntimeSettings(context.Background(), deny); !errors.Is(err, refused) {
		t.Fatalf("authority changed: %v", err)
	}
	if calls != 1 {
		t.Fatal("refused policy launched tool validation")
	}
}

func TestSettingsReadsUseOneFFmpegQualificationUntilInvalidated(t *testing.T) {
	s, _, auth := consoleFixture(t)
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("executable v1"), 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.FFmpegValidation = ffmpegValidator(path, func(context.Context, string) (decoder.ToolchainFacts, error) {
		calls++
		return decoder.ToolchainFacts{}, nil
	})
	for i := 0; i < 20; i++ {
		if _, err := s.Settings(context.Background(), auth); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("20 settings reads launched %d validations", calls)
	}
	if _, err := s.Settings(WithFFmpegRecheck(context.Background()), auth); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("explicit check did not qualify again: %d", calls)
	}
	if err := os.WriteFile(path, []byte("changed executable v2"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settings(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("changed binary did not invalidate: %d", calls)
	}
}
