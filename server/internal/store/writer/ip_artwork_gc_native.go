package writer

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// A mark is permission to attempt exactly the externally-held token/generation
// under the SAME storage custody; it does not report physical completion.
// Backup admission must refuse an outstanding source/blob retirement before
// starting its file copy. A backup starting after mark may never race unlink.
// Finish is called only after exact token/path physical removal has completed;
// cancellation leaves the mark, bytes and token durable for recovery.
func ipArtGCHolds(ctx context.Context, t *commandTurn, target int64, digest, intent string) error {
	if e := ipArtAbsent(ctx, t, ipAMBackup); e != nil {
		return e
	}
	var probes []stmt.ID
	switch target {
	case ipArtSource:
		probes = []stmt.ID{ipAMSourceSelection, ipAMSourceHistory, ipAMSourceReceipt, ipAMSourcePrevious, ipAMSourceIntent}
	case ipArtBlob:
		probes = []stmt.ID{ipAMBlobSmall, ipAMBlobLarge, ipAMBlobAmbient, ipAMBlobSlot}
	case ipArtIntent:
		return ipArtAbsent(ctx, t, ipAMIntentReceipt, intent)
	default:
		return ErrInvalid
	}
	for _, id := range probes {
		if e := ipArtAbsent(ctx, t, id, digest); e != nil {
			return e
		}
	}
	return nil
}
func ipArtGC(ctx context.Context, t *commandTurn, k CommandID, q ipArtworkMaterialRequest, now int64, r *ipArtworkMaterialResult) error {
	g, m, o := q.GC, q.Material, q.Owner
	if e := ipArtGCHolds(ctx, t, g.Target, g.Digest, m.ID); e != nil {
		return e
	}
	if g.Target == ipArtIntent {
		// This draft retires only the private staging manifest after publication
		// transferred canonical-file custody. Unpublished canonical cleanup needs
		// the per-slot deletion-generation DDL hunk in the handoff. Refuse it and
		// retain every byte/slot; never pretend it was merely staging scratch.
		state, e := ipArtVerifyIntent(ctx, t, q)
		if e != nil {
			return e
		}
		want := int64(3)
		if k == IPArtworkGCFinish {
			want = 4
		}
		if state != want {
			return ErrConflict
		}
		for n := 0; n < 3; n++ {
			slot := m.Slots[n]
			if e = ipArtOne(ctx, t, ipAMBlob, slot.Digest, slot.MIME, slot.Width, slot.Height, slot.Bytes); e != nil {
				return e
			}
		}
		if m.SlotCount == 4 {
			if e = ipArtOne(ctx, t, ipAMOriginalHeld, m.SourceDigest, m.Slots[3].Bytes); e != nil {
				return e
			}
		}
	}
	r.Action = g.Target
	r.Token = g.Token
	r.StorageRevision = g.Revision + 1
	r.Blob = g.Digest
	// GC digests are historical scalars. In particular, do NOT populate selected,
	// previous or material_intent_id, whose FKs would pin the object being freed.
	if k == IPArtworkGCMark {
		switch g.Target {
		case ipArtSource:
			if e := ipArtWrite(ctx, t, ipAMSourceMark, g.Token[:], g.Digest, g.Revision, g.OriginalBytes); e != nil {
				return e
			}
		case ipArtBlob:
			if e := ipArtWrite(ctx, t, ipAMBlobMark, g.Token[:], g.Digest, g.Revision); e != nil {
				return e
			}
		case ipArtIntent:
			if e := ipArtWrite(ctx, t, ipAMIntentMark, g.Token[:], now, m.ID, o.id, o.incarnation[:], m.Generation, g.Revision); e != nil {
				return e
			}
		default:
			return ErrInvalid
		}
		r.Count = 1
		return nil
	}
	switch g.Target {
	case ipArtSource:
		// Exact source CAS protects optional original bytes. Referencing blobs keep
		// their separate physical custody; their independent GC cannot skip sources.
		if e := ipArtWrite(ctx, t, ipAMSourceDelete, g.Digest, g.Revision, g.Token[:], g.OriginalBytes); e != nil {
			return e
		}
	case ipArtBlob:
		if e := ipArtWrite(ctx, t, ipAMBlobDelete, g.Digest, g.Revision, g.Token[:]); e != nil {
			return e
		}
	case ipArtIntent:
		row, e := ipArtRead(ctx, t, ipAMIntentRetiring, m.ID, o.id, o.incarnation[:], m.Generation, g.Revision, g.Token[:])
		if e != nil {
			return e
		}
		var count int64
		if e = row.Scan(&count); e != nil {
			return e
		}
		if count < 0 || count > 4 {
			return ErrConflict
		}
		keys, e := ipArtSlotKeys(ctx, t, m.ID)
		if e != nil {
			return e
		}
		if int64(len(keys)) != count {
			return ErrConflict
		}
		// Only private intent/slot staging paths are removed here, NEVER the shared
		// source/blob paths. Finalize receives physical completion from its owner.
		for _, key := range keys {
			if e = ipArtWrite(ctx, t, ipAMSlotDelete, m.ID, key); e != nil {
				return e
			}
		}
		after := int64(-1)
		if count > 0 {
			after = count - 1
		}
		if e = ipArtWrite(ctx, t, ipAMIntentFinish, after, m.ID, m.Generation, g.Revision, g.Token[:], count); e != nil {
			return e
		}
	default:
		return ErrInvalid
	}
	r.StorageRevision = g.Revision
	r.Count = 1
	return nil
}
