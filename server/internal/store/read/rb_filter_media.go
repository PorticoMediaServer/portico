package read

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// The accepted series projection RS task 13 consumes is a different, book-level
// fact. Until IA/IP publish it, a series predicate for a non-Reading title is an
// explicit pending result, never an exact empty one and never raw tags.
var errRBMediaPending = errors.New("rb media filter: required accepted source is not adopted")
var (
	errRBMediaSeriesPending      = fmt.Errorf("%w: accepted book series projection not adopted", errRBMediaPending)
	errRBMediaNarratorPending    = fmt.Errorf("%w: accepted narrator projection not adopted", errRBMediaPending)
	errRBMediaAudioAuthorPending = fmt.Errorf("%w: audiobook author requires the accepted book projection", errRBMediaPending)
)

// Availability states published by the browse contract.
const (
	rbMediaAvailable   = "available"
	rbMediaPartial     = "partial"
	rbMediaUnavailable = "unavailable"
)

var rbMediaEnumOperators = []string{"equals", "not-equals", "in", "not-in"}
var rbMediaNumberOperators = []string{"equals", "not-equals", "less-than", "at-most", "greater-than", "at-least", "between", "in", "not-in", "is-present", "is-missing"}
var rbMediaSetOperators = []string{"contains", "contains-any", "contains-all", "equals", "not-equals", "starts-with", "in", "not-in", "is-present", "is-missing"}

// Values only, like the other closed filter kernels. The caller owns current
// eligibility, snapshot custody and source freshness.
type rbMediaFilter struct{ node *rbFilterNode }

type rbMediaFilterFacts struct {
	Kind          int64
	Availability  string
	Versions      int64
	DiscNumber    int64
	Resolutions   []string
	DynamicRanges []string
	AudioLanguages,
	SubtitleLanguages []string
	// Durations are accepted asset seconds, compared against a whole-millisecond
	// request after the same /1000 normalization the public compiler uses.
	Durations []float64
	Author,
	Series *string
}

func compileRBMediaFilter(raw json.RawMessage) (rbMediaFilter, error) {
	n, err := parseRBClosedNode(raw, rbParseMediaPredicate)
	if err != nil {
		return rbMediaFilter{}, err
	}
	return rbMediaFilter{n}, nil
}

func rbParseMediaPredicate(field, op string, value any) (*rbFilterNode, error) {
	switch field {
	case "narrator":
		return nil, errRBMediaNarratorPending
	case "availability", "resolution", "dynamicRange", "audioLanguage", "subtitleLanguage", "author", "series", "durationMs", "versions", "discNumber":
	default:
		return nil, ErrUnqualified
	}
	operators := rbMediaNumberOperators
	switch field {
	case "availability", "resolution":
		operators = rbMediaEnumOperators
	case "dynamicRange", "audioLanguage", "subtitleLanguage", "author", "series":
		operators = rbMediaSetOperators
	}
	if !slices.Contains(operators, op) {
		return nil, ErrInput
	}
	if op == "is-present" || op == "is-missing" {
		if value != nil {
			return nil, ErrInput
		}
		return &rbFilterNode{Field: field, Operator: op}, nil
	}
	listOp := slices.Contains([]string{"between", "in", "not-in", "contains-any", "contains-all"}, op)
	list, isList := value.([]any)
	if listOp != isList {
		return nil, ErrInput
	}
	if isList {
		if len(list) == 0 || len(list) > 100 || op == "between" && len(list) != 2 {
			return nil, ErrInput
		}
		values := make([]any, len(list))
		for i, x := range list {
			v, err := rbParseMediaValue(field, x)
			if err != nil {
				return nil, err
			}
			values[i] = v
		}
		return &rbFilterNode{Field: field, Operator: op, Value: values}, nil
	}
	v, err := rbParseMediaValue(field, value)
	if err != nil {
		return nil, err
	}
	return &rbFilterNode{Field: field, Operator: op, Value: v}, nil
}

