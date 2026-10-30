package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// End-to-end over the mux: claim a download, transfer it with Range, and check
// the validators a resumable client depends on. Paths are built with filepath
// throughout so this runs unchanged on Windows.
func TestDownloadTransferSupportsRangeAndValidators(t *testing.T) {
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "state", "portico.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	id, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	cat := catalog.New(db)
	host, e := hosted.New(db, id, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	offline, e := downloads.New(downloads.Options{DB: db, Plays: cat.AcceptPlaybackProgressAt})
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Dependencies{DB: db, Identity: id, Catalog: cat, Playback: playback.New(db), Hosted: host, Downloads: offline})
	request := func(method, path, token string, body any) *httptest.ResponseRecorder {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(data))
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	secret, e := os.ReadFile(storagepaths.Path(root, "setup-token"))
	if e != nil {
		t.Fatal(e)
	}
	w := request("POST", "/v1/setup", "", map[string]string{"authMode": "local", "setupToken": string(secret), "username": "owner", "password": "long-test-password", "name": "Test"})
	if w.Code != 201 {
		t.Fatalf("setup %d %s", w.Code, w.Body.String())
	}
	var auth identity.Envelope
	if e = json.Unmarshal(w.Body.Bytes(), &auth); e != nil {
		t.Fatal(e)
	}
	c := catalogtest.New(t, db)
	lib := c.Library("movies", "Movies", "movie", root)
	film := filepath.Join(root, "Film.mp4")
	body := "0123456789abcdef"
	if e = os.WriteFile(film, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(film)
	if e != nil {
		t.Fatal(e)
	}
	item := c.Movie(lib, film, "Film", 2000)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: film, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60})
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{})
	})
	c.Drain()

	w = request("GET", "/v1/items/"+item.Public+"/download-options", auth.AccessToken, nil)
	if w.Code != 200 {
		t.Fatalf("options %d %s", w.Code, w.Body.String())
	}
	var options downloads.DownloadOptionsView
	if e = json.Unmarshal(w.Body.Bytes(), &options); e != nil {
		t.Fatal(e)
	}
	if !options.Policy.AllowDownloads || len(options.Options) == 0 || options.Options[0].Quality != downloads.QualityOriginal {
		t.Fatalf("options %+v", options)
	}

	w = request("POST", "/v1/downloads/preparations", auth.AccessToken, map[string]any{"operationId": "op-http-1", "mediaId": item.Public, "quality": "original"})
	if w.Code != 201 {
		t.Fatalf("prepare %d %s", w.Code, w.Body.String())
	}
	var batch downloads.Batch
	if e = json.Unmarshal(w.Body.Bytes(), &batch); e != nil {
		t.Fatal(e)
	}
	if len(batch.Items) != 1 {
		t.Fatalf("batch %+v", batch)
	}
	preparation := batch.Items[0].ID
	if e = offline.Advance(context.Background()); e != nil {
		t.Fatal(e)
	}
	w = request("GET", "/v1/downloads/preparations/"+preparation, auth.AccessToken, nil)
	if w.Code != 200 {
		t.Fatalf("read %d %s", w.Code, w.Body.String())
	}
	var ready downloads.Preparation
	if e = json.Unmarshal(w.Body.Bytes(), &ready); e != nil {
		t.Fatal(e)
	}
	if ready.State != downloads.StateReady || ready.Artifact.SHA256 == "" {
		t.Fatalf("preparation %+v", ready)
	}

	w = request("GET", "/v1/downloads/preparations?limit=1&ids="+preparation+",missing", auth.AccessToken, nil)
	var sparse downloads.Page
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &sparse) != nil || len(sparse.Items) != 1 || sparse.Items[0].ID != preparation || sparse.NextCursor != "" {
		t.Fatalf("sparse preparation snapshot %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/v1/downloads/preparations/"+preparation+"/grant", auth.AccessToken, map[string]any{"operationId": "op-http-2"})
	if w.Code != 201 {
		t.Fatalf("grant %d %s", w.Code, w.Body.String())
	}
	var grant downloads.Grant
	if e = json.Unmarshal(w.Body.Bytes(), &grant); e != nil {
		t.Fatal(e)
	}

	// The grant is the whole authority: no bearer token is sent here.
	r := httptest.NewRequest("GET", grant.URL, nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != body {
		t.Fatalf("transfer %d %q", w.Code, w.Body.String())
	}
	etag := w.Header().Get("ETag")
	if etag != "\""+"sha256-"+ready.Artifact.SHA256+"\"" {
		t.Fatalf("etag %q", etag)
	}
	if w.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("accept-ranges %q", w.Header().Get("Accept-Ranges"))
	}

	r = httptest.NewRequest("GET", grant.URL, nil)
	r.Header.Set("Range", "bytes=4-7")
	r.Header.Set("If-Range", etag)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != body[4:8] {
		t.Fatalf("range %d %q", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Range") != "bytes 4-7/16" {
		t.Fatalf("content-range %q", w.Header().Get("Content-Range"))
	}
	// A partial file of some other artifact is never continued: a validator
	// that is not this file's gets the whole file back, not a slice of it.
	r = httptest.NewRequest("GET", grant.URL, nil)
	r.Header.Set("Range", "bytes=4-7")
	r.Header.Set("If-Range", "\"sha256-0000000000000000000000000000000000000000000000000000000000000000\"")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != body {
		t.Fatalf("range against another artifact %d %q", w.Code, w.Body.String())
	}
	// A browser saving the file is given the title's name.
	if disposition := w.Header().Get("Content-Disposition"); !strings.Contains(disposition, "filename*=UTF-8''Film%20%282000%29.mp4") {
		t.Fatalf("content-disposition %q", disposition)
	}
	// Renewing keeps the same address; the transfer the device is holding
	// carries on under it.
	w = request("POST", "/v1/downloads/preparations/"+preparation+"/grant", auth.AccessToken, map[string]any{"operationId": "op-http-renew", "renew": grant.Token})
	var renewed downloads.Grant
	if e = json.Unmarshal(w.Body.Bytes(), &renewed); e != nil || w.Code != 200 || !renewed.Renewed || renewed.URL != grant.URL {
		t.Fatalf("renew %d %s", w.Code, w.Body.String())
	}
	// Everything that travels with the file is described in one document, and
	// the claim itself reads as its title.
	if ready.Title.Name != "Film" || ready.Title.Kind != "movie" || ready.Title.Year != 2000 || !ready.Title.OnServer {
		t.Fatalf("claim title %+v", ready.Title)
	}
	w = request("GET", "/v1/downloads/preparations/"+preparation+"/bundle", auth.AccessToken, nil)
	var bundle downloads.Bundle
	if e = json.Unmarshal(w.Body.Bytes(), &bundle); e != nil || w.Code != 200 {
		t.Fatalf("bundle %d %s", w.Code, w.Body.String())
	}
	if bundle.Title.Name != "Film" || bundle.Media.Container != "mp4" || len(bundle.Artwork) != 2 || bundle.Artwork[0].Path != "artwork/poster" {
		t.Fatalf("bundle %+v", bundle)
	}
	// A title with no picture of a kind simply has none: that is an answer,
	// not a failure of the download.
	w = request("GET", grant.URL+"/"+bundle.Artwork[0].Path, "", nil)
	if w.Code != 404 {
		t.Fatalf("missing artwork %d %s", w.Code, w.Body.String())
	}
	w = request("GET", grant.URL+"/artwork/logo", "", nil)
	if w.Code != 404 {
		t.Fatalf("artwork a download does not carry %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/v1/downloads/artifacts/not-a-grant/artwork/poster", "", nil)
	if w.Code != 401 {
		t.Fatalf("artwork without an address %d %s", w.Code, w.Body.String())
	}

	// A receipt is issued, verifies against the published keys, and revalidates.
	w = request("POST", "/v1/downloads/receipts", auth.AccessToken, map[string]any{"operationId": "op-http-3", "preparationIds": []string{preparation}})
	if w.Code != 201 {
		t.Fatalf("receipts %d %s", w.Code, w.Body.String())
	}
	var issued struct {
		Results []downloads.ReceiptOutcome `json:"results"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &issued); e != nil {
		t.Fatal(e)
	}
	if len(issued.Results) != 1 || issued.Results[0].Receipt == nil {
		t.Fatalf("issued %+v", issued)
	}
	w = request("GET", "/v1/downloads/receipt-keys", auth.AccessToken, nil)
	if w.Code != 200 {
		t.Fatalf("keys %d %s", w.Code, w.Body.String())
	}
	var published struct {
		Keys []downloads.ReceiptKey `json:"keys"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &published); e != nil {
		t.Fatal(e)
	}
	if _, e = downloads.VerifyReceipt(published.Keys, *issued.Results[0].Receipt, time.Now().UTC()); e != nil {
		t.Fatalf("published keys do not verify the issued receipt: %v", e)
	}

	// Deferred progress lands in ordinary personal state.
	w = request("POST", "/v1/downloads/progress", auth.AccessToken, map[string]any{
		"operationId": "op-http-4",
		"entries":     []map[string]any{{"playId": "play-http-1", "itemId": item.Public, "positionMs": 12500, "observedAt": "2026-09-14T09:12:00Z"}},
	})
	if w.Code != 200 {
		t.Fatalf("progress %d %s", w.Code, w.Body.String())
	}
	var receipt downloads.ProgressReceipt
	if e = json.Unmarshal(w.Body.Bytes(), &receipt); e != nil {
		t.Fatal(e)
	}
	if receipt.Applied != 1 || receipt.Entries[0].PositionSeconds != 12.5 {
		t.Fatalf("progress receipt %+v", receipt)
	}

	// Usage reflects the one committed claim.
	w = request("GET", "/v1/downloads/usage", auth.AccessToken, nil)
	if w.Code != 200 {
		t.Fatalf("usage %d %s", w.Code, w.Body.String())
	}
	var usage downloads.Usage
	if e = json.Unmarshal(w.Body.Bytes(), &usage); e != nil {
		t.Fatal(e)
	}
	if usage.ProfileCount != 1 || usage.ProfileBytes != int64(len(body)) {
		t.Fatalf("usage %+v", usage)
	}
	// Existing capability URLs must consult the switch on their next use.
	if _, e = db.Exec(`INSERT INTO profile_restrictions(profile_id,allow_downloads) VALUES(?,0)`, auth.Viewer.ProfileID); e != nil {
		t.Fatal(e)
	}
	w = request("GET", grant.URL, "", nil)
	if w.Code != 403 || tl11DownloadErrorCode(t, w) != "downloads_not_allowed" {
		t.Fatalf("disabled grant %d %s", w.Code, w.Body.String())
	}
	if _, e = db.Exec(`DELETE FROM profile_restrictions WHERE profile_id=?`, auth.Viewer.ProfileID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES(?,13,0)`, auth.Viewer.ProfileID); e != nil {
		t.Fatal(e)
	}
	c.Attributes(item.ID, "contentRating", "R")
	c.Drain()
	w = request("POST", "/v1/downloads/preparations", auth.AccessToken, map[string]any{"operationId": "restricted-http", "mediaId": item.Public, "quality": "original"})
	if w.Code != 404 {
		t.Fatalf("restricted preparation %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/v1/downloads/preparations/"+preparation+"/grant", auth.AccessToken, map[string]any{"operationId": "restricted-grant"})
	if w.Code != 404 {
		t.Fatalf("restricted grant %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/v1/downloads/receipts/revalidate", auth.AccessToken, map[string]any{"operationId": "restricted-revalidate", "receiptIds": []string{issued.Results[0].ReceiptID}})
	if w.Code != 200 {
		t.Fatalf("restricted receipt revalidation %d %s", w.Code, w.Body.String())
	}
	var revalidated struct {
		Results []downloads.ReceiptOutcome `json:"results"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &revalidated); e != nil {
		t.Fatal(e)
	}
	if len(revalidated.Results) != 1 || revalidated.Results[0].Outcome != downloads.OutcomeRefused {
		t.Fatalf("restricted receipt %+v", revalidated)
	}

	// An unauthenticated read is refused, and a fabricated grant is not a way in.
	if w = request("GET", "/v1/downloads/preparations", "", nil); w.Code != 401 {
		t.Fatalf("unauthenticated list %d", w.Code)
	}
	r = httptest.NewRequest("GET", "/v1/downloads/artifacts/fabricated", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("fabricated grant %d %s", w.Code, w.Body.String())
	}
}

func tl11DownloadErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error %v: %s", err, w.Body.String())
	}
	return body.Error.Code
}
