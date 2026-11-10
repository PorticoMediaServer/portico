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
// good release still installs.
func TestOnlyASignedMatchingFileIsUsed(t *testing.T) {
	key := titledatatest.NewKey(t)
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
			r.Full()["sha256"] = "00" + r.Full()["sha256"].(string)[2:]
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
		host.Serve(release)
		if err := u.Check(ctx); !errors.Is(err, titledata.ErrRefused) {
			t.Errorf("%s: want a refusal, got %v", name, err)
		}
		if got := files(t, u); !slices.Equal(got, []string{"screen-2026.44.sqlite"}) {
			t.Errorf("%s: the folder holds %v, want only the copy in use", name, got)
		}
	}

	host.Serve(next())
	if err := u.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if got := files(t, u); !slices.Equal(got, []string{"screen-2026.45.sqlite"}) {
		t.Fatalf("after a good release the folder holds %v", got)
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
