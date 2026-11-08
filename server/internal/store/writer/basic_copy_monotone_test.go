package writer

import (
	"errors"
	"testing"
)

func TestBasicCopyCursorIsMonotoneInTheFramework(t *testing.T) {
	at := func(family int64, entry basicCopyCursor) basicCopyTurn { return basicCopyTurn{family: family, entry: entry} }
	k := func(family, phase, id, aux int64, key string) basicCopyCursor {
		return basicCopyCursor{family: family, phase: phase, id: id, aux: aux, key: key}
	}
	for name, tc := range map[string]struct {
		c     basicCopyTurn
		phase int64
		next  basicCopyCursor
		ok    bool
	}{
		"family1_to_2":            {at(1, basicCopyCursor{}), 2, basicCopyCursor{family: 2}, true},
		"family1_in_family":       {at(1, basicCopyCursor{}), 2, k(1, 0, 3, 0, ""), false},
		"family1_skip_to_3":       {at(1, basicCopyCursor{}), 2, basicCopyCursor{family: 3}, false},
		"family1_finish_early":    {at(1, basicCopyCursor{}), 3, basicCopyCursor{}, false},
		"install_forward":         {at(2, k(2, 0, 3, 0, "")), 2, k(2, 0, 7, 0, ""), true},
		"install_same":            {at(2, k(2, 0, 3, 0, "")), 2, k(2, 0, 3, 0, ""), false},
		"install_backward":        {at(2, k(2, 0, 7, 0, "")), 2, k(2, 0, 3, 0, ""), false},
		"install_to_cleanup":      {at(2, k(2, 0, 9, 0, "")), 2, k(2, 1, 0, 0, ""), true},
		"cleanup_to_install":      {at(2, k(2, 1, 4, 1, "")), 2, k(2, 0, 9, 0, ""), false},
		"cleanup_key_forward":     {at(2, k(2, 1, 4, 1, "")), 2, k(2, 1, 9, 1, ""), true},
		"cleanup_first_key":       {at(2, k(2, 1, 0, 0, "")), 2, k(2, 1, 5, 1, ""), true},
		"text_key_forward":        {at(8, k(8, 1, 0, 1, "sub-a")), 2, k(8, 1, 0, 1, "sub-b"), true},
		"text_key_backward":       {at(8, k(8, 1, 0, 1, "sub-b")), 2, k(8, 1, 0, 1, "sub-a"), false},
		"next_family":             {at(5, k(5, 1, 3, 1, "")), 2, basicCopyCursor{family: 6}, true},
		"next_family_dirty":       {at(5, k(5, 1, 3, 1, "")), 2, k(6, 0, 1, 0, ""), false},
		"other_family_checkpoint": {at(5, k(5, 0, 3, 0, "")), 2, k(4, 0, 9, 0, ""), false},
		"family9_complete":        {at(9, basicCopyCursor{family: 9}), 3, basicCopyCursor{}, true},
		"family9_to_10":           {at(9, basicCopyCursor{family: 9}), 2, basicCopyCursor{family: 10}, false},
		"family8_complete":        {at(8, k(8, 1, 0, 1, "x")), 3, basicCopyCursor{}, false},
		"complete_dirty":          {at(9, basicCopyCursor{family: 9}), 3, basicCopyCursor{family: 9}, false},
		"phase_four":              {at(9, basicCopyCursor{family: 9}), 4, basicCopyCursor{}, false},
		"no_family":               {at(0, basicCopyCursor{}), 2, basicCopyCursor{family: 1}, false},
		"aux_two":                 {at(2, k(2, 1, 4, 1, "")), 2, k(2, 1, 9, 2, ""), false},
	} {
		if got := basicCopyStepAllowed(tc.c, tc.phase, tc.next); got != tc.ok {
			t.Fatal(name, "got", got, "want", tc.ok)
		}
	}
	// A refused step never reaches SQL: no turn is needed to refuse it.
	if err := advanceBasicCopyCursor(t.Context(), nil, at(1, basicCopyCursor{}), 3, basicCopyCursor{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("early completion reached the cursor CAS", err)
	}
	// The entry must belong to the turn's family.
	if err := checkBasicCopyEntry(t.Context(), nil, basicCopyTurn{family: 3, changeRevision: 2, entityID: 31, shapeID: 61, entry: basicCopyCursor{family: 2}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign entry family admitted", err)
	}
}
