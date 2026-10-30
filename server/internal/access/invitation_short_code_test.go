package access

import (
	"strings"
	"testing"
)

func TestShortInvitationCodeKeepsItsEightyBitsAndDistinctGrammar(t *testing.T) {
	for _, v := range []struct {
		bytes [10]byte
		want  string
	}{
		{want: "0000000000000000"},
		{bytes: [10]byte{255, 255, 255, 255, 255, 255, 255, 255, 255, 255}, want: "ZZZZZZZZZZZZZZZZ"},
		{bytes: [10]byte{0, 17, 34, 51, 68, 85, 102, 119, 136, 153}, want: "008J4CT4ANK7F24S"},
	} {
		got := shortInvitationEncoding.EncodeToString(v.bytes[:])
		if got != v.want {
			t.Fatalf("encoding: got %q want %q", got, v.want)
		}
		decoded, err := shortInvitationEncoding.DecodeString(got)
		if err != nil || string(decoded) != string(v.bytes[:]) {
			t.Fatal("encoding lost entropy")
		}
	}
	code, err := newShortInvitationCode()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := canonicalShortInvitationCode(code); !ok || got != code {
		t.Fatal("generated code is not canonical")
	}
}

func TestShortInvitationCodeAcceptsOnlyDeclaredCaseAndGrouping(t *testing.T) {
	for _, raw := range []string{"008J4CT4ANK7F24S", "008j4ct4ank7f24s", "008J-4CT4-ANK7-F24S", "008j-4ct4-ank7-f24s"} {
		if got, ok := canonicalShortInvitationCode(raw); !ok || got != "008J4CT4ANK7F24S" {
			t.Fatalf("declared form refused: %q", raw)
		}
		if got, ok := displayShortInvitationCode(raw); !ok || got != "008J-4CT4-ANK7-F24S" {
			t.Fatalf("incorrect display: %q", raw)
		}
	}
	for _, raw := range []string{"", "008J 4CT4 ANK7 F24S", "008J--4CT4-ANK7-F24S", "008J-4CT4ANK7-F24S", "008J-4CT4-ANK7-F24S-", " 008J4CT4ANK7F24S", "008J4CT4ANK7F24S\n", "008J4CT4ANK7F24S\u200b", "００8J4CT4ANK7F24S", strings.Repeat("a", 43)} {
		if _, ok := canonicalShortInvitationCode(raw); ok {
			t.Fatalf("undeclared form accepted: %q", raw)
		}
	}
	for _, ambiguous := range "ILOUilou" {
		if _, ok := canonicalShortInvitationCode("008J4CT4ANK7F24" + string(ambiguous)); ok {
			t.Fatal("ambiguous letter accepted")
		}
	}
}
