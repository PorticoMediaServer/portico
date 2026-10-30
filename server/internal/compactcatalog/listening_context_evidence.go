package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// StepBookContextEvidence advances at most limit file/asset or group/book
// pairs. Each job retains its cursor in the same maintenance transaction as
// its contribution deltas, and the turn clock prevents a huge shared asset
// from starving another pending group or file. Keys are integers: kind 1 is
// an audiobook file entity id, kind 2 a catalog_assets id, kind 3 a book
// group id.
func StepBookContextEvidence(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, fmt.Errorf("book context limit must be 1..500")
	}
	done := 0
	for done < limit {
		var kind int
		var key, phase, cursor int64
		err := tx.QueryRowContext(ctx, `SELECT kind,key,phase,cursor FROM catalog_book_evidence_jobs ORDER BY turn,kind,key LIMIT 1`).Scan(&kind, &key, &phase, &cursor)
		if errors.Is(err, sql.ErrNoRows) {
			return done, nil
		}
		if err != nil {
			return done, err
		}
		if kind == 3 {
			bookID, err := nextBookForGroup(ctx, tx, key, int(phase), cursor)
			if err != nil {
				return done, err
			}
			if bookID == 0 {
				if phase == 0 {
					if err = advanceBookEvidenceJob(ctx, tx, kind, key, 1, 0); err != nil {
						return done, err
					}
				} else if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_evidence_jobs WHERE kind=3 AND key=?`, key); err != nil {
					return done, err
				}
				// A phase change or finished job is a unit of work too, or a
				// backlog of empty jobs would drain in one transaction.
				done++
				continue
			}
			if err = refreshBookGroupMembers(ctx, tx, bookID); err != nil {
				return done, err
			}
			if err = advanceBookEvidenceJob(ctx, tx, kind, key, int(phase), bookID); err != nil {
				return done, err
			}
			done++
			continue
		}
		if kind != 1 && kind != 2 {
			return done, fmt.Errorf("unknown book evidence job kind %d", kind)
		}
		other, err := nextBookEvidencePair(ctx, tx, kind, int(phase), key, cursor)
		if err != nil {
			return done, err
		}
		if other == 0 {
			if phase == 0 {
				if err = advanceBookEvidenceJob(ctx, tx, kind, key, 1, 0); err != nil {
					return done, err
				}
			} else if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_evidence_jobs WHERE kind=? AND key=?`, kind, key); err != nil {
				return done, err
			}
			done++
			continue
		}
		file, asset := key, other
		if kind == 2 {
			file, asset = other, key
		}
		if err = projectBookEvidencePair(ctx, tx, file, asset); err != nil {
			return done, err
		}
		if err = advanceBookEvidenceJob(ctx, tx, kind, key, int(phase), other); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

func advanceBookEvidenceJob(ctx context.Context, tx *sql.Tx, kind int, key int64, phase int, cursor int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_book_evidence_clock SET turn=turn+1 WHERE id=1`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE catalog_book_evidence_jobs SET phase=?,cursor=?,turn=(SELECT turn FROM catalog_book_evidence_clock WHERE id=1) WHERE kind=? AND key=?`, phase, cursor, kind, key)
	return err
}

