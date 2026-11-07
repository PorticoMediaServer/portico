package read

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func TestNativePreviewProofIsNotAnExactPopulation(t *testing.T) {
	s := fullScope(t, "the matrix")
	s.preview = true
	field := fieldProof(t, s, search.ManifestEntity, 10, 1, "The Matrix")
	key := fullResultKey{search.ManifestEntity, 10, 1}
	lead := nativeSearchLead{preview: s.terms[0]}
	proof, match, err := lead.prove(s, key, []candidateSource{{proof: field, own: true}}, true)
	if err != nil || !match || proof.tier != 0 {
		t.Fatal("preview lost verified title", proof, match, err)
	}
	if _, _, err = proveCandidate(s, key, exactTermPopulation{scope: s, term: s.terms[0]}, []candidateSource{{proof: field, own: true}}, true); !errors.Is(err, ErrInput) {
		t.Fatal("preview minted exact evidence", err)
	}
	incomplete := fieldProof(t, s, search.ManifestEntity, 10, 1, "The")
	if _, match, err = lead.prove(s, key, []candidateSource{{proof: incomplete, own: true}}, true); err != nil || match {
		t.Fatal("budget substituted for full conjunction", match, err)
	}
	s.preview = false
	if _, _, err = lead.prove(s, key, []candidateSource{{proof: field, own: true}}, true); !errors.Is(err, ErrInput) {
		t.Fatal("full view borrowed preview proof", err)
	}
}

func TestNativePreviewPartialNeverClaimsExactOrZeroCount(t *testing.T) {
	if _, err := ownFullSearchPage(FullSearchPage{Disposition: SearchResults, Partial: true, CountStatus: SearchCountExact}, 20); !errors.Is(err, ErrInput) {
		t.Fatal("partial exact count", err)
	}
	if _, err := ownFullSearchPage(FullSearchPage{Disposition: SearchResults, Partial: true, Exhausted: true}, 20); !errors.Is(err, ErrInput) {
		t.Fatal("partial exhaustion", err)
	}
	if _, err := ownFullSearchPage(FullSearchPage{Disposition: SearchResults, Partial: true, CountStatus: SearchCountUnknown}, 20); err != nil {
		t.Fatal("unknown is not exact zero", err)
	}
}
