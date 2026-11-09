package read

// Authored collection and playlist windows are reduced in bounded quanta. The
// collection quantum matches the merged RS complete authored-collection reader
// (sixteen raw positions); a playlist uses the common raw window. Both bind
// cap+1 and refuse a longer answer, so a source that ignored its bound cannot
// truncate or overrun the reduction.
const (
	rbGroupLiftCollectionRaw = 16
	rbGroupLiftPlaylistRaw   = MaxRaw
	// rbGroupLiftQuantaPerAdvance bounds one poll to a few snapshots. The
	// original preparation deadline still bounds the whole job.
	rbGroupLiftQuantaPerAdvance = 8
	// rbGroupLiftMaxQueries is the finite declared preparation recipe: the
	// common fence, the accepted root/automatic proof, both same-snapshot byte
	// legs, and the bounded RS member-eligibility points for one collection
	// window. It is never inferred from a request.
	rbGroupLiftMaxQueries = 96
	// rbGroupLiftPageMaxQueries is the page recipe only: the common fence, the
	// authored root/automatic/publication proof and the optional publication
	// point. It stays below the common 64-raw request budget; only Advance needs
	// the full 96-query preparation forecast.
	rbGroupLiftPageMaxQueries = 16
	// rbGroupLiftFixedRowBytes is the fixed per-row decode allowance for the
	// narrow typed lift rows that share the payload window with measured order
	// keys. Wide entity strings are not read by this recipe.
	rbGroupLiftFixedRowBytes = 512
	// rbGroupLiftMetadataBytes funds the typed root/cursor values retained for
	// the whole job. It is reserved before the deduplicated ID vector.
	rbGroupLiftMetadataBytes = 8192
	// rbGroupLiftKeepBytes charges one kept public entity: its eight-byte ID
	// plus the private first-occurrence index entry. Capacity is refused before
	// growth, never truncated.
	rbGroupLiftKeepBytes = 64
	// rbGroupLiftMaxKey bounds one authored order key. The cursor retains one
	// key between quanta; a longer pathological key is refused.
	rbGroupLiftMaxKey = 4096
	// rbGroupLiftMaxResults is the published page bound, matching the existing
	// browse window contract.
	rbGroupLiftMaxResults = 200
)

// rbGroupLiftKeyBytes preflights the measured order-key lengths of one octet
// leg. Each answer names its raw member/entry identity and one actual key
// length; anything else is a torn source, never a partial window.
func rbGroupLiftKeyBytes(sizes [][]any) (int64, error) {
	var total int64
	for _, row := range sizes {
		if len(row) != 2 {
			return 0, ErrUnqualified
		}
		n, ok := row[1].(int64)
		if !ok || n < 0 || n > rbGroupLiftMaxKey {
			return 0, ErrUnqualified
		}
		total += n
	}
	return total, nil
}
