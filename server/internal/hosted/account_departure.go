package hosted

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
	"time"
)

// ConfigureIdentityDepartures supplies the protected key used only by the
// fixed signed leave protocol. Services has no server-data erasure protocol.
func (s *Service) ConfigureIdentityDepartures(keys *networking.ProtectedKeys) error {
	if s.current == nil || keys == nil {
		return networking.ErrInvalid
	}
	s.current.departureKeys = keys
	return nil
}

func (s *Service) PrepareIdentityDeparture(ctx context.Context, reason string) (out *identity.DepartureNotice, err error) {
	if s.current == nil || s.current.departureKeys == nil {
		return nil, networking.ErrUnavailable
	}
	err = s.current.runner.Do(ctx, func(ctx context.Context) error {
		// The immutable installed tuple remains signable offline even if the cached
		// positive claim expired. The notice only destroys that exact tuple.
		v, e := s.current.store.DepartureIntent(ctx)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		wire := identity.DepartureWire{Kind: "portico.server.leave", Version: "1", Audience: s.origin, OperationID: v.OperationID, ServerID: v.ServerID, PublicKey: base64.RawURLEncoding.EncodeToString(v.PublicKey), ClaimGeneration: v.ClaimGeneration, CredentialGeneration: v.CredentialGeneration, NoticeID: identity.Token(), Reason: reason}
		if reason != "moving_to_local" && reason != "start_over_local" {
			return networking.ErrInvalid
		}
		raw, e := json.Marshal(wire)
		if e != nil {
			return e
		}
		sig, e := s.current.departureKeys.Sign(ctx, v.Binding, raw)
		if e != nil {
			return e
		}
		out = &identity.DepartureNotice{Wire: wire, Payload: raw, Signature: sig}
		return nil
	})
	return
}

// DeliverIdentityDepartures sends only immutable notices already committed by
// a move. It cannot discover a new route, claim, identity or membership.
func (s *Service) DeliverIdentityDepartures(ctx context.Context) error {
	if s.current == nil {
		return nil
	}
	return s.current.runner.Do(ctx, func(ctx context.Context) error {
		var id string
		var payload, signature []byte
		e := s.db.QueryRowContext(ctx, `SELECT notice_id,payload,signature FROM identity_service_outbox WHERE delivered_ms=0 AND next_ms<=? ORDER BY created_ms,notice_id LIMIT 1`, time.Now().UnixMilli()).Scan(&id, &payload, &signature)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if e = s.current.transport.SendIdentityDeparture(ctx, networking.SignedProof{Payload: payload, Signature: signature}); e != nil {
			_, saveErr := dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance, `UPDATE identity_service_outbox SET attempts=attempts+1,next_ms=? WHERE notice_id=? AND delivered_ms=0`, time.Now().Add(time.Hour).UnixMilli(), id)
			if saveErr != nil {
				return saveErr
			}
			return e
		}
		_, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance, `UPDATE identity_service_outbox SET delivered_ms=? WHERE notice_id=? AND delivered_ms=0`, time.Now().UnixMilli(), id)
		return e
	})
}
