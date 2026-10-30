package subtitlevideo

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

type selectedStorageInputs struct {
	open func(context.Context, string, string, string) (subtitles.RenderInput, error)
}

func (i selectedStorageInputs) OpenSubtitleInput(ctx context.Context, item, asset, session string) (subtitles.RenderInput, error) {
	return i.open(ctx, item, asset, session)
}

// Obtain the sealed continuation only through the actual public Select path.
// Neither this adapter nor remotesources can manufacture its private fields.
func TestSelectedRemoteStorageNeverFallsBackToPathAcquisition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing retained resolver", true: "retained resolver refuses"}[configured], func(t *testing.T) {
			dir := t.TempDir()
			db, err := persistence.Open(filepath.Join(dir, "state.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			catalog := catalogtest.New(t, db)
			library := catalog.Library("library", "Movies", "movie", dir)
			item := catalog.Movie(library, filepath.Join(dir, "film.mp4"), "Film", 2024)
			catalog.Drain()
			for _, query := range []struct {
				sql  string
				args []any
			}{
				{`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) SELECT 'selected-session','login','account','profile',id,?,1,'playing','hash','grant','2099-01-01T00:00:00Z','request',60 FROM catalog_entities WHERE public_id=pid_blob(?)`, []any{item.Token, item.Public}},
				{`INSERT INTO playback_source_pins(session_id,asset_id,size,modified_ns) VALUES('selected-session',?,1000,1)`, []any{item.Token}},
			} {
				if _, err = db.Exec(query.sql, query.args...); err != nil {
					t.Fatal(err)
				}
			}
			principal := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile", ServerID: "server", Role: "owner"}, Hash: "login", Epoch: 1}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			service, err := subtitles.New(subtitles.Options{DB: db, Directory: filepath.Join(dir, "subtitles"), Storage: storage.New(binary), HelperBinary: binary,
				Authorize: func(_ context.Context, _ *sql.Tx, p identity.Principal, _ string) (identity.Principal, error) {
					return p, nil
				}})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			content := "1\n00:00:01,000 --> 00:00:03,000\nSelected source\n"
			receipt, err := service.Mutate(context.Background(), principal, item.Public, subtitles.Mutation{OperationID: "source-upload", SourceID: item.Token, Scope: "personal", Format: "srt", Language: "en", Title: "English", Rights: "Test fixture transcript", OffsetUS: "0", Content: &content})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE subtitle_render_revisions SET source_evidence='representation' WHERE resource_id=?`, receipt.ResourceID); err != nil {
				t.Fatal(err)
			}
			acquisitions, resolutions := 0, 0
			refused := errors.New("selected source refused")
			adapter := RemoteStorageAdapter{Acquire: func(context.Context, string, string) (RemoteObject, error) {
				acquisitions++
				return nil, errors.New("unexpected generic path acquisition")
			}}
			if configured {
				adapter.Selected = func(ctx context.Context, selectedItem, asset, path string, size, modified int64) (subtitles.RenderInput, error) {
					resolutions++
					invocation, ok := subtitles.PlaybackSourceInvocationFromContext(ctx)
					if !ok || invocation.Facts().SessionID != "selected-session" || selectedItem != item.Public || asset != item.Token || path != filepath.Join(dir, "film.mp4") || size != 1000 || modified != 1 {
						t.Fatal("selected resolver lost the exact invocation and source target")
					}
					return nil, refused
				}
			}
			service.SourceInputs = selectedStorageInputs{open: func(ctx context.Context, selectedItem, asset, session string) (subtitles.RenderInput, error) {
				return adapter.Open(ctx, selectedItem, asset, filepath.Join(dir, "film.mp4"), 1000, 1)
			}}
			_, err = service.Select(context.Background(), principal, item.Public, "selected-session", subtitles.SelectRequest{OperationID: "select-source", Generation: 1, ExpectedRevision: 1, Mode: "track", ResourceID: receipt.ResourceID, ResourceRevision: receipt.Revision, OffsetUS: "0"})
			want := subtitles.ErrUnavailable
			if configured {
				want = refused
			}
			if !errors.Is(err, want) || acquisitions != 0 || configured && resolutions != 1 {
				t.Fatal("selected refusal reached generic provider acquisition", err, acquisitions, resolutions)
			}
		})
	}
}
