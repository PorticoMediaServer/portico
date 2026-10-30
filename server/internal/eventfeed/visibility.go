package eventfeed

import (
	"encoding/json"
)

func contentResource(kind string) bool {
	switch kind {
	case "item", "title", "movie", "show", "season", "episode", "artist", "album", "song", "book", "audiobook_file", "collection", "extra", "disc", "author", "chapter", "ebook", "comic":
		return true
	}
	return false
}

// Personal/progress hints can carry title IDs even when their resource is a
// library. The documented absence of titles means "any title in this library",
// so a hidden or malformed title path becomes the existing generic hint. A
// partially filtered path could wrongly tell another visible page to skip its
// refresh; never publish only the visible suffix of a retained path.
func visiblePersonalTitles(data string, allowed func(string) bool) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(data), &fields) != nil {
		return data
	}
	raw, present := fields["titles"]
	if !present {
		return data
	}
	var titles []string
	visible := json.Unmarshal(raw, &titles) == nil && len(titles) > 0
	if visible {
		for _, id := range titles {
			if !allowed(id) {
				visible = false
				break
			}
		}
	}
	if visible {
		return data
	}
	delete(fields, "titles")
	redacted, err := json.Marshal(fields)
	if err != nil {
		return ""
	}
	return string(redacted)
}
