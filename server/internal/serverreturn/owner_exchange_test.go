package serverreturn

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func ownerExchangeFixture() OwnerExchange {
	return OwnerExchange{TransactionID: "owner_transaction", State: strings.Repeat("s", 43), Verifier: strings.Repeat("v", 43), RegistrationID: "ret_actual", InstallationID: strings.Repeat("i", 43), ServerChallenge: strings.Repeat("c", 43), Purpose: "switch"}
}

func TestExchangeRequestKeepsDistinctFlatVariants(t *testing.T) {
	owner := ownerExchangeFixture()
	normal := Exchange{Code: strings.Repeat("a", 43), TransactionID: "normal_transaction", Verifier: owner.Verifier, RegistrationID: owner.RegistrationID, ServerChallenge: owner.ServerChallenge, InstallationID: owner.InstallationID, Purpose: "sign-in"}
	for _, in := range []ExchangeRequest{{Owner: &owner}, {Registered: &normal}} {
		raw, e := json.Marshal(in)
		var got ExchangeRequest
		if e != nil || json.Unmarshal(raw, &got) != nil {
			t.Fatal("valid variant refused", e)
		}
		if in.Owner != nil {
			if got.Owner == nil || *got.Owner != owner || got.Registered != nil || strings.Contains(string(raw), `"code"`) {
				t.Fatal("owner variant coerced into ordinary exchange")
			}
		} else {
			legacy, _ := json.Marshal(normal)
			if got.Registered == nil || *got.Registered != normal || got.Owner != nil || string(raw) != string(legacy) {
				t.Fatal("normal exchange bytes or code changed")
			}
		}
		if fmt.Sprintf("%v %#v", in, in) != "[server return request redacted] [server return request redacted]" {
			t.Fatal("exchange request leaked through formatting")
		}
	}
	if _, e := json.Marshal(ExchangeRequest{}); e == nil {
		t.Fatal("missing wire variant marshaled")
	}
	if _, e := json.Marshal(ExchangeRequest{Registered: &normal, Owner: &owner}); e == nil {
		t.Fatal("mixed wire variants marshaled")
	}
	if fmt.Sprintf("%v %#v", owner, owner) != "[owner exchange redacted] [owner exchange redacted]" {
		t.Fatal("owner exchange leaked through formatting")
	}
}

func TestOwnerExchangeRejectsMixedOrIncompleteRequests(t *testing.T) {
	owner := ownerExchangeFixture()
	raw, _ := json.Marshal(owner)
	valid := string(raw)
	cases := []string{
		`null`, `[]`, `{}`, valid + `{}`,
		strings.TrimSuffix(valid, "}") + `,"code":"` + strings.Repeat("a", 43) + `"}`,
		strings.TrimSuffix(valid, "}") + `,"state":"` + owner.State + `"}`,
		strings.TrimSuffix(valid, "}") + `,"unknown":false}`,
		strings.Replace(valid, `"state"`, `"State"`, 1),
		strings.Replace(valid, `"state":"`+owner.State+`",`, "", 1),
		strings.Replace(valid, `"state":"`+owner.State+`"`, `"state":null`, 1),
		strings.Replace(valid, `"purpose":"switch"`, `"purpose":"change-owner"`, 1),
		strings.Replace(valid, `"purpose":"switch"`, `"purpose":"sign-in"`, 1),
		strings.Replace(valid, owner.Verifier, "short", 1),
		strings.Replace(valid, owner.RegistrationID, "https://invented-registration.test", 1),
		strings.Replace(valid, owner.InstallationID, owner.InstallationID+`\n`, 1),
	}
	for i, text := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			q := ExchangeRequest{Owner: &owner}
			if q.UnmarshalJSON([]byte(text)) == nil || q.Owner != nil || q.Registered != nil {
				t.Fatal("invalid owner request accepted or retained predecessor")
			}
		})
	}
	// A missing code never becomes owner authority, even with switch purpose.
	noCode := strings.Replace(valid, `"state":"`+owner.State+`",`, "", 1)
	var normal Exchange
	if json.Unmarshal([]byte(noCode), &normal) != nil || normal.Valid() {
		t.Fatal("ordinary Exchange no longer requires code")
	}
}

func TestExchangeRequestSchemaClosesBothVariants(t *testing.T) {
	variants := (ExchangeRequest{}).JSONSchema()["oneOf"].([]any)
	for _, v := range variants {
		if v.(map[string]any)["additionalProperties"] != false {
			t.Fatal("exchange variant is not closed")
		}
	}
	normal, owner := variants[0].(map[string]any), variants[1].(map[string]any)
	if len(normal["required"].([]string)) != 7 || len(owner["required"].([]string)) != 7 {
		t.Fatal("exchange fields became optional")
	}
	if normal["properties"].(map[string]any)["state"] != nil || owner["properties"].(map[string]any)["code"] != nil {
		t.Fatal("wire variants overlap")
	}
}
