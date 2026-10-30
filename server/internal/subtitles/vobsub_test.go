package subtitles

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVobSubCapsuleRewrapsPacketsAndPalette(t *testing.T) {
	element := func(id, body []byte) []byte {
		out := append([]byte{}, id...)
		n := len(body)
		if n < 127 {
			out = append(out, 0x80|byte(n))
		} else {
			out = append(out, 0x40|byte(n>>8), byte(n))
		}
		return append(out, body...)
	}
	spu := []byte{0, 6, 0, 4, 0xff, 0xff}
	private := []byte("size: 720x576\npalette: 000000, ffffff, 000000, ffffff, 000000, ffffff, 000000, ffffff, 000000, ffffff, 000000, ffffff, 000000, ffffff, 000000, ffffff\n")
	tracks := element([]byte{0x16, 0x54, 0xae, 0x6b}, element([]byte{0xae}, element([]byte{0x63, 0xa2}, private)))
	block := append([]byte{0x81, 0x03, 0xe8, 0}, spu...)
	cluster := element([]byte{0x1f, 0x43, 0xb6, 0x75}, append(element([]byte{0xe7}, []byte{0}), element([]byte{0xa0}, element([]byte{0xa1}, block))...))
	capsule := element([]byte{0x18, 0x53, 0x80, 0x67}, append(tracks, cluster...))
	idx, sub, e := VobSubOriginal(capsule)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(idx), "size: 720x576") || !strings.Contains(string(idx), "timestamp: 00:00:01:000, filepos: 000000000") || len(sub) != 2048 || !bytes.Equal(sub[29:35], spu) {
		t.Fatal(string(idx), len(sub))
	}
	if _, e = CanonicalAsset(idx, sub, "vobsub", 0); e != nil {
		t.Fatal(e)
	}
	if probe, e := exec.LookPath("ffprobe"); e == nil {
		dir := t.TempDir()
		idxPath := filepath.Join(dir, "original.idx")
		if e = os.WriteFile(idxPath, idx, 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "original.sub"), sub, 0600); e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		output, e := exec.CommandContext(ctx, probe, "-v", "error", "-show_entries", "stream=codec_name", "-of", "default=nw=1", idxPath).CombinedOutput()
		if e != nil || !strings.Contains(string(output), "dvd_subtitle") {
			t.Fatalf("VobSub decoder did not recognize rewrapped packets: %v %s", e, output)
		}
	}

	if _, _, e = VobSubOriginal([]byte{0x1f, 0x43, 0xb6, 0x75, 0xff, 0}); e == nil {
		t.Fatal("malformed capsule accepted")
	}
}
