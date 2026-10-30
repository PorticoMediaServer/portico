package decoder

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"testing"
	"time"
)

// A generated white bitmap decoded by FFmpeg and the ordinary conversion graph.
func pgsFixture() []byte {
	var out bytes.Buffer
	segment := func(seconds int, kind byte, body []byte) {
		out.WriteString("PG")
		binary.Write(&out, binary.BigEndian, uint32(seconds*90000))
		binary.Write(&out, binary.BigEndian, uint32(0))
		out.WriteByte(kind)
		binary.Write(&out, binary.BigEndian, uint16(len(body)))
		out.Write(body)
	}
	pcs := func(count byte) []byte { return []byte{0, 160, 0, 90, 0x10, 0, 0, 0x80, 0, 0, count} }
	segment(5, 0x16, append(pcs(1), 0, 0, 0, 0, 0, 60, 0, 35))
	segment(5, 0x17, []byte{1, 0, 0, 0, 0, 0, 0, 160, 0, 90})
	segment(5, 0x14, []byte{0, 0, 0, 0, 128, 128, 0, 1, 235, 128, 128, 255})
	rle := bytes.Repeat(append(bytes.Repeat([]byte{1}, 40), 0, 0), 20)
	n := len(rle) + 4
	segment(5, 0x15, append([]byte{0, 0, 0, 0xc0, byte(n >> 16), byte(n >> 8), byte(n), 0, 40, 0, 20}, rle...))
	segment(5, 0x80, nil)
	segment(7, 0x16, pcs(0))
	segment(7, 0x80, nil)
	return out.Bytes()
}

func TestBurnGraphDrawsOnlyAtTheTitleClock(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 30*time.Second)
	facts, e := ProbeToolchain(probeCtx, ffmpeg)
	cancelProbe()
	if e != nil {
		t.Fatal(e)
	}
	for _, format := range []string{"ass", "pgs"} {
		t.Run(format, func(t *testing.T) {
			// Subtitle graph execution has its own deadline: toolchain probing and
			// the other format must not spend this subtest's media budget.
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if format == "ass" && !facts.Filters["ass"] {
				decodertest.Unavailable(t, "libass required")
			}
			file := filepath.Join(t.TempDir(), "selected."+format)
			body := pgsFixture()
			if format == "ass" {
				body = []byte("[Script Info]\nScriptType: v4.00+\nPlayResX: 160\nPlayResY: 90\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, Bold, Alignment\nStyle: Default,Arial,24,&H00FFFFFF,1,5\n[Events]\nFormat: Layer, Start, End, Style, Text\nDialogue: 0,0:00:05.00,0:00:07.00,Default,BURN\n")
			}
			if e = os.WriteFile(file, body, 0600); e != nil {
				t.Fatal(e)
			}
			origin := int64(0)
			if format == "pgs" {
				origin, e = BurnInputOrigin(ctx, decodertest.QualifiedFFprobe(t), file, format)
				if e != nil {
					t.Fatal(e)
				}
			}
			graph, e := BuildConversion(ConversionRequest{ConvertVideo: true, SoftwarePreset: "ultrafast", BurnIn: &BurnIn{File: file, Format: format, InputStartUS: origin, PositionUS: 4000000, Width: 160, Height: 90}, Toolchain: &facts})
			if e != nil {
				t.Fatal(e)
			}
			args := []string{"-v", "error", "-nostdin", "-f", "lavfi", "-i", "color=black:s=160x90:r=24:d=5"}
			args = append(args, graph.ExtraInput...)
			for i := 0; i < len(graph.Video); i++ {
				if graph.Video[i] == "-profile:v" || graph.Video[i] == "-level:v" {
					i++
					continue
				}
				args = append(args, graph.Video[i])
			}
			mapped := "0:v:0"
			if graph.VideoMap != "" {
				mapped = graph.VideoMap
			}
			args = append(args, "-map", mapped, "-an", "-frames:v", "120", "-c:v", "rawvideo", "-pix_fmt", "gray", "-f", "rawvideo", "pipe:1")
			cmd := exec.CommandContext(ctx, ffmpeg, args...)
			// Decoders draw text with Portico's own font configuration (fonts.go),
			// never whatever the host happens to have.
			pack, e := Fonts()
			if e != nil {
				t.Fatal(e)
			}
			cmd.Env = append(os.Environ(), "FONTCONFIG_FILE="+pack.Config)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			frames, e := cmd.Output()
			if e != nil {
				t.Fatalf("%v: %s", e, stderr.String())
			}
			if len(frames) != 160*90*120 {
				t.Fatalf("got %d bytes, diagnostics %s", len(frames), stderr.String())
			}
			bright := make([]int, 120)
			for frame := range bright {
				for _, v := range frames[frame*14400 : (frame+1)*14400] {
					if v > 100 {
						bright[frame]++
					}
				}
			}
			if bright[0] != 0 || bright[30] < 100 || bright[54] < 100 || bright[96] != 0 {
				t.Fatalf("subtitle must be visible at source 5s and 6s, not 4s or 8s: %v", bright)
			}
		})
	}
}
