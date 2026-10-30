package identity

import (
	"context"
	"database/sql"
)

// ControlsRecipient is private original admission evidence, never an event or
// client-supplied audience. HTTP also applies the actual controls-route guard.
type ControlsRecipient struct {
	principal Principal
	family    FamilyState
}

// MatchesDevice checks private admission evidence without exposing its identity.
func (r ControlsRecipient) MatchesDevice(expected string) bool {
	return expected != "" && r.family.DeviceID == expected
}

func (s *Service) CaptureControlsRecipientTx(ctx context.Context, tx *sql.Tx, p Principal) (ControlsRecipient, error) {
	if !CanUsePersonalState(p) {
		return ControlsRecipient{}, ErrForbidden
	}
	family, err := s.SessionFamilyTx(ctx, tx, p)
	if err != nil {
		return ControlsRecipient{}, err
	}
	if family.DeviceID == "" || p.DeviceID != "" && p.DeviceID != family.DeviceID {
		return ControlsRecipient{}, ErrUnauthorized
	}
	return ControlsRecipient{principal: p, family: family}, nil
}

func (s *Service) CheckControlsRecipientTx(ctx context.Context, tx *sql.Tx, original ControlsRecipient, p Principal) error {
	if p.Hash != original.principal.Hash || p.Viewer != original.principal.Viewer || p.Epoch != original.principal.Epoch || p.Grant != original.principal.Grant {
		return ErrUnauthorized
	}
	current, err := s.CaptureControlsRecipientTx(ctx, tx, p)
	if err != nil {
		return err
	}
	if current.family.ID != original.family.ID || current.family.DeviceID != original.family.DeviceID || current.family.TokenGeneration != original.family.TokenGeneration || !current.family.AuthorizationHorizon.Equal(original.family.AuthorizationHorizon) {
		return ErrUnauthorized
	}
	return nil
}
