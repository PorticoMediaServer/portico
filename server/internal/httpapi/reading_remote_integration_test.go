//go:build rcloneintegration

package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// This uses actual rclone DAV backends with real scanned publications. It
// proves Reading's actor/book/lease composition, not all-provider behavior or
// immutable origin identity. No permissive source/actor callback is installed.
func readingRemoteProgram(t *testing.T) string {
	t.Helper()
	program := os.Getenv("PORTICO_RCLONE_TEST_PROGRAM")
	if program == "" {
		t.Fatal("locked rclone fixture executable is required")
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
	clear(binary)
	if hex.EncodeToString(digest[:]) != lock.Targets["linux-amd64"].Files["rclone"] {
		t.Fatal("fixture executable differs from repository lock")
	}
	t.Setenv("PORTICO_RCLONE", program)
	return program
}

func TestReadingRemoteRegistered(t *testing.T)                { readingRemoteRegistered(t, true) }
func TestReadingRemoteDefaultProfilePreparation(t *testing.T) { readingRemoteRegistered(t, false) }

func readingRemoteRegistered(t *testing.T, savedProfile bool) {
	t.Helper()
	_ = readingRemoteProgram(t)
	var err error
	f := newV1Fixture(t, 0)
	managed, err := mounts.New(f.db, f.root, "", f.deps.Storage.Binary, f.deps.Storage)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, kind := range []string{"book", "comic"} {
		if err = os.Mkdir(filepath.Join(root, kind), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for format, raw := range map[string][]byte{"pdf": readingtest.PDF(), "epub": readingtest.EPUB(), "cbz": readingtest.CBZ()} {
		kind := "book"
		if format == "cbz" {
			kind = "comic"
		}
		if err = os.WriteFile(filepath.Join(root, kind, "Original."+format), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	remote, err := remotesources.New(f.db, f.root, managed)
	if err != nil {
		t.Fatal(err)
	}
	var originRequests atomic.Int64
	origin := readingDAVFiles(t, root, &originRequests)
	defer origin.Close()
	principal, err := f.id.Authenticate(f.owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := remote.ConfigureDAV(context.Background(), principal.Hash, "", remotesources.DAVInput{OperationID: fmt.Sprintf("%013d-00000000-0000-4000-8000-000000000001", time.Now().UnixMilli()), Name: "Reading fixture", Root: origin.URL + "/", InsecureLocal: true}, func(tx *sql.Tx) error {
		live, err := f.id.PrincipalByDigestTx(context.Background(), tx, principal.Hash)
		if err == nil && live.Role != "owner" {
			return identity.ErrForbidden
		}
		return err
	})
	if err != nil {
		t.Fatal("supported actual DAV configuration", err)
	}
	remoteRoot := configured.Source.RootPath
	if savedProfile {
		// These are real saved owner settings for this throwaway source, matching
		// the cap1 sequential fixture. No default/policy is rewritten to authorize
		// Reading, and the scan tier remains file_list_only.
		profile, _ := remotesources.SourceReadPreset("list")
		profile.Limits = remotesources.ReadLimits{Connections: 1, RequestsPerSecond: 50}
		err = dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			_, err := remote.ApplySourceReadProfileTx(context.Background(), tx, f.id, principal, remotesources.SourceReadProfileChange{SourceID: configured.Source.ID, Generation: "1", Profile: profile})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	f.deps.Storage.Remote = remote
	f.cat.SetStorage(f.deps.Storage)
	life, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); remote.Run(life) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("owned remote source controller did not stop")
		}
	})
	// Wait for the actual source runtime through its metadata operation; no
	// fixed sleep is treated as proof that its shared process is ready.
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err = remote.InspectRoot(life, remoteRoot)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source runtime readiness", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	d := f.deps
	d.Hosted, d.Mounts, d.RemoteSources = nil, managed, remote
	d.Reading = NewReadingService(d)
	t.Cleanup(func() { _ = d.Reading.Close() })
	h := New(d)
	owner, err := f.id.DirectLogin(context.Background(), "owner", "long-test-password")
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"book", "comic"}
	if !savedProfile {
		kinds = []string{"book"}
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			library, err := f.cat.Create("Real remote "+kind, kind, filepath.Join(remoteRoot, kind))
			if err != nil {
				t.Fatal("real remote library", err)
			}
			// Observe the SAME first queued scan; do not retry a failed job or
			// seed catalogue/observation rows to skip actual source publication.
			readingScanWithin(t, f, library.ID, 120*time.Second)
			if !savedProfile {
				t.Log("actual default-profile first scan completed", "origin requests", originRequests.Load())
			}
			member, err := f.id.CreateDirectMember(context.Background(), owner.AccountToken, identity.DirectMemberCreate{Username: "reader-" + kind, Password: "different-test-password", Name: "Reader", AllowedLibraries: []string{library.ID}})
			if err != nil {
				t.Fatal(err)
			}
			login, err := f.id.DirectLogin(context.Background(), member.Username, "different-test-password")
			if err != nil || login.Session == nil {
				t.Fatal("actual member", err)
			}
			token := login.Session.AccessToken
			w := readingHTTP(t, h, "GET", "/v1/reading/items?libraryId="+library.ID, token, "", nil)
			var items ReadingItemsResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &items) != nil || len(items.Items) != map[string]int{"book": 2, "comic": 1}[kind] {
				t.Fatal("actual remote scan/list", w.Code, w.Body.String())
			}
			for _, item := range items.Items {
				if !savedProfile && item.Format != "epub" {
					continue
				}
				started, beforeRequests := time.Now(), originRequests.Load()
				w = readingHTTP(t, h, "POST", "/v1/reading/sessions", token, "", ReadingSessionRequest{item.ID, item.SourceID})
				if !savedProfile {
					t.Log("actual default-profile Prepare returned", "duration", time.Since(started), "origin requests", originRequests.Load()-beforeRequests, "status", w.Code)
				}
				var manifest ReadingSessionResponse
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &manifest) != nil || manifest.SessionID == "" || manifest.Sequential {
					p, authErr := f.id.Authenticate(token)
					if authErr != nil {
						t.Fatal("real remote actor after refusal", authErr)
					}
					session, _, sourceErr := d.Reading.Prepare(context.Background(), p, item.ID, item.SourceID)
					if session != nil {
						_ = d.Reading.CloseSession(p, session.ID)
					}
					t.Fatal("real remote prepare", item.Format, w.Code, w.Body.String(), "private source stage", sourceErr)
				}
				base := "/v1/reading/sessions/" + manifest.SessionID
				if !savedProfile {
					if len(manifest.Resources) == 0 || len(manifest.Spine) == 0 || manifest.Metadata.Author != fixtureFileAuthor {
						t.Fatal("default-profile manifest did not inspect original source", manifest)
					}
					w = readingHTTP(t, h, "DELETE", base, token, "", nil)
					if w.Code != 204 {
						t.Fatal(w.Code, w.Body.String())
					}
					continue
				}
				read := func() *httptest.ResponseRecorder {
					if item.Format == "pdf" {
						req := httptest.NewRequest("GET", base+"/source", nil)
						req.Header.Set("Authorization", "Bearer "+token)
						req.Header.Set("If-Match", `"`+manifest.Revision+`"`)
						req.Header.Set("Range", "bytes=0-4")
						w := httptest.NewRecorder()
						h.ServeHTTP(w, req)
						return w
					}
					path := base + "/pages/0?width=32"
					if item.Format == "epub" {
						path = base + "/assets?path=" + manifest.Spine[0]
					}
					return readingHTTP(t, h, "GET", path, token, manifest.Revision, nil)
				}
				w = read()
				want := 200
				if item.Format == "pdf" {
					want = 206
				}
				if w.Code != want || w.Body.Len() == 0 {
					t.Fatal("actual remote bytes", item.Format, w.Code, w.Body.String())
				}
				if item.Format == "pdf" && w.Body.String() != "%PDF-" || item.Format == "cbz" && !bytes.HasPrefix(w.Body.Bytes(), []byte{137, 'P', 'N', 'G'}) {
					t.Fatal("wrong actual remote bytes")
				}
				page := int64(0)
				location := ReadingLocation{Format: item.Format, PageIndex: &page, Progression: .25}
				if item.Format == "epub" {
					location.PageIndex = nil
					location.CFI = "epubcfi(/6/4!/4/2:0)"
					location.Section = manifest.Spine[0]
				}
				w = readingHTTP(t, h, "PUT", base+"/progress", token, manifest.Revision, ReadingProgressRequest{Location: location})
				if w.Code != 200 {
					t.Fatal("real remote location", w.Code, w.Body.String())
				}
				if w = readingHTTP(t, h, "GET", base+"/progress", f.owner.AccessToken, manifest.Revision, nil); w.Code != 403 {
					t.Fatal("foreign profile reached remote reader", w.Code, w.Body.String())
				}
				w = read()
				if w.Code != want {
					t.Fatal("foreign request retired actual remote reader", w.Code, w.Body.String())
				}
				principal, err := f.id.Authenticate(token)
				if err != nil {
					t.Fatal(err)
				}
				childCtx, cancelChild := context.WithCancel(context.Background())
				child, err := d.Reading.OpenSession(childCtx, principal, manifest.SessionID)
				if err != nil {
					cancelChild()
					t.Fatal(err)
				}
				cancelChild()
				var cancelledExtent [5]byte
				if n, err := child.ReadAt(cancelledExtent[:], 0); n != 0 || err == nil {
					t.Fatal("canceled Reading child returned bytes", n, err)
				}
				if err := child.Validate(); err == nil {
					t.Fatal("cancelled buffered remote publication validated")
				}
				_ = child.Close()
				w = read()
				if w.Code != want {
					t.Fatal("canceled Reading request retired parent", w.Code, w.Body.String())
				}
				if w = readingHTTP(t, h, "DELETE", base, token, "", nil); w.Code != 204 {
					t.Fatal(w.Code, w.Body.String())
				}
				if w = read(); w.Code != 403 {
					t.Fatal("closed remote reader delivered bytes", w.Code)
				}
			}
			if !savedProfile {
				return
			}
			// Keep an acquired extent reader alive across actual member policy
			// narrowing; a previously validated source/cached range is no grant.
			item := items.Items[0]
			w = readingHTTP(t, h, "POST", "/v1/reading/sessions", token, "", ReadingSessionRequest{item.ID, item.SourceID})
			var current ReadingSessionResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &current) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			principal, err := f.id.Authenticate(token)
			if err != nil {
				t.Fatal(err)
			}
			retained, err := d.Reading.OpenSession(context.Background(), principal, current.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			var extent [5]byte
			if n, err := retained.ReadAt(extent[:], 0); err != nil || n != len(extent) {
				t.Fatal("positive retained remote extent", n, err)
			}
			var tail [2]byte
			if n, err := retained.ReadAt(tail[:], retained.Size-1); n != 1 || err != io.EOF {
				t.Fatal("remote ReaderAt partial EOF contract", n, err)
			}
			before := readingRestrictionProgress(t, &readingFixture{f: f})
			if err = f.id.UpdateDirectMember(context.Background(), owner.AccountToken, member.ID, identity.DirectMemberInput{Username: member.Username, Name: "Reader", ExpectedRevision: member.Revision, AllowedLibraries: []string{}}); err != nil {
				t.Fatal(err)
			}
			if n, err := retained.ReadAt(extent[:], 0); n != 0 || err == nil {
				t.Fatal("revoked remote lease returned extent", n, err)
			}
			page := int64(0)
			location := ReadingLocation{Format: item.Format, PageIndex: &page, Progression: .9}
			if item.Format == "epub" {
				location.PageIndex = nil
				location.CFI = "epubcfi(/6/4!/4/2:0)"
				location.Section = current.Spine[0]
			}
			w = readingHTTP(t, h, "PUT", "/v1/reading/sessions/"+current.SessionID+"/progress", token, current.Revision, ReadingProgressRequest{Location: location})
			if w.Code < 400 {
				t.Fatal("revoked remote actor saved progress", w.Code)
			}
			if after := readingRestrictionProgress(t, &readingFixture{f: f}); after != before {
				t.Fatal("revoked remote actor changed progress")
			}
			if w = readingHTTP(t, h, "POST", "/v1/reading/sessions", token, "", ReadingSessionRequest{item.ID, item.SourceID}); w.Code != 404 {
				t.Fatal("revoked remote library still readable", w.Code, w.Body.String())
			}
			if kind == "book" {
				var pdf ReadingItem
				for _, candidate := range items.Items {
					if candidate.Format == "pdf" {
						pdf = candidate
					}
				}
				ownerPrincipal, err := f.id.Authenticate(f.owner.AccessToken)
				if err != nil || pdf.ID == "" {
					t.Fatal("actual owner/PDF for profile race", err)
				}
				session, _, err := d.Reading.Prepare(context.Background(), ownerPrincipal, pdf.ID, pdf.SourceID)
				if err != nil {
					t.Fatal(err)
				}
				defer d.Reading.CloseSession(ownerPrincipal, session.ID)
				captured, err := d.Reading.OpenSession(context.Background(), ownerPrincipal, session.ID)
				if err != nil {
					t.Fatal(err)
				}
				defer captured.Close()
				other, err := d.Reading.OpenSession(context.Background(), ownerPrincipal, session.ID)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				for _, child := range []*reading.Source{captured, other} {
					if n, err := child.ReadAt(extent[:], 0); n != len(extent) || err != nil {
						t.Fatal("open actual range children", n, err)
					}
				}
				type extentResult struct {
					n   int
					err error
				}
				start := make(chan struct{})
				extents := make(chan extentResult, 2)
				for offset, child := range []*reading.Source{captured, other} {
					go func(offset int, child *reading.Source) {
						<-start
						var b [1]byte
						n, err := child.ReadAt(b[:], int64(offset))
						extents <- extentResult{n, err}
					}(offset, child)
				}
				close(start)
				busy, delivered := 0, 0
				for range 2 {
					got := <-extents
					if errors.Is(got.err, storage.ErrBusy) && got.n == 0 {
						busy++
					} else if got.err == nil && got.n == 1 {
						delivered++
					} else {
						t.Fatal("distinct real extents", got)
					}
				}
				if busy != 1 || delivered != 1 {
					t.Fatal("actual cap1 distinct-extent refusal not exercised", busy, delivered)
				}
				if n, err := captured.ReadAt(extent[:], 0); n != len(extent) || err != nil {
					t.Fatal("temporary source Busy retired the Reading session", n, err)
				}
				before := readingRestrictionProgress(t, &readingFixture{f: f})
				hold, err := dbwork.Begin(context.Background(), f.db, dbwork.ClassInteractive)
				if err != nil {
					t.Fatal(err)
				}
				defer hold.Rollback()
				type outcome struct {
					progress *reading.Progress
					saved    bool
					err      error
				}
				result := make(chan outcome, 1)
				go func() {
					page := int64(0)
					p, saved, err := captured.SaveProgress(0, reading.Location{Format: "pdf", PageIndex: &page, Progression: .2})
					result <- outcome{p, saved, err}
				}()
				deadline := time.Now().Add(30 * time.Second)
				for dbwork.WriteGate().Waiting()[dbwork.ClassInteractive.String()] == 0 {
					select {
					case got := <-result:
						t.Fatal("progress did not reach the actual queued writer", got)
					default:
					}
					if time.Now().After(deadline) {
						t.Fatal("progress never queued behind the profile writer")
					}
					time.Sleep(10 * time.Millisecond)
				}
				profile, err := remotesources.SourceReadProfileTx(context.Background(), hold.Tx(), configured.Source.ID)
				if err != nil {
					t.Fatal(err)
				}
				profile.Limits.RequestsPerSecond--
				updated, err := remote.ApplySourceReadProfileTx(context.Background(), hold.Tx(), f.id, ownerPrincipal, remotesources.SourceReadProfileChange{SourceID: configured.Source.ID, Generation: "1", ExpectedRevision: profile.Revision, Profile: profile})
				if err != nil {
					t.Fatal(err)
				}
				if err = hold.Commit(); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-result:
					if got.err == nil || got.saved || got.progress != nil {
						t.Fatal("queued progress committed across actual source-profile CAS", got)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("profile-fenced progress writer did not return")
				}
				if after := readingRestrictionProgress(t, &readingFixture{f: f}); after != before {
					t.Fatal("profile-fenced writer changed persisted reading progress")
				}
				// Complete the actual profile publisher's post-commit lifecycle.
				// A NEW metadata admission may be Busy while the obsolete physical
				// process retires; no old reading lease is reopened or retargeted.
				if err = remote.ReadProfileCommitted(context.Background(), configured.Source.ID, updated.Profile.Revision); err != nil {
					t.Fatal(err)
				}
				deadline = time.Now().Add(10 * time.Second)
				for {
					_, err = remote.InspectRoot(life, remoteRoot)
					if err == nil {
						break
					}
					if !errors.Is(err, storage.ErrBusy) || time.Now().After(deadline) {
						t.Fatal("new metadata admission after profile publication", err)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if kind == "comic" {
				// Change the actual upstream file after a successful owner extent.
				// An unchanged inventory row cannot authorize replacement bytes or
				// retarget the already captured publication/session.
				w = readingHTTP(t, h, "POST", "/v1/reading/sessions", f.owner.AccessToken, "", ReadingSessionRequest{item.ID, item.SourceID})
				var original ReadingSessionResponse
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &original) != nil {
					t.Fatal("owner before upstream replacement", w.Code, w.Body.String())
				}
				ownerPrincipal, err := f.id.Authenticate(f.owner.AccessToken)
				if err != nil {
					t.Fatal(err)
				}
				captured, err := d.Reading.OpenSession(context.Background(), ownerPrincipal, original.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				defer captured.Close()
				if n, err := captured.ReadAt(extent[:], 0); n != len(extent) || err != nil {
					t.Fatal("original owner extent", n, err)
				}
				upstream, err := os.OpenFile(filepath.Join(root, kind, "Original.cbz"), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := upstream.Write([]byte{0})
				closeErr := upstream.Close()
				if writeErr != nil || closeErr != nil {
					t.Fatal("actual upstream replacement", writeErr, closeErr)
				}
				if n, err := captured.ReadAt(extent[:], 0); n != 0 || err == nil {
					t.Fatal("replacement returned bytes through old session", n, err)
				}
				w = readingHTTP(t, h, "GET", "/v1/reading/sessions/"+original.SessionID+"/pages/0?width=32", f.owner.AccessToken, original.Revision, nil)
				if w.Code != 403 {
					t.Fatal("changed source session was reopened", w.Code, w.Body.String())
				}
			}
			t.Log(fmt.Sprintf("real remote %s scan/member/source/location/close qualified", kind))
		})
	}
}

func TestReadingRemoteSequentialPDF(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_ = readingRemoteProgram(t)
	f := newV1Fixture(t, 0)
	managed, err := mounts.New(f.db, f.root, "", f.deps.Storage.Binary, f.deps.Storage)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := remotesources.New(f.db, f.root, managed)
	if err != nil {
		t.Fatal(err)
	}
	f.deps.Storage.Remote = remote
	f.cat.SetStorage(f.deps.Storage)
	raw := append(readingtest.PDF(), bytes.Repeat([]byte("\n% progressive original PDF comment"), 70000)...)
	modified := time.Unix(1700000000, 0).UTC()
	password := identity.Token()
	var gets, active, peak atomic.Int32
	origin := readingPrivateOrigin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); n > previous && !peak.CompareAndSwap(previous, n); previous = peak.Load() {
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != "reading-fixture" || p != password {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path != "/publications/" && r.URL.Path != "/publications/Original.pdf" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case "OPTIONS":
			w.Header().Set("DAV", "1")
			return
		case "REPORT":
			w.WriteHeader(501)
			return
		case "PROPFIND":
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:">`)
			if r.URL.Path == "/publications/" {
				fmt.Fprint(w, `<d:response><d:href>/publications/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
			}
			if r.URL.Path != "/publications/" || r.Header.Get("Depth") == "1" {
				fmt.Fprintf(w, `<d:response><d:href>/publications/Original.pdf</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>%d</d:getcontentlength><d:getlastmodified>%s</d:getlastmodified><d:getetag>"original-one"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, len(raw), modified.Format(http.TimeFormat))
			}
			fmt.Fprint(w, `</d:multistatus>`)
			return
		case "GET", "HEAD":
			w.Header().Set("Content-Length", fmt.Sprint(len(raw)))
			w.Header().Set("ETag", `"original-one"`)
			w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
			if r.Method == "GET" {
				gets.Add(1)
				_, _ = w.Write(raw)
			}
			return
		default:
			w.WriteHeader(405)
		}
	}))
	defer origin.Close()
	principal, err := f.id.Authenticate(f.owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	source, err := remote.ConfigureDAV(context.Background(), principal.Hash, "", remotesources.DAVInput{OperationID: fmt.Sprintf("%013d-00000000-0000-4000-8000-000000000001", time.Now().UnixMilli()), Name: "Actual sequential origin", Root: origin.URL + "/publications/", Username: "reading-fixture", Password: password, InsecureLocal: true}, func(tx *sql.Tx) error {
		_, err := f.id.PrincipalByDigestTx(context.Background(), tx, principal.Hash)
		return err
	})
	if err != nil || source.Source.RangeSupport != "unsupported" {
		t.Fatal("factual range probe", source.Source.RangeSupport, err)
	}
	profile, _ := remotesources.SourceReadPreset("list")
	profile.Limits = remotesources.ReadLimits{Connections: 1, RequestsPerSecond: 50}
	err = dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, err := remote.ApplySourceReadProfileTx(context.Background(), tx, f.id, principal, remotesources.SourceReadProfileChange{SourceID: source.Source.ID, Generation: "1", Profile: profile})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	life, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); remote.Run(life) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("remote sequential controller did not stop")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err = remote.InspectRoot(life, source.Source.RootPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	library, err := f.cat.Create("Actual stream books", "book", source.Source.RootPath)
	if err != nil {
		t.Fatal(err)
	}
	readingScan(t, f, library.ID)
	d := f.deps
	d.Hosted = nil
	d.Mounts = managed
	d.RemoteSources = remote
	d.Reading = NewReadingService(d)
	t.Cleanup(func() { _ = d.Reading.Close() })
	h := New(d)
	w := readingHTTP(t, h, "GET", "/v1/reading/items?libraryId="+library.ID, f.owner.AccessToken, "", nil)
	var items ReadingItemsResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &items) != nil || len(items.Items) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	item := items.Items[0]
	w = readingHTTP(t, h, "POST", "/v1/reading/sessions", f.owner.AccessToken, "", ReadingSessionRequest{item.ID, item.SourceID})
	var manifest ReadingSessionResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &manifest) != nil || !manifest.Sequential {
		t.Fatal("real fromstart manifest", w.Code, w.Body.String())
	}
	base := "/v1/reading/sessions/" + manifest.SessionID
	w = readingHTTP(t, h, "HEAD", base+"/source", f.owner.AccessToken, manifest.Revision, nil)
	if w.Code != 200 || w.Header().Get("Accept-Ranges") != "none" || w.Header().Get("Content-Length") != fmt.Sprint(len(raw)) {
		t.Fatal(w.Code, w.Header())
	}
	before := gets.Load()
	w = readingHTTP(t, h, "GET", base+"/source", f.owner.AccessToken, manifest.Revision, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), raw) || gets.Load() != before+1 || w.Header().Get("Content-Range") != "" {
		t.Fatal("progressive200 replay/length", w.Code, w.Body.Len(), gets.Load()-before)
	}
	req := httptest.NewRequest("GET", base+"/source", nil)
	req.Header.Set("Authorization", "Bearer "+f.owner.AccessToken)
	req.Header.Set("If-Match", `"`+manifest.Revision+`"`)
	req.Header.Set("Range", "bytes=0-4")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 416 {
		t.Fatal("sequential stream manufactured ranged response", w.Code)
	}
	if peak.Load() > 1 {
		t.Fatal("source connection1 exceeded", peak.Load())
	}
	if w = readingHTTP(t, h, "DELETE", base, f.owner.AccessToken, "", nil); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, forbidden := range []string{password, origin.URL, "reading-fixture"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatal("origin credential escaped")
		}
	}
}

