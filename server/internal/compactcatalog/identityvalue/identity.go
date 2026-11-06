// Package identityvalue contains the catalogue's existing values-only identity
// keys. It has no SQL, source lookup, IO, policy or authority dependencies.
package identityvalue

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// IdentityText folds names for the existing catalogue identity comparison.
func IdentityText(s string) string {
	var b strings.Builder
	space := false
	for _, r := range sorttext.Fold(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
			continue
		}
		// An apostrophe joins ("Schindler's" and "Schindlers" are one word).
		if r == '\'' || r == '’' {
			continue
		}
		space = true
	}
	return b.String()
}

// MovieKey names the existing logical title/year identity. Library namespace
// is supplied by the caller separately; physical source/root IDs are not keys.
func MovieKey(title string, year int) string {
	return "movie:" + IdentityText(title) + ":" + strconv.Itoa(year)
}

// ItemKey preserves the existing root-relative locator and optional part.
func ItemKey(root, path string, part int) string {
	rel := path
	if r, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(r, "..") {
		rel = filepath.ToSlash(r)
	}
	if part > 0 {
		return fmt.Sprintf("file:%s#%d", rel, part)
	}
	return "file:" + rel
}
