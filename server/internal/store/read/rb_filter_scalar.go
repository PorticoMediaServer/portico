package read

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// This closed scalar subset is evaluated only AFTER bounded source retrieval.
// It uses the public parser and preserves SQL's three-valued group semantics.
// Unsupported fields refuse at compile time, including inside negated groups;
// they can never become a false predicate that NOT would turn into permission.
type rbScalarFilter struct{ node *catalog.BrowseNode }
type rbFilterFacts struct {
	title, library string
	kind, year     int64
}
type rbTruth uint8

const (
	rbUnknown rbTruth = iota
	rbFalse
	rbTrue
)

func compileRBScalarFilter(raw json.RawMessage) (rbScalarFilter, error) {
	node, err := catalog.ParseBrowseQuery(raw, "query.filter")
	if err != nil {
		return rbScalarFilter{}, ErrInput
	}
	count := 0
	var visit func(*catalog.BrowseNode, int) error
	visit = func(n *catalog.BrowseNode, depth int) error {
		if n == nil {
			return nil
		}
		count++
		if depth > 8 || count > 128 {
			return ErrInput
		}
		if n.Field != "" {
			switch n.Field {
			case "year", "decade", "title", "library", "entityKind":
			default:
				return ErrUnqualified
			}
			switch n.Operator {
			case "equals", "not-equals", "less-than", "at-most", "greater-than", "at-least", "between", "in", "not-in", "contains", "starts-with", "is-present", "is-missing":
			default:
				return ErrUnqualified
			}
			return nil
		}
		for i := range n.All {
			if err := visit(&n.All[i], depth+1); err != nil {
				return err
			}
		}
		for i := range n.Any {
			if err := visit(&n.Any[i], depth+1); err != nil {
				return err
			}
		}
		return visit(n.Not, depth+1)
	}
	if err = visit(node, 0); err != nil {
		return rbScalarFilter{}, err
	}
	return rbScalarFilter{node}, nil
}
func (p rbScalarFilter) matches(f rbFilterFacts) bool { return rbEvalScalar(p.node, f) == rbTrue }
func rbEvalScalar(n *catalog.BrowseNode, f rbFilterFacts) rbTruth {
	if n == nil {
		return rbTrue
	}
	if n.Not != nil {
		switch rbEvalScalar(n.Not, f) {
		case rbTrue:
			return rbFalse
		case rbFalse:
			return rbTrue
		default:
			return rbUnknown
		}
	}
	if len(n.All) > 0 {
		result := rbTrue
		for i := range n.All {
			v := rbEvalScalar(&n.All[i], f)
			if v == rbFalse {
				return rbFalse
			}
			if v == rbUnknown {
				result = rbUnknown
			}
		}
		return result
	}
	if len(n.Any) > 0 {
		result := rbFalse
		for i := range n.Any {
			v := rbEvalScalar(&n.Any[i], f)
			if v == rbTrue {
				return rbTrue
			}
			if v == rbUnknown {
				result = rbUnknown
			}
		}
		return result
	}
	var number float64
	var text string
	missing, numeric := false, false
	switch n.Field {
	case "year":
		number = float64(f.year)
		numeric = true
		missing = f.year == 0
	case "decade":
		number = float64(f.year / 10 * 10)
		numeric = true
		missing = f.year < 1800 || f.year > 2199
	case "title":
		text = sorttext.Fold(f.title)
	case "library":
		text = f.library // identity values are case-sensitive
	case "entityKind":
		switch f.kind {
		case 1:
			text = "movie"
		case 2:
			text = "show"
		case 3:
			text = "season"
		case 4:
			text = "episode"
		default:
			return rbUnknown
		}
	default:
		return rbUnknown
	}
	truth := func(v bool) rbTruth {
		if v {
			return rbTrue
		}
		return rbFalse
	}
	if n.Operator == "is-missing" {
		return truth(missing)
	}
	if n.Operator == "is-present" {
		return truth(!missing)
	}
	if missing {
		if n.Operator == "not-equals" || n.Operator == "not-in" {
			return rbTrue
		}
		return rbUnknown
	}
	compare := func(value any) (int, bool) {
		if numeric {
			var input float64
			var err error
			switch v := value.(type) {
			case json.Number:
				input, err = v.Float64()
			case string:
				input, err = strconv.ParseFloat(v, 64)
			case float64:
				input = v
			default:
				return 0, false
			}
			if err != nil {
				return 0, false
			}
			if number < input {
				return -1, true
			}
			if number > input {
				return 1, true
			}
			return 0, true
		}
		input, ok := value.(string)
		if !ok {
			return 0, false
		}
		if n.Field != "library" {
			input = sorttext.Fold(input)
		}
		return strings.Compare(text, input), true
	}
	if n.Operator == "between" {
		v, ok := n.Value.([]any)
		if !ok || len(v) != 2 {
			return rbUnknown
		}
		a, okA := compare(v[0])
		b, okB := compare(v[1])
		if !okA || !okB {
			return rbUnknown
		}
		return truth(a >= 0 && b <= 0)
	}
	if n.Operator == "in" || n.Operator == "not-in" {
		v, ok := n.Value.([]any)
		if !ok {
			return rbUnknown
		}
		found := false
		for _, x := range v {
			c, ok := compare(x)
			if !ok {
				return rbUnknown
			}
			if c == 0 {
				found = true
				break
			}
		}
		if n.Operator == "not-in" {
			found = !found
		}
		return truth(found)
	}
	if n.Operator == "contains" || n.Operator == "starts-with" {
		v, ok := n.Value.(string)
		if !ok || numeric {
			return rbUnknown
		}
		v = sorttext.Fold(v)
		if n.Operator == "starts-with" {
			return truth(strings.HasPrefix(text, v))
		}
		return truth(strings.Contains(text, v))
	}
	c, ok := compare(n.Value)
	if !ok {
		return rbUnknown
	}
	switch n.Operator {
	case "equals":
		return truth(c == 0)
	case "not-equals":
		return truth(c != 0)
	case "less-than":
		return truth(c < 0)
	case "at-most":
		return truth(c <= 0)
	case "greater-than":
		return truth(c > 0)
	case "at-least":
		return truth(c >= 0)
	}
	return rbUnknown
}
