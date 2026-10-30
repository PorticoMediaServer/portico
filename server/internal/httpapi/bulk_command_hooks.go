package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func metadataJobEdit(a catalog.JobArguments) metadata.BulkEdit {
	raw, _ := json.Marshal(a)
	var edit metadata.BulkEdit
	json.Unmarshal(raw, &edit)
	return edit
}
func (d Dependencies) bulkCommandHooks() catalog.BulkCommandHooks {
	return catalog.BulkCommandHooks{
		ReadingCollectionAuthorize: d.savedReadingItemAuthorityTx,
		Validate: func(command string, a catalog.JobArguments) error {
			switch command {
			case "refresh":
				if d.Metadata == nil {
					return catalog.ErrJobRequest
				}
				return nil
			case "metadata-edit":
				if d.Metadata == nil {
					return catalog.ErrJobRequest
				}
				if e := metadata.ValidateJobEdit(metadataJobEdit(a)); e != nil {
					return catalog.ErrJobRequest
				}
				return nil
			default:
				return catalog.ErrJobRequest
			}
		},
		Authorize: func(ctx context.Context, tx *sql.Tx, p identity.Principal, command string) error {
			_, err := d.tierAuthorityTx(ctx, tx, p, identity.TierAdmin)
			return err
		},
		Capture: func(ctx context.Context, tx *sql.Tx, p identity.Principal, command, item string, a catalog.JobArguments) (string, error) {
			if command != "metadata-edit" {
				return "", nil
			}
			if d.Metadata == nil {
				return "", errors.New("metadata service unavailable")
			}
			return d.Metadata.JobEditRevision(ctx, tx, item)
		},
		Apply: func(ctx context.Context, tx *sql.Tx, p identity.Principal, command, item, expected string, a catalog.JobArguments) error {
			if d.Metadata == nil {
				return errors.New("metadata service unavailable")
			}
			var e error
			switch command {
			case "refresh":
				e = d.Metadata.QueueRefreshTx(ctx, tx, item)
			case "metadata-edit":
				target, err := d.Metadata.JobEditTarget(ctx, tx, item)
				if err != nil {
					return err
				}
				_, e = d.Metadata.ApplyBulkTargetTx(ctx, tx, target, expected, metadataJobEdit(a), metadata.MBActor{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID}, func(tx *sql.Tx) error {
					_, err := d.tierAuthorityTx(ctx, tx, p, identity.TierAdmin)
					return err
				})
			default:
				return errors.New("command worker unavailable")
			}
			switch {
			case errors.Is(e, metadata.ErrRepairConflict):
				return &catalog.JobItemError{Code: "metadata_conflict"}
			case errors.Is(e, metadata.ErrRepairInput):
				return &catalog.JobItemError{Code: "invalid_metadata_repair"}
			case errors.Is(e, metadata.ErrRefreshKind):
				return &catalog.JobItemError{Code: "unsupported_item_kind"}
			case errors.Is(e, sql.ErrNoRows):
				return &catalog.JobItemError{Code: "not_found"}
			}
			return e
		},
	}
}
