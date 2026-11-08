package read

import "encoding/json"

// Values only: the caller must separately establish the current viewer,
// eligible item identity and sparse personal row, and revalidate publication.
// This kernel is not a read port or an authority capability.
type rbPersonalFilterFacts struct {
	present, favorite, watchlisted bool
	rating                         *int64
}
type rbPersonalFilter struct{ node *rbFilterNode }

func compileRBPersonalFilter(raw json.RawMessage) (rbPersonalFilter, error) {
	n, err := parseRBClosedNode(raw, rbParsePersonalPredicate)
	return rbPersonalFilter{n}, err
}

func rbParsePersonalPredicate(field, op string, value any) (*rbFilterNode, error) {
	switch field {
	case "favorite", "watchlisted":
		if op != "equals" && op != "not-equals" {
			return nil, ErrInput
		}
		v, ok := value.(bool)
		if !ok {
			return nil, ErrInput
		}
		return &rbFilterNode{Field: field, Operator: op, Value: v}, nil
	case "personalRating":
		// The public rating and year wire numbers share finite normalization
		// and operator shapes; rating truth is evaluated separately below.
		n, err := rbParseScalarPredicate("year", op, value)
		if err != nil {
			return nil, err
		}
		n.Field = field
		return n, nil
	default:
		return nil, ErrUnqualified
	}
}

func (p rbPersonalFilter) matches(f rbPersonalFilterFacts) bool { return rbEvalPersonal(p.node, f) }

func rbEvalPersonal(n *rbFilterNode, f rbPersonalFilterFacts) bool {
	if n == nil {
		return true
	}
	if n.Not != nil {
		return !rbEvalPersonal(n.Not, f)
	}
	if len(n.All) != 0 {
		for i := range n.All {
			if !rbEvalPersonal(&n.All[i], f) {
				return false
			}
		}
		return true
	}
	if len(n.Any) != 0 {
		for i := range n.Any {
			if rbEvalPersonal(&n.Any[i], f) {
				return true
			}
		}
		return false
	}
	if n.Field == "favorite" || n.Field == "watchlisted" {
		flag := f.favorite
		if n.Field == "watchlisted" {
			flag = f.watchlisted
		}
		positive := (n.Operator == "equals") == n.Value.(bool)
		found := f.present && flag
		if positive {
			return found
		}
		return !found
	}
	// Each positive personal rating atom is EXISTS over the actual sparse row.
	// Missing rows/NULL ratings make that EXISTS false. Negative operators
	// negate that result, rather than comparing a fabricated default rating.
	negative := n.Operator == "not-equals" || n.Operator == "not-in" || n.Operator == "is-missing"
	found := false
	if f.present && f.rating != nil {
		v := float64(*f.rating) // canonical rating is an integer in [0,10]
		switch n.Operator {
		case "is-present", "is-missing":
			found = true
		case "equals", "not-equals":
			found = v == n.Value.(float64)
		case "less-than":
			found = v < n.Value.(float64)
		case "at-most":
			found = v <= n.Value.(float64)
		case "greater-than":
			found = v > n.Value.(float64)
		case "at-least":
			found = v >= n.Value.(float64)
		case "between":
			values := n.Value.([]any)
			found = v >= values[0].(float64) && v <= values[1].(float64)
		case "in", "not-in":
			for _, value := range n.Value.([]any) {
				if v == value.(float64) {
					found = true
					break
				}
			}
		}
	}
	if negative {
		return !found
	}
	return found
}
