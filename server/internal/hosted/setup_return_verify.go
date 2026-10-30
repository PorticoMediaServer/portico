package hosted

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
	"github.com/PorticoMediaServer/portico/server/internal/wirejson"
)

// VerifySetupReturn verifies the real unchanged envelope after the dedicated
// installed-server-authenticated exchange. It grants no session and spends no
// challenge/nonce. Only CompleteSetupReturnTx may publish the bound admission.
func (s *Service) VerifySetupReturn(ctx context.Context, raw json.RawMessage, q serverreturn.SetupExchange) (identity.PorticoIdentity, string, error) {
	var none identity.PorticoIdentity
	if s == nil || !s.Configured() || s.identity == nil || !q.Valid() || len(raw) > 16384 {
		return none, "", identity.ErrUnauthorized
	}
	fields, err := wirejson.Object(raw, "assertion")
	var grant struct {
		Assertion Signed `json:"assertion"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err != nil || len(fields) != 1 || d.Decode(&grant) != nil {
		return none, "", identity.ErrUnauthorized
	}
	who, nonce, err := s.parseRegisteredReturn(ctx, grant.Assertion, RegisteredReturnExchange{TransactionID: q.TransactionID, Verifier: q.Verifier, InstallationID: q.InstallationID, ServerChallenge: q.ServerChallenge, Purpose: q.Purpose})
	// Setup names no registration (O-163): the assertion's must be empty, as compared above.
	if err != nil || who.Return.CurrentOwnerAccountID != "" || who.Return.PreviousOwnerRole != "" || who.Return.RegistrationID != "" {
		return none, "", identity.ErrUnauthorized
	}
	return who, nonce, nil
}
