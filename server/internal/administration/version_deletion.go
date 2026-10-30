package administration

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// VersionDeletePreview binds confirmation to one file and its title. Unrelated
// library changes do not invalidate it.
type VersionDeletePreview struct {
	DeletePreview
	AssetID    string `json:"assetId"`
	PreviewKey string `json:"previewKey"`
}
type VersionDeleteRequest struct {
	Confirmation string `json:"confirmation"`
	PreviewKey   string `json:"previewKey"`
	OperationID  string `json:"operationId"`
}

func versionTarget(ctx context.Context, tx *sql.Tx, item, asset string) (DeleteTarget, string, error) {
	var playable bool
	if err := tx.QueryRowContext(ctx, `SELECT k.playable FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?)`, item).Scan(&playable); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DeleteTarget{}, "", ErrNotFound
		}
		return DeleteTarget{}, "", err
	}
	if !playable {
		return DeleteTarget{}, "", ErrInput
	}
	t, err := targetFor(ctx, tx, item)
	if err != nil {
		return t, "", err
	}
	for _, f := range t.Files {
		if f.AssetID != asset {
			continue
		}
		var part int
		var start float64
		var end sql.NullFloat64
		var edition string
		err = tx.QueryRowContext(ctx, `SELECT l.part_index,l.start_seconds,l.end_seconds,l.edition FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? AND a.token=?`, t.entityID, asset).Scan(&part, &start, &end, &edition)
		if err != nil {
			return t, "", err
		}
		t.Files, t.Bytes, t.Dependents = []DeleteFile{f}, f.Bytes, []Dependent{}
		if f.Shared {
			t.Bytes = 0
		}
		key := digestOf([]any{t.ItemID, t.Title, t.Kind, t.LibraryID, t.AllowMediaDeletion, f.AssetID, f.Path, f.size, f.modifiedNS, f.root, f.rootIdentity, f.Shared, part, start, end, edition})
		return t, key, nil
	}
	return t, "", ErrNotFound
}

func (s *Service) PreviewDeleteVersion(ctx context.Context, auth Authorize, item, asset string) (VersionDeletePreview, error) {
	if _, valid := entityid.Decode(item); !valid {
		return VersionDeletePreview{}, ErrInput
	}
	out := VersionDeletePreview{AssetID: asset, DeletePreview: DeletePreview{Targets: []DeleteTarget{}, Blocked: []string{}}}
	if validTargets([]string{item}) != nil || validTargets([]string{asset}) != nil {
		return out, ErrInput
	}
	err := s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		t, key, err := versionTarget(ctx, tx, item, asset)
		if err != nil {
			return err
		}
		out.Targets = []DeleteTarget{t}
		out.PreviewKey = key
		out.Confirmation, out.ConfirmationKind = t.Title, "title"
		out.TotalFiles, out.TotalBytes = 1, t.Bytes
		if !t.AllowMediaDeletion {
			out.Blocked = append(out.Blocked, t.LibraryID)
		}
		out.Revision, err = deleteFence(ctx, tx, []string{t.LibraryID})
		return err
	})
	out.TotalBytesText = formatBytes(out.TotalBytes)
	return out, err
}

// DeleteVersion removes just the approved link, keeping the title and personal
// state. If this was its last file the usual missing-title rule takes over.
func (s *Service) DeleteVersion(ctx context.Context, auth Authorize, item, asset string, in VersionDeleteRequest) (DeleteResult, error) {
	return s.DeleteVersionChecked(ctx, auth, nil, item, asset, in)
}

// DeleteVersionChecked supplements the established command with an exact
// selection proof. A completed receipt replays after live owner admission but
// before re-proving a link that this same operation already removed.
func (s *Service) DeleteVersionChecked(ctx context.Context, auth, selection Authorize, item, asset string, in VersionDeleteRequest) (DeleteResult, error) {
	out := DeleteResult{Receipts: []DeleteReceipt{}}
	if validTargets([]string{item}) != nil || validTargets([]string{asset}) != nil || !validOperationID(in.OperationID) || !safeText(in.Confirmation, 400) || len(in.PreviewKey) != 64 {
		return out, ErrInput
	}
	s.files.Lock()
	defer s.files.Unlock()
	const scope = "version-delete"
	digest := digestOf([]any{item, asset, in})
	var t DeleteTarget
	replay := false
	err := s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		stored, found, e := receipt[DeleteResult](ctx, tx, scope, in.OperationID, digest)
		if e != nil {
			return e
		}
		if found {
			out, replay = stored, true
			return nil
		}
		if selection != nil {
			if e = selection(ctx, tx); e != nil {
				return e
			}
		}
		var key string
		t, key, e = versionTarget(ctx, tx, item, asset)
		if e != nil {
			return e
		}
		if !t.AllowMediaDeletion {
			return ErrDenied
		}
		if key != in.PreviewKey {
			return ErrConflict
		}
		if t.Title != in.Confirmation {
			return ErrConfirmation
		}
		return nil
	})
	if err != nil || replay {
		return out, err
	}
	f := t.Files[0]
	r := DeleteReceipt{ItemID: item, Title: t.Title, Removed: true}
	if f.Shared {
		r.FilesKept = 1
	} else {
		removed, code := removeMediaFile(f)
		if code != "" {
			r.Removed, r.Code, r.FilesKept = false, code, 1
		}
		if removed {
			r.FilesDeleted, r.Bytes = 1, f.Bytes
		}
	}
	if r.Removed && !f.Shared {
		// Disk deletion has already happened. Record that fact even if the
		// request is cancelled or title/owner authority changes before unlinking
		// its catalogue association. This transaction grants no new action.
		reconcile, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = s.transaction(reconcile, nil, func(tx *sql.Tx) error {
			return markDeletedFilesTx(reconcile, tx, []DeleteFile{f})
		})
		cancel()
		if err != nil {
			return out, err
		}
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		if r.Removed {
			current, key, e := versionTarget(ctx, tx, item, asset)
			if e != nil {
				return e
			}
			if key != in.PreviewKey {
				return ErrConflict
			}
			var id int64
			if e = tx.QueryRowContext(ctx, `SELECT id FROM catalog_assets WHERE token=?`, asset).Scan(&id); e != nil {
				return e
			}
			if e = compactcatalog.UnlinkAssetTx(ctx, tx, current.entityID, id); e != nil {
				return e
			}
			// This destructive point operation returns the selected title's current
			// missing state; bulk source fanout stays asynchronous and bounded.
			if e = compactcatalog.SettleMissingTx(ctx, tx, current.entityID); e != nil {
				return e
			}
			// Do not let an explicit inventory link put this version back next scan.
			if _, e = tx.ExecContext(ctx, `DELETE FROM inventory_version_links WHERE item_id=? AND asset_id=?`, current.entityID, asset); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE library_revisions SET revision=revision+1 WHERE library_id=?`, t.LibraryID); e != nil {
				return e
			}
		}
		out.Receipts = []DeleteReceipt{r}
		out.BytesDeleted = r.Bytes
		if r.Removed {
			out.Removed = 1
		} else {
			out.Failed = 1
		}
		var e error
		out.Revision, e = deleteFence(ctx, tx, []string{t.LibraryID})
		if e != nil {
			return e
		}
		return saveReceipt(ctx, tx, scope, in.OperationID, digest, out, s.milliseconds())
	})
	return out, err
}
