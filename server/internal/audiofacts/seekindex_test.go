package audiofacts

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The stored form round-trips and refuses anything else.
func TestSeekIndexEncoding(t *testing.T) {
	x := &SeekIndex{Offsets: []int64{417, 52_100, 104_903, 9_000_000_000}, Frames: []int64{0, 132_480, 264_960, 1 << 40}}
	got, err := DecodeSeekIndex(EncodeSeekIndex(x))
	if err != nil || fmt.Sprint(got) != fmt.Sprint(x) {
		t.Fatalf("round trip %v %v", got, err)
	}
	if EncodeSeekIndex(nil) != nil || EncodeSeekIndex(&SeekIndex{}) != nil {
		t.Fatal("an empty index is stored")
	}
	data := EncodeSeekIndex(x)
	for name, bad := range map[string][]byte{"empty": nil, "version": append([]byte{9}, data[1:]...), "truncated": data[:len(data)-1], "trailing": append(append([]byte{}, data...), 0), "repeated offset": EncodeSeekIndex(&SeekIndex{Offsets: []int64{5, 5}, Frames: []int64{0, 10}})} {
		if _, err := DecodeSeekIndex(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The count is bounded before anything is allocated.
	huge := binary.AppendUvarint([]byte{seekIndexVersion}, MaxSeekEntries+1)
	if _, err := DecodeSeekIndex(huge); err == nil {
		t.Fatal("an oversized count was accepted")
	}
}

// Thinning keeps a long file's index within its bound, evenly.
func TestSeekIndexStaysBounded(t *testing.T) {
	b := &indexBuilder{step: 10}
	for i := int64(0); i < 3*MaxSeekEntries+7; i++ {
		b.at(i*100, i*10)
	}
	b.thin()
	if n := len(b.index.Offsets); n > MaxSeekEntries || n < MaxSeekEntries/2 || b.index.Frames[0] != 0 || b.index.Frames[1] != 40 {
		t.Fatalf("thinned to %d entries, first steps %v", n, b.index.Frames[:2])
	}
}

// Real encodes (FFmpeg on the runner; skipped where it isn't installed): VBR MP3
// with its Xing frame, CBR MP3 without one, ADTS AAC, Ogg Opus and Ogg Vorbis.
// The index is built from the file's bytes alone, its MP3/ADTS/Opus frame count
// adds up to the measured raw frames, and decoding the file from an entry lands
// on exactly the frame the entry names: after a few packets to settle the
// decoder, the samples are the ones a decode from the start has at that frame.
func TestSeekIndexLandsOnTheExactFrame(t *testing.T) {
	ffmpeg, e1 := exec.LookPath("ffmpeg")
	ffprobe, e2 := exec.LookPath("ffprobe")
	if e1 != nil || e2 != nil {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	dir := t.TempDir()
	server := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer server.Close()
	// 40 s of a tone whose pitch keeps rising, so no two places sound alike.
	const seconds = 40
	source := filepath.Join(dir, "source.wav")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "aevalsrc=0.4*sin(2*PI*(200+t*12)*t):s=48000:d="+fmt.Sprint(seconds), "-ac", "1", source).CombinedOutput(); err != nil {
		t.Fatalf("source: %v %s", err, out)
	}
	cases := []struct {
		name, file string
		args       []string
		container  string
		codec      string
		// exact: decoding from an entry gives the start's samples (MDCT codecs
		// settle exactly); otherwise the best match must be at the entry.
		exact bool
	}{
		{"VBR MP3 with a Xing frame", "vbr.mp3", []string{"-ar", "44100", "-c:a", "libmp3lame", "-q:a", "2"}, "mp3", "mp3", true},
		{"CBR MP3 without one", "cbr.mp3", []string{"-ar", "44100", "-c:a", "libmp3lame", "-b:a", "128k", "-write_xing", "0"}, "mp3", "mp3", true},
		{"ADTS AAC", "tone.aac", []string{"-c:a", "aac", "-b:a", "128k", "-f", "adts"}, "adts", "aac", true},
		{"Ogg Opus", "tone.opus", []string{"-c:a", "libopus", "-b:a", "96k"}, "ogg", "opus", false},
		{"Ogg Vorbis", "tone.ogg", []string{"-c:a", "libvorbis", "-q:a", "4"}, "ogg", "vorbis", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, c.file)
			args := append([]string{"-hide_banner", "-loglevel", "error", "-y", "-i", source}, c.args...)
			if out, err := exec.Command(ffmpeg, append(args, path)...).CombinedOutput(); err != nil {
				t.Fatalf("encode: %v %s", err, out)
			}
			out, err := exec.Command(ffprobe, Args(server.URL+"/"+c.file)...).Output()
			if err != nil {
				t.Fatalf("ffprobe: %v", err)
			}
			facts, err := Parse(bytes.NewReader(out))
			if err != nil {
				t.Fatalf("facts: %v", err)
			}
			if facts.Container != c.container || facts.Codec != c.codec {
				t.Fatalf("facts %s", facts)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			reads := 0
			read := func(_ context.Context, offset, length int64) ([]byte, error) {
				reads++
				if offset < 0 || length < 1 || length > 1<<20 || offset+length > int64(len(data)) {
					return nil, fmt.Errorf("read outside the file: %d+%d", offset, length)
				}
				return data[offset : offset+length], nil
			}
			index, err := BuildSeekIndex(context.Background(), read, int64(len(data)), facts)
			if err != nil || index == nil {
				t.Fatalf("index %v %v", index, err)
			}
			step := int64(SeekStepSeconds * facts.SampleRate)
			if n := len(index.Offsets); n < seconds/SeekStepSeconds-1 || n > seconds/SeekStepSeconds+2 {
				t.Fatalf("%d entries for %d s", n, seconds)
			}
			for i := 1; i < len(index.Offsets); i++ {
				if index.Offsets[i] <= index.Offsets[i-1] || index.Frames[i]-index.Frames[i-1] < step || index.Frames[i]-index.Frames[i-1] > step+8192 {
					t.Fatalf("entry %d: %d@%d after %d@%d", i, index.Frames[i], index.Offsets[i], index.Frames[i-1], index.Offsets[i-1])
				}
			}
			if reads > len(data)>>20+2 {
				t.Fatalf("%d reads for %d bytes: the file is read more than once", reads, len(data))
			}
			if back, err := DecodeSeekIndex(EncodeSeekIndex(index)); err != nil || fmt.Sprint(back) != fmt.Sprint(index) {
				t.Fatalf("stored form %v", err)
			}

			full := decodeRaw(t, ffmpeg, path)
			if c.codec != "vorbis" && int64(len(full)) != facts.RawFrames {
				t.Fatalf("a whole decode gives %d frames, the facts say %d", len(full), facts.RawFrames)
			}
			// Decode from a middle entry, and from the last.
			for _, k := range []int{len(index.Offsets) / 2, len(index.Offsets) - 2} {
				part := filepath.Join(dir, fmt.Sprintf("part-%d-%s", k, c.file))
				head := []byte{}
				if c.container == "ogg" {
					head = data[:index.Offsets[0]] // the header pages, then the stream from the entry
				}
				if err := os.WriteFile(part, append(append([]byte{}, head...), data[index.Offsets[k]:]...), 0o600); err != nil {
					t.Fatal(err)
				}
				got := decodeRaw(t, ffmpeg, part)
				// Settle: a decoder started part-way needs a few packets (the
				// client's preroll); compare a second after that.
				settle := 6 * 1152
				if c.codec == "opus" || c.codec == "vorbis" {
					settle = 9600
				}
				window := facts.SampleRate
				start := index.Frames[k]
				if len(got) < settle+window || int(start)+settle+window > len(full) {
					t.Fatalf("entry %d: decoded %d frames", k, len(got))
				}
				best, bestErr := 0, math.Inf(1)
				for shift := -4096; shift <= 4096; shift++ {
					at := int(start) + settle + shift
					if at < 0 || at+window > len(full) {
						continue
					}
					e := 0.0
					for i := 0; i < window; i += 7 {
						d := float64(got[settle+i]) - float64(full[at+i])
						e += d * d
					}
					if e < bestErr {
						best, bestErr = shift, e
					}
				}
				if c.codec == "vorbis" {
					// A Vorbis entry is the page's starting granule: the decoder's
					// first packet there primes it, so output begins one packet on.
					if best < 0 || best > 2048 {
						t.Fatalf("entry %d lands %d frames off", k, best)
					}
					continue
				}
				if best != 0 {
					t.Fatalf("entry %d (frame %d at byte %d) lands %d frames off", k, start, index.Offsets[k], best)
				}
				if c.exact {
					for i := 0; i < window; i++ {
						if d := int(got[settle+i]) - int(full[int(start)+settle+i]); d > 1 || d < -1 {
							t.Fatalf("entry %d: sample %d differs by %d", k, i, d)
						}
					}
				}
			}
		})
	}
}

// decodeRaw decodes a file to mono 16-bit samples as the decoder makes them:
// nothing skipped (no encoder delay or padding trimmed), so sample i is raw
// frame i.
func decodeRaw(t *testing.T, ffmpeg, path string) []int16 {
	t.Helper()
	out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-flags2", "+skip_manual", "-i", path, "-map", "0:a:0", "-f", "s16le", "-ac", "1", "-").Output()
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	samples := make([]int16, len(out)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(out[2*i:]))
	}
	return samples
}
