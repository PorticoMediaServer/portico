package writer

import (
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// InventoryFilenameMetadata is the complete pure filename metadata projection
// for InventoryFilenameInput. SortTitle is the explicit override, including
// empty for no override. Producers copy these fields unchanged; they do not
// derive their own sort or identity algorithms.
type InventoryFilenameMetadata struct {
	Title, SortTitle, SortKey, SortHead, SortKeyKeepArticles, SortHeadKeepArticles string
	MetadataLanguage                                                               string
}

// ProjectFilenameMetadata uses the SAME sorttext.Key implementation as the
// legacy canonical filename path. It performs no IO, lookup, authority or
// admission. The driver owns working memory before calling this projection.
func ProjectFilenameMetadata(title, explicitSortTitle, language string) (InventoryFilenameMetadata, error) {
	remaining := maxCanonicalRequestBytes
	for _, text := range [...]string{title, explicitSortTitle, language} {
		if len(text) > remaining {
			return InventoryFilenameMetadata{}, inventoryRequestCapacity()
		}
		remaining -= len(text)
	}
	if strings.TrimSpace(title) == "" || !utf8.ValidString(language) {
		return InventoryFilenameMetadata{}, invalid("inventory_metadata")
	}
	if err := ValidateMetadataShape(title, "", 0, 0, 0); err != nil {
		return InventoryFilenameMetadata{}, err
	}
	if err := ValidateMetadataShape(explicitSortTitle, "", 0, 0, 0); err != nil {
		return InventoryFilenameMetadata{}, err
	}
	key := sorttext.Key(title, explicitSortTitle, language)
	// Match canonical sort_ignore_articles=0: only an absent explicit override
	// passes the title as the explicit argument to keep its articles.
	keepExplicit := explicitSortTitle
	if keepExplicit == "" {
		keepExplicit = title
	}
	keepKey := sorttext.Key(title, keepExplicit, language)
	head := func(value string) string {
		_, size := utf8.DecodeRuneInString(value)
		return value[:size]
	}
	return InventoryFilenameMetadata{Title: strings.Clone(title), SortTitle: strings.Clone(explicitSortTitle),
		SortKey: key, SortHead: head(key), SortKeyKeepArticles: keepKey, SortHeadKeepArticles: head(keepKey),
		MetadataLanguage: strings.Clone(language)}, nil
}
