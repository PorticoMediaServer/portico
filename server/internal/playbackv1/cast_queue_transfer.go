package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strconv"
	"strings"
)

type castTransferStartKey struct{}
type castTransferStart struct {
	intent, queue, entry, item string
	revision                   int64
}

func (s *Service) readCastTransferStart(ctx context.Context, c Caller, intent, queue, entry string) (string, int64, error) {
	var item string
	var revision int64
	err := dbwork.WithReadSnapshot(ctx, s.DB, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		q, err := loadQueue(ctx, tx, queue)
		if err != nil {
			return err
		}
		if c.Principal.Grant != identity.GrantCastTarget || s.CastQueueTransferCheck == nil {
			return identity.ErrForbidden
		}
		value, err := castTransferEntryTx(ctx, tx, q, entry)
		if err != nil {
			return err
		}
		if err = s.CastQueueTransferCheck(ctx, tx, c, intent, queue, entry, value); err != nil {
			return err
		}
		item, revision = value, q.revision
		return nil
	})
	return item, revision, err
}
func (s *Service) recheckCastTransferStartTx(ctx context.Context, tx *sql.Tx, c Caller, b castTransferStart, base row) error {
	if c.Principal.Grant != identity.GrantCastTarget || s.CastQueueTransferCheck == nil || base.queueID != b.queue || base.entryID != b.entry || base.item != b.item {
		return identity.ErrForbidden
	}
	q, err := loadQueue(ctx, tx, b.queue)
	if err != nil {
		return err
	}
	if q.revision != b.revision {
		return ErrRevision
	}
	item, err := castTransferEntryTx(ctx, tx, q, b.entry)
	if err != nil {
		return err
	}
	if item != b.item {
		return ErrRevision
	}
	return s.CastQueueTransferCheck(ctx, tx, c, b.intent, b.queue, b.entry, b.item)
}
func (s *Service) replayCastTransfer(ctx context.Context, c Caller, intent string, r row, key, digest string) error {
	if s.CastQueueTransferReplay == nil {
		return identity.ErrForbidden
	}
	return dbwork.WithReadSnapshot(ctx, s.DB, func(ctx context.Context) error {
		return s.CastQueueTransferReplay(ctx, dbwork.Snapshot(ctx), c, intent, r.id, key, digest)
	})
}

// Every ordinary queue start also captures its custody. A source start admitted
// before transfer cannot publish a new occurrence after the target takes over.
type queueStartCustodyKey struct{}
type queueStartCustody struct {
	queue, ownerKind, ownerID, session, currentEntry, originalEntry string
	generation, revision                                            int64
}

func (s *Service) recheckQueueStartCustodyTx(ctx context.Context, tx *sql.Tx, c Caller, b queueStartCustody) error {
	q, err := loadQueue(ctx, tx, b.queue)
	if err != nil {
		return err
	}
	if !ownsQueue(c, q) || q.custody != b.generation || q.ownerKind != b.ownerKind || q.ownerID != b.ownerID || q.session != b.session {
		return ErrNotFound
	}
	if q.revision != b.revision {
		return ErrRevision
	}
	if c.Principal.Grant == identity.GrantCastTarget && b.session == "" {
		// The initial sessionless path has its stronger selected-entry check.
		return nil
	}
	return s.castQueueAuthorityTx(ctx, tx, c, q.id)
}

// The source session and normalized current-entry header already pin play order.
// Resolve that one immutable member; never rebuild shuffle/Sonic order or scan
// queue structure in transfer creation/acceptance.
func castTransferEntryTx(ctx context.Context, tx *sql.Tx, q queueRow, entry string) (string, error) {
	if q.currentEntry != entry {
		return "", ErrRevision
	}
	snapshot, ordinalText, ok := strings.Cut(entry, "-")
	ordinal, err := strconv.ParseInt(ordinalText, 10, 64)
	if !ok || err != nil || ordinal < 0 {
		return "", ErrNotFound
	}
	var state, hint string
	var built, hintOrdinal int64
	err = tx.QueryRowContext(ctx, `SELECT s.state,s.built,s.hint_ordinal,s.hint_key FROM queue_v1_segments g INDEXED BY queue_v1_segments_entry JOIN queue_v1_snapshots s ON s.id=g.snapshot_id WHERE g.queue_id=? AND s.queue_id=g.queue_id AND g.snapshot_id=? AND g.first_ordinal<=? AND ?<g.first_ordinal+g.count AND NOT EXISTS(SELECT 1 FROM queue_v1_removals r WHERE r.snapshot_id=? AND r.ordinal=?) ORDER BY g.first_ordinal DESC LIMIT 1`, q.id, snapshot, ordinal, ordinal, snapshot, ordinal).Scan(&state, &built, &hintOrdinal, &hint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if state == "building" && ordinal >= built {
		if ordinal == hintOrdinal && hint != "" {
			return hint, nil
		}
		return "", ErrQueueBuilding
	}
	var chunk []byte
	if err = tx.QueryRowContext(ctx, `SELECT keys FROM queue_v1_chunks WHERE snapshot_id=? AND chunk_no=?`, snapshot, ordinal/chunkKeys).Scan(&chunk); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	item, ok := chunkKey(chunk, int(ordinal%chunkKeys))
	if !ok || item == "" {
		return "", ErrNotFound
	}
	return item, nil
}
