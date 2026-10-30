package identity

import "context"

// Development e2e owner. A devtrust build (dev_e2e.go) fills these hooks; every
// other build, and every release build, leaves them nil, so the entry points
// below offer nothing and refuse.
var (
	devE2EUsername func(*Service) (string, bool)
	devE2ESeed     func(context.Context, *Service) error
	devE2ESignIn   func(context.Context, *Service, string) (DirectSignIn, error)
)

// DevE2EUsername is the seeded test owner a development e2e server offers for
// password-free sign-in, and whether it offers one.
func (s *Service) DevE2EUsername() (string, bool) {
	if devE2EUsername == nil {
		return "", false
	}
	return devE2EUsername(s)
}

// SeedDevE2EOwner creates the test owner on a fresh development e2e server. It
// does nothing in any other build or state.
func (s *Service) SeedDevE2EOwner(ctx context.Context) error {
	if devE2ESeed == nil {
		return nil
	}
	return devE2ESeed(ctx, s)
}

// DevE2ESignIn signs the test owner in without a password, exactly as a direct
// sign-in would after its password check. Outside a development e2e server it
// refuses.
func (s *Service) DevE2ESignIn(ctx context.Context, username string) (DirectSignIn, error) {
	if devE2ESignIn == nil {
		return DirectSignIn{}, ErrUnauthorized
	}
	return devE2ESignIn(ctx, s, username)
}
