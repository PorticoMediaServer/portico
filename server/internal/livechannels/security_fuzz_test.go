package livechannels

import "testing"

func FuzzGuideXMLStructure(f *testing.F) {
	for _, s := range []string{`<tv><channel id="one"><display-name>One</display-name></channel></tv>`, `<!DOCTYPE tv SYSTEM "xmltv.dtd"><tv/>`, `<!DOCTYPE tv [<!ENTITY x SYSTEM "file:///etc/passwd">]><tv>&x;</tv>`, `<tv><programme channel="one" start="20261013000000 +0000" stop="20261013010000 +0000"><title>A title</title></programme></tv>`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) <= 64<<10 {
			_ = validateGuideXML(raw)
		}
	})
}

func FuzzPlaylistRows(f *testing.F) {
	f.Add("#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",One\nhttps://example.invalid/stream\n")
	f.Add("#EXTM3U\n#EXTINF:-1,One\nfile:///etc/passwd\n")
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 64<<10 {
			return
		}
		parsed, err := parse(SourceInput{Name: "Fuzz", Playlist: raw, Limits: ImportLimits{Bytes: 64 << 10, Channels: 32, Programmes: 32}})
		defer parsed.close()
		if err == nil {
			if len(parsed.channels) > 32 || len(parsed.choices) > 32 {
				t.Fatal("channel budget exceeded")
			}
			for _, c := range parsed.channels {
				if !validLocator(c.locator) {
					t.Fatal("invalid channel locator accepted")
				}
			}
		}
	})
}
