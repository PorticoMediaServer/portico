package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

var errTVDBEvidence = errors.New("TVDB provider evidence needs reacquisition")

type tvdbStagedCandidate struct {
	ID                int64
	Name              string
	Year              int
	Overview, Payload string
	Aliases           []string
}

func (s *Service) publishTVDBSearch(ctx context.Context, p tvdbPublication, candidates []metadataprovider.SeriesCandidate) error {
	if p.Phase != "search" || len(candidates) > 25 {
		return tvdbProblem("invalid_search")
	}
	staged := make([]tvdbStagedCandidate, 0, len(candidates))
	seen := map[int64]bool{}
	bytes := 0
	for _, candidate := range candidates {
		id, err := strconv.ParseInt(candidate.ID, 10, 64)
		if err != nil || id <= 0 || id > 9007199254740991 || seen[id] || candidate.Type != "series" || len(candidate.Name) > 2048 || len(candidate.Overview) > 65536 || len(candidate.Aliases) > 128 {
			return tvdbProblem("invalid_search")
		}
		seen[id] = true
		aliases := []string{}
		for _, alias := range candidate.Aliases {
			if len(alias) > 2048 {
				return tvdbProblem("invalid_search")
			}
			if alias != "" {
				aliases = append(aliases, alias)
			}
		}
		year, _ := strconv.Atoi(candidate.Year)
		raw, err := json.Marshal(candidate)
		if err != nil {
			return tvdbProblem("invalid_search")
		}
		bytes += len(raw)
		if len(raw) > 1<<20 || bytes > 16<<20 {
			return tvdbProblem("evidence_capacity")
		}
		staged = append(staged, tvdbStagedCandidate{id, candidate.Name, year, candidate.Overview, string(raw), aliases})
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if err = s.activeTVDBPublication(ctx, tx, p); err != nil {
		return err
	}
	for ordinal, v := range staged {
		if _, err = tx.ExecContext(ctx, `INSERT INTO tvdb_publication_candidates VALUES(?,?,?,?,?,?,?)`, p.ID, ordinal, v.ID, v.Name, v.Year, v.Overview, v.Payload); err != nil {
			return err
		}
		for i, alias := range v.Aliases {
			if _, err = tx.ExecContext(ctx, `INSERT INTO tvdb_publication_candidate_aliases VALUES(?,?,?,?)`, p.ID, ordinal, i, alias); err != nil {
				return err
			}
		}
	}
	// Compute the receipt from the normalized rows actually staged in this transaction.
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,provider_id,name,year,overview,payload FROM tvdb_publication_candidates WHERE operation_id=? ORDER BY ordinal LIMIT 26`, p.ID)
	if err != nil {
		return err
	}
	actual := []tvdbStagedCandidate{}
	for rows.Next() {
		var v tvdbStagedCandidate
		var ordinal int
		if err = rows.Scan(&ordinal, &v.ID, &v.Name, &v.Year, &v.Overview, &v.Payload); err != nil {
			rows.Close()
			return err
		}
		if ordinal != len(actual) {
			rows.Close()
			return tvdbProblem("invalid_search")
		}
		actual = append(actual, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(actual) != len(staged) {
		return tvdbProblem("invalid_search")
	}
	for ordinal := range actual {
		actual[ordinal].Aliases = []string{}
		aliases, err := tx.QueryContext(ctx, `SELECT ordinal,alias FROM tvdb_publication_candidate_aliases WHERE operation_id=? AND candidate_ordinal=? ORDER BY ordinal LIMIT 129`, p.ID, ordinal)
		if err != nil {
			return err
		}
		for aliases.Next() {
			var i int
			var alias string
			if err = aliases.Scan(&i, &alias); err != nil {
				aliases.Close()
				return err
			}
			if i != len(actual[ordinal].Aliases) {
				aliases.Close()
				return tvdbProblem("invalid_search")
			}
			actual[ordinal].Aliases = append(actual[ordinal].Aliases, alias)
		}
		err = aliases.Err()
		aliases.Close()
		if err != nil {
			return err
		}
	}
	if tvdbDigest(actual) != tvdbDigest(staged) {
		return tvdbProblem("invalid_search")
	}
	matched := int64(0)
	matches := 0
	for _, v := range actual {
		exact := strings.EqualFold(strings.TrimSpace(v.Name), strings.TrimSpace(p.Title))
		for _, alias := range v.Aliases {
			exact = exact || strings.EqualFold(strings.TrimSpace(alias), strings.TrimSpace(p.Title))
		}
		if exact && p.Year > 0 && v.Year == p.Year {
			matched = v.ID
			matches++
		}
	}
	status, providerStatus := "needs_selection", "unresolved"
	if matches == 1 {
		status = "needs_order"
		providerStatus = "matched"
	} else {
		matched = 0
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM tvdb_series_candidates WHERE show_id=?`, p.ShowID); err != nil {
		return err
	}
	for _, v := range actual {
		if _, err = tx.ExecContext(ctx, `INSERT INTO tvdb_series_candidates VALUES(?,?,?,?,?,?,?)`, p.ShowID, v.ID, v.Name, v.Year, v.Overview, v.Payload, tvdbStamp(s.publicationTime())); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tvdb_publication_operations SET result_digest=? WHERE id=?`, tvdbDigest(actual), p.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET status=?,provider_id=?,attempts=0,error='',next_attempt='',retry_status='',revision=revision+1 WHERE show_id=?`, status, matched, p.ShowID); err != nil {
		return err
	}
	if err = compactcatalog.SetFieldsTx(ctx, tx, p.ShowID, compactcatalog.Automatic, map[string]any{"provider_match_status": providerStatus}); err != nil {
		return err
	}
	if err = s.finishTVDBPublication(ctx, tx, p, "applied", ""); err != nil {
		return err
	}
	return gated.Commit()
}

