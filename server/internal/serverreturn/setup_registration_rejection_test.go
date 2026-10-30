package serverreturn

import (
	"encoding/json"
	"testing"
)

// Setup names no registration (O-163): an exchange that still carries one is refused.
func TestSetupExchangeRefusesARegistration(t *testing.T) {
	owner := ownerExchangeFixture()
	raw, _ := json.Marshal(map[string]string{"transactionId": owner.TransactionID, "state": owner.State, "verifier": owner.Verifier, "registrationId": owner.RegistrationID,
		"installationId": owner.InstallationID, "serverChallenge": owner.ServerChallenge, "purpose": "setup"})
	var q SetupExchange
	if json.Unmarshal(raw, &q) == nil {
		t.Fatal("a setup exchange naming a registration was accepted")
	}
}
