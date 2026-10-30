package playbackv1

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
)

// Only these display facts leave the private committed channel selection.
// Asset IDs, source URLs, offsets, source fences and capabilities never do.
type selectedActivityFacts struct {
	SourceID, ChannelID, Name, Number string
	Title, StartsAt, EndsAt           string
}

func readSelectedActivityFacts(raw string) selectedActivityFacts {
	var selected struct {
		Name, Number, Title string
		Reference           struct{ SourceID, ChannelID string }
		EntryStartMs        int64
		EntryEndMs          int64
	}
	if json.Unmarshal([]byte(raw), &selected) != nil {
		return selectedActivityFacts{}
	}
	out := selectedActivityFacts{SourceID: selected.Reference.SourceID, ChannelID: selected.Reference.ChannelID, Name: selected.Name, Number: selected.Number}
	// These references are looked up against the authorized canonical lineup
	// before publication. A private URL/path is never a public channel identity.
	for _, id := range []*string{&out.SourceID, &out.ChannelID} {
		if len(*id) > 256 || strings.Contains(*id, "://") || strings.HasPrefix(*id, "/") || strings.Contains(*id, "\\") || strings.ContainsFunc(*id, unicode.IsControl) {
			*id = ""
		}
	}
	const maximumMillis = int64(253402300799999)
	if selected.Title != "" && selected.EntryEndMs > 0 && selected.EntryEndMs <= maximumMillis {
		out.Title = selected.Title
		out.EndsAt = time.UnixMilli(selected.EntryEndMs).UTC().Format(time.RFC3339Nano)
		if selected.EntryStartMs > 0 && selected.EntryStartMs < selected.EntryEndMs {
			out.StartsAt = time.UnixMilli(selected.EntryStartMs).UTC().Format(time.RFC3339Nano)
		}
	}
	return out
}
