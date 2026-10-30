package catalog

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Metadata only for the already-authorized bounded result identities, in the
// same search snapshot. This never opens bytes or creates a reader grant.
func (s *Service) nameReadingSearch(ctx context.Context, entries []ContentEntry, ids []int64) error {
	if len(entries) != len(ids) {
		return errors.New("invalid reading search projection")
	}
	if len(entries) == 0 {
		return nil
	}
	tx := dbwork.Snapshot(ctx)
	if tx == nil {
		tx = s.readTx
	}
	if tx == nil {
		return errors.New("reading search requires its request snapshot")
	}
	values, err := compactcatalog.ReadingAcceptedValuesTx(ctx, tx, ids)
	if err != nil {
		return err
	}
	for i := range entries {
		value := values[ids[i]]
		if value.Author != nil {
			entries[i].Author = value.Author.Value
			entries[i].Subtitle = value.Author.Value
		}
		if value.PublicationType != nil {
			switch value.PublicationType.Value {
			case "book", "magazine", "newspaper":
				entries[i].PublicationType = ReadingPublicationType(value.PublicationType.Value)
			}
		}
	}
	return nil
}
