package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
)

// assertSpecResponse checks a real handler answer against the response schema
// the published API document gives for method, path template and the answer's
// status (contract drift, CD-*).
func assertSpecResponse(t *testing.T, method, path string, w *httptest.ResponseRecorder) {
	t.Helper()
	if err := apispec.ValidateErrorEnvelope(w.Code, w.Body.Bytes()); method != "HEAD" && err != nil {
		t.Fatalf("%s %s %d: %v", method, path, w.Code, err)
	}
	doc, schema, err := apispec.Response(method, path, w.Code)
	if err != nil {
		t.Fatal(err)
	}
	if problems := doc.ValidateJSON(schema, w.Body.Bytes()); len(problems) > 0 {
		t.Fatalf("%s %s %d does not match %s: %v\n%s", method, path, w.Code, doc.File, problems, w.Body.String())
	}
}
