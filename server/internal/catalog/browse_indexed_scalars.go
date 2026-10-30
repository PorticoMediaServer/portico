package catalog

import (
	"math"
	"strconv"
	"strings"
)

// indexedScalar compiles a positive year, decade or dateAdded comparison
// against the raw keys catalog_browse_year and catalog_browse_added index, so
// a selective or absent value seeks instead of scanning the library. Each
// rewrite keeps the field expression's exact meaning: a missing year is 0 and
// a missing added date is NULL or empty, and neither matches a positive
// comparison. Negative forms (not-equals, not-in, is-missing, is-present) and
// any value that is not a plain number keep the field expression.
func (c *browseCompiler) indexedScalar(field browseField, node BrowseNode) (string, bool, error) {
	switch field.ID {
	case "year":
		return c.indexedYear(node)
	case "decade":
		return c.indexedDecade(node)
	case "dateAdded":
		const added = "COALESCE(e.added_text,'')"
		switch node.Operator {
		case "less-than", "at-most":
			// "Before" has no lower bound, so the empty key a missing date
			// stores must be excluded as one; the range starts after it.
			clause, handled, err := c.dateComparison(added, node.Operator, node.Value)
			if !handled || err != nil {
				return "", false, nil
			}
			return "(" + added + ">'' AND " + clause + ")", true, nil
		case "equals", "greater-than", "at-least", "between", "within-last-days":
			// Each form has a non-empty lower bound, which the empty key fails.
			clause, handled, err := c.dateComparison(added, node.Operator, node.Value)
			if !handled || err != nil {
				return "", false, nil
			}
			return clause, true, nil
		}
	}
	return "", false, nil
}

func browseNumbers(value any) ([]float64, bool) {
	if number, ok := value.(float64); ok {
		return []float64{number}, true
	}
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		return nil, false
	}
	out := make([]float64, 0, len(values))
	for _, value := range values {
		number, ok := value.(float64)
		if !ok {
			return nil, false
		}
		out = append(out, number)
	}
	return out, true
}

// indexedYear matches NULLIF(e.year,0) OP value. e.year is NOT NULL, so the
// only difference from comparing e.year itself is that 0 never matches.
func (c *browseCompiler) indexedYear(node BrowseNode) (string, bool, error) {
	values, ok := browseNumbers(node.Value)
	if !ok {
		return "", false, nil
	}
	bind := func(clause string, args ...any) (string, bool, error) {
		c.args = append(c.args, args...)
		return clause, true, nil
	}
	switch node.Operator {
	case "equals":
		if len(values) != 1 {
			return "", false, nil
		}
		if values[0] == 0 {
			return "0", true, nil
		}
		return bind("e.year=?", values[0])
	case "in":
		present := []any{}
		for _, value := range values {
			if value != 0 {
				present = append(present, value)
			}
		}
		if len(present) == 0 {
			return "0", true, nil
		}
		return bind("e.year IN ("+placeholders(len(present))+")", present...)
	case "greater-than", "at-least":
		if len(values) != 1 {
			return "", false, nil
		}
		op := map[string]string{"greater-than": ">", "at-least": ">="}[node.Operator]
		if values[0] > 0 || node.Operator == "greater-than" && values[0] == 0 {
			return bind("e.year"+op+"?", values[0])
		}
		return bind("(e.year"+op+"? AND e.year<>0)", values[0])
	case "less-than", "at-most":
		if len(values) != 1 {
			return "", false, nil
		}
		op := map[string]string{"less-than": "<", "at-most": "<="}[node.Operator]
		if values[0] < 0 || node.Operator == "less-than" && values[0] == 0 {
			return bind("e.year"+op+"?", values[0])
		}
		// The upper bound still seeks; missing-year zeros inside the range are
		// stepped over. SQLite will not split this into a negative and a
		// positive seek, and a negative year is a real (if odd) stored value.
		return bind("(e.year"+op+"? AND e.year<>0)", values[0])
	case "between":
		if len(values) != 2 {
			return "", false, nil
		}
		if values[0] > 0 || values[1] < 0 {
			return bind("e.year BETWEEN ? AND ?", values[0], values[1])
		}
		return bind("(e.year BETWEEN ? AND ? AND e.year<>0)", values[0], values[1])
	}
	return "", false, nil
}

// indexedDecade matches CASE WHEN e.year BETWEEN 1800 AND 2199 THEN
// (e.year/10)*10 END OP value as the equivalent set of whole years.
func (c *browseCompiler) indexedDecade(node BrowseNode) (string, bool, error) {
	values, ok := browseNumbers(node.Value)
	if !ok {
		return "", false, nil
	}
	// years is the year interval whose decade lies in [low, high].
	years := func(low, high float64) (int, int) {
		return max(1800, int(math.Ceil(low/10))*10), min(2199, int(math.Floor(high/10))*10+9)
	}
	switch node.Operator {
	case "equals", "between":
		low, high := values[0], values[0]
		if node.Operator == "between" {
			if len(values) != 2 {
				return "", false, nil
			}
			high = values[1]
		} else if len(values) != 1 {
			return "", false, nil
		}
		if math.Abs(low) > 1e6 || math.Abs(high) > 1e6 {
			return "", false, nil
		}
		from, until := years(low, high)
		if from > until {
			return "0", true, nil
		}
		return "e.year BETWEEN " + strconv.Itoa(from) + " AND " + strconv.Itoa(until), true, nil
	case "in":
		list := []string{}
		seen := map[int]bool{}
		for _, value := range values {
			if math.Abs(value) > 1e6 {
				return "", false, nil
			}
			from, until := years(value, value)
			for year := from; year <= until; year++ {
				if !seen[year] {
					seen[year] = true
					list = append(list, strconv.Itoa(year))
				}
			}
		}
		if len(list) == 0 {
			return "0", true, nil
		}
		return "e.year IN (" + strings.Join(list, ",") + ")", true, nil
	}
	return "", false, nil
}
