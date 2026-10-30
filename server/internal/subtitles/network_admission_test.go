package subtitles

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/localonly"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func revokeSubtitleLibrary(f *subtitleFixture) {
	f.catalog.Exec(`INSERT INTO library_metadata_agents(library_id,agent) VALUES('library','local') ON CONFLICT(library_id) DO UPDATE SET agent='local',revision=revision+1`)
}

func TestSubtitlePrivacyIsRecheckedBeforeSearchTransport(t *testing.T) {
	f := newSubtitleFixture(t)
	provider, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[]}`))
	})
	f.s.provider = provider
	changed := false
	ctx := metadataprovider.WithNetworkAdmission(context.Background(), func(context.Context) (func() error, error) {
		return func() error {
			if !changed {
				revokeSubtitleLibrary(f)
				changed = true
			}
			return nil
		}, nil
	})
	_, err := f.s.Search(ctx, f.p, f.item.Public, SearchRequest{SourceID: f.item.Token, Language: "en", Page: 1})
	if !errors.Is(err, localonly.Err) || !changed || fake.count("/subtitles") != 0 {
		t.Fatal("private title reached search transport", err, changed, fake.count("/subtitles"))
	}
}

func TestSubtitleProviderRevisionIsRecheckedBeforeEverySend(t *testing.T) {
	provider, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[]}`))
	})
	changed := false
	ctx := metadataprovider.WithNetworkAdmission(context.Background(), func(context.Context) (func() error, error) {
		return func() error {
			if !changed {
				provider.SetCredentials("replacement-key", "", "")
				changed = true
			}
			return nil
		}, nil
	})
	if _, err := provider.Search(ctx, "Private title", "en", 1); !errors.Is(err, ErrConflict) || fake.count("/subtitles") != 0 {
		t.Fatal("queued request used retired provider settings", err, fake.count("/subtitles"))
	}
	if _, err := provider.Search(context.Background(), "Private title", "en", 1); err != nil || fake.count("/subtitles") != 1 || fake.last().Header.Get("Api-Key") != "replacement-key" {
		t.Fatal("new request did not use current provider settings", err)
	}
}

func TestSubtitleCommittedSettingsBlockUntilRuntimeAdoptsThem(t *testing.T) {
	f := newSubtitleFixture(t)
	provider, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":[]}`)) })
	f.s.provider = provider
	changed := false
	ctx := metadataprovider.WithNetworkAdmission(context.Background(), func(context.Context) (func() error, error) {
		return func() error {
			if !changed {
				f.catalog.Exec(`UPDATE metadata_runtime_settings SET opensubtitles_api_key='replacement-key',revision=revision+1 WHERE singleton=1`)
				changed = true
			}
			return nil
		}, nil
	})
	request := SearchRequest{SourceID: f.item.Token, Language: "en", Page: 1}
	if _, err := f.s.Search(ctx, f.p, f.item.Public, request); !errors.Is(err, ErrConflict) || fake.count("/subtitles") != 0 {
		t.Fatal("queued search crossed a committed provider change", err)
	}
	if _, err := f.s.Search(context.Background(), f.p, f.item.Public, request); !errors.Is(err, ErrConflict) || fake.count("/subtitles") != 0 {
		t.Fatal("new search used settings the runtime had not adopted", err)
	}
	provider.SetCredentials("replacement-key", "", "")
	if _, err := f.s.Search(context.Background(), f.p, f.item.Public, request); err != nil || fake.count("/subtitles") != 1 {
		t.Fatal("current runtime settings did not restore acquisition", err)
	}
}

func TestSubtitlePrivacyStopsLoginAndDownloadFollowups(t *testing.T) {
	t.Run("login", func(t *testing.T) {
		f := newSubtitleFixture(t)
		provider, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/login") {
				revokeSubtitleLibrary(f)
				w.Write([]byte(`{"token":"fixture-session","base_url":"api.opensubtitles.com"}`))
				return
			}
			w.Write([]byte(`{"data":[]}`))
		})
		withAccount(provider)
		f.catalog.Exec(`UPDATE metadata_runtime_settings SET opensubtitles_api_key='fixture-key',opensubtitles_username='fixture-user',opensubtitles_password='fixture-password' WHERE singleton=1`)
		f.s.provider = provider
		_, err := f.s.Search(context.Background(), f.p, f.item.Public, SearchRequest{SourceID: f.item.Token, Language: "en", Page: 1})
		if !errors.Is(err, localonly.Err) || fake.count("/login") != 1 || fake.count("/subtitles") != 0 {
			t.Fatal("search followed login after library revocation", err, fake.count("/login"), fake.count("/subtitles"))
		}
	})
	for _, revoke := range []bool{false, true} {
		name := "download-allowed"
		if revoke {
			name = "download-revoked"
		}
		t.Run(name, func(t *testing.T) {
			f := newSubtitleFixture(t)
			provider, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/download") {
					if revoke {
						revokeSubtitleLibrary(f)
					}
					w.Write([]byte(`{"link":"https://download.example/subtitle.srt"}`))
					return
				}
				w.Write([]byte(`{"data":[{"attributes":{"language":"en","release":"Fixture","files":[{"file_id":42}]}}]}`))
			})
			fetches := 0
			provider.fetch = func(context.Context, string) ([]byte, error) {
				fetches++
				return []byte(plainSubtitle), nil
			}
			f.s.provider = provider
			found, err := f.s.Search(context.Background(), f.p, f.item.Public, SearchRequest{SourceID: f.item.Token, Language: "en", Page: 1})
			if err != nil || len(found.Candidates) != 1 {
				t.Fatal(found, err)
			}
			_, err = f.s.Apply(context.Background(), f.p, f.item.Public, ApplyRequest{OperationID: "provider-download", CandidateID: found.Candidates[0].ID, Scope: "personal", Rights: "Fixture"})
			if revoke {
				if !errors.Is(err, localonly.Err) || fetches != 0 {
					t.Fatal("download link fetched after privacy revocation", err, fetches)
				}
			} else if err != nil || fetches != 1 {
				t.Fatal("ordinary subtitle acquisition changed", err, fetches)
			}
			if fake.count("/download") != 1 {
				t.Fatal("download authorization was not exercised")
			}
		})
	}
}
