package disc

import (
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

var decodedPTS = regexp.MustCompile(`\bpts_time:([0-9.eE+-]+)`)

// This opt-in test runs the real selected-title reader and decoder. Protocol
// structure/seek success alone cannot prove that the decoder lands on that time.
// Only the checked generated fixture cohort is used, never supplied private media.
func TestSelectedDiscTitleActualDecodedSeek(t *testing.T) {
	ffmpeg, helper := os.Getenv("PORTICO_DISC_TEST_FFMPEG"), os.Getenv("PORTICO_DISC_TEST_BINARY")
	if ffmpeg == "" || helper == "" {
		t.Skip("set explicit checked FFmpeg and same-bundle disc helper for real decoded title/seek qualification")
	}
	if !filepath.IsAbs(ffmpeg) || !filepath.IsAbs(helper) || filepath.Dir(ffmpeg) != filepath.Dir(helper) {
		t.Fatal("disc decoder and helper must be explicit tools from the same selected bundle directory")
	}
	root, fixtures := discFixtures(t)
	tool := Tool{Binary: helper}
	for _, fixture := range fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			path := filepath.Join(root, fixture.Path)
			probe, err := tool.ProbeLocal(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			for _, title := range probe.OrderedTitles() {
				t.Run(title.Key(probe.Kind), func(t *testing.T) {
					firstPTS := decodeSelectedDiscFrame(t, ffmpeg, path, probe, title, 0, nil)
					positions := []int64{title.DurationMs / 2, title.DurationMs - 2000}
					if probe.Kind == "bluray" {
						// The checked playlist crosses its first clip boundary at
						// 36 seconds. Seek into the next clip, not just its header.
						positions[0] = (title.Clips[0].OutTime90k-title.Clips[0].InTime90k)/90 + 1500
					}
					for _, position := range positions {
						var seek *Seek
						if probe.Kind == "bluray" {
							value, err := tool.SeekLocal(context.Background(), path, *title.Playlist, position)
							if err != nil {
								t.Fatal(err)
							}
							seek = &value
						}
						pts := decodeSelectedDiscFrame(t, ffmpeg, path, probe, title, position, seek)
						landed := (pts - firstPTS) * 1000
						// Fixture GOPs are 24/24 seconds (Blu-ray), 12/25
						// seconds (DVD). Keep the criterion tied to actual authoring.
						gop := 1000.0
						if probe.Kind == "dvd" {
							gop = 480
						}
						if math.Abs(landed-float64(position)) > gop+1 {
							t.Fatalf("decoded title time %.3fms from requested %dms exceeds %.0fms GOP (firstPTS=%.6f,seekPTS=%.6f)", landed, position, gop, firstPTS, pts)
						}
					}
				})
			}
		})
	}
}

func decodeSelectedDiscFrame(t *testing.T, ffmpeg, path string, probe Probe, title Title, position int64, seek *Seek) float64 {
	t.Helper()
	input, err := probe.FFmpegInput(path, title, position, seek)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "info", "-threads", "1", "-filter_threads", "1", "-copyts"}
	args = append(args, input.Options...)
	args = append(args, "-i", input.URL, "-map", "0:v:0", "-an", "-sn", "-vf", "showinfo", "-frames:v", "1", "-fps_mode", "passthrough", "-f", "null", "-")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: ffmpeg, Args: args, ReadPaths: []string{path}, MaxFileBytes: mediaexec.NoOutputFiles, MaxCPUSeconds: 15})
	if err != nil {
		t.Fatal(err)
	}
	output := &outputBuffer{}
	cmd.Stderr, cmd.Stdout, cmd.WaitDelay = output, io.Discard, time.Second
	if err = cmd.Run(); err != nil {
		t.Fatalf("actual selected-title decode failed: %v\n%s", err, output.data)
	}
	match := decodedPTS.FindSubmatch(output.data)
	if len(match) != 2 {
		t.Fatalf("decoder returned no timestamped video frame: %s", output.data)
	}
	pts, err := strconv.ParseFloat(string(match[1]), 64)
	if err != nil || math.IsNaN(pts) || math.IsInf(pts, 0) {
		t.Fatal("invalid decoded frame timestamp", string(match[1]), err)
	}
	return pts
}
