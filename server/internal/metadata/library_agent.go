package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/localonly"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// A library's metadata agent decides where its titles, artwork and details
// come from, as a Plex agent does. "online" uses Portico's online providers for
// the library's media kind (and the library's own files first); "local" uses
// only what is on disk: file and folder names, embedded tags, NFO and other
// sidecars, and local artwork. A local library never contacts a provider for
// anything: no title lookups, artwork, ratings, lyrics or subtitles.
//
// The agent is the single authority. The per-provider switches that the
// matchers already honour (screen, TVDB, TMDB, MusicBrainz, AcoustID) are kept
// in step with it, and every provider path also checks the agent itself.
const (
	AgentOnline = "online"
	AgentLocal  = "local"
)

var ErrLibraryAgentConflict = errors.New("These metadata settings changed. Refresh before trying again.")
var ErrLibraryAgentInput = requestinput.New("choose a metadata source offered for this library")

// ErrLocalMetadataOnly answers an online lookup requested for a library that
// uses local metadata only.
var ErrLocalMetadataOnly = localonly.Err

type LibraryAgentOption struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Providers   []string `json:"providers"`
	// Languages lists the metadata languages this source can fetch, as
	// lowercase ISO 639-1 codes in a stable order. Always present; empty
	// when the source has no language choice.
	Languages []string `json:"languages"`
	// DefaultLanguage is present exactly when Languages is non-empty.
	DefaultLanguage string `json:"defaultLanguage,omitempty"`
}

type LibraryAgent struct {
	LibraryID   string `json:"libraryId"`
	LibraryKind string `json:"libraryKind"`
	Revision    int64  `json:"revision"`
	Agent       string `json:"agent"`
	// Language is the library's current screen_metadata_policies.language
	// for movie, TV and anime libraries, reported as stored (an older
	// library may hold a regional tag such as en-US). Empty for other kinds.
	Language string               `json:"language,omitempty"`
	Agents   []LibraryAgentOption `json:"agents"`
}

// ScreenMetadataLanguages is the one list of metadata languages the online
// agent can fetch for screen libraries, in stable order. Callers get a fresh
// copy per option so no caller can mutate the shared list.
var ScreenMetadataLanguages = []string{"en", "ja", "fr", "de", "es", "it", "pt", "nl", "sv", "no", "da", "fi", "pl", "cs", "sk", "hu", "ro", "el", "tr", "ru", "uk", "ar", "he", "hi", "th", "vi", "ko", "zh"}

// ValidLibraryKind reports whether kind is one of the kinds POST /v1/libraries
// accepts (catalog.CreateAuthorizedThen). It lives here so the pre-create
// agent listing and the create path share one definition instead of two lists.
func ValidLibraryKind(kind string) bool {
	switch kind {
	case "movie", "tv", "anime", "music", "audiobook", "book", "comic":
		return true
	}
	return false
}

// LibraryLanguageOffered reports whether language is one of agent's Languages
// for kind. Only the online agent for movie, TV and anime offers languages.
func LibraryLanguageOffered(kind, agent, language string) bool {
	if agent != AgentOnline || language == "" {
		return false
	}
	switch kind {
	case "movie", "tv", "anime":
		for _, offered := range ScreenMetadataLanguages {
			if offered == language {
				return true
			}
		}
	}
	return false
}

