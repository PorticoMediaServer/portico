package reading

import (
	"context"
	"database/sql"
)

type ShortRead struct {
	Item                        LibraryRow
	WordCount, EstimatedSeconds int64
}

// Membership is decided over the entire authorized canonical-primary scope
// before LIMIT. No loaded-card sample, profile pace, page estimate or alternate
// shorter version participates. Every joined source witness is current here.
func (s *Service) discoverShortReadsTx(ctx context.Context, tx *sql.Tx, library, cursor string, limit int, visible string, visibleArgs []any) ([]ShortRead, error) {
	out := []ShortRead{}
	seek, after, err := addedSeek("coalesce(e.added_ms,-1)", cursor)
	if err != nil {
		return out, err
	}
	query := `SELECT e.id,coalesce(e.added_ms,-1),pid(e.public_id),cl.library_id,e.title,p.format,a.token,
 EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=e.id AND available.asset_id=a.token AND available.available=1),c.word_count
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id
 JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=` + s.primaryPublicationSQL() + `
 JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id JOIN catalog_assets a ON a.id=p.asset_id
 JOIN reading_text_counts c ON c.item_id=p.item_id AND c.asset_id=p.asset_id
 JOIN inventory_objects o ON o.id=c.inventory_object_id AND o.asset_id=a.token AND o.revision=c.source_revision AND o.retired=0
 JOIN library_sources source ON source.id=o.source_id AND source.library_id=cl.library_id AND source.incarnation=o.root_incarnation
 WHERE e.retired=0 AND cl.retired=0 AND e.kind=20 AND lib.kind='book' AND p.format IN('epub','pdf')
 AND cl.library_id=? AND c.rule_version=? AND c.word_count>0 AND c.word_count<45000 AND (` + visible + `)` + seek + `
 ORDER BY coalesce(e.added_ms,-1) DESC,e.id DESC LIMIT ?`
	args := append(append([]any{library, readingTextRuleVersion}, visibleArgs...), after...)
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var row ShortRead
		item := &row.Item
		if err = rows.Scan(&item.entity, &item.added, &item.ID, &item.LibraryID, &item.Title, &item.Format, &item.SourceID, &item.InventoryAvailable, &row.WordCount); err != nil {
			break
		}
		row.EstimatedSeconds = (row.WordCount*60 + 249) / 250
		out = append(out, row)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	items := make([]LibraryRow, len(out))
	for i := range out {
		items[i] = out[i].Item
	}
	if err = readingAttributionProjectionTx(ctx, tx, items); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Item = items[i]
	}
	return out, nil
}
