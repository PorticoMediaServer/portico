package metadata

import (
	"context"
	"log"
	"time"
)

// logProviderState writes one startup line for each reason online lookups will
// not run, so an operator reading the log can tell "nothing to match" from
// "matching is paused" (B84). It only reads.
func (s *Service) logProviderState(ctx context.Context) {
	var confirmed bool
	var decided string
	if err := s.db.QueryRowContext(ctx, `SELECT confirmed,confirmed_at FROM screen_metadata_consent WHERE singleton=1`).Scan(&confirmed, &decided); err == nil {
		switch screenLookupStatus(confirmed, decided, true) {
		case "needs_consent":
			log.Printf("Metadata providers: waiting for owner consent; film, TV and anime matching paused")
		case "declined":
			log.Printf("Metadata providers: the owner turned off online lookups; film, TV and anime matching paused")
		}
	}
	if s.tmdbPaced == nil {
		log.Printf("Metadata provider TMDB: no credential; matching paused")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT provider,next_attempt FROM metadata_provider_cooldowns WHERE next_attempt>? ORDER BY provider LIMIT 16`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var provider, until string
		if rows.Scan(&provider, &until) == nil {
			log.Printf("Metadata provider %s: rate-limited until %s", provider, until)
		}
	}
}
