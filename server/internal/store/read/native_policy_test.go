package read

import (
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

const nativeTestPolicy readPolicyID = 64997

func init() {
	// Test-only Reading domain. It conveys no production opt-in.
	registerReadPolicy(readPolicyRecipe{id: nativeTestPolicy, domain: "native-test-reading", kinds: []int64{31}, nativeSearch: true, validate: func(q browseQuery, in BrowseInput, _ browseFence) (string, error) {
		rows, err := q("native-test.policy", in.policy.subject)
		if err != nil {
			return "", err
		}
		if len(rows) != 1 || len(rows[0]) != 1 {
			return "", ErrDenied
		}
		witness, ok := rows[0][0].(string)
		if !ok {
			return "", ErrUnqualified
		}
		return witness, nil
	}})
	registerReadPolicy(readPolicyRecipe{id: 64996, domain: "native-test-source", kinds: []int64{32}, nativeSearch: true, validate: func(q browseQuery, in BrowseInput, _ browseFence) (string, error) {
		rows, err := q("full.native.item", in.policy.subject)
		if err != nil {
			return "", err
		}
		if len(rows) != 1 || len(rows[0]) != 19 {
			return "", ErrDenied
		}
		return fmt.Sprintf("source:%v", rows[0][10]), nil
	}})
	registerReadPolicy(readPolicyRecipe{id: 64995, domain: "native-test-byte-pair", kinds: []int64{33}, nativeSearch: true, validate: func(q browseQuery, in BrowseInput, _ browseFence) (string, error) {
		rows, err := q("rs.detail.facts", in.policy.subject, in.Library, in.Kind)
		if err != nil {
			return "", err
		}
		if len(rows) != 1 || len(rows[0]) != 47 || rows[0][0] != in.policy.subject {
			return "", ErrDenied
		}
		return fmt.Sprintf("facts:%d:%v", in.policy.subject, rows[0][5]), nil
	}})
}

func nativePolicyTestSource(id, kind int64) *nativeSource {
	r := []any{id, "sort", make([]byte, 16), "Title", int64(0), int64(1), int64(12), kind, int64(1), make([]byte, 16), int64(1), nil, nil, nil, nil, nil, nil, int64(2026), nil}
	return &nativeSource{key: search.FieldKey{Subject: search.ManifestEntity, SubjectID: id}, kind: kind, library: 1, item: r}
}

func TestNativeSearchPolicyBindsActualSubject(t *testing.T) {
	source := nativePolicyTestSource(44, 31)
	queries := 0
	q := func(name string, args ...any) ([][]any, error) {
		switch name {
		case "full.native.kind":
			return [][]any{{int64(31), "test-reading", int64(0)}}, nil
		case "native-test.policy":
			queries++
			if args[0] != int64(44) {
				t.Fatal("request subject reached validator", args)
			}
			return [][]any{{"current-source-witness"}}, nil
		}
		t.Fatal("unexpected SQL", name)
		return nil, ErrInput
	}
	in := BrowseInput{Library: 1, policy: readPolicyBinding{recipe: rsTestReadingAliasPolicy, subject: 999}}
	proof, _, allowed, err := nativePolicyForSource(q, in, browseFence{maximumAge: 18, allowUnrated: 1}, source)
	if err != nil || !allowed || queries != 1 || proof.binding.recipe != nativeTestPolicy || proof.binding.subject != 44 || !proof.binding.nativeSearch {
		t.Fatal("per-hit closed binding", proof, allowed, err)
	}
	if _, err := bindNativeSearchPolicy(30, 44); !errors.Is(err, ErrUnqualified) {
		t.Fatal("unregistered Reading kind enabled", err)
	}
	fallback, err := bindNativeSearchPolicy(1, 44)
	if err != nil || fallback.recipe != 0 || fallback.subject != 44 || !fallback.nativeSearch {
		t.Fatal("catalogue actual subject", fallback, err)
	}
}

func TestNativeSearchPolicyRestrictedContainerFailsClosed(t *testing.T) {
	source := nativePolicyTestSource(44, 2)
	q := func(string, ...any) ([][]any, error) { return [][]any{{int64(2), "show", int64(1)}}, nil }
	_, _, allowed, err := nativePolicyForSource(q, BrowseInput{Library: 1}, browseFence{maximumAge: 18, allowUnrated: 1}, source)
	if !errors.Is(err, ErrUnqualified) || allowed {
		t.Fatal("own-age authorized a container", allowed, err)
	}
}

func TestNativeSearchPolicyFreshWitnessAndBirth(t *testing.T) {
	source := nativePolicyTestSource(44, 31)
	witness := "before"
	denied := false
	q := func(name string, args ...any) ([][]any, error) {
		switch name {
		case "full.native.item":
			return [][]any{source.item}, nil
		case "full.native.kind":
			return [][]any{{int64(31), "test-reading", int64(0)}}, nil
		case "native-test.policy":
			if args[0] != int64(44) {
				t.Fatal("wrong final subject", args)
			}
			if denied {
				return nil, ErrDenied
			}
			return [][]any{{witness}}, nil
		}
		t.Fatal(name)
		return nil, ErrInput
	}
	in, f := BrowseInput{Library: 1}, browseFence{maximumAge: 18, allowUnrated: 1}
	proof, _, allowed, err := nativePolicyForSource(q, in, f, source)
	if err != nil || !allowed {
		t.Fatal(err)
	}
	if err = validateNativePolicy(q, in, f, proof); err != nil {
		t.Fatal("stable final witness", err)
	}
	witness = "after"
	if err = validateNativePolicy(q, in, f, proof); !errors.Is(err, ErrStale) {
		t.Fatal("policy drift escaped", err)
	}
	witness = "before"
	source.item[9] = []byte("different-birth!")
	if err = validateNativePolicy(q, in, f, proof); !errors.Is(err, ErrStale) {
		t.Fatal("equal-ID recreation escaped", err)
	}
	denied = true
	if err = validateNativePolicy(q, in, f, proof); !errors.Is(err, ErrDenied) {
		t.Fatal("policy denial lost", err)
	}
}

func TestNativeSearchCanonicalGroupFacts(t *testing.T) {
	source := nativePolicyTestSource(44, 1)
	source.item[1] = "Canonical key"
	source.item[17] = int64(1984)
	source.item[18] = "2026-11-07T00:00:00.000000000Z"
	var hit FullSearchHit
	if err := nativeHitFacts(&hit, source); err != nil || hit.SortKey != "Canonical key" || hit.Year != 1984 || hit.AddedAt != source.item[18] {
		t.Fatal("canonical facts", hit, err)
	}
	source.item[18] = nil
	if err := nativeHitFacts(&hit, source); err != nil || hit.AddedAt != "" {
		t.Fatal("NULL added_at", hit, err)
	}
}
