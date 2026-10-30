package localmetadata

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLegacyLocalXMLKeepsUsableEvidence(t *testing.T) {
	for _, tc := range []struct{ encoding, title string }{{"US-ASCII", "Movie"}, {"ISO-8859-1", "Caf\xe9"}, {"windows-1252", "Film \x96 Part 1"}} {
		rows, err := ParseVideoNFO([]byte(`<?xml version="1.0" encoding="`+tc.encoding+`"?><movie><title>`+tc.title+`</title><uniqueid type="tmdb">42</uniqueid></movie>`), false)
		if err != nil || len(rows) != 1 || len(rows[0].IDs) != 1 || !utf8.ValidString(rows[0].Title) {
			t.Fatal(rows, err)
		}
	}
	raw := `<?xml version="1.0" encoding="ISO-8859-1"?><package><metadata><title>Caf` + "\xe9" + `</title><creator>Author</creator><description>` + strings.Repeat("é", 3000) + `</description><identifier scheme="isbn">123</identifier></metadata></package>`
	// Description uses the declared encoding as well.
	raw = strings.ReplaceAll(raw, "é", "\xe9")
	values, err := parseOPF([]byte(raw))
	if err != nil || values["album"] != "Café" || values["author"] != "Author" || values["isbn"] != "123" || values["_partial"] != "true" || len(values["description"]) > 4096 || !utf8.ValidString(values["description"]) {
		t.Fatal(values, err)
	}
	for _, raw := range []string{`<!DOCTYPE movie [<!ENTITY x SYSTEM "file:///etc/passwd">]><movie><title>&x;</title></movie>`, `<?xml version="1.0" encoding="unsupported"?><movie/>`} {
		if _, err := ParseVideoNFO([]byte(raw), false); err == nil {
			t.Fatal("accepted unsafe XML")
		}
	}
}