func nextBookForGroup(ctx context.Context, tx *sql.Tx, groupID int64, phase int, cursor int64) (int64, error) {
	if phase == 0 {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT book_id FROM catalog_book_group_members WHERE group_id=? AND book_id>? ORDER BY book_id LIMIT 1`, groupID, cursor).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return id, err
	}
	var kind int
	var library int64
	var key string
	err := tx.QueryRowContext(ctx, `SELECT kind,library_id,name_key FROM catalog_book_groups WHERE id=? AND retired=0`, groupID).Scan(&kind, &library, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	// No covering index exists for these context lookups in the baseline, so
	// no INDEXED BY hint names one; the predicates still bound the work to
	// one library and key.
	query := `SELECT book_id FROM catalog_book_context WHERE library_id=? AND author_key=? AND author_key!='' AND book_id>? ORDER BY book_id LIMIT 1`
	if kind == 2 {
		query = `SELECT book_id FROM catalog_book_context WHERE library_id=? AND series_key=? AND series_key!='' AND book_id>? ORDER BY book_id LIMIT 1`
	}
	var id int64
	err = tx.QueryRowContext(ctx, query, library, key, cursor).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func nextBookEvidencePair(ctx context.Context, tx *sql.Tx, kind, phase int, key, cursor int64) (int64, error) {
	if phase == 1 {
		var query string
		if kind == 1 {
			query = `SELECT l.asset_id FROM catalog_asset_links l INDEXED BY catalog_asset_links_order JOIN catalog_book_files f ON f.entity_id=l.entity_id WHERE l.entity_id=? AND l.asset_id>? ORDER BY l.asset_id LIMIT 1`
		} else {
			query = `SELECT l.entity_id FROM catalog_asset_links l INDEXED BY catalog_asset_links_asset JOIN catalog_book_files f ON f.entity_id=l.entity_id WHERE l.asset_id=? AND l.entity_id>? ORDER BY l.entity_id LIMIT 1`
		}
		var other int64
		err := tx.QueryRowContext(ctx, query, key, cursor).Scan(&other)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return other, err
	}
	// Phase 0 walks the recorded contributions: the next asset of a file, or
	// the next file of an asset. Each side seeks its own primary key or
	// asset index.
	query := `SELECT asset_id FROM (SELECT asset_id FROM catalog_book_series_contrib WHERE file_id=? AND asset_id>? UNION SELECT asset_id FROM catalog_book_position_contrib WHERE file_id=? AND asset_id>?) ORDER BY asset_id LIMIT 1`
	if kind == 2 {
		query = `SELECT file_id FROM (SELECT file_id FROM catalog_book_series_contrib WHERE asset_id=? AND file_id>? UNION SELECT file_id FROM catalog_book_position_contrib WHERE asset_id=? AND file_id>?) ORDER BY file_id LIMIT 1`
	}
	var other int64
	err := tx.QueryRowContext(ctx, query, key, cursor, key, cursor).Scan(&other)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return other, err
}

type seriesContribution struct {
	bookID               int64
	field, key, spelling string
}
type positionContribution struct {
	bookID   int64
	field    string
	position float64
}

func projectBookEvidencePair(ctx context.Context, tx *sql.Tx, fileID, assetID int64) error {
	affected := map[int64]bool{}
	seriesRows, err := tx.QueryContext(ctx, `SELECT book_id,field,norm_key,spelling FROM catalog_book_series_contrib WHERE file_id=? AND asset_id=?`, fileID, assetID)
	if err != nil {
		return err
	}
	oldSeries := []seriesContribution{}
	for seriesRows.Next() {
		var c seriesContribution
		if err = seriesRows.Scan(&c.bookID, &c.field, &c.key, &c.spelling); err != nil {
			break
		}
		oldSeries = append(oldSeries, c)
	}
	if err == nil {
		err = seriesRows.Err()
	}
	seriesRows.Close()
	if err != nil {
		return err
	}
	positionRows, err := tx.QueryContext(ctx, `SELECT book_id,field,position FROM catalog_book_position_contrib WHERE file_id=? AND asset_id=?`, fileID, assetID)
	if err != nil {
		return err
	}
	oldPositions := []positionContribution{}
	for positionRows.Next() {
		var c positionContribution
		if err = positionRows.Scan(&c.bookID, &c.field, &c.position); err != nil {
			break
		}
		oldPositions = append(oldPositions, c)
	}
	if err == nil {
		err = positionRows.Err()
	}
	positionRows.Close()
	if err != nil {
		return err
	}
	for _, c := range oldSeries {
		if err = removeSeriesTerm(ctx, tx, c); err != nil {
			return err
		}
		affected[c.bookID] = true
	}
	for _, c := range oldPositions {
		if err = removePositionTerm(ctx, tx, c); err != nil {
			return err
		}
		affected[c.bookID] = true
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_series_contrib WHERE file_id=? AND asset_id=?`, fileID, assetID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_position_contrib WHERE file_id=? AND asset_id=?`, fileID, assetID); err != nil {
		return err
	}
	var bookID int64
	var library string
	err = tx.QueryRowContext(ctx, `SELECT f.book_id,l.id FROM catalog_book_files f
	 JOIN catalog_asset_links al ON al.entity_id=f.entity_id AND al.asset_id=?
	 JOIN catalog_books b ON b.entity_id=f.book_id
	 JOIN catalog_entities e ON e.id=f.book_id
	 JOIN catalog_libraries cl ON cl.id=e.library_id
	 JOIN libraries l ON l.id=cl.library_id
	 WHERE f.entity_id=?`, assetID, fileID).Scan(&bookID, &library)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		// The head facts exist already; re-derive the book's head so the
		// context row is present before its effective series is refreshed.
		if err = drainBookContext(ctx, tx, bookID); err != nil {
			return err
		}
		var token string
		if err = tx.QueryRowContext(ctx, `SELECT token FROM catalog_assets WHERE id=?`, assetID).Scan(&token); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			seriesRows, e := tx.QueryContext(ctx, `SELECT field,lower(trim(value)),trim(value) FROM audio_tag_evidence WHERE library_id=? AND asset_id=? AND field IN('series','series_title','show') AND trim(value)<>'' ORDER BY field`, library, token)
			if e != nil {
				return e
			}
			series := []seriesContribution{}
			for seriesRows.Next() {
				c := seriesContribution{bookID: bookID}
				if e = seriesRows.Scan(&c.field, &c.key, &c.spelling); e != nil {
					break
				}
				series = append(series, c)
			}
			if e == nil {
				e = seriesRows.Err()
			}
			seriesRows.Close()
			if e != nil {
				return e
			}
			positionRows, e := tx.QueryContext(ctx, `SELECT field,CAST(value AS REAL) FROM audio_tag_evidence WHERE library_id=? AND asset_id=?
			 AND field IN('series_position','series_index','series-part','series_part')
			 AND trim(value)<>'' AND trim(value) NOT GLOB '*[^0-9.]*'
			 AND length(trim(value))-length(replace(trim(value),'.',''))<=1
			 AND CAST(value AS REAL)>0 AND CAST(value AS REAL)<999999 ORDER BY field`, library, token)
			if e != nil {
				return e
			}
			positions := []positionContribution{}
			for positionRows.Next() {
				c := positionContribution{bookID: bookID}
				if e = positionRows.Scan(&c.field, &c.position); e != nil {
					break
				}
				positions = append(positions, c)
			}
			if e == nil {
				e = positionRows.Err()
			}
			positionRows.Close()
			if e != nil {
				return e
			}
			for _, c := range series {
				if _, e = tx.ExecContext(ctx, `INSERT INTO catalog_book_series_contrib(file_id,asset_id,book_id,field,norm_key,spelling) VALUES(?,?,?,?,?,?)`, fileID, assetID, bookID, c.field, c.key, c.spelling); e != nil {
					return e
				}
				if e = addSeriesTerm(ctx, tx, c); e != nil {
					return e
				}
			}
			for _, c := range positions {
				if _, e = tx.ExecContext(ctx, `INSERT INTO catalog_book_position_contrib(file_id,asset_id,book_id,field,position) VALUES(?,?,?,?,?)`, fileID, assetID, bookID, c.field, c.position); e != nil {
					return e
				}
				if e = addPositionTerm(ctx, tx, c); e != nil {
					return e
				}
			}
			affected[bookID] = true
		}
	}
	for bookID := range affected {
		if err = refreshBookContextEffective(ctx, tx, bookID); err != nil {
			return err
		}
	}
	return nil
}

func addSeriesTerm(ctx context.Context, tx *sql.Tx, c seriesContribution) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_book_series_terms WHERE book_id=? AND norm_key=?)`, c.bookID, c.key).Scan(&exists); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_book_series_terms(book_id,norm_key,occurrences) VALUES(?,?,1) ON CONFLICT(book_id,norm_key) DO UPDATE SET occurrences=occurrences+1`, c.bookID, c.key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_book_series_spellings(book_id,norm_key,spelling,occurrences) VALUES(?,?,?,1) ON CONFLICT(book_id,norm_key,spelling) DO UPDATE SET occurrences=occurrences+1`, c.bookID, c.key, c.spelling); err != nil {
		return err
	}
	if !exists {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_book_context SET series_term_count=series_term_count+1 WHERE book_id=?`, c.bookID)
		return err
	}
	return nil
}

func removeSeriesTerm(ctx context.Context, tx *sql.Tx, c seriesContribution) error {
	var spellingCount, termCount int
	if err := tx.QueryRowContext(ctx, `SELECT occurrences FROM catalog_book_series_spellings WHERE book_id=? AND norm_key=? AND spelling=?`, c.bookID, c.key, c.spelling).Scan(&spellingCount); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT occurrences FROM catalog_book_series_terms WHERE book_id=? AND norm_key=?`, c.bookID, c.key).Scan(&termCount); err != nil {
		return err
	}
	var err error
	if spellingCount == 1 {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_series_spellings WHERE book_id=? AND norm_key=? AND spelling=?`, c.bookID, c.key, c.spelling)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_book_series_spellings SET occurrences=occurrences-1 WHERE book_id=? AND norm_key=? AND spelling=?`, c.bookID, c.key, c.spelling)
	}
	if err != nil {
		return err
	}
	if termCount == 1 {
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_series_terms WHERE book_id=? AND norm_key=?`, c.bookID, c.key); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_book_context SET series_term_count=series_term_count-1 WHERE book_id=?`, c.bookID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_book_series_terms SET occurrences=occurrences-1 WHERE book_id=? AND norm_key=?`, c.bookID, c.key)
	}
	return err
}

func addPositionTerm(ctx context.Context, tx *sql.Tx, c positionContribution) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_book_position_terms WHERE book_id=? AND position=?)`, c.bookID, c.position).Scan(&exists); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_book_position_terms(book_id,position,occurrences) VALUES(?,?,1) ON CONFLICT(book_id,position) DO UPDATE SET occurrences=occurrences+1`, c.bookID, c.position); err != nil {
		return err
	}
	if !exists {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_book_context SET position_term_count=position_term_count+1 WHERE book_id=?`, c.bookID)
		return err
	}
	return nil
}

