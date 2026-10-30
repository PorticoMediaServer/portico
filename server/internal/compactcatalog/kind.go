package compactcatalog

import (
	"fmt"
)

// Kind is a catalog_kinds row. Numbers are persisted and permanent: never
// reorder or reuse one. Kinds are data: code that needs "which kinds" asks the
// kind's flags in catalog_kinds (playable, container, searchable, browsable,
// listening), never a list of these constants; a constant names a kind only
// where a feature belongs to that one kind.
//
// Ranges: 1-13 shipped; 14-19 video and audio growth; 20-29 reading; 30-39
// photos; 40-89 unassigned; 90-99 test fixtures only.
type Kind int

const (
	Movie Kind = iota + 1
	Show
	Season
	Episode
	Artist
	Album
	Track
	Book
	Part
	Collection
	Extra
	Disc
	Author

	Ebook Kind = 20
	Comic Kind = 21
)

var kindNames = [...]string{"", "movie", "show", "season", "episode", "artist", "album", "song", "book", "audiobook_file", "collection", "extra", "disc", "author", Ebook: "ebook", Comic: "comic"}

// ParseKind maps a shipped kind's name to its number.
func ParseKind(name string) (Kind, error) {
	for k := Movie; int(k) < len(kindNames); k++ {
		if kindNames[k] != "" && kindNames[k] == name {
			return k, nil
		}
	}
	return 0, fmt.Errorf("unknown catalogue kind %q", name)
}

func (k Kind) Name() (string, error) {
	if k < Movie || int(k) >= len(kindNames) || kindNames[k] == "" {
		return "", fmt.Errorf("unknown catalogue kind %d", k)
	}
	return kindNames[k], nil
}
