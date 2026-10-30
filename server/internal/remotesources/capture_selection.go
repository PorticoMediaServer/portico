package remotesources

import (
	"context"
	"database/sql"
	"encoding/hex"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
)

var _ ownedcapture.PlaybackCaptureResolver = (*captureProducer)(nil)

// ResolvePlaybackCaptureTx is the optional ticket preflight port on the actual
// source producer. The store issues the opaque eventual capture ID; the source
// issues the policy/physical selection under current original authority. There
// is no provider work, reservation, charge row or permission derived from the
// ID. Actual Admit repeats this selection and the same authority before any
// provider byte, and the ticket owner must retain exact equality throughout.
func (p *captureProducer) ResolvePlaybackCaptureTx(ctx context.Context, tx *sql.Tx, captureID string, request ownedcapture.Request) (ownedcapture.PlaybackCaptureSource, error) {
	var out ownedcapture.PlaybackCaptureSource
	if p == nil || p.sources == nil || p.identities == nil || p.authorize == nil || ctx == nil || tx == nil || len(captureID) != 64 {
		return out, ownedcapture.ErrInput
	}
	if _, err := hex.DecodeString(captureID); err != nil || strings.ToLower(captureID) != captureID {
		return out, ownedcapture.ErrInput
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	target, err := p.resolveTargetTx(ctx, tx, request.AssetID)
	if err != nil {
		return out, err
	}
	if _, err = p.authorizeTargetTx(ctx, tx, request, &target); err != nil {
		return out, err
	}
	var modified int64
	if err = tx.QueryRowContext(ctx, `SELECT modified_ns FROM inventory_objects WHERE id=? AND source_id=? AND revision=? AND asset_id=? AND retired=0`, target.inventoryObjectID, target.librarySourceID, target.inventoryRevision, target.assetID).Scan(&modified); err != nil {
		return out, err
	}
	if modified < 0 {
		return out, ownedcapture.ErrInput
	}
	out = ownedcapture.PlaybackCaptureSource{
		LibraryID: target.libraryID, LibrarySourceID: target.librarySourceID, AssetID: target.assetID,
		InventoryObjectID: target.inventoryObjectID, InventoryRevision: target.inventoryRevision,
		RootIdentity: target.rootIdentity, Incarnation: target.incarnation,
		RemoteSourceID: target.sourceID, EffectiveConfiguration: target.configGeneration,
		ReadPolicyWitness: capturePolicyWitness(&target), ReadAuthorityWitness: captureID,
		LibraryGeneration: target.libraryGeneration, RawConfigurationGeneration: target.sourceGeneration,
		ProfileRevision: target.profile.Revision, Size: target.size, ModifiedNS: modified,
	}
	if err = ctx.Err(); err != nil {
		return ownedcapture.PlaybackCaptureSource{}, err
	}
	return out, nil
}