func rbParseMediaValue(field string, value any) (any, error) {
	if field == "availability" {
		s, ok := rbMediaTextValue(value)
		if !ok || !slices.Contains([]string{rbMediaAvailable, rbMediaPartial, rbMediaUnavailable}, s) {
			return nil, ErrInput
		}
		return s, nil
	}
	if field == "resolution" {
		s, ok := rbMediaTextValue(value)
		if !ok || !slices.Contains([]string{"sd", "720p", "1080p", "4k"}, s) {
			return nil, ErrInput
		}
		return s, nil
	}
	switch field {
	case "durationMs", "versions", "discNumber":
		var s string
		switch x := value.(type) {
		case json.Number:
			s = x.String()
		case string:
			s = strings.TrimSpace(x)
		default:
			return nil, ErrInput
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, ErrInput
		}
		if field == "durationMs" {
			// The published field is whole milliseconds; asset duration is seconds.
			if n < 0 || n > 9007199254740991 || math.Trunc(n) != n {
				return nil, ErrInput
			}
			return n / 1000, nil
		}
		return n, nil
	}
	s, ok := rbMediaTextValue(value)
	if !ok {
		return nil, ErrInput
	}
	return s, nil
}

func rbMediaTextValue(value any) (string, bool) {
	var s string
	switch x := value.(type) {
	case string:
		s = x
	case json.Number:
		s = x.String()
	default:
		return "", false
	}
	if strings.TrimSpace(s) == "" || len(s) > 500 || strings.ContainsAny(s, "\x00\r\n") {
		return "", false
	}
	return s, true
}

// applicable refuses a field that cannot describe this title's kind. A refusal
// is not a false predicate: an unadopted projection keeps its named pending
// reason, so a caller can never publish an empty result as an exact answer.
func (f rbMediaFilter) applicable(kind int64) error {
	var walk func(*rbFilterNode) error
	walk = func(n *rbFilterNode) error {
		if n == nil {
			return nil
		}
		if n.Field != "" {
			switch n.Field {
			case "availability", "versions":
				if !slices.Contains([]int64{1, 4, 7, 9, 20, 21}, kind) {
					return ErrUnqualified
				}
			case "resolution", "dynamicRange", "subtitleLanguage":
				if kind != 1 && kind != 4 {
					return ErrUnqualified
				}
			case "audioLanguage", "durationMs":
				if !slices.Contains([]int64{1, 4, 7, 9}, kind) {
					return ErrUnqualified
				}
			case "discNumber":
				if kind != 7 && kind != 9 {
					return ErrUnqualified
				}
			case "author":
				if kind != 20 && kind != 21 {
					if kind == 8 || kind == 9 {
						return errRBMediaAudioAuthorPending
					}
					return ErrUnqualified
				}
			case "series":
				if kind != 20 && kind != 21 {
					if kind == 8 || kind == 9 {
						return errRBMediaSeriesPending
					}
					return ErrUnqualified
				}
			default:
				return ErrUnqualified
			}
			return nil
		}
		for i := range n.All {
			if err := walk(&n.All[i]); err != nil {
				return err
			}
		}
		for i := range n.Any {
			if err := walk(&n.Any[i]); err != nil {
				return err
			}
		}
		return walk(n.Not)
	}
	return walk(f.node)
}

func (f rbMediaFilter) matches(x rbMediaFilterFacts) bool { return rbEvalMedia(f.node, x) == rbTrue }

func rbEvalMedia(n *rbFilterNode, f rbMediaFilterFacts) rbTruth {
	if n == nil {
		return rbTrue
	}
	if n.Not != nil {
		return rbMediaNegate(rbEvalMedia(n.Not, f))
	}
	if len(n.All) > 0 {
		result := rbTrue
		for i := range n.All {
			switch rbEvalMedia(&n.All[i], f) {
			case rbFalse:
				return rbFalse
			case rbUnknown:
				result = rbUnknown
			}
		}
		return result
	}
	if len(n.Any) > 0 {
		result := rbFalse
		for i := range n.Any {
			switch rbEvalMedia(&n.Any[i], f) {
			case rbTrue:
				return rbTrue
			case rbUnknown:
				result = rbUnknown
			}
		}
		return result
	}
	switch n.Field {
	case "availability":
		values := []string{}
		if f.Availability != "" {
			values = append(values, f.Availability)
		}
		return rbMediaSetTruth(n, values, false)
	case "resolution":
		return rbMediaSetTruth(n, f.Resolutions, false)
	case "dynamicRange":
		return rbMediaSetTruth(n, f.DynamicRanges, true)
	case "audioLanguage":
		return rbMediaSetTruth(n, f.AudioLanguages, true)
	case "subtitleLanguage":
		return rbMediaSetTruth(n, f.SubtitleLanguages, true)
	case "durationMs":
		return rbMediaDurationsTruth(n, f.Durations)
	case "versions":
		return rbMediaNumberTruth(n, float64(f.Versions))
	case "discNumber":
		return rbMediaNumberTruth(n, float64(f.DiscNumber))
	case "author":
		return rbMediaTextTruth(n, f.Author, true)
	case "series":
		return rbMediaTextTruth(n, f.Series, true)
	}
	return rbUnknown
}

