package metadatalanguage

import "testing"

func TestPrimaryOriginalLanguage(t *testing.T) {
	for raw, want := range map[string]string{"ja-JP": "ja", "jpn": "ja", "chi": "zh", "zho-TW": "zh", "cn": "yue", "eng": "en", "pt-BR": "pt", "": "", "xx": "", "und": "", "und-US": "", "not_a_language": "", "zxx": ""} {
		if got := Primary(raw); got != want {
			t.Errorf("%q: got %q want %q", raw, got, want)
		}
	}
}
