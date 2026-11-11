package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/opensubtitles"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
	"github.com/PorticoMediaServer/portico/server-next/internal/secrets"
)

// Server › Metadata: the owner's own provider keys and switches. The keys are sealed with the
// server's key (package secrets), never returned by any route, never logged, and can only be
// replaced or removed. The owner alone sees or changes this page; administrators do not.

// The settings rows. A key's row holds it sealed to the row's own name.
const (
	settingMetadata          = "metadata.settings"
	settingTMDBKey           = "metadata.key.tmdb"
	settingOpenSubtitlesKeys = "metadata.key.opensubtitles"
)

// MetadataSettings is the page as the owner sees it. A key shows only whether one is set.
type MetadataSettings struct {
	TMDB          TMDBSettings          `json:"tmdb"`
	OpenSubtitles OpenSubtitlesSettings `json:"openSubtitles"`
	// IncludeAdult is "Include adult titles" in searches. Off by default.
	IncludeAdult bool `json:"includeAdult"`
}

// TMDBSettings is the owner's TMDB read access token, shown only as set or not.
type TMDBSettings struct {
	KeySet bool `json:"keySet"`
	// EnterAgain says a key is stored but this server cannot open it (its key file was lost
	// or replaced): it must be entered again.
	EnterAgain bool `json:"enterAgain,omitempty"`
}

// OpenSubtitlesSettings is the owner's OpenSubtitles API key and account.
type OpenSubtitlesSettings struct {
	APIKeySet   bool   `json:"apiKeySet"`
	Username    string `json:"username,omitempty"`
	PasswordSet bool   `json:"passwordSet"`
	EnterAgain  bool   `json:"enterAgain,omitempty"`
}

// MetadataSettingsEdit changes the page. A field left out is left as it is; an empty key or
// password removes it.
type MetadataSettingsEdit struct {
	TMDBKey       *string                    `json:"tmdbKey,omitempty"`
	OpenSubtitles *OpenSubtitlesSettingsEdit `json:"openSubtitles,omitempty"`
	IncludeAdult  *bool                      `json:"includeAdult,omitempty"`
}

// OpenSubtitlesSettingsEdit changes the OpenSubtitles key and account.
type OpenSubtitlesSettingsEdit struct {
	APIKey   *string `json:"apiKey,omitempty"`
	Username *string `json:"username,omitempty"`
	Password *string `json:"password,omitempty"`
}

// providerSettings is what the owner saved, the keys opened. It is kept only in memory.
type providerSettings struct {
	TMDBKey      string `json:"-"`
	IncludeAdult bool   `json:"includeAdult"`
	// OpenSubtitles.
	SubtitleKey      string `json:"-"`
	SubtitleUser     string `json:"openSubtitlesUser,omitempty"`
	SubtitlePassword string `json:"-"`
	// enterAgain names the keys stored that this server cannot open.
	enterAgain map[string]bool
}

// openSubtitlesSecret is what is sealed for OpenSubtitles: the key and the account's password.
type openSubtitlesSecret struct {
	APIKey   string `json:"apiKey,omitempty"`
	Password string `json:"password,omitempty"`
}

// providers holds the server's own state of the owner's settings.
type providers struct {
	mu       sync.Mutex
	box      *secrets.Box
	current  providerSettings
	tmdbHost string // TMDB's address; a stand-in's for testing
	// subtitleHost and subtitleEvery are OpenSubtitles' address and spacing; a stand-in's for testing.
	subtitleHost  string
	subtitleEvery time.Duration
}

func (s *Server) metadataSettingsRoutes() {
	Handle(s, "GET /v1/admin/metadata/settings", Owner, func(r *http.Request, _ None) (MetadataSettings, error) {
		s.providers.mu.Lock()
		defer s.providers.mu.Unlock()
		return s.providers.current.page(), nil
	})
	Handle(s, "PUT /v1/admin/metadata/settings", Owner, s.saveMetadataSettings)
}

func (p providerSettings) page() MetadataSettings {
	return MetadataSettings{IncludeAdult: p.IncludeAdult,
		TMDB: TMDBSettings{KeySet: p.TMDBKey != "", EnterAgain: p.enterAgain[settingTMDBKey]},
		OpenSubtitles: OpenSubtitlesSettings{APIKeySet: p.SubtitleKey != "", Username: p.SubtitleUser, PasswordSet: p.SubtitlePassword != "",
			EnterAgain: p.enterAgain[settingOpenSubtitlesKeys]}}
}

// checkSecretField refuses a key, user name or password that cannot be real: too long, or with
// characters no provider gives. The value itself is never in the message.
func checkSecretField(path, value string, limit int) error {
	if len(value) > limit {
		return ErrField(path, "This is longer than any the provider gives.")
	}
	for _, c := range value {
		if c < ' ' || c == 0x7f {
			return ErrField(path, "This has a character the provider never gives.")
		}
	}
	return nil
}

