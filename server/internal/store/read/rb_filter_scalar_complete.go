package read

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// The complete published scalar vocabulary is evaluated only AFTER a bounded
// raw source walk. It keeps the public field types, operators and three-valued
// SQL truth:
//
//   - title is folded text and is never missing.
//   - year is NULLIF(year,0); the published indexed numeric forms are FALSE (not
//     NULL) for a zero year, so NOT includes it.
//   - decade is the 1800..2199 bounded CASE; normal indexed forms are FALSE for
//     an out-of-domain year, while other forms keep the nullable CASE.
//   - releaseDate/dateAdded are date text and are NULL when absent, so every
//     comparison except not-equals is unknown for a missing value.
//   - communityRating/criticRating compile to an EXISTS over provider facts, so
//     every positive form is FALSE when absent and its negative form TRUE.
//   - library and entityKind are opaque identity/enum values compared exactly.
//
// Unsupported fields/operators/types refuse at compile time, including inside a
// negated group; they can never become a false predicate that NOT turns into
// permission.
type rbScalarCompleteFilter struct{ node *rbFilterNode }

// rbScalarNumber distinguishes an absent numeric fact from a real zero. Rating
// keys use zero as their native absent value, while a caller-supplied number is
// never coerced to "missing".
type rbScalarNumber struct {
	value   float64
	present bool
}

// rbScalarCompleteFacts is one admitted entity's current scalar facts. now is
// the immutable relative-date evaluation instant for the whole preparation.
type rbScalarCompleteFacts struct {
	title, library                string
	kind, year                    int64
	releaseDate, dateAdded        string
	communityRating, criticRating rbScalarNumber
	now                           time.Time
}

const (
	rbScalarMaximumBytes    = 64 << 10
	rbScalarMaximumDepth    = 5
	rbScalarMaximumClauses  = 40
	rbScalarMaximumValues   = 100
	rbScalarMaximumDays     = 36500
	rbScalarMaximumTextSize = 500
)

var (
	rbScalarNumberOperators = []string{"equals", "not-equals", "less-than", "at-most", "greater-than", "at-least", "between", "in", "not-in", "is-present", "is-missing"}
	rbScalarDateOperators   = []string{"within-last-days", "equals", "not-equals", "less-than", "at-most", "greater-than", "at-least", "between", "is-present", "is-missing"}
	rbScalarTextOperators   = []string{"equals", "not-equals", "contains", "starts-with", "in", "not-in", "is-present", "is-missing"}
	rbScalarEnumOperators   = []string{"equals", "not-equals", "in", "not-in"}
)

func compileRBScalarCompleteFilter(raw json.RawMessage) (rbScalarCompleteFilter, error) {
	n, err := parseRBCompleteNode(raw)
	if err != nil {
		return rbScalarCompleteFilter{}, err
	}
	return rbScalarCompleteFilter{n}, nil
}

func (p rbScalarCompleteFilter) matches(f rbScalarCompleteFacts) bool {
	return rbEvalCompleteScalar(p.node, f) == rbTrue
}

