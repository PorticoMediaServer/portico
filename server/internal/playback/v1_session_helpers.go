package playback

// V1-only session helpers (B8c). Previously these shared code with the /v2
// ControlService (control_occurrences.go, control_vod.go, queue_playback.go);
// the /v2 branches (playback_occurrences, playback_queue_candidates,
// playback_audio_staging, playback_viewer_leases, playback_preparation_intents)
// are gone. Only playback_sessions, playback_private_presentations and
// playback_physical_source_pins remain.

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
)

// checkSessionPolicy enforces the owner's stream caps for a new presentation.
// Private presentations share their owner's slot.
//
// Whether a title may be converted is not decided here. The owner's
// conversion switch is applied to the plan itself (create refuses a plan that
// converts anything while it is off), so a stream that only repackages the
// file, which converts nothing and has its own owner switch, still plays.
func checkSessionPolicy(ctx context.Context, tx *sql.Tx, p identity.Principal, replacing string) error {
	streams, err := streamlimits.ReadTx(ctx, tx, p.AccountID)
	if err != nil {
		return err
	}
	var accountCap, serverCap sql.NullInt64
	if streams.EffectiveLimit != nil {
		accountCap = sql.NullInt64{Int64: int64(*streams.EffectiveLimit), Valid: true}
	}
	err = tx.QueryRowContext(ctx, `SELECT server_cap FROM playback_owner_policy WHERE singleton=1`).Scan(&serverCap)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, limit := range []struct {
		cap     sql.NullInt64
		account bool
		code    string
	}{
		{accountCap, true, "owner_account_cap"}, {serverCap, false, "owner_server_cap"},
	} {
		if !limit.cap.Valid {
			continue
		}
		n, err := countPlaybackSlots(ctx, tx, now, p.Authority, p.AccountID, limit.account, replacing)
		if err != nil {
			return err
		}
		if n >= limit.cap.Int64 {
			return controlFault(limit.code, 409)
		}
	}
	return nil
}

func countPlaybackSlots(ctx context.Context, tx *sql.Tx, now time.Time, authority, account string, scoped bool, replacing string) (int64, error) {
	return streamlimits.CountTx(ctx, tx, authority, account, scoped, now, "", replacing)
}

// terminateControlOccurrence ends a v1 session (replacement or stop). The /v2
// lease/preparation/occurrence rows no longer exist.
func terminateControlOccurrence(ctx context.Context, tx *sql.Tx, id, state string) error {
	_, e := tx.ExecContext(ctx, `UPDATE playback_sessions SET state='stopped' WHERE id=?`, id)
	return e
}

func checkSessionOccurrenceTx(ctx context.Context, tx *sql.Tx, id string) error {
	return checkSessionOccurrenceModeTx(ctx, tx, id, false)
}

func checkSessionOccurrenceModeTx(ctx context.Context, tx *sql.Tx, id string, initialAudio bool) error {
	if err := checkDiscSessionTx(ctx, tx, id); err != nil {
		return err
	}
	return checkSessionOccurrencePhysicalTx(ctx, tx, id, initialAudio)
}

func (s *Service) checkSessionOccurrenceModeTx(ctx context.Context, tx *sql.Tx, id string, initialAudio bool) error {
	if err := s.checkDiscSessionTx(ctx, tx, id); err != nil {
		return err
	}
	return checkSessionOccurrencePhysicalTx(ctx, tx, id, initialAudio)
}

func checkSessionOccurrencePhysicalTx(ctx context.Context, tx *sql.Tx, id string, initialAudio bool) error {
	var invalidPhysical int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM playback_physical_source_pins pin WHERE pin.session_id=? AND NOT EXISTS(SELECT 1 FROM library_sources source JOIN inventory_objects object ON object.source_id=source.id JOIN playback_sessions ps ON ps.asset_id=object.asset_id JOIN catalog_entities item ON item.id=ps.item_id JOIN catalog_libraries cl ON cl.id=item.library_id AND cl.library_id=source.library_id WHERE ps.id=pin.session_id AND source.id=pin.source_id AND source.incarnation=pin.incarnation AND source.generation=pin.configuration_generation AND source.enabled=1 AND object.root_incarnation=pin.incarnation AND object.retired=0 AND object.state='available')`, id).Scan(&invalidPhysical); e != nil {
		return e
	}
	if invalidPhysical != 0 {
		return identity.ErrUnauthorized
	}
	// Receiver capabilities are private but fully playable, confined to the exact live
	// public session/choice revision and its original media owner. Ordinary prefetch remains limited.
	var receiver, liveReceiver int
	if e := tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(CASE WHEN v.ended_ms=0 AND v.lease_expires_ms>? AND v.generation=a.generation AND COALESCE(json_extract(NULLIF(v.presentation,''),'$.choiceRevision'),1)=a.choice_revision AND v.media_session_id=pp.owner_session_id AND v.account_id=ps.account_id AND v.profile_id=ps.profile_id THEN 1 ELSE 0 END),0) FROM playback_airplay_presentations a JOIN playback_v1_sessions v ON v.id=a.session_id JOIN playback_sessions ps ON ps.id=a.media_session_id JOIN playback_private_presentations pp ON pp.session_id=ps.id WHERE a.media_session_id=?`, time.Now().UnixMilli(), id).Scan(&receiver, &liveReceiver); e != nil {
		return e
	}
	if receiver > 0 {
		if liveReceiver != 1 {
			return identity.ErrUnauthorized
		}
		return nil
	}
	var privateUntil sql.NullInt64
	if e := tx.QueryRowContext(ctx, `SELECT expires_ms FROM playback_private_presentations WHERE session_id=?`, id).Scan(&privateUntil); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	} else if e == nil {
		if !initialAudio || privateUntil.Int64 <= time.Now().UnixMilli() {
			return identity.ErrUnauthorized
		}
		return nil
	}
	return nil
}

// RestartConversion discards converted output and moves the producer to the
// grid slot covering position.
func (s *Service) RestartConversion(ctx context.Context, id string, positionSeconds float64) {
	if s.hls == nil {
		return
	}
	s.hls.restart(ctx, id, positionSeconds)
}