func (s *Server) saveMetadataSettings(r *http.Request, req MetadataSettingsEdit) (MetadataSettings, error) {
	s.providers.mu.Lock()
	defer s.providers.mu.Unlock()
	next := s.providers.current
	next.enterAgain = map[string]bool{}
	for k, v := range s.providers.current.enterAgain {
		next.enterAgain[k] = v
	}
	if req.TMDBKey != nil {
		if err := checkSecretField("tmdbKey", *req.TMDBKey, 2048); err != nil {
			return MetadataSettings{}, err
		}
		next.TMDBKey = *req.TMDBKey
		delete(next.enterAgain, settingTMDBKey)
	}
	if o := req.OpenSubtitles; o != nil {
		for _, f := range []struct {
			path  string
			value *string
			to    *string
			limit int
		}{{"openSubtitles.apiKey", o.APIKey, &next.SubtitleKey, 256}, {"openSubtitles.username", o.Username, &next.SubtitleUser, 100},
			{"openSubtitles.password", o.Password, &next.SubtitlePassword, 256}} {
			if f.value == nil {
				continue
			}
			if err := checkSecretField(f.path, *f.value, f.limit); err != nil {
				return MetadataSettings{}, err
			}
			*f.to = *f.value
		}
		if o.APIKey != nil || o.Password != nil {
			delete(next.enterAgain, settingOpenSubtitlesKeys)
		}
	}
	if req.IncludeAdult != nil {
		next.IncludeAdult = *req.IncludeAdult
	}
	plain, _ := json.Marshal(next)
	values := map[string]string{settingMetadata: string(plain)}
	if req.TMDBKey != nil {
		values[settingTMDBKey] = s.sealSetting(settingTMDBKey, next.TMDBKey)
	}
	if o := req.OpenSubtitles; o != nil && (o.APIKey != nil || o.Password != nil) {
		secret := ""
		if next.SubtitleKey != "" || next.SubtitlePassword != "" {
			raw, _ := json.Marshal(openSubtitlesSecret{APIKey: next.SubtitleKey, Password: next.SubtitlePassword})
			secret = string(raw)
		}
		values[settingOpenSubtitlesKeys] = s.sealSetting(settingOpenSubtitlesKeys, secret)
	}
	if err := s.db.SaveSettings(r.Context(), values, s.now()); err != nil {
		return MetadataSettings{}, err
	}
	s.providers.current = next
	s.useProviders(next)
	return next.page(), nil
}

// sealSetting seals a secret to its row, as the text the row keeps; "" stays "", which removes it.
func (s *Server) sealSetting(name, plain string) string {
	if plain == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString(s.providers.box.Seal(name, []byte(plain)))
}

// openSetting reads a sealed secret. One this server cannot open is reported, never guessed.
func (s *Server) openSetting(ctx context.Context, name string) (string, bool, error) {
	text, ok, err := s.db.Setting(ctx, name)
	if err != nil || !ok || text == "" {
		return "", true, err
	}
	sealed, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return "", false, nil
	}
	plain, err := s.providers.box.Open(name, sealed)
	if errors.Is(err, secrets.ErrCannotOpen) {
		return "", false, nil
	}
	return string(plain), err == nil, err
}

// loadMetadataSettings reads the owner's settings at start and puts them to use.
func (s *Server) loadMetadataSettings(ctx context.Context) error {
	var p providerSettings
	p.enterAgain = map[string]bool{}
	if text, ok, err := s.db.Setting(ctx, settingMetadata); err != nil {
		return err
	} else if ok {
		json.Unmarshal([]byte(text), &p)
	}
	key, opened, err := s.openSetting(ctx, settingTMDBKey)
	if err != nil {
		return err
	}
	p.TMDBKey, p.enterAgain[settingTMDBKey] = key, !opened
	raw, opened, err := s.openSetting(ctx, settingOpenSubtitlesKeys)
	if err != nil {
		return err
	}
	var sub openSubtitlesSecret
	if raw != "" {
		json.Unmarshal([]byte(raw), &sub)
	}
	p.SubtitleKey, p.SubtitlePassword, p.enterAgain[settingOpenSubtitlesKeys] = sub.APIKey, sub.Password, !opened
	if p.enterAgain[settingTMDBKey] || p.enterAgain[settingOpenSubtitlesKeys] {
		s.log.Warn("a provider key stored on this server cannot be opened with its key file; the owner must enter it again")
	}
	s.providers.mu.Lock()
	defer s.providers.mu.Unlock()
	s.providers.current = p
	s.useProviders(p)
	return nil
}

// useProviders hands the settings to what uses them. Called with providers.mu held.
func (s *Server) useProviders(p providerSettings) {
	online := map[string]metadata.Provider{}
	if p.TMDBKey != "" {
		online["tmdb"] = tmdb.New(p.TMDBKey, s.providers.tmdbHost)
	}
	s.metadata.Use(online, p.IncludeAdult)
	// Subtitle search needs the API key and the account (definition 07, rule 67). A new client
	// is made for every save, so a refused password is tried again only once the owner changes it.
	var subtitles metadata.SubtitleProvider
	if p.SubtitleKey != "" && p.SubtitleUser != "" && p.SubtitlePassword != "" {
		subtitles = opensubtitles.New(s.providers.subtitleHost, s.providers.subtitleEvery, p.SubtitleKey, p.SubtitleUser, p.SubtitlePassword)
	}
	s.metadata.UseSubtitles(subtitles)
}

// UseSubtitleHost points the server's OpenSubtitles client at a stand-in, for testing.
func (s *Server) UseSubtitleHost(origin string) {
	s.providers.mu.Lock()
	defer s.providers.mu.Unlock()
	s.providers.subtitleHost, s.providers.subtitleEvery = origin, 0
	s.useProviders(s.providers.current)
}

// UseTMDBHost points the server's TMDB client at another address: a stand-in, for testing.
func (s *Server) UseTMDBHost(origin string) {
	s.providers.mu.Lock()
	defer s.providers.mu.Unlock()
	s.providers.tmdbHost = origin
	s.useProviders(s.providers.current)
}
