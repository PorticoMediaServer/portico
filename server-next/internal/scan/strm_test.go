package scan_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/strm"
)

type strmProber struct {
	t       *testing.T
	content []byte
}

func (p strmProber) ProbeReader(_ context.Context, file io.ReaderAt, size int64) (scan.Facts, error) {
	p.t.Helper()
	raw := make([]byte, size)
	if _, err := file.ReadAt(raw, 0); err != nil || !bytes.Equal(raw, p.content) {
		p.t.Fatal("probe did not receive target bytes")
	}
	return scan.Facts{Title: "Target title must not name pointer", Container: "mp4", DurationMS: 60000,
		Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264", Height: 720}}}, nil
}

func TestSTRMOptInNamesSidecarsAndRescans(t *testing.T) {
	f := newFixture(t, "movies")
	content := []byte("fixture media bytes")
	var requests atomic.Int64
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.ServeContent(w, r, "different-target-name.mp4", time.Time{}, bytes.NewReader(content))
	}))
	defer outside.Close()
	client, address := strm.New(), outside.URL
	f.s.STRM, f.s.ReaderProber = client, strmProber{t, content}
	pointer := filepath.Join(f.root, "Heat (1995).strm")
	if err := os.WriteFile(pointer, []byte(address+"/different-target-name?token=secret"), 0600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(f.root, "Heat (1995).en.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n"), 0600)
	f.scan()
	if len(f.titles(db.KindMovie)) != 0 || requests.Load() != 0 {
		t.Fatal("off library followed STRM")
	}
	options := db.LibraryOptions{STRM: true, Depth: db.DepthFiles, Trash: db.TrashRule{Mode: db.TrashManual}}
	if err := f.d.SetLibraryOptions(ctx, f.lib.ID, options); err != nil {
		t.Fatal(err)
	}
	if stats := f.scan(); stats.Probed != 0 || requests.Load() != 0 {
		t.Fatal("list-only scan opened STRM target")
	}
	options.Depth = db.DepthBasic
	if err := f.d.SetLibraryOptions(ctx, f.lib.ID, options); err != nil {
		t.Fatal(err)
	}
	if stats := f.scan(); stats.Probed != 1 {
		t.Fatalf("STRM scan: %+v", stats)
	}
	movies := f.titles(db.KindMovie)
	film, ok := movies["Heat"]
	if !ok || len(movies) != 1 {
		t.Fatalf("pointer names: %+v", movies)
	}
	f.watch(film.PublicID)
	detail := f.detail(film.PublicID)
	if len(detail.Versions) != 1 {
		t.Fatal("pointer is not a version")
	}
	item, err := f.d.PlaybackItem(ctx, f.viewer, film.PublicID)
	if err != nil || len(item.Versions[0].Tracks) != 2 {
		t.Fatalf("target video and neighbouring subtitle: %+v %v", item, err)
	}
	before := requests.Load()
	f.scan()
	if requests.Load() != before {
		t.Fatal("unchanged pointer fetched target again")
	}
	if err := os.WriteFile(pointer, []byte(address+"/different-target-name?token=changed"), 0600); err != nil {
		t.Fatal(err)
	}
	f.scan()
	if requests.Load() == before {
		t.Fatal("changed pointer was not read")
	}
	before = requests.Load()
	if err := f.d.ForgetProbes(ctx, f.lib.ID); err != nil {
		t.Fatal(err)
	}
	f.s.AllFiles = true
	f.scan()
	f.s.AllFiles = false
	if requests.Load() == before {
		t.Fatal("Scan all files did not reread target")
	}
	options.STRM = false
	if err := f.d.SetLibraryOptions(ctx, f.lib.ID, options); err != nil {
		t.Fatal(err)
	}
	enabled, err := f.d.STRMEnabled(ctx, item.Versions[0].FileID)
	if err != nil || enabled {
		t.Fatal("switch off did not block new acquisition")
	}
	f.scan()
	if _, total, err := f.d.MissingItems(ctx, f.lib.ID, 10); err != nil || total != 1 {
		t.Fatal("disabled pointer is still available")
	}
	options.STRM = true
	f.d.SetLibraryOptions(ctx, f.lib.ID, options)
	f.scan()
	if f.titles(db.KindMovie)["Heat"].PublicID != film.PublicID || !f.detail(film.PublicID).State.Watched {
		t.Fatal("switch changed title identity")
	}
}

func TestBadSTRMIsNotRetriedUntilChanged(t *testing.T) {
	f := newFixture(t, "movies")
	pointer := filepath.Join(f.root, "Film.strm")
	os.WriteFile(pointer, []byte("file:///etc/passwd"), 0600)
	f.scan()
	options := db.LibraryOptions{STRM: true}
	f.d.SetLibraryOptions(ctx, f.lib.ID, options)
	f.scan()
	unread, err := f.d.UnprobedCount(ctx, f.lib.ID)
	if err != nil || unread != 0 {
		t.Fatalf("bad unchanged STRM is retried on every scan: %d %v", unread, err)
	}
	if len(f.titles(db.KindMovie)) != 1 {
		t.Fatal("bad STRM lost its filename title")
	}
}
