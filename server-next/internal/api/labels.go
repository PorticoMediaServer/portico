package api

import (
	"regexp"
	"unicode/utf8"
)

// languageCode is a language as the person's settings hold it: two or three letters.
var languageCode = regexp.MustCompile(`^[a-z]{2,3}$`)

// checkLabel refuses text a person sends that others will be shown: too long, or with a
// character that cannot be shown or that would turn the text around for the reader.
func checkLabel(path, value string, limit int) error {
	if utf8.RuneCountInString(value) > limit {
		return ErrField(path, "This is too long.")
	}
	for _, c := range value {
		if c == utf8.RuneError || c < ' ' || c == 0x7f || (c >= 0x80 && c < 0xa0) || c == ' ' || c == ' ' ||
			(c >= '‪' && c <= '‮') || (c >= '⁦' && c <= '⁩') {
			return ErrField(path, "This has a character that cannot be shown.")
		}
	}
	return nil
}
