package decoder

import (
	"bytes"
	"encoding/binary"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"time"
)

func TestMusicUntouchedPCMBitExactR29(t *testing.T) {
	testtier.Media(t, "encodes and decodes actual bounded music fixtures through FFmpeg")
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	const rate, frames = 88200, 4096
	pcm := make([]byte, frames*2*3)
	seed := uint32(7)
	for n := range pcm {
		seed = seed*1664525 + 1013904223
		pcm[n] = byte(seed >> 24)
	}
	var wave bytes.Buffer
	wave.WriteString("RIFF")
	binary.Write(&wave, binary.LittleEndian, uint32(len(pcm)+36))
	wave.WriteString("WAVEfmt ")
	binary.Write(&wave, binary.LittleEndian, uint32(16))
	for _, n := range []uint16{1, 2} {
		binary.Write(&wave, binary.LittleEndian, n)
	}
	binary.Write(&wave, binary.LittleEndian, uint32(rate))
	binary.Write(&wave, binary.LittleEndian, uint32(rate*6))
	binary.Write(&wave, binary.LittleEndian, uint16(6))
	binary.Write(&wave, binary.LittleEndian, uint16(24))
	wave.WriteString("data")
	binary.Write(&wave, binary.LittleEndian, uint32(len(pcm)))
	wave.Write(pcm)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "tone.wav", time.Time{}, bytes.NewReader(wave.Bytes()))
	}))
	defer server.Close()
	spec := AnalysisSpec{Kind: "audio_flac", MaxDurationUS: 1000000, AudioFrames: frames, AudioOut: frames, AudioRate: rate, AudioSourceRate: rate, AudioChannels: 2, AudioSourceDepth: 24, AudioBitDepth: 24}
	args, e := AnalysisArgs(server.URL, spec)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(strings.Join(args, " "), "aresample") {
		t.Fatal("untouched source resampled", args)
	}
	encoded, e := exec.Command(ff, args...).Output()
	if e != nil {
		t.Fatal(e)
	}
	decode := exec.Command(ff, "-v", "error", "-i", "pipe:0", "-c:a", "pcm_s24le", "-f", "s24le", "pipe:1")
	decode.Stdin = bytes.NewReader(encoded)
	raw, e := decode.Output()
	if e != nil || !bytes.Equal(raw, pcm) {
		t.Fatal("untouched PCM changed", e, len(raw), len(pcm))
	}
	spec.AudioBitDepth = 16
	args, e = AnalysisArgs(server.URL, spec)
	if e != nil || !strings.Contains(strings.Join(args, " "), "resampler=swr:osf=s16:output_sample_bits=16:dither_method=triangular") {
		t.Fatal(args, e)
	}
}

