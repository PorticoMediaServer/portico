package livechannels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strings"
	"time"
)

type PreferenceInput struct {
	RequestID        string `json:"requestId"`
	SourceID         string `json:"sourceId"`
	ChannelID        string `json:"channelId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Favorite         bool   `json:"favorite"`
	Hidden           bool   `json:"hidden"`
}

// SetPreference never treats an authorization/policy revision as ownership.
func (s *Store) SetPreference(ctx context.Context, a Authority, o Owner, in PreferenceInput) (int64, error) {
	if !o.Valid() || !preferenceRequestID(ctx, in.RequestID) || !opaque(in.SourceID) || len(in.ChannelID) != 64 || in.ExpectedRevision < 0 {
		return 0, ErrInvalid
	}
	var result int64
	err := s.transaction(ctx, a, false, func(tx *sql.Tx) error {
		_, allowed, e := authorize(ctx, tx, a, false)
		if e != nil {
			return e
		}
		if !allowed(in.SourceID, in.ChannelID) {
			return ErrDenied
		}
		r, e := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
		if e != nil {
			return e
		}
		if r != nil {
			result, e = PreferenceRevisionTx(ctx, tx, o, in)
			return e
		}

		var exists bool
		if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM live_sources s JOIN live_channel_versions c ON c.generation_id=s.active_generation WHERE s.id=? AND c.channel_id=? AND s.state='active')`, in.SourceID, in.ChannelID).Scan(&exists) != nil {
			return ErrUnavailable
		}
		if !exists {
			return ErrConflict
		}
		result, e = SetPreferenceTx(ctx, tx, o, in)
		return e
	})
	return result, err
}

// SetPreferenceTx is shared persistence, not an authorization gate. The caller
// must validate the exact domain/channel and current viewer in this transaction.
func SetPreferenceTx(ctx context.Context, tx *sql.Tx, o Owner, in PreferenceInput) (int64, error) {
	if !o.Valid() || !preferenceRequestID(ctx, in.RequestID) || in.ExpectedRevision < 0 {
		return 0, ErrInvalid
	}
	if _, _, protocol := usermutation.RequestFrom(ctx); !protocol && strings.HasPrefix(in.RequestID, "p251_") {
		return 0, usermutation.ErrInvalidKey
	}
	var result int64
	digest := id(in.SourceID, in.ChannelID, fmt.Sprint(in.ExpectedRevision), fmt.Sprint(in.Favorite), fmt.Sprint(in.Hidden))
	var old string
	e := sql.ErrNoRows
	_, _, protocol := usermutation.RequestFrom(ctx)
	if !protocol {
		e = tx.QueryRowContext(ctx, `SELECT digest,revision FROM live_preference_receipts WHERE authority=? AND account_id=? AND profile_id=? AND request_id=?`, o.Authority, o.AccountID, o.ProfileID, in.RequestID).Scan(&old, &result)
	}
	if e == nil {
		if old != digest {
			return 0, ErrConflict
		}
		return result, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return 0, ErrUnavailable
	}
	var revision int64
	e = tx.QueryRowContext(ctx, `SELECT revision FROM live_channel_preferences WHERE authority=? AND account_id=? AND profile_id=? AND source_id=? AND channel_id=?`, o.Authority, o.AccountID, o.ProfileID, in.SourceID, in.ChannelID).Scan(&revision)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return 0, ErrUnavailable
	}
	if revision != in.ExpectedRevision {
		return 0, ErrConflict
	}
	result = revision + 1
	if _, e = tx.ExecContext(ctx, `INSERT INTO live_channel_preferences VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(authority,account_id,profile_id,source_id,channel_id) DO UPDATE SET favorite=excluded.favorite,hidden=excluded.hidden,revision=excluded.revision`, o.Authority, o.AccountID, o.ProfileID, in.SourceID, in.ChannelID, in.Favorite, in.Hidden, result); e != nil {
		return 0, ErrUnavailable
	}
	if protocol {
		e = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: in.ChannelID, Revision: result}, time.Now().UnixMilli())
		return result, e
	}

	if _, e = tx.ExecContext(ctx, `INSERT INTO live_preference_receipts VALUES(?,?,?,?,?,?)`, o.Authority, o.AccountID, o.ProfileID, in.RequestID, digest, result); e != nil {
		return 0, ErrUnavailable
	}
	return result, nil
}

// PreferenceRevisionTx projects current state for a previously accepted request.
func PreferenceRevisionTx(ctx context.Context, tx *sql.Tx, o Owner, in PreferenceInput) (int64, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM live_channel_preferences WHERE authority=? AND account_id=? AND profile_id=? AND source_id=? AND channel_id=?`, o.Authority, o.AccountID, o.ProfileID, in.SourceID, in.ChannelID).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return revision, err
}

// Legacy preference requests use opaque channel IDs. Protocol requests retain
// the immutable HTTP key; CheckContext validates its clock and retirement fence.
func preferenceRequestID(ctx context.Context, key string) bool {
	if _, envelope, protocol := usermutation.RequestFrom(ctx); protocol {
		return envelope.Key == key
	}
	return opaque(key)
}
