package hosted

import (
	"context"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

var ErrServicesUnavailable = errors.New("Portico Services is unavailable. Try again when it is reachable.")

// ReturnRegistration is an existing, verified Hosted route registration. It
// grants neither membership nor ownership and cannot publish a new address.
type ReturnRegistration struct {
	ID     string `json:"id"`
	Origin string `json:"origin"`
}

type RegisteredReturnPreparation struct {
	ServerID         string             `json:"serverId"`
	HostedOrigin     string             `json:"hostedOrigin"`
	ClaimOperationID string             `json:"claimOperationId"`
	Registration     ReturnRegistration `json:"registration"`
}

func (s *Service) PrepareRegisteredReturn(ctx context.Context, origin string) (out RegisteredReturnPreparation, err error) {
	if s == nil || s.identity == nil || s.current == nil || !s.Configured() {
		return out, networking.ErrUnavailable
	}
	if err = s.identity.RequireSignInKind(ctx, identity.SignInPortico); err != nil {
		return out, err
	}
	if !networking.RouteOrigin(origin) || !strings.HasPrefix(origin, "https://") {
		return out, identity.ErrDirectInput
	}
	err = s.current.runner.Do(ctx, func(ctx context.Context) error {
		v, e := s.current.store.InstalledIntent(ctx)
		if e != nil {
			return e
		}
		var registration ReturnRegistration
		if e = s.current.transport.CallServer(ctx, v, networking.ResolveReturnRegistration, struct {
			Origin string `json:"origin"`
		}{origin}, &registration); e != nil {
			if errors.Is(e, networking.ErrUnavailable) {
				return ErrServicesUnavailable
			}
			return e
		}
		if registration.Origin != origin || len(registration.ID) != 47 || !strings.HasPrefix(registration.ID, "ret_") {
			return identity.ErrUnauthorized
		}
		for _, c := range registration.ID[4:] {
			if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				return identity.ErrUnauthorized
			}
		}
		if e = s.current.store.Current(ctx, v); e != nil {
			return e
		}
		if e = s.identity.RequireSignInKind(ctx, identity.SignInPortico); e != nil {
			return e
		}
		out = RegisteredReturnPreparation{ServerID: v.ServerID, HostedOrigin: s.origin, ClaimOperationID: v.OperationID, Registration: registration}
		return nil
	})
	return out, err
}
