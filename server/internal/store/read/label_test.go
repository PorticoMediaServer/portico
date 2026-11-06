package read

import (
	"errors"
	"strings"
	"testing"
)

func TestLabelComparisonRulingVectors(t *testing.T) {
	for _, policy := range []LabelPolicy{ProfileLabel, AccountLabel} {
		for _, tt := range []struct {
			values []string
			key    string
		}{
			{[]string{"Horror", "horror", "HORROR", "  Horror ", "\u2003Horror\u00a0"}, "horror"},
			{[]string{"Straße", "STRASSE", "strasse"}, "strasse"},
			{[]string{"ΣΊΣΥΦΟΣ", "σίσυφος", "σίσυφοσ"}, "σίσυφοσ"},
			{[]string{"é", "e\u0301"}, "é"},
			{[]string{"İ"}, "i\u0307"},
			{[]string{"a|b"}, "a|b"},
		} {
			for _, value := range tt.values {
				got, err := LabelKey(value, policy)
				if err != nil || got != tt.key {
					t.Fatalf("policy%d %q: %q %v want%q", policy, value, got, err, tt.key)
				}
			}
		}
		for _, pair := range [][2]string{{"İ", "i"}, {"ＡＢＣ", "ABC"}, {"sci fi", "sci-fi"}, {"sci fi", "scifi"}, {"sci-fi", "scifi"}, {"a|b", "a"}, {"é", "e"}, {"sci  fi", "sci fi"}} {
			a, e1 := LabelKey(pair[0], policy)
			b, e2 := LabelKey(pair[1], policy)
			if e1 != nil || e2 != nil || a == b {
				t.Fatalf("distinct labels merged: %q %q ->%q %q", pair[0], pair[1], a, b)
			}
		}
	}
}

func TestLabelValidationPrecedesTrimmingAndFolding(t *testing.T) {
	for _, policy := range []LabelPolicy{ProfileLabel, AccountLabel} {
		for _, raw := range []string{"", " \u2003 ", "\xff", "\x00Horror", "\tHorror\n", strings.Repeat(" ", 120) + "a", strings.Repeat("a", 121)} {
			if key, err := LabelKey(raw, policy); !errors.Is(err, ErrLabel) || key != "" {
				t.Fatalf("policy%d invalid admitted: %q ->%q %v", policy, raw, key, err)
			}
		}
	}
	//120 four-byte Unicode scalars is the account limit, not120bytes.
	value := strings.Repeat("𠀀", 120)
	if _, err := LabelKey(value, AccountLabel); err != nil {
		t.Fatal("account scalar limit was tightened", err)
	}
	if _, err := LabelKey(value, ProfileLabel); !errors.Is(err, ErrLabel) {
		t.Fatal("profile byte bound ignored")
	}
	if _, err := LabelKey(strings.Repeat("𠀀", 30), ProfileLabel); err != nil {
		t.Fatal("profile byte boundary rejected", err)
	}
	if _, err := LabelKey(strings.Repeat("𠀀", 121), AccountLabel); !errors.Is(err, ErrLabel) {
		t.Fatal("account scalar overflow")
	}
	for _, r := range []string{"\x7f", "\u200b", "\u2066"} {
		if _, err := LabelKey("a"+r, AccountLabel); !errors.Is(err, ErrLabel) {
			t.Fatal("account control/format accepted")
		}
	}
	//NFC must not make an oversized raw input admissible after composition.
	if _, err := LabelKey(strings.Repeat("e\u0301", 41), ProfileLabel); !errors.Is(err, ErrLabel) {
		t.Fatal("normalized before profile byte admission")
	}
	if _, err := LabelKey("a", 0); !errors.Is(err, ErrLabel) {
		t.Fatal("unknown policy accepted")
	}
}