// LibraryAgentOptions lists the sources a library of this kind can choose, the
// default first. New agents (another provider set) are added here.
func LibraryAgentOptions(kind string) []LibraryAgentOption {
	if kind == "book" || kind == "comic" || kind == "audiobook" {
		return []LibraryAgentOption{{ID: AgentLocal, Name: "Local files only", Description: "Uses local files and embedded metadata without online lookups.", Providers: []string{}, Languages: []string{}}}
	}
	online := LibraryAgentOption{ID: AgentOnline, Name: "Portico online metadata", Languages: []string{}}
	switch kind {
	case "movie":
		online.Description = "Matches films with TMDB for titles, artwork, cast and ratings. Your own files and NFO details are used first."
		online.Providers = metadataprovider.ProvidersFor(kind)
		online.Languages = append([]string{}, ScreenMetadataLanguages...)
		online.DefaultLanguage = "en"
	case "tv":
		online.Description = "Matches shows with one online provider for titles, episodes, artwork and ratings: TMDB unless you choose TheTVDB. Your own files and NFO details are used first."
		online.Providers = metadataprovider.ProvidersFor(kind)
		online.Languages = append([]string{}, ScreenMetadataLanguages...)
		online.DefaultLanguage = "en"
	case "anime":
		online.Description = "Matches anime with one online provider for titles, episodes and artwork: TMDB unless you choose TheTVDB or AniList. Your own files and NFO details are used first."
		online.Providers = metadataprovider.ProvidersFor(kind)
		online.Languages = append([]string{}, ScreenMetadataLanguages...)
		online.DefaultLanguage = "en"
	case "audiobook":
		online.Description = "Uses embedded book tags, sidecar details and local covers. Online audiobook sources are not available."
		online.Providers = []string{}
	default:
		online.Description = "Matches albums with MusicBrainz and Cover Art Archive for details and artwork. Embedded tags are used first."
		online.Providers = []string{"musicbrainz", "coverartarchive"}
	}
	return []LibraryAgentOption{online, {ID: AgentLocal, Name: "Local metadata only", Description: "Uses only file and folder names, embedded tags, NFO and other sidecar files, and artwork stored next to your media. Portico never looks anything up online for this library.", Providers: []string{}, Languages: []string{}}}
}

func validLibraryAgent(kind, agent string) bool {
	for _, option := range LibraryAgentOptions(kind) {
		if option.ID == agent {
			return true
		}
	}
	return false
}

