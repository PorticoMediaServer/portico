package sorttext

import "testing"

func TestMetadataSortKeys(t *testing.T) {
	for _, x := range []struct{ title, explicit, language, key, letter string }{
		{"The Éclair", "", "en-US", "eclair", "E"}, {"L’Été", "", "fr", "ete", "E"}, {"Die Straße", "", "de", "strasse", "S"},
		{"The Abbey", "Zulu", "en", "zulu", "Z"}, {"The Abbey", "", "", "abbey", "A"}, {"日本語", "", "ja", "日本語", "日"}, {"Москва", "", "ru", "москва", "М"}, {"Øresund", "", "da", "oresund", "O"}, {"Łódź", "", "pl", "lodz", "L"}, {"42", "", "en", "00000000002:42;", "#"},
	} {
		if got := Key(x.title, x.explicit, x.language); got != x.key {
			t.Errorf("key %q: %q", x.title, got)
		}
		if got := Letter(x.key); got != x.letter {
			t.Errorf("letter %q: %q", x.title, got)
		}
	}
}

func TestNaturalOrderingAndPunctuation(t *testing.T) {
	for _, pair := range [][2]string{{"Rocky 2", "Rocky 10"}, {"Rocky 9", "Rocky 100"}, {"Episode 99999999999999999999", "Episode 100000000000000000000"}} {
		if Key(pair[0], "", "en") >= Key(pair[1], "", "en") {
			t.Fatal(pair)
		}
	}
	for _, title := range []string{"'Til Death", "(500) Days of Summer", "¡Three Amigos!", "★ The Éclair"} {
		key := Key(title, "", "en")
		want := map[string]string{"'Til Death": "T", "(500) Days of Summer": "#", "¡Three Amigos!": "T", "★ The Éclair": "E"}[title]
		if Letter(key) != want {
			t.Errorf("%q: %q %q", title, key, Letter(key))
		}
	}
	if Key("Rocky 0002", "", "en") != Key("Rocky 2", "", "en") {
		t.Fatal("leading zeroes")
	}
}
