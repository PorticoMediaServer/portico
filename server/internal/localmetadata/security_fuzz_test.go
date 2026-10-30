package localmetadata

import "testing"

func FuzzVideoNFOStructure(f *testing.F) {
	for _, seed := range []string{`<movie><title>A film</title><uniqueid type="tmdb">42</uniqueid></movie>`, `<!DOCTYPE movie [<!ENTITY x SYSTEM "file:///etc/passwd">]><movie>&x;</movie>`, `<?xml version="1.0" encoding="windows-1252"?><movie><title>Caf` + string([]byte{0xe9}) + `</title></movie>`} {
		f.Add([]byte(seed), false)
	}
	f.Fuzz(func(t *testing.T, raw []byte, anime bool) {
		if len(raw) > 256<<10 {
			return
		}
		records, err := ParseVideoNFO(raw, anime)
		if err != nil {
			return
		}
		if len(records) > 8 {
			t.Fatal("record budget exceeded")
		}
		for _, r := range records {
			if len(r.IDs) > 16 || len(r.Credits) > 100 || len(r.Title) > 2048 {
				t.Fatal("field budget exceeded")
			}
		}
	})
}
