package apispec

import (
	"encoding/json"
	"os"
	"testing"
)

func TestHandlerGoldenErrorEnvelopes(t *testing.T) {
	raw, err := os.ReadFile("../httpapi/testdata/readpath_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string
		Status   int
		Response string
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, c := range cases {
		if c.Status >= 400 {
			count++
			if err := ValidateErrorEnvelope(c.Status, []byte(c.Response)); err != nil {
				t.Errorf("%s: %v", c.Name, err)
			}
		}
	}
	if count == 0 {
		t.Fatal("no error fixtures checked")
	}
	t.Logf("%d non-success fixtures checked", count)
}

func TestErrorEnvelopeRejectsFlatOrUncodedBodies(t *testing.T) {
	for _, body := range []string{``, `oops`, `{"error":"oops"}`, `{"code":"invalid_request"}`, `{"error":{"message":"Oops"}}`, `{"error":{"code":"invalid_request","message":"Oops","retry":"maybe"}}`} {
		if ValidateErrorEnvelope(400, []byte(body)) == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
