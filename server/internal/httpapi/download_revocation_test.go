package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

type downloadWithdrawingWriter struct {
	*httptest.ResponseRecorder
	withdraw func()
}

func (w *downloadWithdrawingWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if w.withdraw != nil {
		withdraw := w.withdraw
		w.withdraw = nil
		withdraw()
	}
	return n, err
}

func downloadRevocationFixture(t *testing.T) (administrationFixture, downloads.Grant, string) {
	t.Helper()
	f := administrationFixtureNew(t)
	offline, err := downloads.New(downloads.Options{DB: f.deps.DB})
	if err != nil {
		t.Fatal(err)
	}
	f.deps.Downloads = offline
	f.handler = New(f.deps)
	root := t.TempDir()
	path := filepath.Join(root, "film.mp4")
	content := strings.Repeat("abcdefgh01234567", 16384)
	if err = os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, f.deps.DB)
	lib := c.Library("films", "Films", "movie", root)
	item := c.Movie(lib, path, "Film", 2000)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60})
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{})
	})
	c.Drain()
	raw, _ := json.Marshal(map[string]any{"operationId": "revocation-prepare", "mediaId": item.Public, "quality": "original"})
	w := f.call("POST", "/v1/downloads/preparations", f.owner, string(raw))
	var batch downloads.Batch
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &batch) != nil || len(batch.Items) != 1 {
		t.Fatalf("prepare %d %s", w.Code, w.Body.String())
	}
	if err = offline.Advance(context.Background()); err != nil {
		t.Fatal(err)
	}
	w = f.call("POST", "/v1/downloads/preparations/"+batch.Items[0].ID+"/grant", f.owner, `{"operationId":"revocation-grant"}`)
	var grant downloads.Grant
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &grant) != nil {
		t.Fatalf("grant %d %s", w.Code, w.Body.String())
	}
	return f, grant, content
}

// Reproduce the launch audit at the real router boundary: the mutation fully
// commits after the first 32 KiB write, before ServeContent asks for more.
func TestDownloadTransferStopsOnCommittedWithdrawal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, withdrawal := range []string{"profile_revoke", "claim_remove", "downloads_permission"} {
		for _, ranged := range []bool{false, true} {
			name := withdrawal + "/whole"
			if ranged {
				name = withdrawal + "/range"
			}
			t.Run(name, func(t *testing.T) {
				f, grant, content := downloadRevocationFixture(t)
				req := httptest.NewRequest("GET", grant.URL, nil)
				start, status := 0, http.StatusOK
				if ranged {
					start, status = 17, http.StatusPartialContent
					req.Header.Set("Range", "bytes=17-196607")
				}
				w := &downloadWithdrawingWriter{ResponseRecorder: httptest.NewRecorder(), withdraw: func() {
					switch withdrawal {
					case "profile_revoke":
						key := operations.ViewerKey(identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "owner", ProfileID: "owner-profile"}})
						raw, _ := json.Marshal(map[string]string{"profileKey": key, "operationId": "withdraw-profile"})
						result := f.call("POST", "/v1/admin/downloads/revoke", f.owner, string(raw))
						var revoked downloads.ProfileRevocation
						if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &revoked) != nil || revoked.Claims != 1 || revoked.More {
							t.Fatalf("revoke %d %s", result.Code, result.Body.String())
						}
					case "claim_remove":
						result := f.call("GET", "/v1/downloads/preparations/"+grant.PreparationID, f.owner, "")
						var current downloads.Preparation
						if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &current) != nil {
							t.Fatalf("claim %d %s", result.Code, result.Body.String())
						}
						// Removing a ready claim is the cancellation action offered
						// by the API; it deletes all of that claim's grants.
						raw, _ := json.Marshal(downloads.Command{OperationID: "withdraw-claim", Action: downloads.ActionRemove, ExpectedRevision: current.Revision})
						result = f.call("POST", "/v1/downloads/preparations/"+grant.PreparationID+"/actions", f.owner, string(raw))
						if result.Code != 200 {
							t.Fatalf("remove %d %s", result.Code, result.Body.String())
						}
					case "downloads_permission":
						_, err := dbwork.ExecWrite(context.Background(), f.deps.DB, dbwork.ClassSecurityFence, `INSERT INTO profile_restrictions(profile_id,allow_downloads) VALUES('owner-profile',0) ON CONFLICT(profile_id) DO UPDATE SET allow_downloads=0,revision=revision+1`)
						if err != nil {
							t.Fatal(err)
						}
					}
				}}
				f.handler.ServeHTTP(w, req)
				if w.Code != status || w.withdraw != nil || w.Body.Len() != 32*1024 || w.Body.String() != content[start:start+32*1024] {
					t.Fatalf("withdrawn transfer: status=%d bytes=%d; want status=%d and only initial 32 KiB", w.Code, w.Body.Len(), status)
				}
				if next := f.call("GET", grant.URL, "", ""); next.Code < 400 {
					t.Fatalf("new request still allowed: %d", next.Code)
				}
			})
		}
	}
}

func TestDownloadTransferHeadAndMultipartRanges(t *testing.T) {
	f, grant, content := downloadRevocationFixture(t)
	head := f.call("HEAD", grant.URL, "", "")
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "262144" {
		t.Fatalf("HEAD %d length=%q bytes=%d", head.Code, head.Header().Get("Content-Length"), head.Body.Len())
	}
	req := httptest.NewRequest("GET", grant.URL, nil)
	req.Header.Set("Range", "bytes=17-65536,131077-196608")
	req.Header.Set("If-Range", head.Header().Get("ETag"))
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	mediaType, params, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
	if w.Code != http.StatusPartialContent || err != nil || mediaType != "multipart/byteranges" {
		t.Fatalf("multipart response %d type=%q err=%v", w.Code, mediaType, err)
	}
	parts := multipart.NewReader(w.Body, params["boundary"])
	for i, want := range []string{content[17:65537], content[131077:196609]} {
		part, err := parts.NextPart()
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		got, err := io.ReadAll(part)
		if err != nil || string(got) != want {
			t.Fatalf("part %d length=%d err=%v", i, len(got), err)
		}
	}
	if _, err := parts.NextPart(); err != io.EOF {
		t.Fatalf("unexpected multipart tail: %v", err)
	}
}
