package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOwnerReturnPublicRequestKeepsStrictSeparateExtendedNormalVariant(t *testing.T) {
	owner := `{"transactionId":"owner_transaction","state":"` + strings.Repeat("s", 43) + `","verifier":"` + strings.Repeat("v", 43) + `","registrationId":"ret_current","installationId":"d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8","serverChallenge":"` + strings.Repeat("c", 32) + `","purpose":"switch"}`
	var q porticoReturnRequest
	if err := json.Unmarshal([]byte(owner), &q); err != nil || q.Owner == nil || q.Code != "" {
		t.Fatal("owner parser unavailable", err)
	}
	normal := `{"transactionId":"normal_transaction","code":"` + strings.Repeat("c", 43) + `","verifier":"` + strings.Repeat("v", 43) + `","registrationId":"ret_current","installationId":"d297a1dc-3adc-4b1c-a7f2-fbd4640f69e8","serverChallenge":"` + strings.Repeat("c", 32) + `","purpose":"sign-in","moveTicket":"actual_move_ticket"}`
	if err := json.Unmarshal([]byte(normal), &q); err != nil || q.Owner != nil || q.MoveTicket != "actual_move_ticket" {
		t.Fatal("normal move transport lost", err)
	}
	for _, raw := range []string{
		strings.Replace(owner, `"purpose":"switch"`, `"purpose":"sign-in"`, 1),
		strings.Replace(owner, `"state":`, `"State":`, 1),
		strings.Replace(owner, `"purpose":"switch"`, `"purpose":"switch","moveTicket":"mixed"`, 1),
		strings.Replace(owner, `"purpose":"switch"`, `"purpose":"switch","code":"mixed"`, 1),
		strings.Replace(owner, `"transactionId":"owner_transaction"`, `"transactionId":"owner_transaction","transactionId":"other"`, 1),
		strings.Replace(normal, `"moveTicket":"actual_move_ticket"`, `"moveTicket":null`, 1),
		owner + `{}`, `null`,
	} {
		if err := json.Unmarshal([]byte(raw), &q); err == nil {
			t.Fatal("mixed/non-strict return accepted")
		}
	}
	if shape := (porticoReturnRequest{}).JSONSchema(); len(shape["oneOf"].([]any)) != 3 {
		t.Fatal("public variants collapsed")
	}
}
