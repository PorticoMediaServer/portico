package metadata

import "testing"

func TestChoose(t *testing.T) {
	matrix := []Candidate{{Title: "The Matrix", Year: 1999}, {Title: "The Matrix Reloaded", Year: 2003}, {Title: "The Matrix Revisited", Year: 2001}}
	heat := []Candidate{{Title: "Heat", Year: 1995}, {Title: "Heat", Year: 1986}, {Title: "The Heat", Year: 2013}}
	amelie := []Candidate{{Title: "Amélie", OriginalTitle: "Le Fabuleux Destin d'Amélie Poulain", Year: 2001}}
	cases := []struct {
		name       string
		q          Query
		candidates []Candidate
		want       int
		reason     string
	}{
		{"same name and year", Query{Title: "The Matrix", Year: 1999}, matrix, 0, ""},
		{"a spelling slip", Query{Title: "The Matirx", Year: 1999}, matrix, 0, ""},
		{"no year in the names", Query{Title: "the matrix"}, matrix, 0, ""},
		{"filed a year late", Query{Title: "The Matrix", Year: 2000}, matrix, 0, ""},
		{"the wrong year is not the film", Query{Title: "The Matrix", Year: 2010}, matrix, -1, ReasonNoCloseMatch},
		{"the year tells two films apart", Query{Title: "Heat", Year: 1986}, heat, 1, ""},
		{"two films and no year", Query{Title: "Heat"}, heat, -1, ReasonSeveralTitles},
		{"a short name allows no slip", Query{Title: "Heap", Year: 1995}, heat, -1, ReasonNoCloseMatch},
		{"accents and the original title", Query{Title: "Le fabuleux destin d'Amelie Poulain", Year: 2001}, amelie, 0, ""},
		{"nothing found", Query{Title: "Zzyzx"}, nil, -1, ReasonNoResults},
		{"found by id", Query{Title: "Whatever"}, []Candidate{{Title: "The Matrix", Year: 1999, Exact: true}}, 0, ""},
	}
	for _, c := range cases {
		if got, reason := Choose(c.q, c.candidates); got != c.want || reason != c.reason {
			t.Errorf("%s: Choose = %d, %q; want %d, %q", c.name, got, reason, c.want, c.reason)
		}
	}
}
