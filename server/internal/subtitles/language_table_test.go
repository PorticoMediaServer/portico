package subtitles

import "testing"

// A forced track is matched to the audio beside it whichever ISO 639 spelling
// the probe reported: the stored track carries the two-letter code.
func TestForcedTrackMatchesThreeLetterAudioLanguages(t *testing.T) {
	for _, pair := range [][2]string{{"bul", "bg"}, {"hrv", "hr"}, {"fas", "fa"}, {"per", "fa"}, {"tam", "ta"}, {"mal", "ml"}, {"cym", "cy"}, {"wel", "cy"}, {"kat", "ka"}, {"swa", "sw"}} {
		tracks := []AutoCandidate{{ID: "forced", Language: pair[1], Title: "Forced", Forced: true}}
		if chosen := ChooseAutomatic(AutoChoice{AudioLanguage: pair[0]}, tracks); chosen == nil {
			t.Fatalf("a forced %q track was not shown beside %q audio", pair[1], pair[0])
		}
	}
	if chosen := ChooseAutomatic(AutoChoice{AudioLanguage: "bul"}, []AutoCandidate{{ID: "forced", Language: "hr", Forced: true}}); chosen != nil {
		t.Fatal("a forced track was shown beside audio in another language")
	}
}

// Both forms of every ISO 639-2 code that has a two-letter code fold to it,
// and the folds this server already made are kept.
func TestTwoLetterLanguageCoversBothISOForms(t *testing.T) {
	for three, two := range map[string]string{
		"eng": "en", "fre": "fr", "fra": "fr", "ger": "de", "deu": "de", "chi": "zh", "zho": "zh", "heb": "he", "ind": "id",
		"alb": "sq", "sqi": "sq", "arm": "hy", "hye": "hy", "baq": "eu", "eus": "eu", "bur": "my", "mya": "my", "geo": "ka", "kat": "ka",
		"mac": "mk", "mkd": "mk", "mao": "mi", "mri": "mi", "tib": "bo", "bod": "bo", "wel": "cy", "cym": "cy",
		"mal": "ml", "kan": "kn", "mar": "mr", "pan": "pa", "guj": "gu", "afr": "af", "bos": "bs", "glg": "gl", "gle": "ga",
		"khm": "km", "lao": "lo", "nep": "ne", "sin": "si", "swa": "sw", "amh": "am", "kaz": "kk", "uzb": "uz", "aze": "az", "mon": "mn",
		"nor": "no", "nob": "no", "nno": "nn", "fil": "tl", "tgl": "tl",
	} {
		if got, ok := TwoLetterLanguage(three); !ok || got != two {
			t.Fatalf("%s folds to %q, want %q", three, got, two)
		}
	}
	for _, other := range []string{"en", "und", "zxx", "mul", "yue", ""} {
		if got, ok := TwoLetterLanguage(other); ok {
			t.Fatalf("%q was folded to %q", other, got)
		}
	}
}