// parseRBCompleteNode is the closed native projection of the published browse
// expression grammar. It deliberately does not accept the writer/LC subsets'
// narrower vocabularies.
func parseRBCompleteNode(raw json.RawMessage) (*rbFilterNode, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(raw) > rbScalarMaximumBytes || !utf8.Valid(raw) || !rbJSONDepth(raw, rbScalarMaximumDepth*2+2) {
		return nil, ErrInput
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF {
		return nil, ErrInput
	}
	clauses := 0
	return parseRBCompleteValue(value, 0, &clauses)
}

func parseRBCompleteValue(value any, depth int, clauses *int) (*rbFilterNode, error) {
	if depth > rbScalarMaximumDepth {
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
			n, err := parseRBCompleteValue(child, depth+1, clauses)
			if err != nil || n == nil {
				return nil, ErrInput
			}
			return &rbFilterNode{Not: n}, nil
		}
		for _, key := range []string{"all", "any"} {
			child, ok := o[key]
			if !ok {
				continue
			}
			list, ok := child.([]any)
			if !ok || len(list) == 0 || len(list) > rbScalarMaximumClauses {
				return nil, ErrInput
			}
			parsed := make([]rbFilterNode, 0, len(list))
			for _, v := range list {
				n, err := parseRBCompleteValue(v, depth+1, clauses)
				if err != nil || n == nil {
					return nil, ErrInput
				}
				parsed = append(parsed, *n)
			}
			if key == "all" {
				return &rbFilterNode{All: parsed}, nil
			}
			return &rbFilterNode{Any: parsed}, nil
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
	operator, ok := o["operator"].(string)
	if !ok {
		return nil, ErrInput
	}
	v, exists := o["value"]
	if !exists {
		return nil, ErrInput
	}
	relative := operator == "within-last-days"
	if relative != (variant == "relative") {
		return nil, ErrInput
	}
	n, err := parseRBCompletePredicate(field, operator, v)
	if err != nil {
		return nil, err
	}
	*clauses++
	if *clauses > rbScalarMaximumClauses {
		return nil, ErrInput
	}
	return n, nil
}

func parseRBCompletePredicate(field, operator string, value any) (*rbFilterNode, error) {
	var operators []string
	var date bool
	switch field {
	case "year", "communityRating", "criticRating":
		operators = rbScalarNumberOperators
	case "decade":
		operators = []string{"equals", "not-equals", "in", "not-in", "between"}
	case "title":
		operators = rbScalarTextOperators
	case "releaseDate", "dateAdded":
		operators, date = rbScalarDateOperators, true
	case "library", "entityKind":
		operators = rbScalarEnumOperators
	default:
		return nil, ErrUnqualified
	}
	if !slices.Contains(operators, operator) {
		return nil, ErrInput
	}
	if operator == "is-present" || operator == "is-missing" {
		if value != nil {
			return nil, ErrInput
		}
		return &rbFilterNode{Field: field, Operator: operator}, nil
	}
	if operator == "within-last-days" {
		days, err := rbCompleteDays(value)
		if err != nil {
			return nil, ErrInput
		}
		return &rbFilterNode{Field: field, Operator: operator, Value: days}, nil
	}
	listOp := operator == "between" || operator == "in" || operator == "not-in"
	list, isList := value.([]any)
	if listOp != isList {
		return nil, ErrInput
	}
	convert := func(v any) (any, error) {
		switch {
		case date:
			return rbCompleteDate(v)
		case field == "title" || field == "library" || field == "entityKind":
			return rbCompleteText(field, v)
		default:
			return rbCompleteNumber(v)
		}
	}
	if isList {
		if len(list) == 0 || len(list) > rbScalarMaximumValues || operator == "between" && len(list) != 2 {
			return nil, ErrInput
		}
		values := make([]any, len(list))
		for i, candidate := range list {
			converted, err := convert(candidate)
			if err != nil {
				return nil, err
			}
			values[i] = converted
		}
		value = values
	} else {
		converted, err := convert(value)
		if err != nil {
			return nil, err
		}
		value = converted
	}
	return &rbFilterNode{Field: field, Operator: operator, Value: value}, nil
}

func rbCompleteNumber(value any) (float64, error) {
	var text string
	switch v := value.(type) {
	case json.Number:
		text = v.String()
	case string:
		text = strings.TrimSpace(v)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, ErrInput
		}
		return v, nil
	case int64:
		return float64(v), nil
	case int:
		return float64(v), nil
	default:
		return 0, ErrInput
	}
	if text == "" {
		return 0, ErrInput
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, ErrInput
	}
	return n, nil
}

func rbCompleteDays(value any) (int64, error) {
	var number float64
	switch v := value.(type) {
	case json.Number:
		parsed, err := v.Float64()
		if err != nil {
			return 0, ErrInput
		}
		number = parsed
	case float64:
		number = v
	case int64:
		number = float64(v)
	case int:
		number = float64(v)
	default:
		return 0, ErrInput
	}
	if math.IsNaN(number) || number < 1 || number > rbScalarMaximumDays || math.Trunc(number) != number {
		return 0, ErrInput
	}
	return int64(number), nil
}

func rbCompleteText(field string, value any) (string, error) {
	var text string
	switch v := value.(type) {
	case string:
		text = v
	case json.Number:
		text = v.String()
	default:
		return "", ErrInput
	}
	if strings.TrimSpace(text) == "" || len(text) > rbScalarMaximumTextSize || strings.ContainsAny(text, "\x00\r\n") || !utf8.ValidString(text) {
		return "", ErrInput
	}
	if field == "entityKind" && !slices.Contains([]string{"movie", "episode", "show", "season", "song", "album", "artist", "audiobook_file", "book", "author", "collection"}, text) {
		return "", ErrInput
	}
	return text, nil
}

func rbCompleteDate(value any) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", ErrInput
	}
	if _, err := time.Parse("2006-01-02", text); err == nil {
		return text, nil
	}
	if _, err := time.Parse(time.RFC3339, text); err == nil {
		return text, nil
	}
	return "", ErrInput
}

