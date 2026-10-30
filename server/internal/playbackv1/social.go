package playbackv1

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// SocialSession is what Watch Together and receiver handoffs read of a v1
// session (B8a): the social store binds to devices and sessions, never to
// controllers.
type SocialSession struct {
	ID, ItemID, DeviceID, State string
	Live                        bool
	Revision                    int64
	PositionUS                  int64
}

func socialFacts(r row) SocialSession {
	return SocialSession{ID: r.id, ItemID: r.item, DeviceID: r.device, State: r.state, Live: r.ended == 0, Revision: int64(r.revision), PositionUS: r.position * 1000}
}

func sameProfile(p identity.Principal, r row) bool {
	return r.authority == p.Authority && r.account == p.AccountID && r.profile == p.ProfileID
}

// SocialSessionTx reads one session owned by the principal's profile. A missing
// session and another profile's session are both sql.ErrNoRows.
func (s *Service) SocialSessionTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) (SocialSession, error) {
	r, err := loadRow(ctx, tx, id)
	if errors.Is(err, ErrNotFound) || err == nil && !sameProfile(p, r) {
		return SocialSession{}, sql.ErrNoRows
	}
	if err != nil {
		return SocialSession{}, err
	}
	facts := socialFacts(r)
	facts.Live = facts.Live && r.lease > s.now().UnixMilli()
	return facts, nil
}

// DeviceSessionTx reads the live session a device is playing. present is false
// when the device no longer exists (signed out or revoked); a present device
// with nothing playing returns a zero session.
func (s *Service) DeviceSessionTx(ctx context.Context, tx *sql.Tx, device string) (SocialSession, bool, error) {
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM identity_devices WHERE id=?`, device).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return SocialSession{}, false, nil
	} else if err != nil {
		return SocialSession{}, false, err
	}
	r, err := scanRow(tx.QueryRowContext(ctx, `SELECT `+rowSelect+` FROM playback_v1_sessions WHERE device_id=? AND ended_ms=0 AND lease_expires_ms>? ORDER BY created_ms DESC LIMIT 1`, device, s.now().UnixMilli()))
	if errors.Is(err, sql.ErrNoRows) {
		return SocialSession{}, true, nil
	}
	if err != nil {
		return SocialSession{}, true, err
	}
	return socialFacts(r), true, nil
}

// EndSessionTx ends a session the principal's profile owns inside the caller's
// transaction (a handoff commit). after fences the grant and wakes watchers; the
// caller runs it once the transaction commits, and not at all on rollback.
func (s *Service) EndSessionTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id, reason string) (func(), error) {
	r, err := loadRow(ctx, tx, id)
	if errors.Is(err, ErrNotFound) || err == nil && !sameProfile(p, r) {
		return nil, identity.ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if r.ended > 0 {
		return nil, nil
	}
	if err = s.endTx(ctx, tx, r, s.now(), reason, ""); err != nil {
		return nil, err
	}
	return func() {
		s.cancelPreparedFor(context.Background(), r.id)
		s.hub.wake()
		if isChannel(r) {
			s.wakeChannels()
		}
		if r.media != "" {
			_ = s.Playback.StopV1(context.Background(), r.media)
		}
	}, nil
}

// SourceSession is the committed identity used by source readers. It is internal:
// source tokens, media IDs and device ownership never appear in a public DTO.
// PresentationGeneration fences player reports; MediaGeneration fences bytes.
type SourceSession struct {
	SessionID, DeviceID, ItemID, VersionID, MediaID   string
	Revision, PresentationGeneration, MediaGeneration int
	Live                                              bool
}

// SourceSessionTx returns only the caller profile's committed session. A
// missing session and another profile's session are indistinguishable.
func (s *Service) SourceSessionTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string) (SourceSession, error) {
	r, err := loadRow(ctx, tx, id)
	if errors.Is(err, ErrNotFound) || err == nil && !sameProfile(p, r) {
		return SourceSession{}, sql.ErrNoRows
	}
	if err != nil {
		return SourceSession{}, err
	}
	return SourceSession{SessionID: r.id, DeviceID: r.device, ItemID: r.item, VersionID: r.version, MediaID: r.media, Revision: r.revision, PresentationGeneration: r.generation, MediaGeneration: r.mediaGeneration, Live: r.ended == 0 && r.lease > s.now().UnixMilli()}, nil
}
