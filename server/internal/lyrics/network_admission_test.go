package lyrics

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/localonly"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func TestLyricsPrivacyIsRecheckedAfterAdmissionBeforeTransport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"search", "bulk"} {
		for _, change := range []struct {
			name, sql string
			want      error
		}{
			{"provider", `UPDATE configuration SET value='false' WHERE key='lyrics_lrclib_enabled'`, ErrConflict},
			{"provider-revision", `UPDATE configuration SET value=CAST(value AS INTEGER)+1 WHERE key='lyrics_provider_revision'`, ErrConflict},
			{"library-provider", `UPDATE library_preferences SET lyrics_source='files',revision=revision+1 WHERE library_id='library'`, ErrConflict},
			{"library-local", `INSERT INTO library_metadata_agents(library_id,agent) VALUES('library','local') ON CONFLICT(library_id) DO UPDATE SET agent='local',revision=revision+1`, localonly.Err},
		} {
			t.Run(mode+"/"+change.name, func(t *testing.T) {
				c, bulk, names := bulkFixture(t)
				requests := 0
				bulk.Service.Provider = &LRCLIB{origin: "https://lyrics.example", client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
					requests++
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[]`)), Header: http.Header{}}, nil
				})}}
				changed := false
				// The existing admission composition gives a deterministic pause
				// between capture and the check immediately before client.Do.
				ctx := metadataprovider.WithNetworkAdmission(context.Background(), func(context.Context) (func() error, error) {
					return func() error {
						if !changed {
							c.Exec(change.sql)
							changed = true
						}
						return nil
					}, nil
				})
				if mode == "bulk" {
					outcome, err := bulk.fetchOne(ctx, "privacy", names["song-a"].Public, 1)
					if err != nil || outcome != "skipped" {
						t.Fatal(outcome, err)
					}
				} else {
					access := Access{Actor: Actor{Authority: "local", AccountID: "owner", ProfileID: "profile", Owner: true}, ServerID: "server", LibraryID: "library", ItemID: names["song-a"].Public, ViewerFence: "fence", Authorize: func(*sql.Tx) error { return nil }}
					view, err := bulk.Service.View(ctx, access, Target{})
					if err != nil || view.Source == nil {
						t.Fatal(view, err)
					}
					_, err = bulk.Service.Search(ctx, access, SearchInput{Target: Target{SourceID: view.Source.ID, SourceVersion: view.Source.Version}, Provider: "lrclib", Language: "und"}, "")
					if !errors.Is(err, change.want) {
						t.Fatal("revoked request was not refused", err)
					}
				}
				if !changed || requests != 0 {
					t.Fatal("request reached transport after privacy revocation", changed, requests)
				}
			})
		}
	}
}