func TestMusicTruePeakLimiterAndAudibleLengthR29(t *testing.T) {
	testtier.Media(t, "encodes and decodes actual bounded music fixtures through FFmpeg")
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	const rate, frames, channels = 48000, 9600, 2
	pcm := make([]byte, frames*channels*3)
	for n := 0; n < frames; n++ {
		v := int32(math.Round(0.9 * math.Sin(2*math.Pi*19000*float64(n)/rate) * (1<<23 - 1)))
		for c := 0; c < channels; c++ {
			off := (n*channels + c) * 3
			pcm[off], pcm[off+1], pcm[off+2] = byte(v), byte(v>>8), byte(v>>16)
		}
	}
	var wave bytes.Buffer
	wave.WriteString("RIFF")
	binary.Write(&wave, binary.LittleEndian, uint32(len(pcm)+36))
	wave.WriteString("WAVEfmt ")
	binary.Write(&wave, binary.LittleEndian, uint32(16))
	binary.Write(&wave, binary.LittleEndian, uint16(1))
	binary.Write(&wave, binary.LittleEndian, uint16(channels))
	binary.Write(&wave, binary.LittleEndian, uint32(rate))
	binary.Write(&wave, binary.LittleEndian, uint32(rate*channels*3))
	binary.Write(&wave, binary.LittleEndian, uint16(channels*3))
	binary.Write(&wave, binary.LittleEndian, uint16(24))
	wave.WriteString("data")
	binary.Write(&wave, binary.LittleEndian, uint32(len(pcm)))
	wave.Write(pcm)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "tone.wav", time.Time{}, bytes.NewReader(wave.Bytes()))
	}))
	defer server.Close()
	args, e := AnalysisArgs(server.URL, AnalysisSpec{Kind: "audio_flac", MaxDurationUS: 1000000, AudioFrames: frames, AudioOut: frames, AudioRate: rate, AudioSourceRate: rate, AudioChannels: channels, AudioSourceDepth: 24, AudioBitDepth: 24, AudioTruePeakLimit: true, AudioGainDB: 12})
	if e != nil {
		t.Fatal(e)
	}
	encoded, e := exec.Command(ff, args...).Output()
	if e != nil {
		t.Fatal(e)
	}
	decode := func(data []byte, oversample bool, gain bool) []byte {
		args := []string{"-v", "error", "-i", "pipe:0"}
		filter := ""
		if gain {
			filter = "volume=12dB:precision=double"
		}
		if oversample {
			if filter != "" {
				filter += ","
			}
			filter += "aresample=192000:resampler=soxr:precision=28:osf=dbl"
		}
		if filter != "" {
			args = append(args, "-af", filter)
		}
		args = append(args, "-c:a", "pcm_f64le", "-f", "f64le", "pipe:1")
		cmd := exec.Command(ff, args...)
		cmd.Stdin = bytes.NewReader(data)
		raw, e := cmd.Output()
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	if raw := decode(encoded, false, false); len(raw) != frames*channels*8 {
		t.Fatal("limiter changed audible length", len(raw))
	}
	peak := func(data []byte) float64 {
		var p float64
		for len(data) >= 8 {
			v := math.Abs(math.Float64frombits(binary.LittleEndian.Uint64(data)))
			p = math.Max(p, v)
			data = data[8:]
		}
		return p
	}
	if control := peak(decode(wave.Bytes(), true, true)); control <= 2 {
		t.Fatal("ineffective over-level test", control)
	}
	got := peak(decode(encoded, true, false))
	if got > 1 || got < 0.2 {
		t.Fatal("true-peak ceiling failed or muted", got)
	}
	t.Logf("four-times reconstructed peak=%f; exact audible frames=%d", got, frames)
}

func TestMusic24BitDitherActuallyQuantizesR29(t *testing.T) {
	testtier.Media(t, "encodes and decodes actual bounded music fixtures through FFmpeg")
	ff, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	const frames, rate = 4096, 48000
	var input bytes.Buffer
	input.WriteString("RIFF")
	binary.Write(&input, binary.LittleEndian, uint32(frames*4+36))
	input.WriteString("WAVEfmt ")
	binary.Write(&input, binary.LittleEndian, uint32(16))
	binary.Write(&input, binary.LittleEndian, uint16(1))
	binary.Write(&input, binary.LittleEndian, uint16(1))
	binary.Write(&input, binary.LittleEndian, uint32(rate))
	binary.Write(&input, binary.LittleEndian, uint32(rate*4))
	binary.Write(&input, binary.LittleEndian, uint16(4))
	binary.Write(&input, binary.LittleEndian, uint16(32))
	input.WriteString("data")
	binary.Write(&input, binary.LittleEndian, uint32(frames*4))
	// Exactly half of one 24-bit LSB in a 32-bit integer source. Undithered
	// conversion collapses every sample to the same integer; TPDF does not.
	for n := 0; n < frames; n++ {
		binary.Write(&input, binary.LittleEndian, int32(128))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "tone.wav", time.Time{}, bytes.NewReader(input.Bytes()))
	}))
	defer server.Close()
	args, e := AnalysisArgs(server.URL, AnalysisSpec{Kind: "audio_flac", MaxDurationUS: 1000000, AudioFrames: frames, AudioOut: frames, AudioRate: rate, AudioSourceRate: rate, AudioChannels: 1, AudioSourceDepth: 32, AudioBitDepth: 24})
	if e != nil {
		t.Fatal(e)
	}
	data, e := exec.Command(ff, args...).Output()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(ff, "-v", "error", "-i", "pipe:0", "-c:a", "pcm_s32le", "-f", "s32le", "pipe:1")
	cmd.Stdin = bytes.NewReader(data)
	raw, e := cmd.Output()
	if e != nil || len(raw) != frames*4 {
		t.Fatal(len(raw), e)
	}
	levels := map[int32]bool{}
	sum := int64(0)
	for len(raw) > 0 {
		v := int32(binary.LittleEndian.Uint32(raw))
		if v&255 != 0 || v < -512 || v > 512 {
			t.Fatal("incorrect used sample depth", v)
		}
		levels[v] = true
		sum += int64(v)
		raw = raw[4:]
	}
	mean := float64(sum) / frames
	if len(levels) < 2 || math.Abs(mean-128) > 40 {
		t.Fatal("missing or biased 24-bit dither", levels, mean)
	}
}
