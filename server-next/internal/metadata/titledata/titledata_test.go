package titledata_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/titledata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/titledata/titledatatest"
)

var ctx = context.Background()

func newUpdater(t *testing.T, key ed25519.PrivateKey) (*titledata.Updater, *titledatatest.Host) {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	setSwitch(t, d, true)
	host := &titledatatest.Host{}
	return &titledata.Updater{DB: d, Dir: filepath.Join(t.TempDir(), "title-data"), Source: host,
		Key: key.Public().(ed25519.PublicKey)}, host
}

func setSwitch(t *testing.T, d *db.DB, on bool) {
	t.Helper()
	raw, _ := json.Marshal(titledata.Setting{Download: &on, Address: titledatatest.Address})
	if err := d.SaveSetting(ctx, titledata.SettingKey, string(raw), timeNow()); err != nil {
		t.Fatal(err)
	}
}

// files lists what is kept in the updater's folder.
func files(t *testing.T, u *titledata.Updater) []string {
	t.Helper()
	entries, err := os.ReadDir(u.Dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

var sample = titledatatest.File{
	Vocabulary: []titledatatest.Tag{{ID: 1, Key: "mood:cozy", RowLabel: "cozy stories"}},
	Works:      []titledatatest.Work{{QID: 1, IDs: map[int]string{titledatatest.TMDBMovie: "603"}, Tags: map[int]int{1: 3}}},
}

// A release signed by the server's key is installed. Every way a list or file can be wrong is
// refused, and each time the copy in use stays, nothing half-downloaded is left, and the next
// good release still installs. Each case starts from its own server, so no case can pass on
// what an earlier one left behind.
func TestOnlyASignedMatchingFileIsUsed(t *testing.T) {
	key := titledatatest.NewKey(t)
	installed := func() (*titledata.Updater, *titledatatest.Host) {
		u, host := newUpdater(t, key)
		first := sample
		first.Version = "2026.44"
		host.Serve(titledatatest.Publish(t, key, 5, first))
		if err := u.Check(ctx); err != nil {
			t.Fatal(err)
		}
		if got := files(t, u); !slices.Equal(got, []string{"screen-2026.44.sqlite"}) {
			t.Fatalf("after the first release the folder holds %v", got)
		}
		return u, host
	}

	next := func() *titledatatest.Release { return titledatatest.Publish(t, key, 6, sample) }
	for name, release := range map[string]*titledatatest.Release{
		"signed by another key": func() *titledatatest.Release {
			r := next()
			r.Key = titledatatest.NewKey(t)
			r.Sign()
			return r
		}(),
		"changed after signing": func() *titledatatest.Release {
			r := next()
			r.List[len(r.List)-2] = ' '
			return r
		}(),
		"names another key": func() *titledatatest.Release {
			r := next()
			r.Manifest["key_id"] = titledata.KeyID(titledatatest.NewKey(t).Public().(ed25519.PublicKey))
			r.Sign()
			return r
		}(),
		"an older release": titledatatest.Publish(t, key, 4, sample),
		"another schema": func() *titledatatest.Release {
			r := next()
			r.Manifest["schema_version"] = 2
			r.Sign()
			return r
		}(),
		"a different SHA-256": func() *titledatatest.Release {
			r := next()
			sum, err := hex.DecodeString(r.Full()["sha256"].(string))
			if err != nil {
				t.Fatal(err)
			}
			sum[0] ^= 0xff // always another value, whatever the real one is
			r.Full()["sha256"] = hex.EncodeToString(sum)
			r.Sign()
			return r
		}(),
		"a different download size": func() *titledatatest.Release {
			r := next()
			r.Full()["bytes"] = len(r.Gzip) - 1
			r.Sign()
			return r
		}(),
		"a different expanded size": func() *titledatatest.Release {
			r := next()
			r.Full()["uncompressed_bytes"] = r.Full()["uncompressed_bytes"].(int) - 1
			r.Sign()
			return r
		}(),
		"a file on another host": func() *titledatatest.Release {
			r := next()
			r.Full()["url"] = "https://elsewhere.example/screen.sqlite.gz"
			r.Sign()
			return r
		}(),
		"a file of another version than the list": func() *titledatatest.Release {
			other := sample
			other.Version = "2026.46"
			r := titledatatest.Publish(t, key, 6, other)
			r.Manifest["domains"].(map[string]any)["screen"].(map[string]any)["dataset_version"] = "2026.45"
			r.Sign()
			return r
		}(),
		"a delta where a full file belongs": func() *titledatatest.Release {
			delta := sample
			delta.Kind = "delta"
			return titledatatest.Publish(t, key, 6, delta)
		}(),
		"a file of another schema": func() *titledatatest.Release {
			r := next()
			r.Gzip = gzipOf(t, func(raw []byte) { raw[60+3] = 2 }) // user_version is bytes 60 to 63 of the header
			r.Full()["sha256"], r.Full()["bytes"] = sha(r.Gzip), len(r.Gzip)
			r.Sign()
			return r
		}(),
		"a damaged file": func() *titledatatest.Release {
			r := next()
			r.Gzip = gzipOf(t, func(raw []byte) {
				for i := 4096; i < len(raw); i++ {
					raw[i] ^= 0x5a
				}
			})
			r.Full()["sha256"], r.Full()["bytes"] = sha(r.Gzip), len(r.Gzip)
			r.Sign()
			return r
		}(),
	} {
		u, host := installed()
		host.Serve(release)
		if err := u.Check(ctx); !errors.Is(err, titledata.ErrRefused) {
			t.Errorf("%s: want a refusal, got %v", name, err)
		}
		if got := files(t, u); !slices.Equal(got, []string{"screen-2026.44.sqlite"}) {
			t.Errorf("%s: the folder holds %v, want only the copy in use", name, got)
		}
		host.Serve(next())
		if err := u.Check(ctx); err != nil {
			t.Errorf("%s: the good release after it: %v", name, err)
		}
		if got := files(t, u); !slices.Equal(got, []string{"screen-2026.45.sqlite"}) {
			t.Errorf("%s: after a good release the folder holds %v", name, got)
		}
	}

	u, host := installed()
	host.Serve(next())
	if err := u.Check(ctx); err != nil {
		t.Fatal(err)
	}
	requests := host.Requests
	if err := u.Check(ctx); err != nil || host.Requests != requests+2 {
		t.Fatalf("a check with nothing new should fetch only the list and its signature: %d requests, %v", host.Requests-requests, err)
	}
}

// Switched off, the file goes and nothing is asked for. Switched on again, the newest release
// accepted before still decides: an older list is refused.
func TestSwitchedOffRemovesTheFileAndAsksNothing(t *testing.T) {
	key := titledatatest.NewKey(t)
	u, host := newUpdater(t, key)
	host.Serve(titledatatest.Publish(t, key, 5, sample))
	if err := u.Check(ctx); err != nil {
		t.Fatal(err)
	}
	setSwitch(t, u.DB, false)
	requests := host.Requests
	if err := u.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if got := files(t, u); len(got) != 0 || host.Requests != requests {
		t.Fatalf("switched off: the folder holds %v and %d requests were made", got, host.Requests-requests)
	}
	setSwitch(t, u.DB, true)
	host.Serve(titledatatest.Publish(t, key, 4, sample))
	if err := u.Check(ctx); !errors.Is(err, titledata.ErrRefused) {
		t.Fatalf("an older release after switching on again: want a refusal, got %v", err)
	}
}

func timeNow() time.Time { return time.Now() }

// gzipOf is the sample file, changed by change, then compressed.
func gzipOf(t *testing.T, change func([]byte)) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "screen.sqlite")
	titledatatest.Write(t, path, sample)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	change(raw)
	var packed bytes.Buffer
	zw := gzip.NewWriter(&packed)
	zw.Write(raw)
	zw.Close()
	return packed.Bytes()
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Plain HTTP is used only from this machine's own loopback address, where a test publication is
// served; anywhere else the list must come over HTTPS.
func TestPlainHTTPOnlyFromLoopback(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "list") }))
	defer local.Close()
	body, err := titledata.HTTPS{}.Open(ctx, local.URL+"/v3/manifest.json")
	if err != nil {
		t.Fatalf("a list on the loopback address is refused: %v", err)
	}
	body.Close()
	for _, address := range []string{"http://data.getportico.tv/v3/manifest.json", "http://localhost.example/v3/manifest.json",
		"http://192.168.1.2/v3/manifest.json", "ftp://127.0.0.1/v3/manifest.json"} {
		if _, err := (titledata.HTTPS{}).Open(ctx, address); !errors.Is(err, titledata.ErrRefused) {
			t.Fatalf("%s: %v, want refused", address, err)
		}
	}
}