func readLibraryAgent(ctx context.Context, tx *sql.Tx, library string) (LibraryAgent, error) {
	out := LibraryAgent{LibraryID: library}
	var lang sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT l.kind,a.revision,a.agent,p.language FROM libraries l JOIN library_metadata_agents a ON a.library_id=l.id LEFT JOIN screen_metadata_policies p ON p.library_id=l.id WHERE l.id=?`, library).Scan(&out.LibraryKind, &out.Revision, &out.Agent, &lang)
	out.Agents = LibraryAgentOptions(out.LibraryKind)
	if out.LibraryKind == "book" || out.LibraryKind == "comic" || out.LibraryKind == "audiobook" {
		out.Agent = AgentLocal
	}
	if err != nil {
		return out, err
	}
	switch out.LibraryKind {
	case "movie", "tv", "anime":
		if lang.Valid {
			out.Language = lang.String
		}
	default:
		out.Language = ""
	}
	return out, nil
}

// LibraryAgent reads a library's metadata source.
func (s *Service) LibraryAgent(ctx context.Context, library string) (LibraryAgent, error) {
	read, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return LibraryAgent{}, err
	}
	defer read.Rollback()
	return readLibraryAgent(ctx, read.Tx(), library)
}

// SetLibraryAgent changes a library's metadata source under an optimistic
// revision.
func (s *Service) SetLibraryAgent(ctx context.Context, library string, expected int64, agent string, authorize func(*sql.Tx) error) (LibraryAgent, error) {
	if _, _, protocol := usermutation.RequestFrom(ctx); protocol && authorize == nil {
		return LibraryAgent{}, ErrLibraryAgentConflict
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return LibraryAgent{}, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return LibraryAgent{}, err
		}
	}
	r, err := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
	if err != nil {
		return LibraryAgent{}, err
	}
	if r != nil {
		return readLibraryAgent(ctx, tx, library)
	}
	current, err := readLibraryAgent(ctx, tx, library)
	if err != nil {
		return current, err
	}
	if current.Revision != expected {
		return current, ErrLibraryAgentConflict
	}
	if err = ApplyLibraryAgentTx(ctx, tx, library, agent); err != nil {
		return current, err
	}
	out, err := readLibraryAgent(ctx, tx, library)
	if err != nil {
		return out, err
	}
	if err = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: library, Revision: out.Revision}, time.Now().UnixMilli()); err != nil {
		return LibraryAgent{}, err
	}
	return out, gated.Commit()
}

// ApplyLibraryAgentTx records the agent and brings the per-provider switches
// into step with it inside the caller's transaction. Choosing the current
// agent again changes nothing.
func ApplyLibraryAgentTx(ctx context.Context, tx *sql.Tx, library, agent string) error {
	var kind, current string
	if err := tx.QueryRowContext(ctx, `SELECT l.kind,a.agent FROM libraries l JOIN library_metadata_agents a ON a.library_id=l.id WHERE l.id=?`, library).Scan(&kind, &current); err != nil {
		return err
	}
	if !validLibraryAgent(kind, agent) {
		return ErrLibraryAgentInput
	}
	if agent == current {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE library_metadata_agents SET agent=?,revision=revision+1 WHERE library_id=?`, agent, library); err != nil {
		return err
	}
	online := agent == AgentOnline
	switch kind {
	case "movie", "tv", "anime":
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT providers FROM screen_metadata_policies WHERE library_id=?`, library).Scan(&raw); err != nil {
			return err
		}
		providers := []string{}
		_ = json.Unmarshal([]byte(raw), &providers)
		if online && len(providers) == 0 {
			providers = defaultScreenProviders(kind)
		}
		seen := map[string]bool{}
		for _, p := range providers {
			seen[p] = true
		}
		stored, _ := json.Marshal(providers)
		if _, err := tx.ExecContext(ctx, `UPDATE screen_metadata_policies SET revision=revision+1,enabled=?,providers=? WHERE library_id=?`, online, string(stored), library); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tvdb_provider_policies SET enabled=?,revision=revision+1 WHERE library_id=?`, online && seen["tvdb"], library); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE metadata_provider_policies SET enabled=? WHERE library_id=? AND provider='tmdb'`, online && seen["tmdb"], library); err != nil {
			return err
		}
		// The screen policy trigger requests the pass. Agent changes have never
		// rematched identities, and must not acquire that behavior here.
	case "music", "audiobook":
		if _, err := tx.ExecContext(ctx, `UPDATE mb_provider_policies SET enabled=? WHERE library_id=? AND enabled<>?`, online, library, online); err != nil {
			return err
		}
		if !online {
			if _, err := tx.ExecContext(ctx, `UPDATE audio_metadata_policies SET acoustid_enabled=0,revision=revision+1 WHERE library_id=? AND acoustid_enabled<>0`, library); err != nil {
				return err
			}
		}
	}
	return nil
}

// syncLibraryAgentTx records what an older per-provider switch implies about
// the library's source: turning its online providers on selects the online
// agent, turning them all off selects local-only. It does not touch the
// switches themselves (the caller just set them).
func syncLibraryAgentTx(ctx context.Context, tx *sql.Tx, library string, online bool) error {
	agent := AgentLocal
	if online {
		agent = AgentOnline
	}
	_, err := tx.ExecContext(ctx, `UPDATE library_metadata_agents SET agent=?,revision=revision+1 WHERE library_id=? AND agent<>?`, agent, library, agent)
	return err
}

// LibraryLocalOnlyTx reports whether the library must never go online.
func LibraryLocalOnlyTx(ctx context.Context, tx *sql.Tx, library string) (bool, error) {
	return localonly.Library(ctx, tx, library)
}

// NewLibraryAgentOptions publishes the stored default used by library creation.
// Existing library locale and the independent provider consent are not changed.
func (s *Service) NewLibraryAgentOptions(ctx context.Context, kind string) ([]LibraryAgentOption, error) {
	if !ValidLibraryKind(kind) {
		return nil, ErrLibraryAgentInput
	}
	var selected string
	if err := dbwork.QueryRow(ctx, s.db, `SELECT default_language FROM metadata_runtime_settings WHERE singleton=1`).Scan(&selected); err != nil {
		return nil, err
	}
	options := LibraryAgentOptions(kind)
	for i := range options {
		if len(options[i].Languages) == 0 {
			continue
		}
		for _, language := range options[i].Languages {
			if language == selected {
				options[i].DefaultLanguage = selected
				break
			}
		}
	}
	return options, nil
}
