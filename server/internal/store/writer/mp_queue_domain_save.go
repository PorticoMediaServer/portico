package writer

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Saving is a bounded queue-to-UO value transfer. MP never clones the playlist
// writer. UO appends these exact values in this SAME turn, then the parent
// calls mpFinishQueueSave with its actual accepted count before saving receipt.
type mpQueueSaveSlice struct {
	queue, key, playlist, profile, order string
	revision, custody, from, next, total int64
	entries, items                       []string
}

func mpQueueOrderDigest(l mpQueueLayout) [32]byte {
	h := sha256.New()
	h.Write([]byte("MP-selected-order-v1\x00"))
	put := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	integer := func(v int64) { var n [8]byte; binary.BigEndian.PutUint64(n[:], uint64(v)); h.Write(n[:]) }
	put(l.head.id)
	integer(l.head.revision)
	integer(l.head.custody)
	integer(mpBool(l.head.shuffle))
	integer(l.head.seed)
	integer(l.head.lap)
	integer(l.head.domain)
	integer(l.head.first)
	for _, g := range l.segments {
		put(g.id)
		put(g.snapshot)
		integer(g.first)
		integer(g.count)
		integer(g.removed)
		for _, o := range l.removals[g.snapshot] {
			if o >= g.first && o < g.first+g.count {
				integer(o)
			}
		}
	}
	for _, s := range l.spans {
		integer(mpBool(s.source))
		integer(s.start)
		integer(s.count)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func mpQueueSaveDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpDefinition("mp.queue.save_begin", `INSERT INTO queue_v1_saves(profile_id,key,queue_id,queue_order,playlist_id,caller,total,next_ordinal,saved,state,created_ms,updated_ms) VALUES(?,?,?,?,?,'',?,0,0,'building',?,?)`, "sssssiii", stmt.Write),
		mpDefinition("mp.queue.save_read", `SELECT queue_id,queue_order,playlist_id,total,next_ordinal,saved,state FROM queue_v1_saves WHERE profile_id=? AND key=?`, "ss", stmt.Read),
		mpDefinition("mp.queue.save_cursor", `UPDATE queue_v1_saves SET next_ordinal=?,saved=saved+?,state=?,updated_ms=? WHERE profile_id=? AND key=? AND queue_id=? AND queue_order=? AND next_ordinal=? AND state='building'`, "i isissssi", stmt.Write),
		mpDefinition("mp.queue.save_fail", `UPDATE queue_v1_saves SET state='failed',error_code='queue_changed',updated_ms=? WHERE profile_id=? AND key=? AND state='building'`, "iss", stmt.Write),
	}
}
func mpBeginQueueSave(ctx context.Context, t *commandTurn, token, queue, key, playlist string, revision, custody int64, now time.Time) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueSave); err != nil {
		return out, err
	}
	if !mpID(key) || !mpID(playlist) {
		return out, ErrInvalid
	}
	h, err := mpQueueBoundary(ctx, t, token, queue, now, false, false)
	if err != nil {
		return out, err
	}
	if err = mpRequireRevision(h, revision, custody); err != nil {
		return out, err
	}
	l, err := mpLoadLayout(ctx, t, h)
	if err != nil {
		return out, err
	}
	digest := mpQueueOrderDigest(l)
	if err = mpExec(ctx, t, "mp.queue.save_begin", h.profile, key, queue, hex.EncodeToString(digest[:]), playlist, l.total(), now.UnixMilli(), now.UnixMilli()); err != nil {
		return out, err
	}
	out = mpEffect(l, true)
	out.count = l.total()
	out.done = l.total() == 0
	return out, nil
}
func mpReadQueueSaveSlice(ctx context.Context, t *commandTurn, token, queue, key string, expected int64, now time.Time) (out mpQueueSaveSlice, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueSave); err != nil {
		return out, err
	}
	h, err := mpQueueBoundary(ctx, t, token, queue, now, false, false)
	if err != nil {
		return out, err
	}
	l, err := mpLoadLayout(ctx, t, h)
	if err != nil {
		return out, err
	}
	row, err := mpQuery(ctx, t, "mp.queue.save_read", h.profile, key)
	if err != nil {
		return out, err
	}
	var saved int64
	var state string
	if err = row.Scan(&out.queue, &out.order, &out.playlist, &out.total, &out.from, &saved, &state); err != nil {
		return out, err
	}
	d := mpQueueOrderDigest(l)
	if state != "building" || out.queue != queue || out.from != expected || out.order != hex.EncodeToString(d[:]) || out.total != l.total() {
		return out, ErrConflict
	}
	out.key = key
	out.profile = h.profile
	out.revision = h.revision
	out.custody = h.custody
	out.next = min(out.total, out.from+mpMaxSlice)
	for p := out.from; p < out.next; p++ {
		entry, err := l.entryAt(p)
		if err != nil {
			return out, err
		}
		item, err := mpQueueKey(ctx, t, l, entry)
		if err != nil {
			return out, err
		}
		out.entries = append(out.entries, entry)
		out.items = append(out.items, item)
	}
	return out, nil
}
func mpFinishQueueSave(ctx context.Context, t *commandTurn, token string, s mpQueueSaveSlice, accepted int64, now time.Time) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueSave); err != nil {
		return out, err
	}
	if accepted < 0 || accepted > int64(len(s.items)) || s.next-s.from != int64(len(s.items)) || len(s.items) > mpMaxSlice {
		return out, ErrInvalid
	}
	h, err := mpQueueBoundary(ctx, t, token, s.queue, now, false, false)
	if err != nil {
		return out, err
	}
	if h.profile != s.profile || h.revision != s.revision || h.custody != s.custody {
		return out, ErrConflict
	}
	state := "building"
	if s.next == s.total {
		state = "saved"
	}
	if err = mpExec(ctx, t, "mp.queue.save_cursor", s.next, accepted, state, now.UnixMilli(), s.profile, s.key, s.queue, s.order, s.from); err != nil {
		return out, err
	}
	out = mpEffect(mpQueueLayout{head: h}, s.next != s.from)
	out.cursor = s.next
	out.count = accepted
	out.done = s.next == s.total
	return out, nil
}

// Retain/park resets session custody without continuing a stale presentation.
// A caller supplies an exact bounded set; replacement never enumerates every
// device/profile queue. Parent coordinates its retained birth-owner lifetime.
func mpRetainQueue(ctx context.Context, t *commandTurn, token, queue string, revision, custody int64, now time.Time) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueSave); err != nil {
		return out, err
	}
	h, err := mpQueueBoundary(ctx, t, token, queue, now, false, false)
	if err != nil {
		return out, err
	}
	if err = mpRequireRevision(h, revision, custody); err != nil {
		return out, err
	}
	n, err := mpCancelQueuePreparations(ctx, t, queue, "retained", now)
	if err != nil {
		return out, err
	}
	if h.session == "" && n == 0 {
		return mpEffect(mpQueueLayout{head: h}, false), nil
	}
	if err = mpQueueStopCurrent(ctx, t, h, "retained", now); err != nil {
		return out, err
	}
	next := h
	next.session = ""
	next.revision++
	next.updated = now.UnixMilli()
	if err = mpPublishQueue(ctx, t, h, next); err != nil {
		return out, err
	}
	return mpEffect(mpQueueLayout{head: next}, true), nil
}
