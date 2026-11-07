package read

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// The legacy parser is an oracle in READ tests only. It cannot become a native
// writer/plan init dependency through the production read package.
func TestRBScalarParserPublishedSubsetParity(t *testing.T) {
	var convert func(*catalog.BrowseNode) *rbFilterNode
	convert = func(n *catalog.BrowseNode) *rbFilterNode {
		if n == nil {
			return nil
		}
		out := &rbFilterNode{Field: n.Field, Operator: n.Operator, Value: n.Value, Not: convert(n.Not)}
		for i := range n.All {
			out.All = append(out.All, *convert(&n.All[i]))
		}
		for i := range n.Any {
			out.Any = append(out.Any, *convert(&n.Any[i]))
		}
		return out
	}
	corpus := []string{"", "null", "{}", "[]", `{"variant":"group","not":{}}`, `{"variant":"group","all":[]}`, `{"variant":"predicate","field":"year","operator":"equals"}`, `{} {}`, `{} x`}
	for _, field := range []string{"year", "decade", "title", "library", "entityKind"} {
		for _, op := range []string{"equals", "not-equals", "less-than", "at-most", "greater-than", "at-least", "between", "in", "not-in", "contains", "starts-with", "is-present", "is-missing"} {
			for _, v := range []string{`null`, `2000`, `" 2000 "`, `"NaN"`, `"movie"`, `"Movies"`, `""`, `true`, `[1990,2000]`, `["movie","episode"]`, `[]`, `[null]`} {
				leaf := fmt.Sprintf(`{"variant":"predicate","field":%q,"operator":%q,"value":%s}`, field, op, v)
				corpus = append(corpus, leaf, `{"variant":"group","not":`+leaf+`}`)
			}
		}
	}
	leaf := `{"variant":"predicate","field":"year","operator":"equals","value":2000}`
	for _, n := range []int{40, 41} {
		corpus = append(corpus, `{"variant":"group","all":[`+strings.TrimSuffix(strings.Repeat(leaf+",", n), ",")+`]}`)
	}
	for _, n := range []int{5, 6} {
		raw := leaf
		for range n {
			raw = `{"variant":"group","not":` + raw + `}`
		}
		corpus = append(corpus, raw)
	}
	for _, raw := range corpus {
		want, we := catalog.ParseBrowseQuery(json.RawMessage(raw), "query")
		got, ge := parseRBScalarNode(json.RawMessage(raw))
		// The native parser is stricter on trailing malformed JSON: accepted
		// normalized configurations can never contain that transport corruption.
		if raw == "{} x" {
			if ge == nil {
				t.Fatal("trailing malformed JSON accepted")
			}
			continue
		}
		if (we == nil) != (ge == nil) || we == nil && !reflect.DeepEqual(convert(want), got) {
			t.Fatalf("%s\npublic=%+v %v\nnative=%+v %v", raw, want, we, got, ge)
		}
	}
}
