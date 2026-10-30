package compactcatalog

// Vocab is a catalog_terms vocabulary. Numbers are stored and permanent: a new
// vocabulary is a new number, never a new table, and a number is never reused.
type Vocab int

const (
	VocabGenre Vocab = 1
	// Reserved: 2 publisher, 3 format, 4 camera, 5 lens, 6 location, 7 mood,
	// 8 keyword. 100 and up: provider-specific vocabularies.
)
