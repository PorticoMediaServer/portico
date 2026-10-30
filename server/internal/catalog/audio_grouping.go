package catalog

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var audioReleaseID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func albumIdentityKey(tags map[string]string, artist, title string, year int) string {
	if id := strings.TrimSpace(tags["musicbrainz_albumid"]); audioReleaseID.MatchString(id) {
		return "musicbrainz:" + strings.ToLower(id)
	}
	key, _ := json.Marshal([]any{localKey(artist), localKey(title), year, audioEdition(tags)})
	return string(key)
}

// Number runs sort numerically; punctuation and artist names are kept whole.
func naturalAudioName(path string) string {
	name := strings.ToLower(filepath.Base(path))
	var out strings.Builder
	for i := 0; i < len(name); {
		if name[i] >= '0' && name[i] <= '9' {
			j := i
			for j < len(name) && name[j] >= '0' && name[j] <= '9' {
				j++
			}
			digits := strings.TrimLeft(name[i:j], "0")
			if digits == "" {
				digits = "0"
			}
			out.WriteByte('~')
			out.WriteString(fmt.Sprintf("%04d", len(digits)))
			out.WriteString(digits)
			i = j
		} else {
			out.WriteByte(name[i])
			i++
		}
	}
	return out.String()
}