func removePositionTerm(ctx context.Context, tx *sql.Tx, c positionContribution) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT occurrences FROM catalog_book_position_terms WHERE book_id=? AND position=?`, c.bookID, c.position).Scan(&count); err != nil {
		return err
	}
	var err error
	if count == 1 {
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_position_terms WHERE book_id=? AND position=?`, c.bookID, c.position); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_book_context SET position_term_count=position_term_count-1 WHERE book_id=?`, c.bookID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_book_position_terms SET occurrences=occurrences-1 WHERE book_id=? AND position=?`, c.bookID, c.position)
	}
	return err
}

func refreshBookContextEffective(ctx context.Context, tx *sql.Tx, bookID int64) error {
	var seriesCount, positionCount int
	if err := tx.QueryRowContext(ctx, `SELECT series_term_count,position_term_count FROM catalog_book_context WHERE book_id=?`, bookID).Scan(&seriesCount, &positionCount); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	seriesName, seriesKey := "", ""
	position := float64(0)
	if seriesCount == 1 {
		if err := tx.QueryRowContext(ctx, `SELECT norm_key FROM catalog_book_series_terms WHERE book_id=? LIMIT 1`, bookID).Scan(&seriesKey); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT spelling FROM catalog_book_series_spellings WHERE book_id=? AND norm_key=? ORDER BY spelling LIMIT 1`, bookID, seriesKey).Scan(&seriesName); err != nil {
			return err
		}
	}
	if positionCount == 1 {
		if err := tx.QueryRowContext(ctx, `SELECT position FROM catalog_book_position_terms WHERE book_id=? LIMIT 1`, bookID).Scan(&position); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE catalog_book_context SET series_name=?,series_key=?,series_index=?,has_file=EXISTS(SELECT 1 FROM catalog_book_files f WHERE f.book_id=?) WHERE book_id=?`, seriesName, seriesKey, position, bookID, bookID)
	if err != nil {
		return err
	}
	return refreshBookGroupMembers(ctx, tx, bookID)
}

// ReprojectBookFileTx re-reads one audiobook file's series and position
// evidence at once, after the file moved to another book: nothing derived
// still names the book it left.
func ReprojectBookFileTx(ctx context.Context, tx *sql.Tx, fileID int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT asset_id FROM catalog_asset_links WHERE entity_id=?1
 UNION SELECT asset_id FROM catalog_book_series_contrib WHERE file_id=?1
 UNION SELECT asset_id FROM catalog_book_position_contrib WHERE file_id=?1`, fileID)
	if err != nil {
		return err
	}
	var assets []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		assets = append(assets, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, asset := range assets {
		if err = projectBookEvidencePair(ctx, tx, fileID, asset); err != nil {
			return err
		}
	}
	return nil
}