// ConfigureDAV must see a genuinely admitted private origin, never loopback
// or a forged DNS result. The locked fixture helper owns this job's bridge.
func readingPrivateOrigin(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	ip, err := netip.ParseAddr(os.Getenv("PORTICO_PRIVATE_ORIGIN_TEST_HOST"))
	if err != nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		t.Fatal("run locked fixture helper with PORTICO_PRIVATE_ORIGIN_TEST=1")
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	if err = server.Listener.Close(); err != nil {
		listener.Close()
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return server
}

// The DAV directory/document metadata is derived from actual fixture files;
// finite206 is implemented by net/http against their real open descriptors.
func readingDAVFiles(t *testing.T, root string, requests *atomic.Int64) *httptest.Server {
	t.Helper()
	return readingPrivateOrigin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		relative := strings.TrimPrefix(r.URL.Path, "/")
		if relative != "" && !filepath.IsLocal(relative) {
			http.NotFound(w, r)
			return
		}
		file := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Stat(file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case "OPTIONS":
			w.Header().Set("DAV", "1")
			return
		case "REPORT":
			w.WriteHeader(501)
			return
		case "PROPFIND":
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(207)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:">`)
			emit := func(name string, info os.FileInfo) {
				href := "/"
				if name != "" {
					parts := strings.Split(filepath.ToSlash(name), "/")
					for i := range parts {
						parts[i] = url.PathEscape(parts[i])
					}
					href += strings.Join(parts, "/")
				}
				if info.IsDir() && !strings.HasSuffix(href, "/") {
					href += "/"
				}
				fmt.Fprintf(w, `<d:response><d:href>%s</d:href><d:propstat><d:prop>`, html.EscapeString(href))
				if info.IsDir() {
					fmt.Fprint(w, `<d:resourcetype><d:collection/></d:resourcetype>`)
				} else {
					fmt.Fprintf(w, `<d:resourcetype/><d:getcontentlength>%d</d:getcontentlength><d:getlastmodified>%s</d:getlastmodified><d:getetag>"%d-%d"</d:getetag>`, info.Size(), info.ModTime().UTC().Format(http.TimeFormat), info.Size(), info.ModTime().UnixNano())
				}
				fmt.Fprint(w, `</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
			}
			emit(relative, info)
			if info.IsDir() && r.Header.Get("Depth") == "1" {
				entries, err := os.ReadDir(file)
				if err != nil {
					return
				}
				for _, entry := range entries {
					child := filepath.Join(file, entry.Name())
					value, err := os.Stat(child)
					if err != nil {
						return
					}
					emit(filepath.Join(relative, entry.Name()), value)
				}
			}
			fmt.Fprint(w, `</d:multistatus>`)
			return
		case "HEAD", "GET":
			if info.IsDir() {
				http.NotFound(w, r)
				return
			}
			body, err := os.Open(file)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer body.Close()
			w.Header().Set("ETag", fmt.Sprintf(`"%d-%d"`, info.Size(), info.ModTime().UnixNano()))
			http.ServeContent(w, r, info.Name(), info.ModTime(), body)
		default:
			w.WriteHeader(405)
		}
	}))
}
