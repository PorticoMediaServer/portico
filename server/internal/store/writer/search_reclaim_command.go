package writer

import (
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
)

type searchFragmentReclaimRequest struct {
	variant, bankKey, engineVersion string
	snapshot                        searchFragmentReclaimSnapshot
}
type searchFragmentReclaimCommand struct {
	identity CommandIdentity
	digest   RequestDigest
	request  searchFragmentReclaimRequest
	valid    bool
}

const searchFragmentReclaimFixedBytes = 8192 // complete fixed-wire ceiling, shared-encoder tested

func searchReclaimTexts(r *searchFragmentReclaimRequest) []*string {
	f := &r.snapshot.fragment
	return []*string{&r.variant, &r.bankKey, &r.engineVersion, &r.snapshot.owner.phase, &f.row.Revision, &f.row.Prefix, &f.row.Lexical, &f.row.Normalized}
}
func searchReclaimPreparationBytes(r searchFragmentReclaimRequest) (uint64, error) {
	wire := uint64(searchFragmentReclaimFixedBytes)
	for _, p := range searchReclaimTexts(&r) {
		if uint64(len(*p)) > maxCanonicalRequestBytes-wire {
			return 0, invalid("reclaim_request_bytes")
		}
		wire += uint64(len(*p))
	}
	return 65536 + 4*wire, nil
}
func newSearchFragmentReclaimCommand(identity CommandIdentity, r searchFragmentReclaimRequest) (*searchFragmentReclaimCommand, error) {
	if _, err := searchReclaimPreparationBytes(r); err != nil {
		return nil, err
	}
	if r.variant != "fragment_reclaim" || r.bankKey != "v1" || r.engineVersion != searchpolicy.EngineVersion {
		return nil, ErrInvalid
	}
	// FIRST field-1 entity rows have the same finite TEXT envelope as their
	// producing command. Bytes remain opaque (for example "09"), never parsed.
	if len(r.snapshot.fragment.row.Revision) > searchFirstRevisionBytes {
		return nil, invalid("reclaim_first_revision_bytes")
	}
	if _, err := planSearchFragmentReclaim(r.snapshot, identity); err != nil {
		return nil, err
	}
	f, _, err := prepareSearchRetainedFragment(r.snapshot.fragment)
	if err != nil {
		return nil, err
	}
	r.snapshot.fragment = f
	for _, p := range searchReclaimTexts(&r) {
		if !utf8.ValidString(*p) {
			return nil, ErrInvalid
		}
		*p = strings.Clone(*p)
	}
	digest, err := canonicalRequestDigest(SearchFragmentReclaim, 1, identity, r)
	if err != nil {
		return nil, err
	}
	return &searchFragmentReclaimCommand{identity, digest, r, true}, nil
}
