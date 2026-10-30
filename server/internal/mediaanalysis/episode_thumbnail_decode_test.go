package mediaanalysis

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

// The test executable dispatches the same confinement helpers as the server.
func TestMain(m *testing.M) {
	for _, helper := range []func([]string) (bool, error){decoder.RunHelper, mediaexec.RunHelper} {
		if handled, err := helper(os.Args[1:]); handled {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

type episodeDecodeInput struct {
	file           *os.File
	observed       os.FileInfo
	mu             sync.Mutex
	bytes, maxRead int64
	closed         int
}

func (i *episodeDecodeInput) Evidence() string { return "observed:controlled-two-episode-video" }
func (i *episodeDecodeInput) Size() int64      { return i.observed.Size() }
func (i *episodeDecodeInput) Validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := os.Stat(i.file.Name())
	if err != nil {
		return err
	}
	if !os.SameFile(i.observed, current) || current.Size() != i.observed.Size() || !current.ModTime().Equal(i.observed.ModTime()) {
		return ErrConflict
	}
	return nil
}
func (i *episodeDecodeInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	if err := i.Validate(ctx); err != nil {
		return nil, err
	}
	if offset < 0 || length < 1 || length > 1<<20 || offset > i.Size()-length {
		return nil, ErrBudget
	}
	data := make([]byte, length)
	n, err := i.file.ReadAt(data, offset)
	if err != nil {
		return nil, err
	}
	if n != len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	i.mu.Lock()
	i.bytes += length
	i.maxRead = max(i.maxRead, length)
	i.mu.Unlock()
	return data, i.Validate(ctx)
}
func (i *episodeDecodeInput) Close() error {
	i.mu.Lock()
	i.closed++
	i.mu.Unlock()
	return i.file.Close()
}

func TestEpisodeThumbnailConfinedRangeDecodeKeepsLogicalEpisode(t *testing.T) {
	testtier.Media(t, "generates and decodes one real bounded video frame through the production range bridge")
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := decoder.ResolveLibraries(ffmpeg, ffprobe, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory := canonicalFixtureDir(t)
	path := filepath.Join(directory, "two-episodes.mp4")
	args := []string{"-nostdin", "-hide_banner", "-v", "error", "-threads", "1", "-filter_threads", "1", "-filter_complex_threads", "1", "-f", "lavfi", "-i", "color=red:s=320x180:r=10:d=3", "-f", "lavfi", "-i", "color=blue:s=320x180:r=10:d=3", "-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0", "-an", "-c:v", "libx264", "-threads:v", "1", "-preset", "ultrafast", "-g", "10", "-pix_fmt", "yuv420p", "-movflags", "+faststart", path}
	command, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: ffmpeg, Args: args, WriteDirs: []string{directory}, Background: true, MaxFileBytes: 8 << 20, MaxCPUSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("controlled video: %v: %s", err, output)
	}
	// A sparse legal MP4 free box makes full materialization observable even
	// though the controlled six-second video itself compresses to a few KiB.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	end, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		file.Close()
		t.Fatal(err)
	}
	const padding = 16 << 20
	var header [8]byte
	binary.BigEndian.PutUint32(header[:4], padding)
	copy(header[4:], "free")
	if _, err = file.Write(header[:]); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Truncate(end + padding); err != nil {
		file.Close()
		t.Fatal(err)
	}
	observed, err := file.Stat()
	if err != nil {
		file.Close()
		t.Fatal(err)
	}
	input := &episodeDecodeInput{file: file, observed: observed}
	supervisor := &storage.Supervisor{Limit: 1}
	checks := 0
	result, err := MakeEpisodeThumbnail(ctx, EpisodeThumbnailRequest{
		Mode: EpisodeThumbnailOffMode, Manual: true, DurationUS: 6_000_000, StartUS: 3_000_000, EndUS: 6_000_000,
		Open: func(context.Context) (Input, error) { return input, nil },
		// This callback checks helper obligations only. It does not stand in for
		// the separate production owner/job/source-profile authority producer.
		Authorize: func(_ context.Context, operation, cost string, manual bool) error {
			checks++
			if operation != catalog.EpisodeThumbnailOperation || cost != EpisodeThumbnailCostClass || !manual {
				return ErrInput
			}
			return nil
		},
		Decoder: func(in Input) Decode {
			bridge := subtitlevideo.NewMediaBridge(in)
			return func(ctx context.Context, key string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
				return bridge.With(ctx, func(url string, reservation *decoder.EndpointReservation) error {
					// As in the production renderer's default configuration, the
					// command resolves its own exact dependencies. This argument is
					// for extra configured library files, not the resolved list.
					return decoder.RunAnalysis(ctx, supervisor, key, ffmpeg, url, reservation, spec, consume)
				})
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	image, err := jpeg.Decode(bytes.NewReader(result.JPEG))
	if err != nil {
		t.Fatal(err)
	}
	r, _, b, _ := image.At(160, 90).RGBA()
	if result.PositionUS != 3_300_000 || result.Origin != "generated" || b < 30000 || b < r*2 {
		t.Fatalf("decoded another logical episode: position=%d red=%d blue=%d", result.PositionUS, r, b)
	}
	input.mu.Lock()
	defer input.mu.Unlock()
	if checks != 3 || input.closed != 1 || input.maxRead > 1<<20 || input.bytes <= 0 || input.bytes >= input.Size()/2 {
		t.Fatalf("unbounded/unfinished read: checks=%d close=%d max=%d bytes=%d size=%d", checks, input.closed, input.maxRead, input.bytes, input.Size())
	}
	if active := supervisor.Stats().BackgroundActive; active != 0 {
		t.Fatal("decoder not physically retired", active)
	}
	t.Logf("one 320x180 blue frame at %dus; ranged bytes=%d/%d, max extent=%d; decoder retired", result.PositionUS, input.bytes, input.Size(), input.maxRead)
	t.Logf("resolved tool dependencies=%d; default renderer has no extra configured library files", len(libraries))
}
