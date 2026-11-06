// Frozen pre-extraction references from reviewed main7edf15c37.
// identity.go SHA256 e5e35dadde50ccf234fad8b93bafaf6ed0d1c2ee9a740c07e97819c0e2de877c
// write.go SHA256 470173c20ff311da412d041ee4e57be2e8625dcedd1cf44a01a143d0019c7f9e
// Keep independent of production functions; only test names have changed.
package identityvalue

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

func legacyIdentityText(s string) string {
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

func legacyMovieKey(title string, year int) string {
	return "movie:" + legacyIdentityText(title) + ":" + strconv.Itoa(year)
}

func legacyItemKey(root, path string, part int) string {
	rel := path
	if r, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(r, "..") {
		rel = filepath.ToSlash(r)
	}
	if part > 0 {
		return fmt.Sprintf("file:%s#%d", rel, part)
	}
	return "file:" + rel
}