type tvdbEvidence struct {
	Provider                 int64
	Season, Number, Absolute sql.NullInt64
	Title, Overview, Payload string
}
type tvdbPageReceipt struct {
	Page               int
	Next               sql.NullInt64
	Rows, Bytes        int
	Digest             string
	Content, Validated int64
}

func tvdbNullable(n *int) sql.NullInt64 {
	if n == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*n), Valid: true}
}
func (s *Service) readTVDBPageEvidence(ctx context.Context, tx *sql.Tx, p tvdbPublication, page int) ([]tvdbEvidence, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.provider_id,e.season_number,e.number,e.absolute_number,e.title,e.overview,e.payload FROM tvdb_evidence_page_ownership o JOIN tvdb_episode_evidence e ON e.show_id=o.show_id AND e.generation=o.generation AND e.provider_id=o.provider_id WHERE o.show_id=? AND o.generation=? AND o.set_incarnation=? AND o.page=? ORDER BY e.provider_id LIMIT 1001`, p.ShowID, p.Generation, p.Set, page)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	values := []tvdbEvidence{}
	bytes := 0
	for rows.Next() {
		var v tvdbEvidence
		if err = rows.Scan(&v.Provider, &v.Season, &v.Number, &v.Absolute, &v.Title, &v.Overview, &v.Payload); err != nil {
			return nil, 0, err
		}
		bytes += len(v.Payload)
		if v.Provider <= 0 || v.Provider > 9007199254740991 || len(v.Title) > 2048 || len(v.Overview) > 65536 || len(v.Payload) > 1<<20 || bytes > 16<<20 || len(values) >= 1000 {
			return nil, 0, errTVDBEvidence
		}
		values = append(values, v)
	}
	return values, bytes, rows.Err()
}

// prefix is the exact number of pages expected. Final completeness additionally
// requires one terminal descriptor; a bare set.complete flag is never authority.
func (s *Service) attestTVDBSet(ctx context.Context, tx *sql.Tx, p tvdbPublication, prefix int, terminal bool) ([]tvdbPageReceipt, error) {
	if prefix < 0 || prefix > 100 {
		return nil, errTVDBEvidence
	}
	rows, err := tx.QueryContext(ctx, `SELECT page,next_page,row_count,byte_count,result_digest,content_revision,validated_revision FROM tvdb_publication_pages WHERE show_id=? AND set_incarnation=? ORDER BY page LIMIT 101`, p.ShowID, p.Set)
	if err != nil {
		return nil, err
	}
	pages := []tvdbPageReceipt{}
	for rows.Next() {
		var v tvdbPageReceipt
		if err = rows.Scan(&v.Page, &v.Next, &v.Rows, &v.Bytes, &v.Digest, &v.Content, &v.Validated); err != nil {
			rows.Close()
			return nil, err
		}
		pages = append(pages, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(pages) != prefix || (terminal && prefix == 0) {
		return nil, errTVDBEvidence
	}
	count, bytes := 0, 0
	for i, v := range pages {
		if v.Page != i || v.Content != v.Validated || v.Validated == 0 {
			return nil, errTVDBEvidence
		}
		if terminal && i == len(pages)-1 {
			if v.Next.Valid {
				return nil, errTVDBEvidence
			}
		} else if !v.Next.Valid || v.Next.Int64 != int64(i+1) {
			return nil, errTVDBEvidence
		}
		evidence, size, err := s.readTVDBPageEvidence(ctx, tx, p, i)
		if err != nil {
			return nil, err
		}
		count += len(evidence)
		bytes += size
		if count > 20000 || bytes > 16<<20 || v.Rows != len(evidence) || v.Bytes != size || v.Digest != tvdbDigest(evidence) {
			return nil, errTVDBEvidence
		}
	}
	return pages, nil
}
func (s *Service) commitTVDBPage(ctx context.Context, p tvdbPublication, page metadataprovider.EpisodePage) error {
	if p.Phase != "page" || page.SeriesID != p.Provider || string(page.Order) != p.Order || page.Page != p.Page || p.Page < 0 || p.Page >= 100 || len(page.Episodes) > 1000 {
		return tvdbProblem("invalid_episodes")
	}
	if page.NextPage != nil && (*page.NextPage != p.Page+1 || *page.NextPage >= 100) {
		return tvdbProblem("invalid_pagination")
	}
	staged := make([]tvdbEvidence, 0, len(page.Episodes))
	seen := map[int64]bool{}
	bytes := 0
	for _, episode := range page.Episodes {
		if episode.ID <= 0 || episode.ID > 9007199254740991 || episode.SeriesID != p.Provider || seen[episode.ID] || len(episode.Name) > 2048 || len(episode.Overview) > 65536 {
			return tvdbProblem("invalid_episodes")
		}
		seen[episode.ID] = true
		for _, n := range []*int{episode.SeasonNumber, episode.Number, episode.AbsoluteNumber} {
			if n != nil && (*n < 0 || int64(*n) > 9007199254740991) {
				return tvdbProblem("invalid_episodes")
			}
		}
		raw, err := json.Marshal(episode)
		if err != nil {
			return tvdbProblem("invalid_episodes")
		}
		bytes += len(raw)
		if len(raw) > 1<<20 || bytes > 16<<20 {
			return tvdbProblem("evidence_capacity")
		}
		staged = append(staged, tvdbEvidence{episode.ID, tvdbNullable(episode.SeasonNumber), tvdbNullable(episode.Number), tvdbNullable(episode.AbsoluteNumber), episode.Name, episode.Overview, string(raw)})
	}
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if err = s.activeTVDBPublication(ctx, tx, p); err != nil {
		return err
	}
	prior, err := s.attestTVDBSet(ctx, tx, p, p.Page, false)
	if err != nil {
		return err
	}
	totalRows, totalBytes := len(staged), bytes
	for _, v := range prior {
		totalRows += v.Rows
		totalBytes += v.Bytes
	}
	if totalRows > 20000 || totalBytes > 16<<20 {
		return tvdbProblem("evidence_capacity")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO tvdb_publication_pages(show_id,set_incarnation,page,next_page) VALUES(?,?,?,?)`, p.ShowID, p.Set, p.Page, page.NextPage); err != nil {
		return err
	}
	for _, v := range staged {
		var duplicate int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM tvdb_episode_evidence WHERE show_id=? AND generation=? AND provider_id=?`, p.ShowID, p.Generation, v.Provider).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate != 0 {
			return tvdbProblem("invalid_episodes")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO tvdb_episode_evidence VALUES(?,?,?,?,?,?,?,?,?,?)`, p.ShowID, p.Generation, v.Provider, v.Season, v.Number, v.Absolute, v.Title, v.Overview, v.Payload, tvdbStamp(s.publicationTime())); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO tvdb_evidence_page_ownership VALUES(?,?,?,?,?)`, p.ShowID, p.Generation, v.Provider, p.Set, p.Page); err != nil {
			return err
		}
	}
	actual, size, err := s.readTVDBPageEvidence(ctx, tx, p, p.Page)
	if err != nil {
		return err
	}
	// Compare canonical ordering regardless of provider response order.
	byID := map[int64]tvdbEvidence{}
	for _, v := range staged {
		byID[v.Provider] = v
	}
	if len(actual) != len(staged) || size != bytes {
		return errTVDBEvidence
	}
	for _, v := range actual {
		if tvdbDigest(v) != tvdbDigest(byID[v.Provider]) {
			return errTVDBEvidence
		}
	}
	digest := tvdbDigest(actual)
	if _, err = tx.ExecContext(ctx, `UPDATE tvdb_publication_pages SET row_count=?,byte_count=?,result_digest=? WHERE show_id=? AND set_incarnation=? AND page=?`, len(actual), size, digest, p.ShowID, p.Set, p.Page); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tvdb_publication_pages SET validated_revision=content_revision WHERE show_id=? AND set_incarnation=? AND page=?`, p.ShowID, p.Set, p.Page); err != nil {
		return err
	}
	terminal := page.NextPage == nil
	if _, err = s.attestTVDBSet(ctx, tx, p, p.Page+1, terminal); err != nil {
		return err
	}
	status, next := "pending_apply", p.Page
	if !terminal {
		status = "pending_episodes"
		next = *page.NextPage
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tvdb_publication_sets SET complete=? WHERE show_id=? AND incarnation=?`, terminal, p.ShowID, p.Set); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tvdb_publication_operations SET result_digest=? WHERE id=?`, digest, p.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tvdb_jobs SET status=?,page=?,staged_bytes=?,attempts=0,error='',next_attempt='',retry_status='',revision=revision+1 WHERE show_id=?`, status, next, totalBytes, p.ShowID); err != nil {
		return err
	}
	if err = s.finishTVDBPublication(ctx, tx, p, "applied", ""); err != nil {
		return err
	}
	return gated2.Commit()
}
