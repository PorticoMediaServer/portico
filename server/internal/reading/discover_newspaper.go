package reading

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// One canonical archive slot, never an invented "on this day" recommendation.
func (s *Service) discoverNewspaperTx(ctx context.Context, tx *sql.Tx, p identity.Principal, library, visible string, visibleArgs []any) (*PeriodicalGroup, error) {
	if s.SeriesScope == nil {
		return nil, identity.ErrForbidden
	}
	if _, err := s.SeriesScope(ctx, tx, p, library); err != nil {
		return nil, err
	}
	var handle int64
	if err := tx.QueryRowContext(ctx, `SELECT cl.id FROM catalog_libraries cl JOIN libraries lib ON lib.id=cl.library_id WHERE cl.library_id=? AND cl.retired=0 AND lib.kind='book'`, library).Scan(&handle); err != nil {
		return nil, err
	}
	generation, err := compactcatalog.ReadingProjectionReadyTx(ctx, tx, handle)
	if errors.Is(err, compactcatalog.ErrBuilding) {
		return nil, ErrChanged
	}
	if err != nil {
		return nil, err
	}
	groups, _, err := s.periodicalGroupsTx(ctx, tx, handle, generation.Ready, "newspaper", visible, visibleArgs, nil, 1)
	if err != nil || len(groups) == 0 {
		return nil, err
	}
	group := groups[0]
	item := &group.Latest
	if len(group.ID) != 43 || group.Name == "" || group.IssueCount < 1 || group.IssueCount > 9007199254740991 || len(item.IssueDate) != 10 || !compactcatalog.ValidReadingIssueDate(item.IssueDate) {
		return nil, ErrChanged
	}
	items := []LibraryRow{*item}
	if err = readingAttributionProjectionTx(ctx, tx, items); err != nil {
		return nil, err
	}
	group.Latest = items[0]
	if group.Latest.PublicationType == nil || group.Latest.PublicationType.Value != "newspaper" || group.Latest.Series == nil || group.Latest.Series.ID != group.ID {
		return nil, ErrChanged
	}
	return &group, nil
}
