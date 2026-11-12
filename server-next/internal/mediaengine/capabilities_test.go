package mediaengine_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaengine"
)

// answered waits until the engine has asked its FFmpeg what it can do.
func answered(t *testing.T, e *mediaengine.Engine) mediaengine.Capabilities {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if c := e.Capabilities(context.Background()); !c.Asking {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatal("FFmpeg was never asked")
		}
	}
}

func engineWith(t *testing.T, ffmpeg, ffprobe string) (*mediaengine.Engine, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	folder := filepath.Join(t.TempDir(), "transcodes")
	e, err := mediaengine.New(ctx, d, mediaengine.Options{Folder: folder, FFmpeg: ffmpeg, FFprobe: ffprobe})
	if err != nil {
		t.Fatal(err)
	}
	return e, folder
}

// "What this server can do" hears from the engine what its FFmpeg is and where it converts;
// with no FFmpeg, that nothing converts and no reason beyond that.
func TestTheEngineSaysWhatItsToolsCanDo(t *testing.T) {
	e, folder := engineWith(t, filepath.Join(t.TempDir(), "no-ffmpeg"), "")
	if c := answered(t, e); c.Converter || c.Problem != "" || c.ConversionFolder != folder || len(c.Graphics) != 0 {
		t.Fatalf("with no FFmpeg: %+v", c)
	}
	e, _ = engineWith(t, decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t))
	if c := answered(t, e); !c.Converter || c.FFmpeg == "" || c.Version == "" {
		t.Fatalf("with FFmpeg: %+v", c)
	}
}
