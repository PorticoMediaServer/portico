package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

type tl11AdmissionFixture struct {
	t       *testing.T
	db      *sql.DB
	d       Dependencies
	c       *catalogtest.Catalog
	item    catalogtest.Item
	profile string
	token   string
	h       http.Handler
}

func newTL11AdmissionFixture(t *testing.T, kind string) *tl11AdmissionFixture {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	host, err := hosted.New(db, ident, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	d := Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), Playback: playback.New(db), Hosted: host, PlaybackV1: &playbackv1.Service{}}
	h := New(d)
	secret, err := os.ReadFile(storagepaths.Path(root, "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	setup := httptest.NewRequest("POST", "/v1/setup", bytes.NewBufferString(`{"authMode":"local","setupToken":"`+string(secret)+`","username":"owner","password":"long-test-password","name":"Test"}`))
	setup.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, setup)
	if w.Code != 201 {
		t.Fatalf("setup %d %s", w.Code, w.Body.String())
	}
	var owner identity.Envelope
	if err = json.Unmarshal(w.Body.Bytes(), &owner); err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	var item catalogtest.Item
	switch kind {
	case "song":
		music := c.Library("music", "Music", "music", root)
		artist := c.Artist(music, "Artist")
		album := c.Album(artist, "Album", 2020)
		item = c.Song(album, 1, filepath.Join(root, "track.mp4"), "Track")
	case "audiobook_file":
		books := c.Library("books", "Books", "audiobook", root)
		book := c.Book(books, "Book", "Author")
		item = c.BookFile(book, 1, filepath.Join(root, "part.mp4"))
	default:
		movies := c.Library("movies", "Movies", "movie", root)
		item = c.Movie(movies, filepath.Join(root, "film.mp4"), "Film", 2000)
	}
	c.Drain()
	if kind == "song" || kind == "audiobook_file" {
		if _, err = db.Exec(`INSERT INTO audio_render_facts(asset_id,size,modified_ns,container,codec,sample_rate,channels,bit_depth,raw_frames,duration_frames,start_frames,end_frames,trim_source,decoder_config,measured_ms) VALUES(?,1000,1,'mp4','aac',44100,2,0,26462128,26460000,2112,16,'itunsmpb','EhA=',1)`, item.Token); err != nil {
			t.Fatal(err)
		}
		f := &tl11AdmissionFixture{t: t, db: db, d: d, c: c, item: item, profile: owner.Viewer.ProfileID, token: owner.AccessToken, h: h}
		f.declareAudioDecode()
		return f
	}
	return &tl11AdmissionFixture{t: t, db: db, d: d, c: c, item: item, profile: owner.Viewer.ProfileID, token: owner.AccessToken, h: h}
}

func (f *tl11AdmissionFixture) request(method, path string, headers map[string]string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+f.token)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func (f *tl11AdmissionFixture) declareAudioDecode() {
	f.t.Helper()
	caps := webCapabilities()
	caps["audioDecode"] = []map[string]any{{"codec": "aac", "containers": []string{"mp4", "adts"}, "maxSampleRate": 96000, "maxChannels": 8}}
	get := f.request("GET", "/v1/me/devices/current/capabilities", nil, nil)
	headers := map[string]string{}
	if get.Code == 200 {
		var doc playbackv1.CapabilitiesDocument
		if err := json.Unmarshal(get.Body.Bytes(), &doc); err != nil {
			f.t.Fatal(err)
		}
		headers["If-Match"] = playbackv1.ETag(doc.Revision)
	}
	if w := f.request("PUT", "/v1/me/devices/current/capabilities", headers, caps); w.Code != 204 {
		f.t.Fatalf("capabilities: %d %s", w.Code, w.Body.String())
	}
}

func tl11StartBody(item string) map[string]any { return map[string]any{"itemId": item} }