// rbEvalCompleteScalar mirrors SQL's AND/OR/NOT three-valued truth.
func rbEvalCompleteScalar(n *rbFilterNode, f rbScalarCompleteFacts) rbTruth {
	if n == nil {
		return rbTrue
	}
	if n.Not != nil {
		switch rbEvalCompleteScalar(n.Not, f) {
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
			switch rbEvalCompleteScalar(&n.All[i], f) {
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
			switch rbEvalCompleteScalar(&n.Any[i], f) {
			case rbTrue:
				return rbTrue
			case rbUnknown:
				result = rbUnknown
			}
		}
		return result
	}
	switch n.Field {
	case "year":
		return rbCompleteYearTruth(n, f.year, false)
	case "decade":
		return rbCompleteYearTruth(n, f.year, true)
	case "communityRating":
		return rbCompleteRatingTruth(n, f.communityRating)
	case "criticRating":
		return rbCompleteRatingTruth(n, f.criticRating)
	case "releaseDate":
		return rbCompleteDateTruth(n, f.releaseDate, f.now)
	case "dateAdded":
		return rbCompleteDateTruth(n, f.dateAdded, f.now)
	case "title":
		return rbCompleteTextTruth(n, f.title, true)
	case "library":
		return rbCompleteTextTruth(n, f.library, false)
	case "entityKind":
		name, ok := rbCompleteKindName(f.kind)
		if !ok {
			return rbUnknown
		}
		return rbCompleteTextTruth(n, name, false)
	}
	return rbUnknown
}

func rbCompleteKindName(kind int64) (string, bool) {
	switch kind {
	case 1:
		return "movie", true
	case 2:
		return "show", true
	case 3:
		return "season", true
	case 4:
		return "episode", true
	case 5:
		return "artist", true
	case 6:
		return "album", true
	case 7:
		return "song", true
	case 8:
		return "book", true
	case 9:
		return "audiobook_file", true
	case 10:
		return "collection", true
	case 11:
		return "extra", true
	case 12:
		return "disc", true
	case 13:
		return "author", true
	}
	return "", false
}

func rbCompleteYearTruth(n *rbFilterNode, year int64, decade bool) rbTruth {
	number := float64(year)
	missing := year == 0
	if decade {
		number = float64(year / 10 * 10)
		missing = year < 1800 || year > 2199
	}
	if n.Operator == "is-present" {
		return rbTruthOf(!missing)
	}
	if n.Operator == "is-missing" {
		return rbTruthOf(missing)
	}
	if missing {
		if n.Operator == "not-equals" || n.Operator == "not-in" {
			return rbTrue
		}
		if !decade {
			switch n.Operator {
			case "equals", "in", "less-than", "at-most", "greater-than", "at-least", "between":
				// The published indexed year predicates explicitly exclude zero.
				return rbFalse
			}
		} else if rbIndexedDecadeTruth(n) {
			return rbFalse
		}
		return rbUnknown
	}
	return rbCompleteNumberTruth(n, number)
}

func rbCompleteRatingTruth(n *rbFilterNode, fact rbScalarNumber) rbTruth {
	if n.Operator == "is-present" {
		return rbTruthOf(fact.present)
	}
	if n.Operator == "is-missing" {
		return rbTruthOf(!fact.present)
	}
	negative := n.Operator == "not-equals" || n.Operator == "not-in"
	if !fact.present {
		// The published item field is an EXISTS over provider rows. A missing
		// row makes every positive form FALSE, and its negated form TRUE.
		if negative {
			return rbTrue
		}
		return rbFalse
	}
	return rbCompleteNumberTruth(n, fact.value)
}

func rbCompleteNumberTruth(n *rbFilterNode, number float64) rbTruth {
	compare := func(value any) (int, bool) {
		input, ok := value.(float64)
		if !ok {
			return 0, false
		}
		switch {
		case number < input:
			return -1, true
		case number > input:
			return 1, true
		default:
			return 0, true
		}
	}
	if n.Operator == "between" {
		values, ok := n.Value.([]any)
		if !ok || len(values) != 2 {
			return rbUnknown
		}
		first, okFirst := compare(values[0])
		last, okLast := compare(values[1])
		if !okFirst || !okLast {
			return rbUnknown
		}
		return rbTruthOf(first >= 0 && last <= 0)
	}
	if n.Operator == "in" || n.Operator == "not-in" {
		values, ok := n.Value.([]any)
		if !ok {
			return rbUnknown
		}
		found := false
		for _, value := range values {
			cmp, ok := compare(value)
			if !ok {
				return rbUnknown
			}
			if cmp == 0 {
				found = true
				break
			}
		}
		if n.Operator == "not-in" {
			found = !found
		}
		return rbTruthOf(found)
	}
	cmp, ok := compare(n.Value)
	if !ok {
		return rbUnknown
	}
	switch n.Operator {
	case "equals":
		return rbTruthOf(cmp == 0)
	case "not-equals":
		return rbTruthOf(cmp != 0)
	case "less-than":
		return rbTruthOf(cmp < 0)
	case "at-most":
		return rbTruthOf(cmp <= 0)
	case "greater-than":
		return rbTruthOf(cmp > 0)
	case "at-least":
		return rbTruthOf(cmp >= 0)
	}
	return rbUnknown
}

// rbCompleteStoredInstant reads the UTC text the catalogue stores: a calendar
// date at midnight, or an RFC 3339 timestamp of any fraction precision.
func rbCompleteStoredInstant(text string) (time.Time, bool) {
	if text == "" {
		return time.Time{}, false
	}
	if at, err := time.Parse("2006-01-02", text); err == nil {
		return at, true
	}
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}, false
	}
	return at.UTC(), true
}

