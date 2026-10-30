package apikit

import (
	"encoding/json"
	"fmt"
	"sort"
)

// discriminateResponse marks every union of object alternatives in a response
// schema with the property that tells them apart (O-159 R-09b). A 1.x client
// reads responses open, so an added field must never make one alternative look
// like another: each alternative carries a required property whose single const
// value names it. The runtime dispatches on that tag, and a tag it does not know
// is an unknown variant, not a guess. A union without such a tag fails
// generation, unless its route is still listed in pending (the R-09b migration
// list, which ends empty).
// The tag is always named variant, except the union nodes in
// grandfathered, keyed "route path", which keep the domain tag they already had
// (an event's type, a reading location's format) before R-09b.
const unionTagName = "variant"

func discriminateResponse(route string, s any, pending map[string]bool, grandfathered map[string]string, used map[string]bool) error {
	return walkUnions(s, "$", func(node map[string]any, at string, alternatives []any) error {
		objects := 0
		for _, alternative := range alternatives {
			if objectSchema(alternative) {
				objects++
			}
		}
		if objects < 2 {
			return nil // A nullable member (X | null) or a scalar union needs no tag.
		}
		if tag := unionTag(alternatives); tag == unionTagName || tag != "" && grandfathered[route+" "+at] == tag {
			if tag != unionTagName {
				used[route+" "+at] = true
			}
			node["x-discriminator"] = tag
			return nil
		} else if tag != "" {
			return fmt.Errorf("route %s: response union at %s is tagged by %q; response unions are tagged by %q", route, at, tag, unionTagName)
		}
		if pending[route] {
			return nil
		}
		return fmt.Errorf("route %s: response union at %s has no required const %s", route, at, unionTagName)
	})
}

func walkUnions(s any, at string, visit func(map[string]any, string, []any) error) error {
	switch v := s.(type) {
	case map[string]any:
		for _, key := range []string{"anyOf", "oneOf"} {
			if alternatives, ok := v[key].([]any); ok {
				if err := visit(v, at, alternatives); err != nil {
					return err
				}
				for i, alternative := range alternatives {
					if err := walkUnions(alternative, fmt.Sprintf("%s|%s%d", at, key, i), visit); err != nil {
						return err
					}
				}
			}
		}
		if properties, ok := v["properties"].(map[string]any); ok {
			keys := make([]string, 0, len(properties))
			for key := range properties {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if err := walkUnions(properties[key], at+"."+key, visit); err != nil {
					return err
				}
			}
		}
		for _, key := range []string{"items", "additionalProperties"} {
			if child, ok := v[key].(map[string]any); ok {
				if err := walkUnions(child, at+"["+key+"]", visit); err != nil {
					return err
				}
			}
		}
	case []any:
		for i, child := range v {
			if err := walkUnions(child, fmt.Sprintf("%s[%d]", at, i), visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func objectSchema(s any) bool {
	m, ok := s.(map[string]any)
	if !ok {
		return false
	}
	_, hasProperties := m["properties"]
	return m["type"] == "object" || hasProperties
}

// unionTag is the property every alternative requires with one const value,
// distinct across alternatives. variant is preferred.
func unionTag(alternatives []any) string {
	var candidates []string
	first, _ := alternatives[0].(map[string]any)
	if properties, ok := first["properties"].(map[string]any); ok {
		for key := range properties {
			candidates = append(candidates, key)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		rank := func(key string) int {
			switch key {
			case unionTagName:
				return 0
			case "kind":
				return 1
			}
			return 2
		}
		if rank(candidates[i]) != rank(candidates[j]) {
			return rank(candidates[i]) < rank(candidates[j])
		}
		return candidates[i] < candidates[j]
	})
	for _, key := range candidates {
		seen := map[string]bool{}
		ok := true
		for _, alternative := range alternatives {
			m, isObject := alternative.(map[string]any)
			value, has := singleTag(m, key)
			if !isObject || !has || seen[value] {
				ok = false
				break
			}
			seen[value] = true
		}
		if ok {
			return key
		}
	}
	return ""
}

func singleTag(alternative map[string]any, key string) (string, bool) {
	properties, _ := alternative["properties"].(map[string]any)
	property, _ := properties[key].(map[string]any)
	if property == nil || !required(alternative, key) {
		return "", false
	}
	var value any
	if c, ok := property["const"]; ok {
		value = c
	} else if enum := enumValues(property["enum"]); len(enum) == 1 {
		value = enum[0]
	} else {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	raw, _ := json.Marshal(text)
	return string(raw), true
}

func required(alternative map[string]any, key string) bool {
	switch list := alternative["required"].(type) {
	case []string:
		for _, value := range list {
			if value == key {
				return true
			}
		}
	case []any:
		for _, value := range list {
			if value == key {
				return true
			}
		}
	}
	return false
}

func enumValues(v any) []any {
	switch list := v.(type) {
	case []any:
		return list
	case []string:
		out := make([]any, len(list))
		for i, value := range list {
			out[i] = value
		}
		return out
	}
	return nil
}
