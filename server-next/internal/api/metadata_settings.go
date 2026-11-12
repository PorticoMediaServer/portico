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
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// Server › Metadata (definition area 4, rule 8): the owner's own provider keys and the metadata
// switches, in the shape the apps' Metadata page reads. Only the owner reads or changes it. A
// key is sealed with the server's key (package secrets) in a settings row of its own, and no
// route, log line or settings document ever carries it back: the page says only whether one is
// set. The switches are the settings document "metadata" (metadata.Document).

// The rows the owner's keys are sealed in, each sealed to its own name.
const (
	settingTMDBKey = "metadata.key.tmdb"
	// settingOpenSubtitles holds the OpenSubtitles API key and account (openSubtitlesAccount).
	settingOpenSubtitles = "metadata.key.opensubtitles"
)

// MetadataSettings is Server › Metadata as the owner's page reads it.
type MetadataSettings struct {
	// Revision moves on with every save of the page; a save sends the one it read.
	Revision int64 `json:"revision"`
	// IncludeAdult is "Include adult titles", off by default.
	IncludeAdult bool `json:"includeAdult"`
	// TMDBCredentialConfigured says the owner's own TMDB read access token is in use. Without
	// one, TMDB is asked with Portico's key, built into the server.
	TMDBCredentialConfigured bool `json:"tmdbCredentialConfigured"`
	// TMDBCredentialUnreadable says the owner saved a key of their own that this server can no
	// longer open (its key file was lost or replaced since): Portico's key is in use until the
	// owner enters theirs again.
	TMDBCredentialUnreadable bool `json:"tmdbCredentialUnreadable,omitempty"`
	// The owner's OpenSubtitles API key and account, which subtitle search needs (definition
	// area 7, rule 67): whether a key and a password are saved, and the account's username.
	OpenSubtitlesKeyConfigured      bool   `json:"openSubtitlesKeyConfigured"`
	OpenSubtitlesUsername           string `json:"openSubtitlesUsername"`
	OpenSubtitlesPasswordConfigured bool   `json:"openSubtitlesPasswordConfigured"`
	// OpenSubtitles says how OpenSubtitles last answered Test connection. See metadata_subtitle_keys.go.
	OpenSubtitles OpenSubtitlesStatus `json:"openSubtitles"`
}

// MetadataSettingsChange is a save of the page. A field left out keeps its value. An empty key
// removes the owner's own, and Portico's is used again.
type MetadataSettingsChange struct {
	// ExpectedRevision is the revision the page read; a page saved since is refused (412).
	// Left out, the save is made over whatever is there.
	ExpectedRevision *int64  `json:"expectedRevision,omitempty"`
	IncludeAdult     *bool   `json:"includeAdult,omitempty"`
	TMDBReadToken    *string `json:"tmdbReadToken,omitempty"`
	// The OpenSubtitles key, username and password. An empty username removes the account and
	// its password; a new username without a password leaves none saved.
	OpenSubtitlesAPIKey   *string `json:"openSubtitlesApiKey,omitempty"`
	OpenSubtitlesUsername *string `json:"openSubtitlesUsername,omitempty"`
	OpenSubtitlesPassword *string `json:"openSubtitlesPassword,omitempty"`
}

// providerKeys is the owner's keys, opened, kept in memory only.
type providerKeys struct {
	mu   sync.Mutex
	box  *secrets.Box
	tmdb string // the owner's own TMDB token, or ""
	// unreadable names the rows holding a key this server cannot open.
	unreadable map[string]bool
	// tmdbHost is TMDB's address; a stand-in's for testing.
	tmdbHost string
	// subtitles is the owner's OpenSubtitles key and account, and checked what Test connection
	// last found. subtitleHost and subtitleEvery are OpenSubtitles' address and spacing; a
	// stand-in's for testing.
	subtitles     openSubtitlesAccount
	checked       subtitleCheck
	subtitleHost  string
	subtitleEvery time.Duration
}

func (s *Server) metadataSettingsRoutes() {
	Handle(s, "GET /v1/admin/metadata/settings", Owner, func(r *http.Request, _ None) (MetadataSettings, error) {
		return s.metadataSettingsPage()
	})
	Handle(s, "PUT /v1/admin/metadata/settings", Owner, s.saveMetadataSettings)
	Handle(s, "POST /v1/admin/metadata/settings/opensubtitles/check", Owner, s.checkOpenSubtitles)
}