// rbCompleteDatePeriod is the period one date operand names. A calendar date
// names its whole UTC day; an RFC 3339 instant names the smallest unit written.
func rbCompleteDatePeriod(value any) (from, until time.Time, ok bool) {
	text, _ := value.(string)
	if day, err := time.Parse("2006-01-02", text); err == nil {
		return day, day.AddDate(0, 0, 1), true
	}
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	unit := time.Second
	if dot := strings.LastIndexByte(text, '.'); dot >= 0 {
		end := strings.IndexAny(text[dot+1:], "Zz+-")
		if end < 0 {
			end = len(text) - dot - 1
		}
		for range min(end, 9) {
			unit /= 10
		}
	}
	at = at.UTC().Truncate(unit)
	return at, at.Add(unit), true
}

func rbCompleteDateTruth(n *rbFilterNode, text string, now time.Time) rbTruth {
	missing := text == ""
	if n.Operator == "is-present" {
		return rbTruthOf(!missing)
	}
	if n.Operator == "is-missing" {
		return rbTruthOf(missing)
	}
	if missing {
		// A missing date is SQL NULL: only not-equals includes it explicitly.
		if n.Operator == "not-equals" {
			return rbTrue
		}
		return rbUnknown
	}
	stored, ok := rbCompleteStoredInstant(text)
	if !ok {
		return rbUnknown
	}
	inRange := func(from, until time.Time) bool {
		return !stored.Before(from) && stored.Before(until)
	}
	if n.Operator == "within-last-days" {
		days, ok := n.Value.(int64)
		if !ok || now.IsZero() {
			return rbUnknown
		}
		from := now.Add(-time.Duration(days) * 24 * time.Hour)
		return rbTruthOf(inRange(from, now.Add(time.Nanosecond)))
	}
	if n.Operator == "between" {
		values, ok := n.Value.([]any)
		if !ok || len(values) != 2 {
			return rbUnknown
		}
		first, _, okFirst := rbCompleteDatePeriod(values[0])
		_, last, okLast := rbCompleteDatePeriod(values[1])
		if !okFirst || !okLast {
			return rbUnknown
		}
		return rbTruthOf(inRange(first, last))
	}
	from, until, ok := rbCompleteDatePeriod(n.Value)
	if !ok {
		return rbUnknown
	}
	switch n.Operator {
	case "equals":
		return rbTruthOf(inRange(from, until))
	case "not-equals":
		return rbTruthOf(!inRange(from, until))
	case "less-than":
		return rbTruthOf(stored.Before(from))
	case "at-most":
		return rbTruthOf(stored.Before(until))
	case "greater-than":
		return rbTruthOf(!stored.Before(until))
	case "at-least":
		return rbTruthOf(!stored.Before(from))
	}
	return rbUnknown
}

