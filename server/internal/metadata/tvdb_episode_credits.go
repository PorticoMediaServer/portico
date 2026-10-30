package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// tvdbEpisodeCharacterProvider is the one TVDB call the episode-credits step
// needs. It is asserted, not part of TVDBProvider, so the show pipeline's
// fakes keep working unchanged; production's *TVDB implements it.
type tvdbEpisodeCharacterProvider interface {
	EpisodeCharacters(context.Context, int64) ([]metadataprovider.TVDBEpisodeCharacter, error)
}

// tvdbEpisodeCreditPage bounds one step: one upstream request per episode.
const tvdbEpisodeCreditPage = 8

// tvdbEpisodeCreditsPlan bounds the step's one write: up to eight episodes'
// credits, about forty-five rows each.
var tvdbEpisodeCreditsPlan = dbwork.WritePlan{Name: "tvdb-episode-credits", Rows: 1024, Pages: 256, VMOps: 500_000, Why: "the episodes fetched in one step publish their replaced credit sets in one writer turn, after the fetch and outside any snapshot"}

// tvdbEpisodeCreditAttempts bounds retries for an episode TVDB cannot supply
// (a deleted upstream record must not be refetched forever).
const tvdbEpisodeCreditAttempts = 5

// tvdbEpisodeCreditCleanup bounds the orphaned evidence one step removes, so
// the cleanup stays inside the writer's per-transaction effect budget.
const tvdbEpisodeCreditCleanup = 32

type tvdbEpisodeCreditClaim struct {
	item, providerID int64
}

