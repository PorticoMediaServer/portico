package enrich

import (
	"encoding/json"
	"testing"
)

func TestTMDBJSONRefusesLossyUnicodeReplacement(t *testing.T) {
	for _, body := range []string{
		`{"title":"\ud800"}`, `{"title":"\udfff"}`,
		`{"title":"\ud800x"}`, `{"title":"\ud800\ud800"}`,
		`{"title":"\ud800\u0000"}`, `{"title":"\ud800\\uDC00"}`,
		`{"title":"\uDC00\uD800"}`, `{"unselected":{"name":"\uDBFF"}}`,
	} {
		if !json.Valid([]byte(body)) {
			t.Fatal("fixture must demonstrate encoding/json's accepted surrogate syntax")
		}
		if validTMDBJSON([]byte(body)) {
			t.Fatal("lossy Unicode accepted", body)
		}
	}
	if validTMDBJSON([]byte{'"', 255, '"'}) {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestTMDBJSONPreservesValidEscapesAndUnicode(t *testing.T) {
	for _, body := range []string{
		`{"title":"\uD83D\uDE00"}`, `{"title":"\ud800\udc00"}`,
		`{"title":"\uDBFF\uDFFF"}`, `{"title":"\\ud800"}`,
		`{"title":"\"quoted\" \\ backslash"}`, `{"title":"é é 😀 �"}`,
		`{"title":"\u0000\uD7FF\uE000\ufffd"}`, `{"empty":[],"null":null}`,
	} {
		if !validTMDBJSON([]byte(body)) {
			t.Fatal("valid source changed/refused", body)
		}
	}
	for _, body := range []string{"", `{`, `"\uD800`, `{"x":\}`, `{} {}`, `"\ud800\uZZZZ"`} {
		if validTMDBJSON([]byte(body)) {
			t.Fatal("invalid syntax accepted", body)
		}
	}
}
