package reading

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// New issues means the newest actual catalogue additions among eligible
// magazines. Publication date never stands in for the time it was added.
func (s *Service) discoverNewIssuesTx(ctx context.Context, tx *sql.Tx, p identity.Principal, library, cursor string, limit int, visible string, visibleArgs []any) ([]LibraryRow, error) {
	out := []LibraryRow{}
	seek, after, err := addedSeek("e.added_ms", cursor)
	if err != nil {
		return out, err
	}
	if s.SeriesScope == nil {
		return out, identity.ErrForbidden
	}
	if _, err := s.SeriesScope(ctx, tx, p, library); err != nil {
		return out, err
	}
	var handle int64
	if err := tx.QueryRowContext(ctx, `SELECT cl.id FROM catalog_libraries cl JOIN libraries lib ON lib.id=cl.library_id WHERE cl.library_id=? AND cl.retired=0 AND lib.kind='book'`, library).Scan(&handle); err != nil {
		return out, err
	}
	generation, err := compactcatalog.ReadingProjectionReadyTx(ctx, tx, handle)
	if errors.Is(err, compactcatalog.ErrBuilding) {
		return out, ErrChanged
	}
	if err != nil {
		return out, err
	}
	query := `SELECT e.id,e.added_ms,pid(e.public_id),cl.library_id,e.title,p.format,a.token,
 EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=e.id AND available.asset_id=a.token AND available.available=1),m.issue_date
 FROM reading_attribution_members m JOIN catalog_entities e ON e.id=m.item_id AND e.library_id=m.library_id
 JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id
 JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=` + s.primaryPublicationSQL() + `
 JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id JOIN catalog_assets a ON a.id=p.asset_id
 WHERE m.library_id=? AND m.generation=? AND e.retired=0 AND cl.retired=0
 AND lib.kind='book' AND e.kind=20 AND p.format IN(` + s.bookFormatsSQL() + `)
 AND m.publication_type='magazine' AND m.series_id IS NOT NULL AND m.issue_date IS NOT NULL
 AND e.added_ms IS NOT NULL AND (` + visible + `)` + seek + `
 ORDER BY e.added_ms DESC,e.id DESC LIMIT ?`
	args := append(append([]any{handle, generation.Ready}, visibleArgs...), after...)
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var row LibraryRow
		if err = rows.Scan(&row.entity, &row.added, &row.ID, &row.LibraryID, &row.Title, &row.Format, &row.SourceID, &row.InventoryAvailable, &row.IssueDate); err != nil {
			break
		}
		if !compactcatalog.ValidReadingIssueDate(row.IssueDate) {
			err = ErrChanged
			break
		}
		out = append(out, row)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = readingAttributionProjectionTx(ctx, tx, out); err != nil {
		return nil, err
	}
	for _, item := range out {
		if item.PublicationType == nil || item.PublicationType.Value != "magazine" || item.Series == nil || len(item.Series.ID) != 43 || item.Series.Name == "" {
			return nil, ErrChanged
		}
	}
	return out, nil
}
