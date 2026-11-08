package read

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Native closed projection of the published browse AST. General catalogue
// parsing stays outside native writer/plan composition; unsupported predicates
// refuse, including beneath NOT. Limits and scalar normalization match browse.
type rbFilterNode struct {
	Field, Operator string
	Value           any
	All, Any        []rbFilterNode
	Not             *rbFilterNode
}

func parseRBScalarNode(raw json.RawMessage) (*rbFilterNode, error) {
	return parseRBClosedNode(raw, rbParseScalarPredicate)
}

// The compiled caller chooses a closed predicate vocabulary. LC and saved-view
// scalar calls retain their original whitelist; personal fields are opt-in.
func parseRBClosedNode(raw json.RawMessage, predicate func(string, string, any) (*rbFilterNode, error)) (*rbFilterNode, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(raw) > 64<<10 || !rbJSONDepth(raw, 32) {
		return nil, ErrInput
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF {
		return nil, ErrInput
	}
	clauses := 0
	return rbParseClosedNode(value, 0, &clauses, predicate)
}

func rbParseClosedNode(value any, depth int, clauses *int, predicate func(string, string, any) (*rbFilterNode, error)) (*rbFilterNode, error) {
	if depth > 5 {
		return nil, ErrInput
	}
	o, ok := value.(map[string]any)
	if !ok {
		return nil, ErrInput
	}
	if len(o) == 0 {
		if depth == 0 {
			return nil, nil
		}
		return nil, ErrInput
	}
	variant, ok := o["variant"].(string)
	if !ok {
		return nil, ErrInput
	}
	if variant == "group" {
		if len(o) != 2 {
			return nil, ErrInput
		}
		if child, ok := o["not"]; ok {
			n, e := rbParseClosedNode(child, depth+1, clauses, predicate)
			if e != nil {
				return nil, e
			}
			if n == nil {
				return nil, ErrInput
			}
			return &rbFilterNode{Not: n}, nil
		}
		for _, key := range []string{"all", "any"} {
			if child, ok := o[key]; ok {
				list, ok := child.([]any)
				if !ok || len(list) == 0 || len(list) > 40 {
					return nil, ErrInput
				}
				parsed := make([]rbFilterNode, 0, len(list))
				for _, v := range list {
					n, e := rbParseClosedNode(v, depth+1, clauses, predicate)
					if e != nil {
						return nil, e
					}
					if n == nil {
						return nil, ErrInput
					}
					parsed = append(parsed, *n)
				}
				if key == "all" {
					return &rbFilterNode{All: parsed}, nil
				}
				return &rbFilterNode{Any: parsed}, nil
			}
		}
		return nil, ErrInput
	}
	if variant != "predicate" && variant != "relative" {
		return nil, ErrInput
	}
	if len(o) != 4 {
		return nil, ErrInput
	}
	field, ok := o["field"].(string)
	if !ok {
		return nil, ErrInput
	}
	op, ok := o["operator"].(string)
	if !ok {
		return nil, ErrInput
	}
	v, exists := o["value"]
	if !exists {
		return nil, ErrInput
	}
	if variant == "relative" || op == "within-last-days" {
		return nil, ErrUnqualified
	}
	n, err := predicate(field, op, v)
	if err != nil {
		return nil, err
	}
	*clauses++
	if *clauses > 40 {
		return nil, ErrInput
	}
	return n, nil
}

func rbParseScalarPredicate(field, op string, v any) (*rbFilterNode, error) {
	var operators []string
	switch field {
	case "year":
		operators = []string{"equals", "not-equals", "less-than", "at-most", "greater-than", "at-least", "between", "in", "not-in", "is-present", "is-missing"}
	case "decade":
		operators = []string{"equals", "not-equals", "in", "not-in", "between"}
	case "title":
		operators = []string{"equals", "not-equals", "contains", "starts-with", "in", "not-in", "is-present", "is-missing"}
	case "library", "entityKind":
		operators = []string{"equals", "not-equals", "in", "not-in"}
	default:
		return nil, ErrUnqualified
	}
	if !slices.Contains(operators, op) {
		return nil, ErrInput
	}
	if op == "is-present" || op == "is-missing" {
		if v != nil {
			return nil, ErrInput
		}
		return &rbFilterNode{Field: field, Operator: op}, nil
	}
	listOp := op == "between" || op == "in" || op == "not-in"
	list, isList := v.([]any)
	if listOp != isList {
		return nil, ErrInput
	}
	if isList {
		if len(list) == 0 || len(list) > 100 || op == "between" && len(list) != 2 {
			return nil, ErrInput
		}
		values := make([]any, len(list))
		for i, x := range list {
			var e error
			values[i], e = rbParseScalarValue(field, x)
			if e != nil {
				return nil, e
			}
		}
		v = values
	} else {
		var e error
		v, e = rbParseScalarValue(field, v)
		if e != nil {
			return nil, e
		}
	}
	return &rbFilterNode{Field: field, Operator: op, Value: v}, nil
}

func rbParseScalarValue(field string, v any) (any, error) {
	if field == "year" || field == "decade" {
		var s string
		switch x := v.(type) {
		case json.Number:
			s = x.String()
		case string:
			s = strings.TrimSpace(x)
		default:
			return nil, ErrInput
		}
		n, e := strconv.ParseFloat(s, 64)
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, ErrInput
		}
		return n, nil
	}
	var s string
	switch x := v.(type) {
	case string:
		s = x
	case json.Number:
		s = x.String()
	default:
		return nil, ErrInput
	}
	if strings.TrimSpace(s) == "" || len(s) > 500 || strings.ContainsAny(s, "\x00\r\n") {
		return nil, ErrInput
	}
	if field == "entityKind" && !slices.Contains([]string{"movie", "episode", "show", "season", "song", "album", "artist", "audiobook_file", "book", "author", "collection"}, s) {
		return nil, ErrInput
	}
	return s, nil
}
