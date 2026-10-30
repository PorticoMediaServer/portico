package metadata

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Shared publication helpers. The legacy movie match/detail publication loop
// (metadata_jobs, detail_jobs and metadata_work) had no production caller since
// the screen worker took over film and TV matching; it was removed in B84. The
// tables remain because migrations are immutable.

var errPublicationInput = errors.New("invalid provider evidence")

const publicationClockFormat = time.RFC3339

// observedSource is one observed media part, as the MusicBrainz claim records it.
type observedSource struct {
	Asset          string
	Part           int
	Start          float64
	End            sql.NullFloat64
	Path           string
	Size, Modified int64
	Available      bool
}

func (s *Service) publicationTime() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
func publicationDigest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte("portico.metadata-publication.v1\x00"), b...)))
}
func publicationText(text string, limit int) bool {
	if !utf8.ValidString(text) {
		return false
	}
	n := 0
	for _, r := range text {
		n++
		if r > 0xffff {
			n++
		}
		if n > limit || r == 0x7f || r < 0x20 && (r != '\n' && r != '\r' && r != '\t') {
			return false
		}
	}
	return true
}
func publicationTitle(text string, limit int) bool {
	return publicationText(text, limit) && strings.IndexFunc(text, unicode.IsControl) < 0
}
