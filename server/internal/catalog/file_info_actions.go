package catalog

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type FileInfoActionTarget struct {
	ItemID, AssetID, LibraryID, Kind string
	Item, Asset                      int64
}

// ResolveAction is a point-only supplement to the command's existing authority
// and physical-custody checks. The same proof is repeated in the command's
// accepting transaction, never trusted as a bearer grant.
func (r *FileInfoRuntime) ResolveAction(ctx context.Context, caller readprep.Caller, action FileInfoActionProof) (FileInfoActionTarget, error) {
	proof, ad, e := r.opening(ctx, caller, action.Opening)
	if e != nil {
		return FileInfoActionTarget{}, e
	}
	if !ad.Owner {
		return FileInfoActionTarget{}, readprep.ErrStale
	}
	p, e := r.resolveOccurrence(ctx, caller, action.Opening, action.Occurrence, proof, ad, false)
	if e != nil {
		return FileInfoActionTarget{}, e
	}
	if p.IsGroup {
		return FileInfoActionTarget{}, readprep.ErrInvalid
	}
	var out FileInfoActionTarget
	out.Item = p.Coordinate.Item
	out.Asset = p.Coordinate.Asset
	e = fileInfoQuery(ctx, r.db).QueryRowContext(ctx, `SELECT pid(e.public_id),a.token,cl.library_id,k.name FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_asset_links l ON l.entity_id=e.id AND l.asset_id=? JOIN catalog_assets a ON a.id=l.asset_id WHERE e.id=? AND e.retired=0`, out.Asset, out.Item).Scan(&out.ItemID, &out.AssetID, &out.LibraryID, &out.Kind)
	return out, e
}
