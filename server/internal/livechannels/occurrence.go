package livechannels

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Programme IDs survive a guide generation and future time correction. A reused
// provider ID after its previous airing, or contradictory episode evidence, is a
// new occurrence. Titles are never used to identify or reconcile recordings.
func programmeIdentityTx(ctx context.Context, tx *sql.Tx, source, generation string, p parsedProgramme) (string, error) {
	var existing string
	err := tx.QueryRowContext(ctx, `SELECT id FROM live_programmes WHERE generation_id=? AND provider_key=?`, generation, p.key).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnavailable
	}
	if p.lineage != "provider-id" {
		return id(source, p.key), nil
	}
	var priorID, priorEnd, episode string
	err = tx.QueryRowContext(ctx, `SELECT p.id,p.end_utc,COALESCE(m.episode_id,'') FROM live_programmes p JOIN live_generations g ON g.id=p.generation_id JOIN live_operations o ON o.generation_id=g.id LEFT JOIN live_programme_metadata m ON m.generation_id=p.generation_id AND m.id=p.id WHERE g.source_id=? AND p.provider_key=? AND o.status='published' ORDER BY (g.id=(SELECT active_generation FROM live_sources WHERE id=?)) DESC,o.published_at DESC,g.id DESC LIMIT 1`, source, p.key, source).Scan(&priorID, &priorEnd, &episode)
	if errors.Is(err, sql.ErrNoRows) {
		return id(source, p.key), nil
	}
	if err != nil {
		return "", ErrUnavailable
	}
	end, e := time.Parse(time.RFC3339, priorEnd)
	if e != nil {
		return "", ErrUnavailable
	}
	// Only the provider's explicit identity establishes continuity. A past airing
	// cannot be rewritten into a new future one, even when the title is identical.
	reused := (!end.After(time.Now()) && p.start.After(end)) || (episode != "" && p.episode != "" && episode != p.episode)
	if reused {
		return id(source, p.key, p.start.Format(time.RFC3339), p.episode), nil
	}
	return priorID, nil
}

func (s *Store) ProgrammeTx(ctx context.Context, tx *sql.Tx, source, channel, generation, programme string) (Programme, error) {
	p := Programme{ID: programme, ChannelID: channel}
	var facts string
	e := tx.QueryRowContext(ctx, `SELECT p.title,p.start_utc,p.end_utc,p.lineage,COALESCE(m.series_id,''),COALESCE(m.episode_id,''),COALESCE(m.new_evidence,'unknown'),COALESCE(m.description,''),COALESCE(m.facts,'{}') FROM live_sources s JOIN live_programmes p ON p.generation_id=s.active_generation LEFT JOIN live_programme_metadata m ON m.generation_id=p.generation_id AND m.id=p.id WHERE s.id=? AND s.state='active' AND s.active_generation=? AND p.channel_id=? AND p.id=?`, source, generation, channel, programme).Scan(&p.Title, &p.Start, &p.End, &p.Lineage, &p.SeriesID, &p.EpisodeID, &p.NewEvidence, &p.Description, &facts)
	if errors.Is(e, sql.ErrNoRows) {
		return p, ErrConflict
	}
	if e != nil {
		return p, ErrUnavailable
	}
	p.ApplyFacts(facts)
	return p, nil
}
