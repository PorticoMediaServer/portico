package sorttext

import "testing"

func TestKey(t *testing.T) {
	for _, x := range []struct{ title, explicit, language, key, letter string }{
		{"The Éclair", "", "en-US", "eclair", "E"}, {"L’Été", "", "fr", "ete", "E"}, {"Die Straße", "", "de", "strasse", "S"},
		{"The Abbey", "Zulu", "en", "zulu", "Z"}, {"The Abbey", "", "", "abbey", "A"}, {"日本語", "", "ja", "日本語", "日"},
		{"Øresund", "", "da", "oresund", "O"}, {"(500) Days of Summer", "", "en", "00000000003:500;) days of summer", "#"},
		{"'Til Death", "", "en", "til death", "T"},
	} {
		if got := Key(x.title, x.explicit, x.language); got != x.key {
			t.Errorf("key of %q is %q, want %q", x.title, got, x.key)
		}
		if got := Letter(x.key); got != x.letter {
			t.Errorf("letter of %q is %q, want %q", x.title, got, x.letter)
		}
	}
}

func TestNumbersSortByValue(t *testing.T) {
	for _, pair := range [][2]string{{"Rocky 2", "Rocky 10"}, {"Rocky 9", "Rocky 100"}} {
		if Key(pair[0], "", "en") >= Key(pair[1], "", "en") {
			t.Errorf("%q should sort before %q", pair[0], pair[1])
		}
	}
}
