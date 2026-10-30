package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"

	"golang.org/x/text/language"
)

type LibraryProviderPolicy struct {
	Providers        []string
	Language, Region string
	RefreshMode      string
	Revision         int64
	Consent          bool
}

// DefaultAdministrationPolicy is the factory reset policy from the library
// agent and provider defaults, before a per-library edit. Resetting this
// selection never changes the independent provider-consent authority.
func DefaultAdministrationPolicy(kind string) LibraryProviderPolicy {
	p := LibraryProviderPolicy{Providers: []string{}, RefreshMode: "replace_unlocked"}
	switch kind {
	case "movie", "tv", "anime":
		p.Providers = defaultScreenProviders(kind)
		p.Language, p.Region = LibraryAgentOptions(kind)[0].DefaultLanguage, "US"
	case "music":
		p.Providers = []string{"musicbrainz"}
	}
	return p
}

// AdministrationPolicyTx reads effective runtime policy; old admin JSON never
// overrides this state or establishes remote-provider consent.
func AdministrationPolicyTx(ctx context.Context, tx *sql.Tx, library, kind string) (LibraryProviderPolicy, error) {
	p := LibraryProviderPolicy{Providers: []string{}}
	if kind == "movie" || kind == "tv" || kind == "anime" {
		screen, err := readScreenPolicy(ctx, tx, library)
		if err != nil {
			return p, err
		}
		p.Revision = screen.Revision + screen.ConsentRevision - 2
		p.Language = screen.Language
		p.Region = screen.Region
		p.RefreshMode = screen.RefreshMode
		p.Consent = screen.Confirmed
		if screen.Enabled {
			p.Providers = screen.Providers
		}
		return p, nil
	}
	if kind == "music" || kind == "audiobook" {
		var enabled bool
		var a, m int64
		err := tx.QueryRowContext(ctx, `SELECT a.revision,m.revision,m.enabled FROM audio_metadata_policies a JOIN mb_provider_policies m ON m.library_id=a.library_id WHERE a.library_id=?`, library).Scan(&a, &m, &enabled)
		if errors.Is(err, sql.ErrNoRows) {
			return p, nil
		}
		if err != nil {
			return p, err
		}
		p.Revision = a + m - 2
		if enabled {
			p.Providers = []string{"musicbrainz"}
		}
		p.Consent = enabled
		return p, nil
	}
	return p, nil
}

// ApplyAdministrationPolicyTx never creates credentials or grants consent. The
// protected process configuration owns provider credentials; this adapter only
// changes the already-supported per-library selection and locale.
func ApplyAdministrationPolicyTx(ctx context.Context, tx *sql.Tx, library, kind string, p LibraryProviderPolicy) error {
	old, err := AdministrationPolicyTx(ctx, tx, library, kind)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(p.Providers)
	prior, _ := json.Marshal(old.Providers)
	if string(raw) == string(prior) && p.Language == old.Language && p.Region == old.Region && (p.RefreshMode == "" || p.RefreshMode == old.RefreshMode) {
		return nil
	}
	if kind == "movie" || kind == "tv" || kind == "anime" {
		if p.Language == "" {
			p.Language = old.Language
		}
		if p.Language == "" {
			p.Language = "en"
		}
		if _, err = language.Parse(p.Language); err != nil {
			return err
		}
		if p.Region != "" && (len(p.Region) != 2 || p.Region[0] < 'A' || p.Region[0] > 'Z' || p.Region[1] < 'A' || p.Region[1] > 'Z') {
			return requestinput.New("invalid metadata region")
		}
		if len(p.Providers) > 1 {
			return requestinput.New("choose one metadata provider for a library")
		}
		seen := map[string]bool{}
		for _, provider := range p.Providers {
			if seen[provider] || (provider != "tmdb" && provider != "tvdb" && (provider != "anilist" || kind != "anime")) {
				return requestinput.New("unsupported library provider")
			}
			seen[provider] = true
		}
		if p.RefreshMode == "" {
			p.RefreshMode = old.RefreshMode
		}
		if p.RefreshMode != "replace_unlocked" && p.RefreshMode != "fill_missing" {
			return requestinput.New("invalid metadata refresh mode")
		}
		enabled := len(p.Providers) > 0
		if _, err = tx.ExecContext(ctx, `UPDATE screen_metadata_policies SET revision=revision+1,enabled=?,providers=?,language=?,region=?,refresh_mode=? WHERE library_id=?`, enabled, string(raw), p.Language, p.Region, p.RefreshMode, library); err != nil {
			return err
		}
		if err = syncLibraryAgentTx(ctx, tx, library, enabled); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE tvdb_provider_policies SET enabled=?,refresh_mode=?,revision=revision+1 WHERE library_id=?`, seen["tvdb"], p.RefreshMode, library); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE metadata_provider_policies SET enabled=?,language=?,region=?,refresh_mode=? WHERE library_id=? AND provider='tmdb'`, seen["tmdb"], p.Language, p.Region, p.RefreshMode, library); err != nil {
			return err
		}
		if err = screenRequestRematchTx(ctx, tx, library, p.Providers); err != nil {
			return err
		}
		return nil
	}
	if kind == "music" || kind == "audiobook" {
		if p.Language != "" || p.Region != "" || len(p.Providers) > 1 || (len(p.Providers) == 1 && p.Providers[0] != "musicbrainz") {
			return requestinput.New("unsupported music metadata setting")
		}
		enabled := len(p.Providers) == 1
		// MusicBrainz has an existing independent opt-in. This general settings
		// page can disable it; enabling needs that domain's explicit consent flow.
		if enabled && !old.Consent {
			return requestinput.New("MusicBrainz consent required")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE audio_metadata_policies SET revision=revision+1 WHERE library_id=?`, library); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE mb_provider_policies SET enabled=? WHERE library_id=?`, enabled, library); err != nil {
			return err
		}
		return syncLibraryAgentTx(ctx, tx, library, enabled)
	}
	if len(p.Providers) > 0 || p.Language != "" || p.Region != "" {
		return errors.New("remote metadata unavailable for this library")
	}
	return nil
}
