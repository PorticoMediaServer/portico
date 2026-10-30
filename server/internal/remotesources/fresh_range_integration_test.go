//go:build rcloneintegration

package remotesources

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

func lockedRcloneFixture(t *testing.T) string {
	t.Helper()
	program := os.Getenv("PORTICO_RCLONE_TEST_PROGRAM")
	if program == "" {
		t.Fatal("PORTICO_RCLONE_TEST_PROGRAM must name the locked rclone executable")
	}
	lockRaw, err := os.ReadFile("../../../third_party/rclone/bundle.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Targets map[string]struct{ Files map[string]string }
	}
	if err = json.Unmarshal(lockRaw, &lock); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(program)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	if hex.EncodeToString(digest[:]) != lock.Targets["linux-amd64"].Files["rclone"] {
		t.Fatal("unqualified integration executable")
	}
	clear(binary)
	return program
}

func TestFreshRangeRealMetadataAndRetainedRevocations(t *testing.T) {
	program := lockedRcloneFixture(t)
	for _, changed := range []string{"mtime", "observation", "root", "actor", "process", "sequential/clean", "sequential/mtime", "sequential/actor", "sequential/process"} {
		t.Run(changed, func(t *testing.T) {
			sequential := strings.HasPrefix(changed, "sequential/")
			changed = strings.TrimPrefix(changed, "sequential/")
			f := newObservationFixture(t)
			identities, owner, family := observationOwner(t, f)
			if err := os.WriteFile(f.file, bytes.Repeat([]byte("A"), 16), 0600); err != nil {
				t.Fatal(err)
			}
			mtime := time.Unix(1700000000, 23)
			if err := os.Chtimes(f.file, mtime, mtime); err != nil {
				t.Fatal(err)
			}
			port := func() int {
				listener, err := net.Listen("tcp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				p := listener.Addr().(*net.TCPAddr).Port
				_ = listener.Close()
				return p
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			lease, err := f.s.reserveSourceProcess(ctx, "remote", "1", 0)
			if err != nil {
				t.Fatal(err)
			}
			spec := rcloneprogram.ServeCommandSpec{SourceID: "remote", SourceGeneration: "1", PrivateDirectory: t.TempDir(), Configuration: []byte("[fixture]\ntype=local\n"), Remote: "fixture:" + filepath.Dir(f.file), Port: port(), MetadataPort: port(), Username: "private", Password: "fixture-only", Connections: 1, RequestsPerSecond: 2, SequentialReads: sequential,
				Enforce: func(*exec.Cmd) error { return nil }} // Local fixture only; no egress/production-authority claim.
			process, err := rcloneprogram.StartServe(ctx, rcloneprogram.Program{Path: program, Included: true}, spec, lease)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				process.Stop()
				wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := process.Wait(wait); !errors.Is(err, rcloneprogram.ErrServeStopped) || !lease.released.Load() {
					t.Error("process custody", err, lease.released.Load())
				}
			})
			check := func(ctx context.Context) error {
				snapshot, err := dbwork.BeginSnapshot(ctx, f.s.db)
				if err != nil {
					return err
				}
				defer snapshot.Rollback()
				return currentLocalOwnerTx(ctx, snapshot.Tx(), identities, owner)
			}
			metadata, err := process.Metadata(ctx)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := metadata.Stat(ctx, "film.mkv", check)
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := process.Endpoint(ctx)
			if err != nil {
				t.Fatal(err)
			}
			served, err := endpoint.Stat(ctx, "film.mkv", check)
			if err != nil {
				t.Fatal(err)
			}
			facts := remotemedia.FileObservation{SourceID: "remote", Incarnation: "1", Object: "film.mkv", ObjectID: fresh.ID, Size: fresh.Size, ModifiedNS: fresh.Modified.UnixNano(), ModifiedKnown: true, ProviderHashes: fresh.Hashes, ServeETag: served.ETag}
			ref, err := f.record(t, ScanObservation{Facts: facts}, 0)
			if err != nil {
				t.Fatal(err)
			}
			notices := 0
			admission := FreshReadAdmission{Observation: ref, ProcessGeneration: process.Generation(), Check: check, OnChanged: func() { notices++ }}
			var read func(context.Context, []byte) (int, error)
			if sequential {
				if _, err = f.s.db.Exec(`UPDATE remote_sources SET range_support='unsupported',error_code='range_unsupported'`); err != nil {
					t.Fatal(err)
				}
				session, err := f.s.OpenFreshSequential(ctx, process, admission)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				read = session.Read
			} else {
				session, err := f.s.OpenFreshRange(ctx, process, admission)
				if err != nil {
					t.Fatal(err)
				}
				defer session.Close()
				read = func(ctx context.Context, p []byte) (int, error) { return session.ReadAt(ctx, p, 8) }
			}
			// Exercise an actual transport reconnect at the minimum connection
			// profile. Its fresh observation cannot wait on the range dial slot.
			endpoint.Close()
			p := make([]byte, 4)
			if n, err := read(ctx, p); err != nil || n != 4 || string(p) != "AAAA" {
				t.Fatal("real admitted range", n, string(p), err)
			}
			if sequential && changed == "clean" {
				p = make([]byte, 12)
				if n, err := read(ctx, p); n != 12 || err != nil || string(p) != strings.Repeat("A", 12) {
					t.Fatal("real sequential remainder", n, string(p), err)
				}
				if n, err := read(ctx, p); n != 0 || err != io.EOF {
					t.Fatal("real sequential end", n, err)
				}
				return
			}
			switch changed {
			case "mtime":
				if err = os.Chtimes(f.file, mtime.Add(time.Second), mtime.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			case "observation":
				_, err = f.s.db.Exec(`UPDATE remote_scan_observations SET revision=revision+1`)
			case "root":
				_, err = f.s.db.Exec(`UPDATE remote_sources SET root=?`, t.TempDir())
			case "actor":
				_, err = f.s.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, family)
			case "process":
				process.Stop()
				_ = process.Wait(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			p = []byte("----")
			if sequential {
				p = []byte(strings.Repeat("-", 12))
			}
			unchanged := string(p)
			for range 2 {
				if n, err := read(ctx, p); n != 0 || err == nil || string(p) != unchanged {
					t.Fatal("revoked range resumed", changed, n, string(p), err)
				}
			}
			if changed == "mtime" || changed == "observation" || changed == "root" {
				if notices != 1 {
					t.Fatal("rescan notification count", notices)
				}
			}
		})
	}
}

// This origin deliberately ignores Range. Its one progressive body is larger
// than the VFS chunk size used for ranged sources: a second range reopen would
// repeat the prefix. The sequential mode must use one actual from-start open.
func TestFreshSequentialRealRangeUnsupportedOrigin(t *testing.T) {
	program := lockedRcloneFixture(t)
	f := newObservationFixture(t)
	const size = 2<<20 + 7
	modified := time.Unix(1700000000, 0).UTC()
	var gets atomic.Int32
	var routeMismatch, redirect atomic.Bool
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || host != "127.0.0.1" {
			routeMismatch.Store(true)
		}
		if redirect.Load() {
			http.Redirect(w, r, "http://169.254.169.254/latest/", 302)
			return
		}
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<a href="film.mkv">film.mkv</a>`)
			return
		}
		if r.URL.Path != "/film.mkv" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(size))
		w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Accept-Ranges", "none")
		if r.Method == http.MethodHead {
			return
		}
		gets.Add(1)
		// Ignore every Range, including rclone's bytes=0- initial hint.
		block := make([]byte, 32<<10)
		for offset := 0; offset < size; {
			n := min(len(block), size-offset)
			for i := range block[:n] {
				block[i] = byte((offset + i) % 251)
			}
			if _, err := w.Write(block[:n]); err != nil {
				return
			}
			offset += n
		}
	}))
	defer origin.Close()
	port := func() int {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		return ln.Addr().(*net.TCPAddr).Port
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	lease, err := f.s.reserveSourceProcess(ctx, "remote", "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := (remotemedia.Policy{}).Approve(ctx, origin.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := remotemedia.StartPolicyProxy(ctx, remotemedia.Policy{Approvals: []remotemedia.Approval{approval}}, "proxy", "fixture-only", lease.Check)
	if err != nil {
		lease.Release()
		t.Fatal(err)
	}
	defer func() {
		proxy.Close()
		wait, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := proxy.Wait(wait); err != nil {
			t.Error(err)
		}
	}()
	cacheDirectory := t.TempDir()
	process, err := rcloneprogram.StartServe(ctx, rcloneprogram.Program{Path: program, Included: true}, rcloneprogram.ServeCommandSpec{SourceID: "remote", SourceGeneration: "1", PrivateDirectory: t.TempDir(), Configuration: []byte("[fixture]\ntype=http\nurl=" + origin.URL + "/\n"), Remote: "fixture:", Port: port(), MetadataPort: port(), Username: "private", Password: "fixture-only", Connections: 1, RequestsPerSecond: 2, SequentialReads: true, CacheDirectory: cacheDirectory, CacheBytes: 32 << 20, CacheFloor: 1 << 20, Enforce: func(cmd *exec.Cmd) error {
		cmd.Env = append(cmd.Env, "RCLONE_HTTP_PROXY="+proxy.Locator())
		// The provider sees127.0.0.2 if rclone bypasses the explicit proxy; the
		// policy proxy's pinned origin dial uses127.0.0.1. No external account.
		cmd.Args = append(cmd.Args, "--bind=127.0.0.2")
		return proxy.Check(ctx)
	}}, lease)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		process.Stop()
		wait, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if err := process.Wait(wait); !errors.Is(err, rcloneprogram.ErrServeStopped) {
			t.Error(err)
		}
	}()
	check := func(ctx context.Context) error { return lease.Check(ctx) } // Source custody only; no Reading actor permission claim.
	metadata, err := process.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := metadata.Stat(ctx, "film.mkv", check)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := process.ObservationEndpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	served, err := endpoint.Stat(ctx, "film.mkv", check)
	if err != nil {
		t.Fatal(err)
	}
	facts := remotemedia.FileObservation{SourceID: "remote", Incarnation: "1", Object: "film.mkv", ObjectID: upstream.ID, Size: upstream.Size, ModifiedKnown: !upstream.Modified.IsZero(), ModifiedNS: upstream.Modified.UnixNano(), ProviderHashes: upstream.Hashes, ServeETag: served.ETag}
	if facts.Size != size {
		t.Fatal("origin size", facts.Size)
	}
	ref, err := f.record(t, ScanObservation{Facts: facts}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec(`UPDATE remote_sources SET range_support='unsupported',error_code='range_unsupported'`); err != nil {
		t.Fatal(err)
	}
	session, err := f.s.OpenFreshSequential(ctx, process, FreshReadAdmission{Observation: ref, ProcessGeneration: process.Generation(), Check: check, OnChanged: func() { t.Error("unchanged source reported changed") }})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	buffer := make([]byte, 1<<20)
	total := 0
	for total < size {
		n, err := session.Read(ctx, buffer)
		if err != nil || n == 0 {
			t.Fatal("progressive body", total, n, err)
		}
		for i, b := range buffer[:n] {
			if b != byte((total+i)%251) {
				t.Fatalf("prefix replay at %d", total+i)
			}
		}
		total += n
	}
	if n, err := session.Read(ctx, buffer); n != 0 || err != io.EOF {
		t.Fatal(n, err)
	}
	if gets.Load() != 1 {
		t.Fatal("sequential origin body reopened", gets.Load())
	}
	if entries, err := os.ReadDir(cacheDirectory); err != nil || len(entries) != 0 {
		t.Fatal("sequential fallback wrote a VFS cache", entries, err)
	}
	if routeMismatch.Load() {
		t.Fatal("rclone bypassed the pinned policy proxy")
	}
	redirect.Store(true)
	if err := session.StopAndRecheck(ctx); err == nil {
		t.Fatal("metadata redirect escaped policy proxy")
	}
	if gets.Load() != 1 {
		t.Fatal("failed check replayed media body", gets.Load())
	}
}
