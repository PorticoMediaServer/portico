package read

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRBScalarFilterMissingAndNegation(t *testing.T) {
	for _, test := range []struct {
		raw  string
		year int64
		want bool
	}{
		{`{"variant":"predicate","field":"year","operator":"not-equals","value":2000}`, 0, true},
		{`{"variant":"group","not":{"variant":"predicate","field":"year","operator":"equals","value":2000}}`, 0, false},
		{`{"variant":"group","not":{"variant":"predicate","field":"year","operator":"equals","value":2000}}`, 2001, true},
		{`{"variant":"predicate","field":"decade","operator":"not-in","value":["1990","2000"]}`, 1700, true},
		{`{"variant":"predicate","field":"year","operator":"between","value":["1990","1999"]}`, 1999, true},
		{`{"variant":"predicate","field":"year","operator":"is-missing","value":null}`, 0, true},
	} {
		p, err := compileRBScalarFilter(json.RawMessage(test.raw))
		if err != nil {
			t.Fatal(err)
		}
		if got := p.matches(rbFilterFacts{year: test.year}); got != test.want {
			t.Fatal(test.raw, test.year, got)
		}
	}
}
func TestRBScalarFilterRejectsUnsupportedUnderNot(t *testing.T) {
	p, err := compileRBScalarFilter(json.RawMessage(`{"variant":"group","not":{"variant":"predicate","field":"genre","operator":"equals","value":"Horror"}}`))
	if !errors.Is(err, ErrUnqualified) || p.node != nil {
		t.Fatal("unsupported field became a negated match", p, err)
	}
}
func TestRBScalarFilterUsesPublicFoldAndLiteralContains(t *testing.T) {
	p, err := compileRBScalarFilter(json.RawMessage(`{"variant":"predicate","field":"title","operator":"contains","value":"é_%"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p.matches(rbFilterFacts{title: "É_% literal"}) || p.matches(rbFilterFacts{title: "E-anything"}) {
		t.Fatal("literal wildcards or public fold changed")
	}
	p, err = compileRBScalarFilter(json.RawMessage(`{"variant":"predicate","field":"library","operator":"equals","value":"Movies"}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.matches(rbFilterFacts{library: "movies"}) {
		t.Fatal("identity comparison folded")
	}
}
