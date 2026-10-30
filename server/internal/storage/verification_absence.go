package storage

import (
	"context"
	"database/sql"
)

// ValidateInventoryAbsenceTx revalidates a source's absence proof in the
// transaction that will mark a catalogued file missing.
//
// A local proof needs no second answer: InventoryAbsence opened the catalogued
// parent and compared the root's own identity, and the caller re-reads the same
// catalogue row here. A remote proof is derived from retained provider listing
// evidence, so the adapter must fence the exact recorded listing and the source
// configuration captured when the proof was taken. A current configuration read
// alone is not that capture and must never be accepted as one.
//
// The method is intentionally on Client rather than on RemoteSources: a storage
// adapter that cannot answer remote absence proofs keeps compiling, and this
// method then refuses the write instead of silently retiring the source.
func (c *Client) ValidateInventoryAbsenceTx(ctx context.Context, tx *sql.Tx, key string, r InventoryRequest) error {
	if tx == nil {
		return ErrInventoryChanged
	}
	if !c.IsRemote(r.Root) {
		// Local callers already proved absence against the root and parent in
		// their own writer-visible request; there is no retained evidence to
		// re-check.
		return nil
	}
	absence, ok := c.Remote.(RemoteInventoryAbsence)
	if !ok {
		return ErrRemoteCursor
	}
	return absence.ValidateInventoryAbsenceTx(ctx, tx, key, r)
}
