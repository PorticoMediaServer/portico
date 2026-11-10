package mediaanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

// Scrub previews, chapter pictures and an episode picture are made from a real
// generated file by the real confined decoder, and arrive at the sink as files
// a player can use.
func TestPreviewsAreMadeFromARealFile(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	path := filepath.Join(t.TempDir(), "episode.mkv")
	generate := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-t", "24", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-g", "24", path)
	if out, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	decode := FileDecode(&processpool.Supervisor{Limit: 1}, ffmpeg, nil, file, info.Size())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	const duration = 24_000_000
	video := decoder.PreviewVideoFacts{DynamicRange: "sdr"}

	type made struct {
		kind          string
		index         int
		width, height int
		data          []byte
	}
	var files []made
	sink := func(kind, contentType string, index int, startUS, endUS int64, width, height int, data []byte) error {
		if startUS < 0 || endUS > duration || endUS <= startUS {
			t.Errorf("%s %d covers %d to %d", kind, index, startUS, endUS)
		}
		files = append(files, made{kind, index, width, height, append([]byte{}, data...)})
		return nil
	}

	settings := TrickplaySettings{IntervalSeconds: 2, TileWidth: 160, MaxTiles: 16}
	descriptor, err := MakeTrickplay(ctx, decode, "trickplay", TrickplayRequest{DurationUS: duration, SourceVideo: video, Settings: settings}, sink)
	if err != nil {
		if os.Getenv("PORTICO_REQUIRE_DECODER_SANDBOX") != "1" && strings.Contains(err.Error(), "sandbox") {
			t.Skipf("no decoder sandbox here: %v", err)
		}
		t.Fatal(err)
	}
	if descriptor.FrameCount != 12 || descriptor.TileWidth != 160 || descriptor.TileHeight != 90 || len(files) != descriptor.TileCount+1 {
		t.Fatalf("descriptor %+v with %d files", descriptor, len(files))
	}
	for _, f := range files[:descriptor.TileCount] {
		shape, err := jpeg.DecodeConfig(bytes.NewReader(f.data))
		if f.kind != TrickplaySheetKind || err != nil || shape.Width != descriptor.Width || shape.Height != descriptor.Height {
			t.Fatalf("sheet %d is not a %dx%d JPEG: %v %+v", f.index, descriptor.Width, descriptor.Height, err, shape)
		}
	}
	var stored TrickplayDescriptor
	last := files[len(files)-1]
	if last.kind != TrickplayDescriptorKind || json.Unmarshal(last.data, &stored) != nil || stored != descriptor {
		t.Fatalf("the set's descriptor was not handed over last: %s", last.data)
	}
	if cues := strings.Count(TrickplayVTT(descriptor), " --> "); cues != descriptor.FrameCount {
		t.Fatalf("%d cues for %d frames", cues, descriptor.FrameCount)
	}

	files = nil
	chapters := []Chapter{{0, 8_000_000}, {8_000_000, 24_000_000}}
	if err = MakeChapterImages(ctx, decode, "chapters", duration, video, chapters, sink); err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[1].kind != ChapterImageKind || files[1].width != 320 || files[1].height != 180 {
		t.Fatalf("chapter pictures: %d", len(files))
	}

	request := EpisodeThumbnailRequest{Mode: EpisodeThumbnailDefaultMode, DurationUS: duration, EndUS: duration, SourceVideo: video}
	thumbnail, err := MakeEpisodeThumbnail(ctx, decode, request)
	if err != nil || thumbnail.Origin != "generated" || thumbnail.Width != 320 || thumbnail.PositionUS != duration/10 {
		t.Fatalf("thumbnail %+v %v", thumbnail.Origin, err)
	}
	// A provider's picture, or the library's setting, answers without decoding.
	request.ProviderStill = true
	if provided, err := MakeEpisodeThumbnail(ctx, nil, request); err != nil || provided.Origin != "provider" || provided.JPEG != nil {
		t.Fatalf("a provider still was not preferred: %+v %v", provided.Origin, err)
	}
	request.ProviderStill, request.Mode = false, EpisodeThumbnailOffMode
	if off, err := MakeEpisodeThumbnail(ctx, nil, request); err != nil || off.Origin != "fallback" {
		t.Fatalf("thumbnails off still made one: %+v %v", off.Origin, err)
	}
}
