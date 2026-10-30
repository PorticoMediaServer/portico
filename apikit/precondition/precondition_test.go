package precondition

import (
	"net/http/httptest"
	"testing"
)

func TestStrongPreconditions(t *testing.T) {
	r := httptest.NewRequest("PATCH", "/v1/test", nil)
	if Match(r, "1", nil) == nil {
		t.Fatal("missing precondition accepted")
	}
	for _, tag := range []string{`W/"1"`, `"2"`, `*`, `"1", "2"`} {
		r.Header.Set("If-Match", tag)
		if Match(r, "1", nil) == nil {
			t.Fatal("invalid precondition", tag)
		}
	}
	r.Header.Set("If-Match", `"1"`)
	if err := Match(r, "1", nil); err != nil {
		t.Fatal(err)
	}
	r.Header.Set("If-None-Match", "*")
	if err := Create(r, false); err != nil {
		t.Fatal(err)
	}
	if Create(r, true) == nil {
		t.Fatal("existing resource overwritten")
	}
}
