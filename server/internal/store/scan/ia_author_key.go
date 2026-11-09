package scan

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

// AudiobookAuthor keeps one exact folder author value beside the catalogue's
// canonical identity key. Both travel together so a caller cannot pair the key
// of one author with the bytes of another. The value is never trimmed,
// normalized or case-folded: "Straße" and "STRASSE" are different keys, and
// composed and decomposed accents are different keys too. An empty author gets
// "author:" and a whitespace-only author keeps its own distinct key; neither
// grants an author membership, which the accept decides from the exact value.
type AudiobookAuthor struct {
	author, key string
}

// Author returns the exact folder author bytes.
func (a AudiobookAuthor) Author() string { return a.author }

// Key returns the exact dbwork.AuthorIdentityKey of Author.
func (a AudiobookAuthor) Key() string { return a.key }

// Member reports whether the exact value may become a kind-13 author identity.
// The key is still exact for blanks; only membership is withheld.
func (a AudiobookAuthor) Member() bool { return strings.TrimSpace(a.author) != "" }

// AudiobookFolderAuthor reads only the author field of already-prepared
// structured folder evidence. ASIN, narrators, series and sequence stay
// separate on naming.AudiobookFolder and are never concatenated into the value
// or the key. Preparation, source proof and acceptance remain the writer's.
func AudiobookFolderAuthor(folder naming.AudiobookFolder) AudiobookAuthor {
	return AudiobookAuthor{author: strings.Clone(folder.Author), key: dbwork.AuthorIdentityKey(folder.Author)}
}
