package writer

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
)

// Preparation envelopes are values, never a grant or a writer Work profile.
// The closed command recipe selects them; they are not request authority.
// Revision is separate from Q's three prepared-column byte count.
type searchFragmentPreparationBudget struct {
	bytes, revisionBytes, tokenWork, preparationBytes uint64
}

// A copied, bounded native row. The streams remain independent; there is no
// row-local substring/conjunction proof or implied source authority here.
type searchPreparedFragment struct {
	row              searchmanifest.Row
	bytes, tokenWork uint64
	leaf             [32]byte
	valid            bool
}

// This is payload preparation only, before writer admission. A complete
// accepting request must additionally bind its native owner/image/seal/source
// witnesses through W's ONE request encoder and obtain the SAME grant later.
func prepareSearchFragment(row searchmanifest.Row, budget searchFragmentPreparationBudget) (searchPreparedFragment, error) {
	n, required, err := searchFragmentPayloadBytes(row, budget)
	if err != nil {
		return searchPreparedFragment{}, err
	}
	if required > budget.preparationBytes {
		return searchPreparedFragment{}, invalid("fragment_preparation_bytes")
	}
	// All lengths and the total reservation precede UTF-8 walks, token work,
	// cloning and hashing, including an already unchunkable native token.
	if row.Field == 0 || row.Field > 64 || row.Ordinal < 0 ||
		!utf8.ValidString(row.Revision) || !utf8.ValidString(row.Prefix) ||
		!utf8.ValidString(row.Lexical) || !utf8.ValidString(row.Normalized) {
		return searchPreparedFragment{}, invalid("fragment_native_row")
	}
	work, err := searchFragmentTokenWork(row, budget.tokenWork)
	if err != nil {
		return searchPreparedFragment{}, err
	}
	row.Revision, row.Prefix = strings.Clone(row.Revision), strings.Clone(row.Prefix)
	row.Lexical, row.Normalized = strings.Clone(row.Lexical), strings.Clone(row.Normalized)
	leaf, err := searchmanifest.Leaf(row)
	if err != nil {
		return searchPreparedFragment{}, err
	}
	return searchPreparedFragment{row: row, bytes: n, tokenWork: work, leaf: leaf, valid: true}, nil
}

// Four TEXT copies plus finite hash/row bookkeeping. This covers only the
// payload; it cannot borrow W's native/common reserve or certify a whole turn.
func searchFragmentPayloadBytes(row searchmanifest.Row, b searchFragmentPreparationBudget) (prepared, memory uint64, err error) {
	if b.bytes == 0 || b.bytes > maxCanonicalRequestBytes || b.revisionBytes == 0 ||
		b.revisionBytes > maxCanonicalRequestBytes || b.tokenWork == 0 {
		return 0, 0, invalid("fragment_preparation_envelope")
	}
	if uint64(len(row.Revision)) > b.revisionBytes {
		return 0, 0, invalid("fragment_revision_bytes")
	}
	for _, value := range [...]string{row.Prefix, row.Lexical, row.Normalized} {
		if uint64(len(value)) > b.bytes-prepared {
			return 0, 0, invalid("fragment_column_bytes")
		}
		prepared += uint64(len(value))
	}
	// Independently bounded components make the addition/multiplication safe.
	return prepared, 65536 + 4*(prepared+uint64(len(row.Revision))), nil
}

func searchFragmentTokenWork(row searchmanifest.Row, limit uint64) (uint64, error) {
	var work uint64
	charge := func(n uint64) bool {
		if n > limit-work {
			return false
		}
		work += n
		return true
	}
	// Q scalar-hex words are canonical lowercase six-digit scalar groups,
	// separated by ONE ASCII space. The shared recipe clips ordinary words to
	// twelve scalars and numeric words to six; no token is truncated here.
	for rest := row.Prefix; rest != ""; {
		word, tail, more := strings.Cut(rest, " ")
		if len(word) == 0 || len(word)%6 != 0 || len(word)/6 > facettext.IndexedSearchPrefixRunes || more && tail == "" {
			return 0, invalid("fragment_prefix_tokens")
		}
		numeric := true
		for at := 0; at < len(word); at += 6 {
			var scalar rune
			for _, c := range []byte(word[at : at+6]) {
				scalar <<= 4
				switch {
				case c >= '0' && c <= '9':
					scalar |= rune(c - '0')
				case c >= 'a' && c <= 'f':
					scalar |= rune(c-'a') + 10
				default:
					return 0, invalid("fragment_prefix_tokens")
				}
			}
			numeric = numeric && unicode.IsNumber(scalar)
			if !utf8.ValidRune(scalar) {
				return 0, invalid("fragment_prefix_tokens")
			}
		}
		if numeric && len(word)/6 > facettext.NumericSearchPrefixRunes {
			return 0, invalid("fragment_prefix_tokens")
		}
		if !charge(uint64(1 + min(facettext.IndexedSearchPrefixRunes, len(word)/6))) {
			return 0, invalid("fragment_token_work")
		}
		rest = tail
	}
	// At most one unicode61 token per scalar, with main plus three prefixes;
	// this does not assume Go and SQLite have the same Unicode word boundaries.
	for word := range strings.FieldsSeq(row.Lexical) {
		if !charge(4 * uint64(utf8.RuneCountInString(word))) {
			return 0, invalid("fragment_token_work")
		}
	}
	if !charge(uint64(max(0, utf8.RuneCountInString(row.Normalized)-2))) {
		return 0, invalid("fragment_token_work")
	}
	return work, nil
}