// saveState writes what a server remembers about its title data, as an older build left it.
func saveState(t *testing.T, d *db.DB, st map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(st)
	if err := d.SaveSetting(ctx, "title_data_state", string(raw), timeNow()); err != nil {
		t.Fatal(err)
	}
}

// A new format of the data is published in a folder of its own whose release numbers start
// again at 1. A server that moves to the new format accepts its first release, though it had
// accepted a higher number in the old format, and never opens the file it kept in the old one.
// (The test moves from the format before this server's to this server's.)
func TestANewFormatStartsItsOwnReleaseNumbers(t *testing.T) {
	key := titledatatest.NewKey(t)
	u, host := newUpdater(t, key)
	if err := os.MkdirAll(u.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A usable file, so only its format keeps it out.
	titledatatest.Write(t, filepath.Join(u.Dir, "screen-2026.30.sqlite"), sample)
	saveState(t, u.DB, map[string]any{"schema": db.TitleDataSchema - 1, "sequence": 40, "version": "2026.30",
		"file": "screen-2026.30.sqlite"})
	if err := u.Open(ctx); err != nil {
		t.Fatal(err)
	}
	if got := files(t, u); len(got) != 0 {
		t.Fatalf("the file kept in the old format is still there after a restart: %v", got)
	}
	host.Serve(titledatatest.Publish(t, key, 1, sample))
	if err := u.Check(ctx); err != nil {
		t.Fatalf("release 1 of the new format is refused: %v", err)
	}
	if got := files(t, u); !slices.Equal(got, []string{"screen-2026.45.sqlite"}) {
		t.Fatalf("after release 1 of the new format the folder holds %v", got)
	}
	// From here the new format's numbers count: an older one of it is refused.
	host.Serve(titledatatest.Publish(t, key, 2, sample))
	if err := u.Check(ctx); err != nil {
		t.Fatal(err)
	}
	host.Serve(titledatatest.Publish(t, key, 1, sample))
	if err := u.Check(ctx); !errors.Is(err, titledata.ErrRefused) {
		t.Fatalf("release 1 after release 2 of the same format: %v", err)
	}
}

// What a server saved before it recorded the format is the format it read then, 3: an older
// release of that format is still refused, and its file is still opened.
func TestAStateSavedBeforeTheFormatWasRecordedIsFormat3(t *testing.T) {
	if db.TitleDataSchema != 3 {
		t.Skip("this server reads another format: such a state is now the old format")
	}
	key := titledatatest.NewKey(t)
	u, host := newUpdater(t, key)
	host.Serve(titledatatest.Publish(t, key, 5, sample))
	if err := u.Check(ctx); err != nil {
		t.Fatal(err)
	}
	saveState(t, u.DB, map[string]any{"sequence": 5, "version": "2026.45", "file": "screen-2026.45.sqlite"})
	if err := u.Open(ctx); err != nil {
		t.Fatal(err)
	}
	if got := files(t, u); !slices.Equal(got, []string{"screen-2026.45.sqlite"}) {
		t.Fatalf("after a restart the folder holds %v", got)
	}
	host.Serve(titledatatest.Publish(t, key, 4, sample))
	if err := u.Check(ctx); !errors.Is(err, titledata.ErrRefused) {
		t.Fatalf("release 4 after release 5: %v", err)
	}
}