func rbMediaTruth(v bool) rbTruth {
	if v {
		return rbTrue
	}
	return rbFalse
}

func rbMediaSetTruth(n *rbFilterNode, values []string, fold bool) rbTruth {
	canon := func(s string) string {
		if fold {
			return sorttext.Fold(s)
		}
		return s
	}
	have := make([]string, len(values))
	for i, v := range values {
		have[i] = canon(v)
	}
	want := func(x any) (string, bool) {
		s, ok := x.(string)
		if !ok {
			return "", false
		}
		return canon(s), true
	}
	switch n.Operator {
	case "is-present":
		return rbMediaTruth(len(have) > 0)
	case "is-missing":
		return rbMediaTruth(len(have) == 0)
	case "contains-all":
		list, ok := n.Value.([]any)
		if !ok || len(list) == 0 {
			return rbUnknown
		}
		for _, x := range list {
			v, ok := want(x)
			if !ok {
				return rbUnknown
			}
			if !slices.Contains(have, v) {
				return rbFalse
			}
		}
		return rbTrue
	}
	var wanted []string
	switch n.Operator {
	case "equals", "contains", "not-equals":
		v, ok := want(n.Value)
		if !ok {
			return rbUnknown
		}
		wanted = []string{v}
	case "starts-with":
		v, ok := want(n.Value)
		if !ok {
			return rbUnknown
		}
		wanted = []string{v}
	case "in", "contains-any", "not-in":
		values, ok := n.Value.([]any)
		if !ok {
			return rbUnknown
		}
		for _, x := range values {
			v, ok := want(x)
			if !ok {
				return rbUnknown
			}
			wanted = append(wanted, v)
		}
	default:
		return rbUnknown
	}
	hit := false
	for _, v := range have {
		for _, w := range wanted {
			if n.Operator == "starts-with" && strings.HasPrefix(v, w) || n.Operator != "starts-with" && v == w {
				hit = true
				break
			}
		}
		if hit {
			break
		}
	}
	switch n.Operator {
	case "not-equals", "not-in":
		return rbMediaTruth(!hit)
	}
	return rbMediaTruth(hit)
}

func rbMediaDurationsTruth(n *rbFilterNode, values []float64) rbTruth {
	if n.Operator == "is-present" {
		return rbMediaTruth(len(values) > 0)
	}
	if n.Operator == "is-missing" {
		return rbMediaTruth(len(values) == 0)
	}
	compare := func(v float64, x any) (int, bool) {
		want, ok := x.(float64)
		if !ok {
			return 0, false
		}
		switch {
		case v < want:
			return -1, true
		case v > want:
			return 1, true
		}
		return 0, true
	}
	hit := func(x any) rbTruth {
		if len(values) == 0 {
			return rbFalse
		}
		unknown := false
		for _, v := range values {
			c, ok := compare(v, x)
			if !ok {
				unknown = true
				continue
			}
			if rbMediaNumericOperator(n.Operator, c) {
				return rbTrue
			}
		}
		if unknown {
			return rbUnknown
		}
		return rbFalse
	}
	switch n.Operator {
	case "equals", "not-equals":
		if n.Operator == "not-equals" {
			return rbMediaNegate(hit(n.Value))
		}
		return hit(n.Value)
	case "less-than", "at-most", "greater-than", "at-least":
		return hit(n.Value)
	case "between":
		list, ok := n.Value.([]any)
		if !ok || len(list) != 2 {
			return rbUnknown
		}
		if len(values) == 0 {
			return rbFalse
		}
		unknown := false
		for _, v := range values {
			low, okLow := compare(v, list[0])
			high, okHigh := compare(v, list[1])
			if !okLow || !okHigh {
				unknown = true
				continue
			}
			if low >= 0 && high <= 0 {
				return rbTrue
			}
		}
		if unknown {
			return rbUnknown
		}
		return rbFalse
	case "in", "not-in":
		list, ok := n.Value.([]any)
		if !ok {
			return rbUnknown
		}
		found := false
		for _, x := range list {
			if hit(x) == rbTrue {
				found = true
				break
			}
		}
		if n.Operator == "not-in" {
			return rbMediaTruth(!found)
		}
		return rbMediaTruth(found)
	}
	return rbUnknown
}

