package scan

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// InventoryObservation is W's owned observation surface. W retains the actual
// Scope, original funded lifetime and retirement debt. There is no reader,
// callback, source authority or scope accessor here. The owner must keep the
// preparation and call Close even after an observation/construction error;
// a pending Close retains custody and must be joined on that SAME preparation.
type InventoryObservation interface {
	ObserveInventory(context.Context) (nativeinventory.Page, error)
	Close(context.Context) error
}

// ObserveN1 receives only W-owned raw values and freezes their original command
// and reference. identity/template must come from the same W preparation's
// Snapshot. This function never submits, ACKs, closes or advances a cursor.
// The caller offers this operation before invoking it, for monotonic timing.
func ObserveN1(ctx context.Context, observation InventoryObservation, identity writer.CommandIdentity, template writer.InventoryAcceptInput, filename FilenameNaming) (PreparedN1, error) {
	if ctx == nil || observation == nil {
		return PreparedN1{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return PreparedN1{}, err
	}
	page, err := observation.ObserveInventory(ctx)
	if err != nil {
		return PreparedN1{}, err // ignore every output on error
	}
	if err := ctx.Err(); err != nil {
		return PreparedN1{}, err
	}
	request := nativeinventory.Request{Root: template.Source.ResolvedRoot, RelativePath: template.Directory.Path,
		RootIdentity: template.Source.RootIdentity, DirectoryRevision: template.Directory.Revision,
		Cursor: template.Directory.ExpectedCursor, FollowSymlinks: template.Source.FollowSymlinks, Limit: 1}
	prepared, err := BindN1(identity, template, InventoryTurn{Request: request, Page: page}, filename)
	if err != nil {
		return PreparedN1{}, err
	}
	if err := ctx.Err(); err != nil {
		return PreparedN1{}, err
	}
	return prepared, nil
}
