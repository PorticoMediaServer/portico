package read

// This is the approved interim membership scope until kind-8 books have the
// imported Unicode author identity key. For ASCII names, an author NOCASE seek
// can match only ASCII book-author values; no separate book payload is needed
// to establish that property. Empty names have no legacy author identity, and
// embedded NUL cannot safely use SQLite's text collation.
//
// Rare non-ASCII names can fold to an ASCII identity (for example Kelvin sign
// U+212A to "k"). The lead explicitly deferred that unmatched-variant case to
// the stored Unicode key. This gate is not full Unicode membership parity.
func rbAuthorIndexedName(name string) error {
	if name == "" {
		return ErrNotReady
	}
	for i := range name {
		if name[i] == 0 || name[i] >= 128 {
			return ErrNotReady
		}
	}
	return nil
}