func (s *Server) metadataSettingsPage() (MetadataSettings, error) {
	doc, err := s.settings.Read(metadata.Document.Name())
	if err != nil {
		return MetadataSettings{}, err
	}
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	// The document's revision is 0 until its first save; the page's starts at 1.
	return MetadataSettings{Revision: doc.Revision + 1, IncludeAdult: metadata.Document.Get(s.settings).IncludeAdult,
		TMDBCredentialConfigured: s.keys.tmdb != "", TMDBCredentialUnreadable: s.keys.unreadable[settingTMDBKey],
		OpenSubtitlesKeyConfigured: s.keys.subtitles.APIKey != "", OpenSubtitlesUsername: s.keys.subtitles.Username,
		OpenSubtitlesPasswordConfigured: s.keys.subtitles.Password != "", OpenSubtitles: s.subtitleStatus()}, nil
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

func (s *Server) saveMetadataSettings(r *http.Request, req MetadataSettingsChange) (MetadataSettings, error) {
	for _, f := range []struct {
		path  string
		value *string
		limit int
	}{{"tmdbReadToken", req.TMDBReadToken, 2048}, {"openSubtitlesApiKey", req.OpenSubtitlesAPIKey, 256},
		{"openSubtitlesUsername", req.OpenSubtitlesUsername, 100}, {"openSubtitlesPassword", req.OpenSubtitlesPassword, 256}} {
		if f.value == nil {
			continue
		}
		if err := checkSecretField(f.path, *f.value, f.limit); err != nil {
			return MetadataSettings{}, err
		}
	}
	match := int64(-1)
	if req.ExpectedRevision != nil {
		match = *req.ExpectedRevision - 1
	}
	// The switches are saved first: that save is the one refused when the page is out of date,
	// and every save of the page moves its revision on, a key's alone too.
	change := map[string]any{}
	if req.IncludeAdult != nil {
		change["includeAdult"] = *req.IncludeAdult
	}
	raw, _ := json.Marshal(change)
	if _, err := s.settings.Write(r.Context(), CallerFrom(r.Context()).UserID, metadata.Document.Name(), raw, match, s.now()); errors.Is(err, settings.ErrStale) {
		return MetadataSettings{}, errStale()
	} else if err != nil {
		return MetadataSettings{}, err
	}
	if req.TMDBReadToken != nil {
		if err := s.saveKey(r.Context(), settingTMDBKey, *req.TMDBReadToken); err != nil {
			return MetadataSettings{}, err
		}
		s.keys.mu.Lock()
		s.keys.tmdb = *req.TMDBReadToken
		delete(s.keys.unreadable, settingTMDBKey)
		s.useTMDB()
		s.keys.mu.Unlock()
	}
	if err := s.saveSubtitleAccount(r.Context(), req); err != nil {
		return MetadataSettings{}, err
	}
	return s.metadataSettingsPage()
}

// saveKey seals a key to its row; an empty one removes the row.
func (s *Server) saveKey(ctx context.Context, name, plain string) error {
	if plain == "" {
		return s.db.DeleteSetting(ctx, name)
	}
	return s.db.SaveSetting(ctx, name, base64.StdEncoding.EncodeToString(s.keys.box.Seal(name, []byte(plain))), s.now())
}

// openKey reads a sealed key. One this server cannot open (its key file was lost or replaced
// since) is left unused, as if none were set, and is said on the page (unreadable) and in the
// log: the owner enters it again.
func (s *Server) openKey(ctx context.Context, name string) (string, error) {
	text, ok, err := s.db.Setting(ctx, name)
	if err != nil || !ok {
		return "", err
	}
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	sealed, err := base64.StdEncoding.DecodeString(text)
	if err == nil {
		var plain []byte
		if plain, err = s.keys.box.Open(name, sealed); err == nil {
			return string(plain), nil
		}
	}
	s.keys.unreadable[name] = true
	s.log.Warn("a provider key stored on this server cannot be opened with its key file; the owner must enter it again in Settings › Metadata", "setting", name)
	return "", nil
}

// loadProviderKeys opens the owner's keys at start and puts them to use.
func (s *Server) loadProviderKeys(ctx context.Context) error {
	box, err := secrets.Open(s.dataDir)
	if err != nil {
		return err
	}
	s.keys.box, s.keys.tmdbHost, s.keys.unreadable = box, tmdb.Origin, map[string]bool{}
	s.keys.subtitleHost, s.keys.subtitleEvery = opensubtitles.Origin, opensubtitles.Every
	key, err := s.openKey(ctx, settingTMDBKey)
	if err != nil {
		return err
	}
	account, err := s.openKey(ctx, settingOpenSubtitles)
	if err != nil {
		return err
	}
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	s.keys.tmdb = key
	if account != "" {
		json.Unmarshal([]byte(account), &s.keys.subtitles)
	}
	s.useTMDB()
	s.useSubtitles()
	return nil
}

// useTMDB gives the lookups TMDB with the key that takes precedence: the owner's own, saved in
// Settings › Metadata, else Portico's built-in one (tmdb.ProjectToken), so a new server looks
// titles up with nothing to set. Called with keys.mu held.
func (s *Server) useTMDB() {
	key := s.keys.tmdb
	if key == "" {
		key = tmdb.ProjectToken
	}
	s.metadata.Use(tmdb.New(key, s.keys.tmdbHost))
}

// UseTMDBHost points the server's TMDB client at another address: a stand-in, for testing.
func (s *Server) UseTMDBHost(origin string) {
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	s.keys.tmdbHost = origin
	s.useTMDB()
}
