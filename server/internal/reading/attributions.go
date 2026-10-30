package reading

import (
	"context"
	"database/sql"
	"net/url"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Metadata-only projection for rows already selected by the real library/title
// predicate in this snapshot. One bounded query, never per-item Prepare or I/O.
func readingAttributionProjectionTx(ctx context.Context, tx *sql.Tx, items []LibraryRow) error {
	ids := make([]int64, len(items))
	for i := range items {
		ids[i] = items[i].entity
	}
	values, err := compactcatalog.ReadingAcceptedValuesTx(ctx, tx, ids)
	if err != nil {
		return err
	}
	for i := range items {
		v := values[items[i].entity]
		items[i].Author = v.Author
		items[i].PublicationType = v.PublicationType
		items[i].Series = nil
		if v.Series != nil {
			s := &SeriesValue{ID: compactcatalog.ReadingSeriesID(items[i].LibraryID, v.Series.Value), Name: v.Series.Value, Source: v.Series.Source}
			if v.SeriesNumber != nil {
				n := v.SeriesNumber.Value
				s.Number = &n
			}
			items[i].Series = s
		}
	}
	return readingArtworkProjectionTx(ctx, tx, items)
}

func (s *Service) publicationAttributions(ctx context.Context, p identity.Principal, selected Selection) (compactcatalog.ReadingValues, string, error) {
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return compactcatalog.ReadingValues{}, "", err
	}
	defer g.Rollback()
	current, err := s.selectTx(ctx, g.Tx(), p, selected.ItemID, selected.SourceID)
	if err != nil {
		return compactcatalog.ReadingValues{}, "", err
	}
	if current.Revision != selected.Revision {
		return compactcatalog.ReadingValues{}, "", ErrChanged
	}
	values, err := compactcatalog.ReadingAcceptedValuesTx(ctx, g.Tx(), []int64{selected.Entity})
	if err != nil {
		return compactcatalog.ReadingValues{}, "", err
	}
	rows := []LibraryRow{{ID: selected.ItemID, entity: selected.Entity}}
	if err = readingArtworkProjectionTx(ctx, g.Tx(), rows); err != nil {
		return compactcatalog.ReadingValues{}, "", err
	}
	return values[selected.Entity], rows[0].CoverURL, nil
}

// One bounded lookup for the visible selected entities, never archive I/O or a provider fetch.
func readingArtworkProjectionTx(ctx context.Context, tx *sql.Tx, items []LibraryRow) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, len(items))
	for i := range items {
		ids[i] = items[i].entity
	}
	covers, err := compactcatalog.ReadingCoverQueriesTx(ctx, tx, ids)
	if err != nil {
		return err
	}
	for i := range items {
		if digest := covers[items[i].entity]; digest != "" {
			items[i].CoverURL = "/v1/items/" + url.PathEscape(items[i].ID) + "/art/cover?" + digest
		}
	}
	return nil
}
