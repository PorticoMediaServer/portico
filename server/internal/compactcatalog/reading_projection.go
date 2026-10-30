package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
	"unicode/utf8"
)

// ReadingProjectionRuleVersion folds classifier changes and this membership
// policy. Bumping membership must not invalidate unchanged original package
// classification evidence; future classifier changes still invalidate readers
// and rebuilding automatically through the composite version.
const ReadingProjectionRuleVersion = ReadingPublicationTypeRuleVersion*1000 + 2

// ReadingGeneration identifies one complete effective-attribution projection.
// This metadata is not library/content authority. A consumer must authorize its
// explicit Reading library and retain this same snapshot for every read.
type ReadingGeneration struct {
	Namespace, Input, Ready int64
}

func ReadingProjectionReadyTx(ctx context.Context, tx *sql.Tx, library int64) (ReadingGeneration, error) {
	var out ReadingGeneration
	var rule int
	err := tx.QueryRowContext(ctx, `SELECT namespace,input_generation,ready_generation,publication_rule_version FROM reading_attribution_state WHERE library_id=?`, library).Scan(&out.Namespace, &out.Input, &out.Ready, &rule)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (out.Input != out.Ready || out.Ready == 0 || rule != ReadingProjectionRuleVersion) {
		return ReadingGeneration{}, ErrBuilding
	}
	return out, err
}

