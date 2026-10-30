package playback

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A sparse PCM source makes a real thirteen-hour final seek inexpensive: only
// its header and last seconds need disk blocks. FFmpeg still reads the real
// source descriptor and produces the final HLS segment on the production path.
func TestLongAudiobookFinalSegmentUsesSourceTimestamp(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	path := filepath.Join(root, "long.wav")
	const seconds = 13*3600 + 6
	const rate = 8000
	const bytes = seconds * rate * 2
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], bytes+36)
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], 1)
	binary.LittleEndian.PutUint32(header[24:], rate)
	binary.LittleEndian.PutUint32(header[28:], rate*2)
	binary.LittleEndian.PutUint16(header[32:], 2)
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], bytes)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(header); err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(bytes + 44); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mustAudioExec(t, db, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',X'00','profile',1)`)
	insertFixtureSession(t, db, "login", "owner", "profile", "owner", "2099-01-01T00:00:00Z")
	_, item, _ := catalogFixture(t, db, "lib", root, compactcatalog.Part, "item", "Long book", compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "wav", AudioCodec: "pcm_s16le", Duration: seconds})
	h, err := NewHLS(ctx, db, filepath.Join(root, "hls"), ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown(context.Background())
	s := New(db)
	s.ConfigureHLS(h)
	s.ConfigureDelivery(nil, nil, nil, "")
	p := identity.Principal{Hash: "login", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}, Epoch: 1}
	raw, _ := json.Marshal(documentOf(chromeProfile()))
	if _, err = s.PublishCapabilities(ctx, p, raw); err != nil {
		t.Fatal(err)
	}
	session, err := s.Create(p, item, "automatic", "long-book")
	if err != nil {
		t.Fatal(err)
	}
	if session.Mode != "hls" {
		t.Fatalf("source was not converted: %s", session.Mode)
	}
	last := hlsSegmentCount(seconds) - 1
	if err = h.start(ctx, session.ID, last); err != nil {
		t.Fatal(err)
	}
	grant := strings.TrimSuffix(strings.TrimPrefix(session.StreamURL, "/v1/media/"), "/master.m3u8")
	var final string
	for {
		final, err = h.FileContext(ctx, grant, hlsSegmentFile(last))
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("final seek failed", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	out, err := exec.CommandContext(ctx, decodertest.QualifiedFFprobe(t), "-v", "error", "-select_streams", "a:0", "-show_entries", "packet=pts_time", "-of", "csv=p=0", final).Output()
	if err != nil {
		t.Fatal(err)
	}
	first, err := strconv.ParseFloat(strings.TrimSuffix(strings.Split(strings.TrimSpace(string(out)), "\n")[0], ","), 64)
	if err != nil || math.Abs(first-float64(last*HLSSegmentSeconds)) > .2 {
		t.Fatalf("wrong last timestamp: %f %v (%s)", first, err, out)
	}
	manifest, err := os.ReadFile(filepath.Join(h.root, session.ID, "master.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), hlsSegmentFile(last)) {
		t.Fatal("final audio timestamp not advertised")
	}
}