func rbCompleteTextTruth(n *rbFilterNode, text string, fold bool) rbTruth {
	if n.Operator == "is-present" {
		return rbTrue
	}
	if n.Operator == "is-missing" {
		return rbFalse
	}
	value := func(v any) (string, bool) {
		s, ok := v.(string)
		if !ok {
			return "", false
		}
		if fold {
			return sorttext.Fold(s), true
		}
		return s, true
	}
	target := text
	if fold {
		target = sorttext.Fold(text)
	}
	if n.Operator == "between" {
		values, ok := n.Value.([]any)
		if !ok || len(values) != 2 {
			return rbUnknown
		}
		first, okFirst := value(values[0])
		last, okLast := value(values[1])
		if !okFirst || !okLast {
			return rbUnknown
		}
		return rbTruthOf(strings.Compare(target, first) >= 0 && strings.Compare(target, last) <= 0)
	}
	if n.Operator == "in" || n.Operator == "not-in" {
		values, ok := n.Value.([]any)
		if !ok {
			return rbUnknown
		}
		found := false
		for _, candidate := range values {
			s, ok := value(candidate)
			if !ok {
				return rbUnknown
			}
			if target == s {
				found = true
				break
			}
		}
		if n.Operator == "not-in" {
			found = !found
		}
		return rbTruthOf(found)
	}
	if n.Operator == "contains" || n.Operator == "starts-with" {
		s, ok := value(n.Value)
		if !ok {
			return rbUnknown
		}
		if n.Operator == "starts-with" {
			return rbTruthOf(strings.HasPrefix(target, s))
		}
		return rbTruthOf(strings.Contains(target, s))
	}
	s, ok := value(n.Value)
	if !ok {
		return rbUnknown
	}
	switch n.Operator {
	case "equals":
		return rbTruthOf(target == s)
	case "not-equals":
		return rbTruthOf(target != s)
	}
	return rbUnknown
}

func rbTruthOf(v bool) rbTruth {
	if v {
		return rbTrue
	}
	return rbFalse
}