// tvdbEpisodeCreditClaims merges at most eight active library streams. Every
// excluded library starts at or after the eighth initial minimum, so it cannot
// contribute an earlier item to the global first eight. Each seek retains the
// authoritative joins; an inconsistent head requires repair, never a full scan.
func (s *Service) tvdbEpisodeCreditClaims(ctx context.Context) ([]tvdbEpisodeCreditClaim, error) {
	type stream struct {
		library int64
		next    tvdbEpisodeCreditClaim
	}
	rows, err := dbwork.Query(ctx, s.db, `SELECT h.library_id,h.next_item,h.next_provider FROM tvdb_episode_credit_heads h INDEXED BY tvdb_episode_credit_heads_due JOIN catalog_libraries cl ON cl.id=h.library_id JOIN tvdb_provider_policies tp ON tp.library_id=cl.library_id AND tp.enabled=1 WHERE h.enabled=1 AND h.next_item IS NOT NULL ORDER BY h.next_item,h.library_id LIMIT ?`, tvdbEpisodeCreditPage)
	if err != nil {
		return nil, err
	}
	var streams []stream
	for rows.Next() {
		var st stream
		if err = rows.Scan(&st.library, &st.next.item, &st.next.providerID); err != nil {
			rows.Close()
			return nil, err
		}
		streams = append(streams, st)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	seek := func(library, afterItem, afterProvider int64) (tvdbEpisodeCreditClaim, bool, error) {
		rows, err := dbwork.Query(ctx, s.db, `SELECT p.item_id,p.provider_id FROM tvdb_episode_credit_pending p INDEXED BY tvdb_episode_credit_pending_library JOIN provider_evidence pe ON pe.item_id=p.item_id AND pe.provider='tvdb' AND pe.provider_id=p.provider_id JOIN catalog_episodes ep ON ep.entity_id=p.item_id JOIN catalog_entities e ON e.id=p.item_id AND e.kind=4 AND e.library_id=p.library_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN tvdb_provider_policies tp ON tp.library_id=cl.library_id AND tp.enabled=1 JOIN tvdb_episode_credit_evidence ce ON ce.item_id=p.item_id AND ce.provider_id=p.provider_id AND ce.credits IS NULL AND ce.attempts<5 WHERE p.library_id=? AND (p.item_id,p.provider_id)>(?,?) ORDER BY p.item_id,p.provider_id LIMIT 1`, library, afterItem, afterProvider)
		if err != nil {
			return tvdbEpisodeCreditClaim{}, false, err
		}
		defer rows.Close()
		var c tvdbEpisodeCreditClaim
		found := rows.Next()
		if found {
			err = rows.Scan(&c.item, &c.providerID)
		}
		if err == nil {
			err = rows.Err()
		}
		return c, found, err
	}
	for _, st := range streams {
		first, found, err := seek(st.library, st.next.item, st.next.providerID-1)
		if err != nil {
			return nil, err
		}
		if !found || first != st.next {
			return nil, fmt.Errorf("TVDB episode-credit head for library %d requires repair", st.library)
		}
	}
	claims := make([]tvdbEpisodeCreditClaim, 0, tvdbEpisodeCreditPage)
	for len(streams) > 0 && len(claims) < tvdbEpisodeCreditPage {
		best := 0
		for i := 1; i < len(streams); i++ {
			if streams[i].next.item < streams[best].next.item {
				best = i
			}
		}
		st := &streams[best]
		claims = append(claims, st.next)
		if len(claims) == tvdbEpisodeCreditPage {
			break
		}
		next, found, err := seek(st.library, st.next.item, st.next.providerID)
		if err != nil {
			return nil, err
		}
		if found {
			st.next = next
		} else {
			streams = append(streams[:best], streams[best+1:]...)
		}
	}
	return claims, nil
}

// tvdbEpisodeCreditsStep gives TVDB-matched episodes their cast. TVDB episodes
// are delegated past the screen pipeline (screenEpisodeStep answers
// delegated_tvdb), so nothing ever writes their screen credits; this step
// claims a bounded page of matched episodes with no credit evidence, fetches
// each one's /episodes/{id}/extended characters, and writes them as the
// episode item's tvdb credits. It runs when TVDBStep has no projection or
// acquisition work, so it never delays a show publication.
func (s *Service) tvdbEpisodeCreditsStep(ctx context.Context) error {
	ctx = s.lookupContext(ctx)
	provider, ok := s.tvdb.(tvdbEpisodeCharacterProvider)
	if !ok {
		return nil
	}
	var claims, stale []tvdbEpisodeCreditClaim
	// This is a read-pick, not a reservation. Admit the catalogue walk as
	// background reading and close its snapshot before fetching or publishing.
	err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.db, func(readCtx context.Context) error {
		var err error
		claims, err = s.tvdbEpisodeCreditClaims(readCtx)
		if err != nil {
			return err
		}
		// A rematch leaves evidence for a pair the catalog no longer holds.
		// Find at most one writer-sized batch here, outside the writer; each
		// key is rechecked before it is deleted.
		rows, err := dbwork.Query(readCtx, s.db, `SELECT item_id,provider_id FROM tvdb_episode_credit_cleanup ORDER BY item_id,provider_id LIMIT ?`, tvdbEpisodeCreditCleanup)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c tvdbEpisodeCreditClaim
			if err := rows.Scan(&c.item, &c.providerID); err != nil {
				return err
			}
			stale = append(stale, c)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	if len(claims) == 0 && len(stale) == 0 {
		return nil
	}
	// Fetches run outside the read snapshot. Publication retains the original
	// context and all its lookup guards, never the closed read transaction.
	type result struct {
		credits []metadataprovider.TVDBEpisodeCharacter
		err     error
	}
	results := make([]result, len(claims))
	for i, c := range claims {
		if err = ctx.Err(); err != nil {
			return err
		}
		results[i].credits, results[i].err = provider.EpisodeCharacters(ctx, c.providerID)
	}
	return dbwork.WithWriteTx(dbwork.WithPlan(ctx, tvdbEpisodeCreditsPlan), s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		if err := checkLookupContextTx(ctx, tx); err != nil {
			return err
		}
		stamp := tvdbStamp(s.publicationTime())
		for i, c := range claims {
			// The pair was picked before the fetch. A rematch, a kind or
			// library move, a disabled policy or a deleted episode in the
			// meantime means these credits no longer belong to this item: drop
			// the result and leave no evidence or attempt for the old pair.
			var current bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM provider_evidence pe JOIN catalog_episodes e ON e.entity_id=pe.item_id JOIN catalog_entities i ON i.id=pe.item_id JOIN catalog_libraries cl ON cl.id=i.library_id JOIN tvdb_provider_policies tp ON tp.library_id=cl.library_id AND tp.enabled=1 WHERE pe.item_id=? AND pe.provider='tvdb' AND pe.provider_id=? AND i.kind=4)`, c.item, c.providerID).Scan(&current); err != nil {
				return err
			}
			if !current {
				continue
			}
			if results[i].err != nil {
				if _, err := tx.ExecContext(ctx, `INSERT INTO tvdb_episode_credit_evidence(item_id,provider_id,observed_at,attempts) VALUES(?,?,?,1) ON CONFLICT(item_id,provider_id) DO UPDATE SET observed_at=excluded.observed_at,attempts=tvdb_episode_credit_evidence.attempts+1`, c.item, c.providerID, stamp); err != nil {
					return err
				}
				continue
			}
			if err := writeTVDBEpisodeCredits(ctx, tx, c.item, results[i].credits, "tvdb:episode:"+strconv.FormatInt(c.providerID, 10), stamp); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO tvdb_episode_credit_evidence(item_id,provider_id,observed_at,credits,attempts) VALUES(?,?,?,COALESCE((SELECT count(*) FROM catalog_credits WHERE entity_id=? AND provider='tvdb'),0),0) ON CONFLICT(item_id,provider_id) DO UPDATE SET observed_at=excluded.observed_at,credits=excluded.credits,attempts=0`, c.item, c.providerID, stamp, c.item); err != nil {
				return err
			}
		}
		for _, c := range stale {
			if _, err := tx.ExecContext(ctx, `DELETE FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=? AND NOT EXISTS(SELECT 1 FROM provider_evidence pe WHERE pe.item_id=? AND pe.provider='tvdb' AND pe.provider_id=?)`, c.item, c.providerID, c.item, c.providerID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM tvdb_episode_credit_cleanup WHERE item_id=? AND provider_id=?`, c.item, c.providerID); err != nil {
				return err
			}
		}
		return nil
	})
}

// writeTVDBEpisodeCredits replaces one episode item's tvdb credit family with
// the extended record's characters. Other providers' rows (and NFO rows) are
// untouched: a selected provider owns only its own family. Person identity is
// the TVDB person id, the same key movies and show cast use, so a guest star
// is one /v1/people person everywhere.
func writeTVDBEpisodeCredits(ctx context.Context, tx *sql.Tx, item int64, chars []metadataprovider.TVDBEpisodeCharacter, accepted ...string) error {
	credits := make([]compactcatalog.Credit, 0, len(chars))
	for ordinal, c := range chars {
		department := "Guest"
		switch {
		case strings.EqualFold(c.Type, "Actor") || strings.EqualFold(c.Type, "Acting") || strings.EqualFold(c.Type, "Cast"):
			department = "Acting"
		case strings.EqualFold(c.Type, "Director") || strings.EqualFold(c.Type, "Directing"):
			department = "Directing"
		case strings.EqualFold(c.Type, "Writer") || strings.EqualFold(c.Type, "Writing"):
			department = "Writing"
		case strings.EqualFold(c.Type, "Creator") || strings.EqualFold(c.Type, "Production") || strings.EqualFold(c.Type, "Producer"):
			department = c.Type
		}
		role := c.Character
		if role == "" {
			role = c.Type
		}
		person := strconv.FormatInt(c.PersonID, 10)
		credits = append(credits, compactcatalog.Credit{
			PersonKey:        "tvdb:" + person,
			PersonName:       c.PersonName,
			ProviderPersonID: person,
			CreditID:         person + ":" + strconv.Itoa(ordinal),
			CreditedName:     c.PersonName,
			Role:             role,
			Department:       department,
			Ordinal:          ordinal,
		})
	}
	if len(accepted) == 2 {
		return compactcatalog.SetCreditsFromRecordTx(ctx, tx, item, "tvdb", accepted[0], accepted[1], credits)
	}
	if len(accepted) != 0 {
		return ErrRepairInput
	}
	return compactcatalog.SetCreditsTx(ctx, tx, item, "tvdb", credits)
}