func rbMediaNumberTruth(n *rbFilterNode, v float64) rbTruth {
	switch n.Operator {
	case "is-present":
		return rbTrue
	case "is-missing":
		return rbFalse
	}
	compare := func(x any) (int, bool) {
		want, ok := x.(float64)
		if !ok {
			return 0, false
		}
		switch {
		case v < want:
			return -1, true
		case v > want:
			return 1, true
		}
		return 0, true
	}
	switch n.Operator {
	case "equals", "not-equals", "less-than", "at-most", "greater-than", "at-least":
		c, ok := compare(n.Value)
		if !ok {
			return rbUnknown
		}
		if n.Operator == "not-equals" {
			return rbMediaTruth(c != 0)
		}
		return rbMediaTruth(rbMediaNumericOperator(n.Operator, c))
	case "between":
		list, ok := n.Value.([]any)
		if !ok || len(list) != 2 {
			return rbUnknown
		}
		low, okLow := compare(list[0])
		high, okHigh := compare(list[1])
		if !okLow || !okHigh {
			return rbUnknown
		}
		return rbMediaTruth(low >= 0 && high <= 0)
	case "in", "not-in":
		list, ok := n.Value.([]any)
		if !ok {
			return rbUnknown
		}
		found := false
		for _, x := range list {
			if c, ok := compare(x); ok && c == 0 {
				found = true
				break
			}
		}
		if n.Operator == "not-in" {
			return rbMediaTruth(!found)
		}
		return rbMediaTruth(found)
	}
	return rbUnknown
}

func rbMediaTextTruth(n *rbFilterNode, value *string, fold bool) rbTruth {
	if n.Operator == "is-present" || n.Operator == "is-missing" {
		return rbMediaTruth((value != nil) == (n.Operator == "is-present"))
	}
	negative := n.Operator == "not-equals" || n.Operator == "not-in"
	if value == nil {
		return rbMediaTruth(negative)
	}
	canon := func(s string) string {
		if fold {
			return sorttext.Fold(s)
		}
		return s
	}
	have := canon(*value)
	switch n.Operator {
	case "equals", "contains", "not-equals":
		want, ok := n.Value.(string)
		if !ok {
			return rbUnknown
		}
		equal := have == canon(want)
		if negative {
			return rbMediaTruth(!equal)
		}
		return rbMediaTruth(equal)
	case "starts-with":
		want, ok := n.Value.(string)
		if !ok {
			return rbUnknown
		}
		return rbMediaTruth(strings.HasPrefix(have, canon(want)))
	case "in", "not-in", "contains-any", "contains-all":
		list, ok := n.Value.([]any)
		if !ok || len(list) == 0 {
			return rbUnknown
		}
		all, matchAny := n.Operator == "contains-all", false
		for _, x := range list {
			want, ok := x.(string)
			if !ok {
				return rbUnknown
			}
			match := have == canon(want)
			if all && !match {
				return rbFalse
			}
			if !all && match {
				matchAny = true
			}
		}
		if all {
			return rbTrue
		}
		if n.Operator == "not-in" {
			return rbMediaTruth(!matchAny)
		}
		return rbMediaTruth(matchAny)
	}
	return rbUnknown
}

func rbMediaNumericOperator(op string, c int) bool {
	switch op {
	case "equals":
		return c == 0
	case "less-than":
		return c < 0
	case "at-most":
		return c <= 0
	case "greater-than":
		return c > 0
	case "at-least":
		return c >= 0
	}
	return false
}

func rbMediaNegate(t rbTruth) rbTruth {
	switch t {
	case rbTrue:
		return rbFalse
	case rbFalse:
		return rbTrue
	}
	return rbUnknown
}