// StepReadingAttribution runs on the existing catalogue worker. One call visits
// at most min(limit,100) title inputs, including retired/no-publication/no-fact
// titles, or deletes that many obsolete members. Accepted-value resolution has
// the real per-title candidate/history cost; this is not a bound on fact rows.
// Scan and cleanup are separate commits. No ready member is maintained in place.
func StepReadingAttribution(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	limit = max(1, min(limit, 100))
	// Rule changes retire readiness before any reader can consume a stale type.
	// Invalidate one indexed library per step, then use the normal bounded rebuild.
	var stale int64
	ruleErr := tx.QueryRowContext(ctx, `SELECT library_id FROM reading_attribution_state WHERE publication_rule_version<? ORDER BY publication_rule_version,namespace LIMIT 1`, ReadingProjectionRuleVersion).Scan(&stale)
	if errors.Is(ruleErr, sql.ErrNoRows) {
		ruleErr = tx.QueryRowContext(ctx, `SELECT library_id FROM reading_attribution_state WHERE publication_rule_version>? ORDER BY publication_rule_version,namespace LIMIT 1`, ReadingProjectionRuleVersion).Scan(&stale)
	}
	if ruleErr == nil {
		_, err := tx.ExecContext(ctx, `UPDATE reading_attribution_state SET publication_rule_version=?,input_generation=input_generation+1,phase='scan' WHERE library_id=?`, ReadingProjectionRuleVersion, stale)
		return 1, err
	}
	if !errors.Is(ruleErr, sql.ErrNoRows) {
		return 0, ruleErr
	}
	var library, input, build, after int64
	var phase string
	// Seek the pending prefix and take its oldest served scope directly from
	// the index. Ready libraries never participate in the scheduler's scan.
	err := tx.QueryRowContext(ctx, `SELECT library_id,input_generation,build_generation,after_item,phase FROM reading_attribution_state WHERE (input_generation<>ready_generation)=1 ORDER BY served_ms,namespace LIMIT 1`).Scan(&library, &input, &build, &after, &phase)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE reading_attribution_state SET served_ms=? WHERE library_id=?`, time.Now().UnixMilli(), library); err != nil {
		return 0, err
	}
	if build != input {
		_, err = tx.ExecContext(ctx, `UPDATE reading_attribution_state SET build_generation=input_generation,after_item=0,phase='scan' WHERE library_id=?`, library)
		return 1, err
	}
	if phase == "cleanup" {
		result, err := tx.ExecContext(ctx, `DELETE FROM reading_attribution_members WHERE library_id=? AND (generation,item_id) IN(SELECT generation,item_id FROM reading_attribution_members WHERE library_id=? AND generation<>? ORDER BY generation,item_id LIMIT ?)`, library, library, input, limit)
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil || n > 0 {
			return int(n), err
		}
		// The scan reached true EOF in a previous commit, and this transaction
		// proves obsolete cleanup complete. Only the captured current build may
		// become ready. A source invalidation never updates ready itself.
		_, err = tx.ExecContext(ctx, `UPDATE reading_attribution_state SET ready_generation=?,phase='ready' WHERE library_id=? AND input_generation=? AND build_generation=? AND phase='cleanup' AND NOT EXISTS(SELECT 1 FROM reading_attribution_members WHERE library_id=? AND generation<>?)`, input, library, input, input, library, input)
		return 1, err
	}
	if phase != "scan" {
		return 0, ErrReadingValue
	}
	// Immutable membership belongs to the real title, not whichever version is
	// currently preferred or decodable on this server. Any linked original in the
	// explicit library/kind format set qualifies; readers apply runtime readiness
	// and choose their eligible primary BEFORE pagination in their own snapshot.
	rows, err := tx.QueryContext(ctx, `SELECT e.id,e.sort_key,cl.library_id,
 CASE WHEN e.retired=0 AND cl.retired=0 AND ((lib.kind='book' AND e.kind=20) OR (lib.kind='comic' AND e.kind=21)) AND EXISTS(
 SELECT 1 FROM reading_publications p JOIN catalog_asset_links link ON link.entity_id=p.item_id AND link.asset_id=p.asset_id
 WHERE p.item_id=e.id
 AND ((e.kind=20 AND p.format IN('epub','pdf','mobi','azw3')) OR (e.kind=21 AND p.format IN('cbz','pdf','cb7','cbr')))) THEN 1 ELSE 0 END
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id
 WHERE e.library_id=? AND e.id>? ORDER BY e.id LIMIT ?`, library, after, limit)
	if err != nil {
		return 0, err
	}
	type title struct {
		id                  int64
		sort, publicLibrary string
		eligible            bool
	}
	var titles []title
	var ids []int64
	for rows.Next() {
		var t title
		if err = rows.Scan(&t.id, &t.sort, &t.publicLibrary, &t.eligible); err != nil {
			rows.Close()
			return 0, err
		}
		titles = append(titles, t)
		if t.eligible {
			ids = append(ids, t.id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	values, err := ReadingAcceptedValuesTx(ctx, tx, ids)
	if err != nil {
		return 0, err
	}
	for _, title := range titles {
		after = title.id
		if !title.eligible {
			continue
		}
		if !utf8.ValidString(title.sort) {
			return 0, ErrReadingValue
		}
		v := values[title.id]
		var author, authorKey, authorSource, series, seriesKey, seriesID, seriesSource, publicationType, publicationTypeSource, issueDate, issueDateSource any
		if v.IssueDate != nil {
			issueDate, issueDateSource = v.IssueDate.Value, v.IssueDate.Source
		}
		if v.PublicationType != nil {
			publicationType, publicationTypeSource = v.PublicationType.Value, v.PublicationType.Source
		}
		if v.Author != nil {
			author, authorKey, authorSource = v.Author.Value, ReadingValueKey(v.Author.Value), v.Author.Source
		}
		if v.Series != nil {
			series, seriesKey, seriesID, seriesSource = v.Series.Value, ReadingValueKey(v.Series.Value), ReadingSeriesID(title.publicLibrary, v.Series.Value), v.Series.Source
		}
		numbered, number := 1, float64(0)
		if v.Series != nil && v.SeriesNumber != nil {
			numbered, number = 0, v.SeriesNumber.Value
			if math.IsInf(number, 0) || math.IsNaN(number) || number < 0 {
				return 0, ErrReadingValue
			}
			if number == 0 {
				number = 0 // Canonical positive zero, including a stored -0.
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO reading_attribution_members(library_id,generation,item_id,author_value,author_key,author_source,series_value,series_key,series_id,series_source,numbered_order,number_value,sort_key,publication_type,publication_type_source,issue_date,issue_date_source) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, library, input, title.id, author, authorKey, authorSource, series, seriesKey, seriesID, seriesSource, numbered, number, title.sort, publicationType, publicationTypeSource, issueDate, issueDateSource); err != nil {
			return 0, err
		}
	}
	if len(titles) == 0 {
		// An empty indexed continuation proves EOF. A full batch never does.
		_, err = tx.ExecContext(ctx, `UPDATE reading_attribution_state SET phase='cleanup' WHERE library_id=? AND input_generation=? AND build_generation=?`, library, input, input)
		return 1, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE reading_attribution_state SET after_item=? WHERE library_id=? AND input_generation=? AND build_generation=?`, after, library, input, input)
	return len(titles), err
}
