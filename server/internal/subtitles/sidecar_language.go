package subtitles

import "strings"

func sidecarLanguage(s string) string {
	aliases := map[string]string{"english": "en", "french": "fr", "german": "de", "spanish": "es", "italian": "it", "portuguese": "pt", "brazilian": "pt-BR", "pob": "pt-BR", "russian": "ru", "ukrainian": "uk", "polish": "pl", "czech": "cs", "slovak": "sk", "hungarian": "hu", "romanian": "ro", "dutch": "nl", "swedish": "sv", "norwegian": "no", "danish": "da", "finnish": "fi", "greek": "el", "turkish": "tr", "arabic": "ar", "hebrew": "he", "japanese": "ja", "jp": "ja", "korean": "ko", "chinese": "zh", "chs": "zh-Hans", "cht": "zh-Hant", "thai": "th", "vietnamese": "vi", "indonesian": "id", "hindi": "hi"}
	if v := aliases[strings.ToLower(s)]; v != "" {
		return v
	}
	// Unknown descriptive words remain titles rather than becoming und language.
	if len(s) != 2 && len(s) != 3 && !strings.Contains(s, "-") {
		return ""
	}
	if v, e := Language(s); e == nil && v != "und" {
		return v
	}
	return ""
}
