package metadata

import (
	"context"
	"database/sql"
	"math"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func readingRepairKind(kind string) bool { return kind == "ebook" || kind == "comic" }
func readingAttributionField(field string) bool {
	return field == "author" || field == "series" || field == "seriesNumber" || field == "publicationType"
}

func readingAttributionSpec(field, label string) RepairFieldSpec {
	spec := textSpec(field, label, "general", true)
	if field != "seriesNumber" {
		spec.MaxLength = 4096
	}
	return spec
}

// readReadingOwnerFields returns the effective scalar owner values for Reading
// attribution together with the genuine automatic values. The canonical source
// is the whole automatic value: a field it omits is genuinely absent, so a
// recorded automatic column never stands in for it. Locked owner values then
// overlay that canonical base, whether or not an owner row exists at all.
func readReadingOwnerFields(ctx context.Context, tx *sql.Tx, entity int64) (map[string]string, map[string]string, error) {
	automatic, err := readingAutomaticOwnerValues(ctx, tx, entity)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]string{"author": "", "series": "", "seriesNumber": "", "publicationType": ""}
	for field, value := range automatic {
		out[field] = value
	}
	rows, err := tx.QueryContext(ctx, `SELECT field,value,locked FROM metadata_owner_fields WHERE kind='item' AND entity_id=? AND field IN('author','series','seriesNumber','publicationType')`, entity)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var field, value string
		var locked bool
		if err = rows.Scan(&field, &value, &locked); err != nil {
			return nil, nil, err
		}
		if locked && strings.TrimSpace(value) != "" {
			if field == "publicationType" && !compactcatalog.ValidReadingPublicationType(value) {
				return nil, nil, ErrRepairInput
			}
			out[field] = value
		}
	}
	return out, automatic, rows.Err()
}

// readingAutomaticOwnerValues reads the genuine automatic Reading attribution
// in the caller's transaction. Only fields a current source value carries are
// reported; an omitted field is genuine absence.
func readingAutomaticOwnerValues(ctx context.Context, tx *sql.Tx, entity int64) (map[string]string, error) {
	values, err := compactcatalog.ReadingAutomaticFieldsTx(ctx, tx, entity)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if values.PublicationType != nil {
		out["publicationType"] = values.PublicationType.Value
	}
	if values.Author != nil {
		out["author"] = values.Author.Value
	}
	if values.Series != nil {
		out["series"] = values.Series.Value
	}
	if values.SeriesNumber != nil {
		out["seriesNumber"] = strconv.FormatFloat(values.SeriesNumber.Value, 'g', -1, 64)
	}
	return out, nil
}

func readingAttributionEdit(field string, current RepairField, edit RepairFieldEdit) (RepairFieldEdit, error) {
	if edit.Values != nil {
		return edit, ErrRepairInput
	}
	clear := edit.Automatic || edit.Locked != nil && !*edit.Locked || edit.Value != nil && strings.TrimSpace(*edit.Value) == ""
	if clear {
		value, locked := current.Automatic, false
		return RepairFieldEdit{Value: &value, Locked: &locked}, nil
	}
	if field == "publicationType" && edit.Value != nil && !compactcatalog.ValidReadingPublicationType(*edit.Value) {
		return edit, ErrRepairInput
	}
	if field == "seriesNumber" && edit.Value != nil {
		value := strings.TrimSpace(*edit.Value)
		n, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || strings.ContainsAny(value, "xXpP") {
			return edit, ErrRepairInput
		}
		edit.Value = &value
	}
	return edit, nil
}

func readingAttributionChanged(ctx context.Context, tx *sql.Tx, entity int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE library_revisions SET revision=revision+1 WHERE library_id=(SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=?)`, entity)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return sql.ErrNoRows
	}
	return err
}

// Preserve edit accounting even when the effective owner value is unchanged,
// without attempting an INSERT or naming input columns in the accounting write.
func storeReadingRepairFieldTx(ctx context.Context, tx *sql.Tx, entity int64, field, value, automatic string, locked bool, actor, now string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_owner_fields WHERE kind='item' AND entity_id=? AND field=?)`, entity, field).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		_, err := tx.ExecContext(ctx, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,?,?,?,?,?,?)`, entity, field, value, automatic, locked, actor, now)
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE metadata_owner_fields SET automatic_value=?,actor=?,observed_at=? WHERE kind='item' AND entity_id=? AND field=?`, automatic, actor, now, entity, field); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE metadata_owner_fields SET value=?,locked=? WHERE kind='item' AND entity_id=? AND field=? AND (value IS NOT ? OR locked IS NOT ?)`, value, locked, entity, field, value, locked)
	return err
}
