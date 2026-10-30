package decoder

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

// A synthetic, freely generated DSD sigma-delta tone. Files are temporary;
// no commercial sample or copyrighted music enters the repository.
func musicDSF(mult int) []byte {
	rate := 2822400 * mult
	samples := rate / 10
	block := 4096
	n := ((samples+7)/8 + block - 1) / block * block
	channel := bytes.Repeat([]byte{0x69}, n)
	sigma := 0.0
	for i := 0; i < samples; i++ {
		sigma += .6 * math.Sin(2*math.Pi*1000*float64(i)/float64(rate))
		bit := sigma >= 0
		if bit {
			sigma -= 1
			channel[i/8] |= 1 << uint(i%8)
		} else {
			sigma += 1
			channel[i/8] &= ^(1 << uint(i%8))
		}
	}
	var b bytes.Buffer
	write := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("DSD ")
	write(uint64(28))
	write(uint64(28 + 52 + 12 + 2*n))
	write(uint64(0))
	b.WriteString("fmt ")
	write(uint64(52))
	for _, v := range []uint32{1, 0, 2, 2, uint32(rate), 1} {
		write(v)
	}
	write(uint64(samples))
	write(uint32(block))
	write(uint32(0))
	b.WriteString("data")
	write(uint64(12 + 2*n))
	for i := 0; i < n; i += block {
		b.Write(channel[i : i+block])
		b.Write(channel[i : i+block])
	}
	return b.Bytes()
}
func TestMusicDSD64Through256R29(t *testing.T) {
	testtier.Media(t, "encodes and decodes actual bounded music fixtures through FFmpeg")
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	probe, e := exec.LookPath("ffprobe")
	if e != nil {
		t.Skip("ffprobe unavailable")
	}
	dir := t.TempDir()
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer server.Close()
	for _, mult := range []int{1, 2, 4} {
		t.Run(fmt.Sprint(64*mult), func(t *testing.T) {
			name := fmt.Sprintf("dsd%d.dsf", 64*mult)
			if e := os.WriteFile(filepath.Join(dir, name), musicDSF(mult), 0600); e != nil {
				t.Fatal(e)
			}
			raw, e := exec.Command(probe, audiofacts.Args(server.URL+"/"+name)...).Output()
			if e != nil {
				t.Fatal(e)
			}
			facts, e := audiofacts.Parse(bytes.NewReader(raw))
			if e != nil {
				t.Fatal(e)
			}
			const target = 176400
			frames := int64(math.Round(float64(facts.DurationFrames) * target / float64(facts.SampleRate)))
			args, e := AnalysisArgs(server.URL+"/"+name, AnalysisSpec{Kind: "audio_flac", MaxDurationUS: 1_000_000, AudioStart: facts.StartFrames, AudioFrames: facts.DurationFrames, AudioSourceRate: facts.SampleRate, AudioOut: frames, AudioRate: target, AudioChannels: 2, AudioBitDepth: 24, AudioGainDB: -6})
			if e != nil {
				t.Fatal(e)
			}
			data, e := exec.Command(ff, args...).Output()
			if e != nil {
				t.Fatal(e)
			}
			out := fmt.Sprintf("dsd%d.flac", 64*mult)
			if e = os.WriteFile(filepath.Join(dir, out), data, 0600); e != nil {
				t.Fatal(e)
			}
			raw, e = exec.Command(probe, audiofacts.Args(server.URL+"/"+out)...).Output()
			if e != nil {
				t.Fatal(e)
			}
			got, e := audiofacts.Parse(bytes.NewReader(raw))
			if e != nil || got.SampleRate != target || got.BitDepth != 24 || got.DurationFrames != frames {
				t.Fatal(got, e)
			}
			t.Logf("DSD%d source %s -> PCM %s", 64*mult, facts, got)
		})
	}
}