func TestRestrictedTitleCannotEnterPlaybackAuthority(t *testing.T) {
	f := newTL11AdmissionFixture(t, "movie")
	p, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES(?,13,0)`, f.profile); err != nil {
		t.Fatal(err)
	}
	f.c.Attributes(f.item.ID, "contentRating", "R")
	request := httptest.NewRequest("POST", "/v1/playback/sessions", nil)
	if _, err = f.d.admitPlayback(request, p, f.item.Public); !errors.Is(err, identity.ErrContentRestricted) {
		t.Fatalf("immediate playback admission: %v", err)
	}
	f.c.Drain()
	if _, err = f.d.admitPlayback(request, p, f.item.Public); !errors.Is(err, identity.ErrContentRestricted) {
		t.Fatalf("playback admission: %v", err)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = f.d.playbackAuthorityTx(context.Background(), tx, p, f.item.Public); !errors.Is(err, identity.ErrContentRestricted) {
		t.Fatalf("transaction authority: %v", err)
	}
	if _, err = f.d.playbackFamilyAuthorityTx(context.Background(), tx, p, f.item.Public); !errors.Is(err, identity.ErrContentRestricted) {
		t.Fatalf("queue family authority: %v", err)
	}
}

func TestProfileFeatureSwitchesGuardRoutes(t *testing.T) {
	f := newTL11AdmissionFixture(t, "movie")
	if _, err := f.db.Exec(`INSERT INTO profile_restrictions(profile_id,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together) VALUES(?,0,0,0,0)`, f.profile); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/downloads", "/v1/items/" + f.item.Public + "/download-options", "/v1/channels", "/v1/guide", "/v1/dvr/recordings", "/v1/groups"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+f.token)
		if _, err := f.d.principal(r); !errors.Is(err, errFeatureRestricted) {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestTopShelfRejectsQueryCredentials(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/topshelf?token=secret", nil)
	if topShelfBearer(r) != "" {
		t.Fatal("query credential accepted")
	}
}

func TestServerHostedCSPAllowsBlobPlayback(t *testing.T) {
	w := httptest.NewRecorder()
	New(Dependencies{}).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	policy := w.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"media-src 'self' blob:", "worker-src 'self' blob:"} {
		if !strings.Contains(policy, directive) {
			t.Fatalf("CSP missing %q: %s", directive, policy)
		}
	}
}

// The audio media routes are grant-based, so a restricted viewer cannot start
// a session or obtain a grant for a hidden song or audiobook file.
func TestRestrictedAudioCannotObtainGrant(t *testing.T) {
	for _, kind := range []string{"song", "audiobook_file"} {
		t.Run(kind, func(t *testing.T) {
			f := newTL11AdmissionFixture(t, kind)
			if _, err := f.db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES(?,13,0)`, f.profile); err != nil {
				t.Fatal(err)
			}
			f.c.Attributes(f.item.ID, "contentRating", "R")
			f.c.Drain()
			w := f.request("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "hidden-audio-" + kind}, tl11StartBody(f.item.Public))
			if w.Code != 404 {
				t.Fatalf("restricted start: %d %s", w.Code, w.Body.String())
			}
			var count int
			if err := f.db.QueryRow(`SELECT count(*) FROM playback_v1_sessions`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("hidden audio created %d sessions", count)
			}
			if _, err := f.db.Exec(`DELETE FROM profile_restrictions WHERE profile_id=?`, f.profile); err != nil {
				t.Fatal(err)
			}
			w = f.request("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "visible-audio-" + kind}, tl11StartBody(f.item.Public))
			var session playbackv1.SessionView
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &session) != nil {
				t.Fatalf("visible audio session: %d %s", w.Code, w.Body.String())
			}
			if session.Kind != "audio" || session.Presentation.AudioRender == nil || !strings.HasPrefix(session.Presentation.AudioRender.URL, "/v1/media/") {
				t.Fatalf("visible audio session %+v", session)
			}
		})
	}
}
