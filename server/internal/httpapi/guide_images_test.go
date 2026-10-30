package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// Programme images check current source access and rating before serving bytes.
func TestGuideImageServing(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1);INSERT INTO libraries(id,name,kind,root) VALUES('lib','Films','movie','/media/films')`); err != nil {
		t.Fatal(err)
	}
	owner, err := ident.Issue("owner", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(db)
	control, err := hosted.New(db, ident, "http://127.0.0.1:19410", base64.RawURLEncoding.EncodeToString(make([]byte, 32)), zeroHostedRootID)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: cat, Ingestion: ingestion.New(db, cat, assets.Probe{}), Hosted: control})
	request := func(method, path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	store, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	authority := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	saved, err := store.Save(context.Background(), authority, livechannels.SourceInput{ID: strings.Repeat("ab", 24), RequestID: strings.Repeat("cd", 24), Name: "Source", TunerCount: 1,
		Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",One\nhttps://provider.invalid/stream\n",
		Guide:    `<tv><channel id="one"/><programme channel="one" start="20261013000000 +0000" stop="20261013010000 +0000"><title>Adult programme</title><rating system="MPAA"><value>R</value></rating><icon src="https://img.invalid/kept.png"/></programme></tv>`})
	if err != nil {
		t.Fatal(err)
	}
	var programme string
	if err = db.QueryRow(`SELECT id FROM live_programmes WHERE generation_id=?`, saved.Generation).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	imagePath := "/v1/guide/programme-images/" + saved.Generation + "/" + programme
	// Store one image directly: the digest of its own bytes, served from the
	// state directory's guide-images root.
	image := bytes.Repeat([]byte{7}, 1024)
	sum := sha256.Sum256(image)
	digest := hex.EncodeToString(sum[:])
	if _, err = db.Exec(`INSERT INTO live_programme_images(url,small_digest,large_digest,media_type,small_width,small_height,small_bytes,large_width,large_height,large_bytes,stored_ms) SELECT column1,column2,column2,column3,column4,column5,1,column4,column5,1,column6 FROM (VALUES(?,?,?,?,?,?))`, "https://img.invalid/kept.png", digest, "image/png", 64, 64, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(storagepaths.Path(root, "guide-images"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(storagepaths.Path(root, "guide-images", digest+".png"), image, 0600); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", imagePath, ""); w.Code != 401 {
		t.Fatalf("no credential answered %d: %s", w.Code, w.Body.String())
	}
	for _, path := range []string{
		"/v1/guide/programme-images/" + saved.Generation + "/unknown",
		"/v1/guide/programme-images/unknown/" + programme,
		"/v1/guide/programme-images/" + strings.ToUpper(saved.Generation) + "/" + programme,
	} {
		if w := request("GET", path, owner.AccessToken); w.Code != 404 {
			t.Fatalf("GET %s answered %d: %s", path, w.Code, w.Body.String())
		}
	}
	w := request("GET", imagePath, owner.AccessToken)
	if w.Code != 200 {
		t.Fatalf("stored image answered %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), image) {
		t.Fatal("stored image bytes differ")
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content type %q", got)
	}
	if got := w.Header().Get("ETag"); got != `"`+digest+`"` {
		t.Fatalf("etag %q", got)
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("cache control %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("content type options %q", got)
	}
	if strings.Contains(w.Body.String()+w.Header().Get("ETag"), "img.invalid") {
		t.Fatal("provider URL leaked in response")
	}
	if w := request("GET", "/v1/guide/images/"+digest, owner.AccessToken); w.Code != 404 {
		t.Fatalf("retired digest route: %d", w.Code)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','member',x'00','member-profile',1)`); err != nil {
		t.Fatal(err)
	}
	member, err := ident.Issue("member", "member-profile", "local", "member", 1)
	if err != nil {
		t.Fatal(err)
	}
	if w := request("GET", imagePath, member.AccessToken); w.Code != 404 {
		t.Fatalf("member fetched owner-only source image: %d %s", w.Code, w.Body)
	}
	if _, err = db.Exec(`INSERT INTO live_source_settings(source_id,viewer_access) VALUES(?,'server-members') ON CONFLICT(source_id) DO UPDATE SET viewer_access=excluded.viewer_access`, saved.ID); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", imagePath, member.AccessToken); w.Code != 200 {
		t.Fatalf("visible image: %d %s", w.Code, w.Body)
	}
	if _, err = db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,blocked_labels,allow_unrated,revision) VALUES('member-profile',12,'[]',1,1)`); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", imagePath, nil)
	r.Header.Set("Authorization", "Bearer "+member.AccessToken)
	r.Header.Set("If-None-Match", `"`+digest+`"`)
	hidden := httptest.NewRecorder()
	handler.ServeHTTP(hidden, r)
	if hidden.Code != 404 || bytes.Equal(hidden.Body.Bytes(), image) {
		t.Fatalf("restricted programme image: %d %s", hidden.Code, hidden.Body)
	}
	if _, err = db.Exec(`UPDATE live_sources SET active_generation='' WHERE id=?`, saved.ID); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", imagePath, owner.AccessToken); w.Code != 404 {
		t.Fatalf("superseded programme image: %d %s", w.Code, w.Body)
	}

}
