package compactcatalog

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"unicode/utf8"
)

// ReadingAutomaticFieldsTx is the editor's read-only automatic-value seam.
// It never adopts evidence or opens a source. Callers retain their actual
// same-transaction item/library authorization. No facts means genuine absence.
func ReadingAutomaticFieldsTx(ctx context.Context, tx *sql.Tx, item int64) (ReadingValues, error) {
	values, err := readingAutomaticValuesTx(ctx, tx, []int64{item})
	return values[item], err
}

func readingAutomaticValuesTx(ctx context.Context, tx *sql.Tx, items []int64) (map[int64]ReadingValues, error) {
	out := make(map[int64]ReadingValues, len(items))
	if len(items) == 0 {
		return out, nil
	}
	if len(items) > 100 {
		return nil, ErrReadingValue
	}
	args := make([]any, 0, len(items))
	marks := make([]string, 0, len(items))
	for _, id := range items {
		if id < 1 {
			return nil, ErrReadingValue
		}
		if _, exists := out[id]; exists {
			return nil, ErrReadingValue
		}
		out[id] = ReadingValues{}
		args = append(args, id)
		marks = append(marks, "?")
	}
	primaries, err := readingPrimaryPublicationsTx(ctx, tx, strings.Join(marks, ","), args)
	if err != nil {
		return nil, err
	}
	if err = readingPathAutomaticTx(ctx, tx, primaries, out); err != nil {
		return nil, err
	}
	chosen, chosenArgs := readingPrimaryBindings(primaries)
	// Attribution belongs to a title, independently of which version a reader
	// opens. Use the same deterministic primary-version rule as Reading List:
	// available inventory first, then asset id. Alternate versions are retained;
	// this baseline fact is descriptive, never an eligibility or bytes grant.
	query := `WITH chosen(item_id,asset_id) AS (` + chosen + `) SELECT e.id,v.field,v.text_value,v.number_value,v.source
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id
 JOIN libraries lib ON lib.id=cl.library_id
 JOIN chosen c ON c.item_id=e.id JOIN reading_publications p ON p.item_id=c.item_id AND p.asset_id=c.asset_id
 JOIN catalog_asset_links link ON link.entity_id=p.item_id AND link.asset_id=p.asset_id
 JOIN catalog_assets a ON a.id=p.asset_id
 JOIN reading_source_values v ON v.item_id=p.item_id AND v.asset_id=p.asset_id AND v.source='file-metadata'
 JOIN inventory_objects o ON o.id=v.inventory_object_id AND o.asset_id=a.token AND o.revision=v.source_revision AND o.retired=0
 JOIN library_sources s ON s.id=o.source_id AND s.library_id=cl.library_id AND s.incarnation=o.root_incarnation
 WHERE e.id IN (` + strings.Join(marks, ",") + `) AND e.retired=0 AND cl.retired=0
 AND ((e.kind=20 AND lib.kind='book' AND p.format IN('epub','pdf','mobi','azw3')) OR (e.kind=21 AND lib.kind='comic' AND p.format IN('cbz','pdf','cb7','cbr')))
 ORDER BY e.id,v.field,CASE v.source WHEN 'file-metadata' THEN 0 WHEN 'file-name' THEN 1 ELSE 2 END`
	rows, err := tx.QueryContext(ctx, query, append(append([]any{}, chosenArgs...), args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[int64]map[string]bool{}
	for rows.Next() {
		var id int64
		var field string
		var text sql.NullString
		var number sql.NullFloat64
		var source ReadingValueSource
		if err = rows.Scan(&id, &field, &text, &number, &source); err != nil {
			return nil, err
		}
		if seen[id] == nil {
			seen[id] = map[string]bool{}
		}
		if seen[id][field] {
			continue
		}
		seen[id][field] = true
		if source != ReadingFileMetadata && source != ReadingFileName && source != ReadingFolder {
			return nil, ErrReadingValue
		}
		v := out[id]
		switch field {
		case "author", "series":
			if !text.Valid || number.Valid || strings.TrimSpace(text.String) == "" || len(text.String) > 4096 || !utf8.ValidString(text.String) || strings.ContainsAny(text.String, "\x00\r\n") {
				return nil, ErrReadingValue
			}
			value := &ReadingValue{Value: text.String, Source: source}
			if field == "author" {
				v.Author = value
			} else {
				v.Series = value
			}
		case "issueDate":
			if !text.Valid || number.Valid || source != ReadingFileName || !ValidReadingIssueDate(text.String) {
				return nil, ErrReadingValue
			}
			v.IssueDate = &ReadingValue{Value: text.String, Source: source}
		case "seriesNumber":
			if text.Valid || !number.Valid || math.IsInf(number.Float64, 0) || math.IsNaN(number.Float64) || number.Float64 < 0 {
				return nil, ErrReadingValue
			}
			v.SeriesNumber = &ReadingNumber{Value: number.Float64, Source: source}
		default:
			return nil, ErrReadingValue
		}
		out[id] = v
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	// Only a current explicit file fact on the canonical primary version supplies a type.
	typeRows, err := tx.QueryContext(ctx, `SELECT e.id,v.publication_type FROM catalog_entities e
      JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id
      JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=(SELECT primary_version.asset_id FROM reading_publications primary_version WHERE primary_version.item_id=e.id ORDER BY EXISTS(SELECT 1 FROM inventory_item_availability eligible WHERE eligible.item_id=primary_version.item_id AND eligible.asset_id=(SELECT token FROM catalog_assets WHERE id=primary_version.asset_id) AND eligible.available=1) DESC,primary_version.asset_id LIMIT 1)
      JOIN catalog_asset_links link ON link.entity_id=p.item_id AND link.asset_id=p.asset_id JOIN catalog_assets a ON a.id=p.asset_id
      JOIN reading_publication_types v ON v.item_id=p.item_id AND v.asset_id=p.asset_id
      JOIN inventory_objects o ON o.id=v.inventory_object_id AND o.asset_id=a.token AND o.revision=v.source_revision AND o.retired=0
      JOIN library_sources s ON s.id=o.source_id AND s.library_id=cl.library_id AND s.incarnation=o.root_incarnation
      WHERE e.id IN (`+strings.Join(marks, ",")+`) AND e.retired=0 AND cl.retired=0
      AND ((e.kind=20 AND lib.kind='book' AND p.format='epub') OR (e.kind=21 AND lib.kind='comic' AND p.format='cbz'))
      AND v.source_format=p.format
      AND v.publication_type IS NOT NULL AND v.evidence_complete=1 AND v.classifier_version=?`, append(append([]any(nil), args...), ReadingPublicationTypeRuleVersion)...)
	if err != nil {
		return nil, err
	}
	defer typeRows.Close()
	for typeRows.Next() {
		var item int64
		var value string
		if err = typeRows.Scan(&item, &value); err != nil {
			return nil, err
		}
		if !ValidReadingPublicationType(value) {
			return nil, ErrReadingValue
		}
		v := out[item]
		v.PublicationType = &ReadingValue{Value: value, Source: ReadingFileMetadata}
		out[item] = v
	}
	return out, typeRows.Err()
}

// ReadingAcceptedValuesTx composes actual stored candidates and exact owner
// rows for a bounded, already-authorized page. It creates no adoption rows.
func ReadingAcceptedValuesTx(ctx context.Context, tx *sql.Tx, items []int64) (map[int64]ReadingValues, error) {
	automatic, err := readingAutomaticValuesTx(ctx, tx, items)
	if err != nil {
		return nil, err
	}
	owners, err := ReadingOwnerValuesTx(ctx, tx, items)
	if err != nil {
		return nil, err
	}
	for id, owner := range owners {
		v := automatic[id]
		if owner.PublicationType != nil {
			v.PublicationType = owner.PublicationType
		}
		if owner.Author != nil {
			v.Author = owner.Author
		}
		if owner.Series != nil {
			v.Series = owner.Series
		}
		if owner.SeriesNumber != nil {
			v.SeriesNumber = owner.SeriesNumber
		}
		automatic[id] = v
	}
	return automatic, nil
}
