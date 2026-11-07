package writer

import "context"

// One exact source child, never a user/subscriber enumeration or cascading
// resource delete. Parent supplies cleanup authority and co-commits receipt,
// cursor and actual-byte refund. Historical creator authority is insufficient.
type uoSavedCleanupInput struct {
	resource, entry, authority, account, profile string
	security                                     uoSavedAuthority
}
type uoSavedCleanupEffect struct{ rows, bytes int64 }

func applyUOSavedCleanup(ctx context.Context, t *commandTurn, q uoSavedCleanupInput) (out uoSavedCleanupEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if q.security == nil || !uoPersonalText(q.resource, 128) {
		return out, ErrInvalid
	}
	if err = q.security.checkSaved(ctx, t, uoSavedInput{id: q.resource, action: uoSavedDelete}, "cleanup"); err != nil {
		return out, err
	}
	deleted, e := wc008DraftInteger(ctx, t, "uo.saved.tombstone", q.resource)
	if e != nil {
		return out, e
	}
	if deleted != 1 {
		return out, ErrConflict
	}
	if q.entry != "" {
		if q.authority != "" || q.account != "" || q.profile != "" || !uoPersonalText(q.entry, 128) {
			return out, ErrInvalid
		}
		r, e := readUOSavedEntry(ctx, t, q.security, "entry", q.resource, q.entry)
		if e != nil {
			return out, e
		}
		// resource_id is also persisted in this child; fixed scalars are charged by
		// the parent's schema row accounting, variable text by this exact sum.
		out.bytes = int64(len(r.id) + len(r.key) + len(q.resource))
		_, err = wc008DraftInteger(ctx, t, "uo.saved.remove", q.resource, q.entry)
	} else {
		for _, s := range []string{q.authority, q.account, q.profile} {
			if !uoPersonalText(s, 128) {
				return out, ErrInvalid
			}
		}
		out.bytes, err = wc008DraftInteger(ctx, t, "uo.saved.share_bytes", q.resource, q.authority, q.account, q.profile)
		if err != nil {
			return out, err
		}
		_, err = wc008DraftInteger(ctx, t, "uo.saved.unshare", q.resource, q.authority, q.account, q.profile)
	}
	if err != nil {
		return out, err
	}
	out.rows = 1
	return out, nil
}
