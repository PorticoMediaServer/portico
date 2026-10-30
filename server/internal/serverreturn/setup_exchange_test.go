package serverreturn

import (
	"encoding/json"
	"testing"
)

func TestSetupExchangeIsDistinctAndCannotWidenSwitch(t *testing.T) {
	owner := ownerExchangeFixture()
	// Setup carries no registration (O-163); everything else mirrors the owner exchange.
	setup := SetupExchange{TransactionID: owner.TransactionID, State: owner.State, Verifier: owner.Verifier, InstallationID: owner.InstallationID, ServerChallenge: owner.ServerChallenge, Purpose: "setup"}
	raw, err := json.Marshal(ExchangeRequest{Setup: &setup})
	var got ExchangeRequest
	if err != nil || json.Unmarshal(raw, &got) != nil || got.Setup == nil || got.Owner != nil || got.Registered != nil || *got.Setup != setup {
		t.Fatal("setup not kept separate", err)
	}
	var switchOnly OwnerExchange
	if json.Unmarshal(raw, &switchOnly) == nil {
		t.Fatal("switch boundary accepted setup")
	}
	if _, err = json.Marshal(ExchangeRequest{Setup: &setup, Owner: &owner}); err == nil {
		t.Fatal("mixed purposes marshaled")
	}
	if json.Unmarshal([]byte(`{"state":null}`), &got) == nil || got.Setup != nil || got.Owner != nil || got.Registered != nil {
		t.Fatal("failed parse retained old authority")
	}
}

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
