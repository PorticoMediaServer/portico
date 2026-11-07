package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"
)

type absentValue struct{}
type planRowDifference struct {
	name             string
	index            int
	expected, actual any
}
type goldenDifference struct {
	path, context    string
	expected, actual any
	row              *planRowDifference
}

// CompareJSON keeps compacted-JSON equality except the approved whole native
// compile profiles, and reports the first structural difference without
// rewriting either input. Numbers retain their
// original precision. FK audits and statement goldens share these diagnostics.
func CompareJSON(expected, actual []byte) error {
	return compareJSONForPlatform(expected, actual, runtime.GOOS+"/"+runtime.GOARCH)
}

func compareJSONForPlatform(expected, actual []byte, platform string) error {
	if len(expected) == 0 {
		return fmt.Errorf("plan: missing reviewed golden")
	}
	var want, got bytes.Buffer
	if err := json.Compact(&want, expected); err != nil {
		return fmt.Errorf("plan: invalid golden: %w", err)
	}
	if err := json.Compact(&got, actual); err != nil {
		return fmt.Errorf("plan: invalid actual JSON: %w", err)
	}
	projectedWant, projectedGot, err := schemaComparison(want.Bytes(), got.Bytes())
	if err != nil {
		return fmt.Errorf("plan: schema evidence: %w", err)
	}
	matched, err := compileOptionEquivalence(projectedWant, projectedGot, platform)
	if err != nil {
		return fmt.Errorf("plan: compile-option profile: %w", err)
	}
	want.Reset()
	want.Write(matched)
	got.Reset()
	got.Write(projectedGot)
	if bytes.Equal(want.Bytes(), got.Bytes()) {
		return nil
	}
	decode := func(data []byte) any {
		var value any
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		_ = d.Decode(&value)
		return value
	}
	before, after := decode(want.Bytes()), decode(got.Bytes())
	cohorts := map[string][]string{}
	for _, root := range []any{before, after} {
		object, _ := root.(map[string]any)
		plans, _ := object["plans"].([]any)
		for _, value := range plans {
			p, _ := value.(map[string]any)
			id, ok := p["id"].(string)
			cohort, found := p["cohort"].(string)
			if ok && found {
				if !containsString(cohorts[id], cohort) {
					cohorts[id] = append(cohorts[id], cohort)
				}
			}
		}
	}
	for _, names := range cohorts {
		sort.Strings(names)
	}
	difference := firstGoldenDifference(before, after, "$", "", nil, cohorts)
	if difference == nil {
		// Object order or numeric spelling can differ despite an equivalent decoded
		// tree. Preserve the former exact gate instead of normalizing that change.
		difference = &goldenDifference{path: "$ (JSON encoding)", expected: want.String(), actual: got.String()}
	}
	oldValue, newValue := differenceValues(difference.expected, difference.actual)
	context := ""
	if difference.context != "" {
		context = " (" + difference.context + ")"
	}
	message := fmt.Sprintf("plan: golden differs at %s%s\nexpected: %s\nactual: %s", difference.path, context, oldValue, newValue)
	if difference.row != nil {
		r := difference.row
		message += fmt.Sprintf("\ncompact plan diff:\n- %s row %d: %s\n+ %s row %d: %s", r.name, r.index, compactValue(r.expected), r.name, r.index, compactValue(r.actual))
	}
	return fmt.Errorf("%s", message)
}
func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func goldenContext(value any, cohorts map[string][]string) string {
	object, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	if child, ok := object["child"].(string); ok {
		return fmt.Sprintf("FK %s/%v -> %v", child, object["id"], object["parent"])
	}
	id, ok := object["id"].(string)
	if !ok {
		return ""
	}
	if cohort, ok := object["cohort"].(string); ok {
		return "statement " + id + " / cohort " + cohort
	}
	if _, ok := object["sql"]; ok {
		return "statement " + id + " / cohorts " + strings.Join(cohorts[id], ",")
	}
	return ""
}
func firstGoldenDifference(want, got any, path, context string, row *planRowDifference, cohorts map[string][]string) *goldenDifference {
	a, b := goldenContext(want, cohorts), goldenContext(got, cohorts)
	if a != "" && b != "" && a != b {
		context = "expected " + a + "; actual " + b
	} else if b != "" {
		context = b
	} else if a != "" {
		context = a
	}
	changed := func() *goldenDifference {
		return &goldenDifference{path: path, context: context, expected: want, actual: got, row: row}
	}
	switch left := want.(type) {
	case map[string]any:
		right, ok := got.(map[string]any)
		if !ok {
			return changed()
		}
		keys := []string{}
		for key := range left {
			keys = append(keys, key)
		}
		for key := range right {
			if _, ok := left[key]; !ok {
				keys = append(keys, key)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			// Prefer the actual plan row over duplicate SQL metadata for a changed
			// statement, while environment changes remain first.
			priority := func(key string) int {
				if path == "$" {
					switch key {
					case "environment", "connection":
						return 0
					case "plans":
						return 1
					case "manifest":
						return 2
					}
				}
				return 3
			}
			a, b := priority(keys[i]), priority(keys[j])
			if a != b {
				return a < b
			}
			return keys[i] < keys[j]
		})
		for _, key := range keys {
			old, ok := left[key]
			if !ok {
				old = absentValue{}
			}
			current, ok := right[key]
			if !ok {
				current = absentValue{}
			}
			if difference := firstGoldenDifference(old, current, path+"."+key, context, row, cohorts); difference != nil {
				return difference
			}
		}
	case []any:
		right, ok := got.([]any)
		if !ok {
			return changed()
		}
		length := max(len(left), len(right))
		for i := 0; i < length; i++ {
			var old, current any = absentValue{}, absentValue{}
			if i < len(left) {
				old = left[i]
			}
			if i < len(right) {
				current = right[i]
			}
			currentRow := row
			if strings.HasSuffix(path, ".values") {
				name := strings.TrimSuffix(path, ".values")
				name = name[strings.LastIndex(name, ".")+1:]
				currentRow = &planRowDifference{name: name, index: i, expected: old, actual: current}
			}
			if difference := firstGoldenDifference(old, current, fmt.Sprintf("%s[%d]", path, i), context, currentRow, cohorts); difference != nil {
				return difference
			}
		}
	default:
		// Decoder leaves only comparable scalar types, with json.Number preserving
		// the exact source spelling rather than rounding through float64.
		if want != got {
			return changed()
		}
	}
	return nil
}
func compactValue(value any) string {
	if _, missing := value.(absentValue); missing {
		return "<missing>"
	}
	data, _ := json.Marshal(value)
	if len(data) > 280 {
		return string(data[:280]) + fmt.Sprintf("… (%d JSON bytes)", len(data))
	}
	return string(data)
}
func differenceValues(want, got any) (string, string) {
	left, a := want.(string)
	right, b := got.(string)
	if a && b && len(left)+len(right) > 320 {
		offset := 0
		for offset < len(left) && offset < len(right) && left[offset] == right[offset] {
			offset++
		}
		excerpt := func(value string) string {
			start := min(max(0, offset-48), len(value))
			end := min(len(value), offset+96)
			data, _ := json.Marshal(value[start:end])
			return fmt.Sprintf("string bytes=%d, difference offset=%d: %s", len(value), offset, data)
		}
		return excerpt(left), excerpt(right)
	}
	return compactValue(want), compactValue(got)
}

// CompareExactJSON preserves the FK audit's stricter decompressed-byte gate,
// while sharing structural/plan diagnostics with ordinary compacted goldens.
func CompareExactJSON(expected, actual []byte) error {
	if len(expected) == 0 {
		return fmt.Errorf("plan: missing reviewed golden")
	}
	if bytes.Equal(expected, actual) {
		return nil
	}
	if before, after, err := schemaComparison(expected, actual); err != nil {
		return err
	} else if !bytes.Equal(before, expected) || !bytes.Equal(after, actual) {
		return CompareJSON(expected, actual)
	}
	if err := CompareJSON(expected, actual); err != nil {
		return err
	}
	before, after := differenceValues(string(expected), string(actual))
	return fmt.Errorf("plan: golden differs at $ (JSON whitespace)\nexpected: %s\nactual: %s", before, after)
}
