package apikit

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// CheckOpenAPI is the validity gate every generated document passes before it
// is written. It checks the parts of OpenAPI 3.1 and JSON Schema 2020-12 the
// generator emits, structurally and without a validator dependency: operation
// identity, parameters against the path template, response keys, reference
// targets, and every schema keyword's type and bounds. An unknown schema
// keyword is an error, so a misspelt or invented keyword cannot slip into the
// published contract; extensions use the x- prefix.
func CheckOpenAPI(raw []byte) error {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("openapi: %w", err)
	}
	c := openAPIChecker{document: document}
	c.check()
	if len(c.problems) == 0 {
		return nil
	}
	sort.Strings(c.problems)
	shown := c.problems
	if len(shown) > 20 {
		shown = append(shown[:20:20], fmt.Sprintf("and %d more", len(c.problems)-20))
	}
	return errors.New("openapi document is invalid:\n  " + strings.Join(shown, "\n  "))
}

type openAPIChecker struct {
	document map[string]any
	problems []string
}

func (c *openAPIChecker) fail(at, format string, args ...any) {
	c.problems = append(c.problems, at+": "+fmt.Sprintf(format, args...))
}

var (
	openAPIMethods    = map[string]bool{"get": true, "put": true, "post": true, "delete": true, "options": true, "head": true, "patch": true, "trace": true}
	openAPIParameters = map[string]bool{"query": true, "header": true, "path": true, "cookie": true}
	responseKey       = regexp.MustCompile(`^(default|[1-5][0-9][0-9]|[1-5]XX)$`)
	pathTemplate      = regexp.MustCompile(`\{([^{}/]+)\}`)
	schemaTypes       = map[string]bool{"string": true, "integer": true, "number": true, "boolean": true, "object": true, "array": true, "null": true}
)

func (c *openAPIChecker) check() {
	if version, _ := c.document["openapi"].(string); !strings.HasPrefix(version, "3.1.") {
		c.fail("openapi", "must be a 3.1.x version string")
	}
	info, _ := c.document["info"].(map[string]any)
	if title, _ := info["title"].(string); title == "" {
		c.fail("info.title", "required")
	}
	if version, _ := info["version"].(string); version == "" {
		c.fail("info.version", "required")
	}
	components, _ := c.document["components"].(map[string]any)
	if schemas, ok := components["schemas"].(map[string]any); ok {
		for _, name := range sortedKeys(schemas) {
			c.schema("#/components/schemas/"+name, schemas[name])
		}
	}
	paths, ok := c.document["paths"].(map[string]any)
	if !ok {
		c.fail("paths", "must be an object")
		return
	}
	operations := map[string]string{}
	for _, path := range sortedKeys(paths) {
		item, ok := paths[path].(map[string]any)
		if !strings.HasPrefix(path, "/") || !ok {
			c.fail(path, "a path is an object keyed by a template starting with /")
			continue
		}
		template := map[string]bool{}
		for _, match := range pathTemplate.FindAllStringSubmatch(path, -1) {
			if template[match[1]] {
				c.fail(path, "repeats template parameter %q", match[1])
			}
			template[match[1]] = true
		}
		if strings.Count(path, "{") != len(pathTemplate.FindAllString(path, -1)) || strings.Count(path, "}") != strings.Count(path, "{") {
			c.fail(path, "unbalanced template braces")
		}
		for _, method := range sortedKeys(item) {
			at := strings.ToUpper(method) + " " + path
			if !openAPIMethods[method] {
				c.fail(at, "unknown path item member")
				continue
			}
			operation, ok := item[method].(map[string]any)
			if !ok {
				c.fail(at, "an operation is an object")
				continue
			}
			id, _ := operation["operationId"].(string)
			if id == "" {
				c.fail(at, "operationId required")
			} else if previous, seen := operations[id]; seen {
				c.fail(at, "operationId %q is also used by %s", id, previous)
			} else {
				operations[id] = at
			}
			c.operation(at, method, template, operation)
		}
	}
}

func (c *openAPIChecker) operation(at, method string, template map[string]bool, operation map[string]any) {
	declared := map[string]bool{}
	if raw, present := operation["parameters"]; present {
		parameters, ok := raw.([]any)
		if !ok {
			c.fail(at, "parameters must be an array")
		}
		for index, raw := range parameters {
			where := fmt.Sprintf("%s parameters[%d]", at, index)
			parameter, ok := raw.(map[string]any)
			if !ok {
				c.fail(where, "a parameter is an object")
				continue
			}
			name, _ := parameter["name"].(string)
			in, _ := parameter["in"].(string)
			if name == "" || !openAPIParameters[in] {
				c.fail(where, "needs a name and in: query, header, path or cookie")
				continue
			}
			if declared[in+" "+name] {
				c.fail(where, "duplicate %s parameter %q", in, name)
			}
			declared[in+" "+name] = true
			if required, present := parameter["required"]; present {
				if _, ok := required.(bool); !ok {
					c.fail(where, "required must be a boolean")
				}
			}
			if in == "path" {
				if parameter["required"] != true {
					c.fail(where, "path parameter %q must be required", name)
				}
				if !template[name] {
					c.fail(where, "path parameter %q is not in the path template", name)
				}
			}
			schema, present := parameter["schema"]
			if !present {
				c.fail(where, "schema required")
				continue
			}
			c.schema(where+" schema", schema)
		}
	}
	for _, name := range sortedKeys(template) {
		if !declared["path "+name] {
			c.fail(at, "template parameter %q is not declared", name)
		}
	}
	if raw, present := operation["requestBody"]; present {
		if method == "get" || method == "head" {
			c.fail(at, "a read operation declares a request body")
		}
		body, _ := raw.(map[string]any)
		if required, present := body["required"]; present {
			if _, ok := required.(bool); !ok {
				c.fail(at+" requestBody", "required must be a boolean")
			}
		}
		c.content(at+" requestBody", body["content"], true)
	}
	responses, ok := operation["responses"].(map[string]any)
	if !ok || len(responses) == 0 {
		c.fail(at, "responses required")
		return
	}
	for _, status := range sortedKeys(responses) {
		where := at + " responses." + status
		if !responseKey.MatchString(status) {
			c.fail(where, "not a status code, range or default")
		}
		response, ok := responses[status].(map[string]any)
		if !ok {
			c.fail(where, "a response is an object")
			continue
		}
		if description, _ := response["description"].(string); description == "" {
			c.fail(where, "description required")
		}
		if content, present := response["content"]; present {
			c.content(where, content, false)
		}
	}
}

func (c *openAPIChecker) content(at string, raw any, required bool) {
	content, ok := raw.(map[string]any)
	if !ok || required && len(content) == 0 {
		c.fail(at, "content must be an object of media types")
		return
	}
	for _, media := range sortedKeys(content) {
		where := at + " " + media
		if !strings.Contains(media, "/") {
			c.fail(where, "not a media type")
		}
		object, ok := content[media].(map[string]any)
		if !ok {
			c.fail(where, "a media type is an object")
			continue
		}
		for _, key := range sortedKeys(object) {
			switch {
			case key == "schema":
				c.schema(where+" schema", object[key])
			case key == "example", key == "examples", key == "encoding", strings.HasPrefix(key, "x-"):
			default:
				c.fail(where, "unknown media type member %q", key)
			}
		}
	}
}

// schemaKeywords maps each permitted keyword to the type it constrains ("" when
// it applies to any instance).
var schemaKeywords = map[string]string{
	"type": "", "enum": "", "const": "", "default": "", "title": "", "description": "", "examples": "",
	"deprecated": "", "readOnly": "", "writeOnly": "", "$ref": "", "anyOf": "", "oneOf": "", "allOf": "", "not": "",
	"format": "", "contentEncoding": "string", "contentMediaType": "string", "minimum": "number", "maximum": "number", "exclusiveMinimum": "number", "exclusiveMaximum": "number",
	"multipleOf": "number", "minLength": "string", "maxLength": "string", "pattern": "string",
	"items": "array", "prefixItems": "array", "minItems": "array", "maxItems": "array", "uniqueItems": "array",
	"properties": "object", "additionalProperties": "object", "required": "object", "minProperties": "object",
	"maxProperties": "object", "propertyNames": "object", "patternProperties": "object",
}

func (c *openAPIChecker) schema(at string, raw any) {
	if _, ok := raw.(bool); ok {
		return
	}
	schema, ok := raw.(map[string]any)
	if !ok {
		c.fail(at, "a schema is an object or a boolean")
		return
	}
	types := map[string]bool{}
	switch t := schema["type"].(type) {
	case nil:
	case string:
		types[t] = true
	case []any:
		for _, entry := range t {
			name, _ := entry.(string)
			if types[name] {
				c.fail(at, "type repeats %q", name)
			}
			types[name] = true
		}
	default:
		c.fail(at, "type must be a string or an array of strings")
	}
	for name := range types {
		if !schemaTypes[name] {
			c.fail(at, "unknown type %q", name)
		}
	}
	for _, key := range sortedKeys(schema) {
		value := schema[key]
		applies, known := schemaKeywords[key]
		if !known {
			if !strings.HasPrefix(key, "x-") {
				c.fail(at, "unknown schema keyword %q", key)
			}
			continue
		}
		// A keyword for another type is ignored by a validator, which in a
		// generated contract means the generator meant something it did not say.
		if applies != "" && len(types) > 0 && !types[applies] && !(applies == "number" && types["integer"]) {
			c.fail(at, "%s does not apply to type %v", key, sortedKeys(types))
		}
		switch key {
		case "type", "default", "const", "examples":
		case "title", "description", "format", "pattern", "contentEncoding", "contentMediaType":
			text, ok := value.(string)
			if !ok {
				c.fail(at, "%s must be a string", key)
			} else if key == "pattern" {
				if err := compileECMAPattern(text); err != nil {
					c.fail(at, "pattern does not compile: %v", err)
				}
			}
		case "deprecated", "readOnly", "writeOnly", "uniqueItems":
			if _, ok := value.(bool); !ok {
				c.fail(at, "%s must be a boolean", key)
			}
		case "$ref":
			c.reference(at, value)
		case "enum":
			values, ok := value.([]any)
			if !ok || len(values) == 0 {
				c.fail(at, "enum must be a non-empty array")
				break
			}
			seen := map[string]bool{}
			for _, entry := range values {
				encoded, _ := json.Marshal(entry)
				if seen[string(encoded)] {
					c.fail(at, "enum repeats %s", encoded)
				}
				seen[string(encoded)] = true
			}
		case "anyOf", "oneOf", "allOf", "prefixItems":
			list, ok := value.([]any)
			if !ok || len(list) == 0 {
				c.fail(at, "%s must be a non-empty array of schemas", key)
				break
			}
			for index, entry := range list {
				c.schema(fmt.Sprintf("%s.%s[%d]", at, key, index), entry)
			}
		case "not", "items", "additionalProperties", "propertyNames":
			c.schema(at+"."+key, value)
		case "properties", "patternProperties":
			members, ok := value.(map[string]any)
			if !ok {
				c.fail(at, "%s must be an object of schemas", key)
				break
			}
			for _, name := range sortedKeys(members) {
				if key == "patternProperties" {
					if err := compileECMAPattern(name); err != nil {
						c.fail(at, "pattern property %q does not compile", name)
					}
				}
				c.schema(at+"."+key+"."+name, members[name])
			}
		case "required":
			names, ok := value.([]any)
			if !ok || len(names) == 0 {
				c.fail(at, "required must be a non-empty array of property names")
				break
			}
			properties, _ := schema["properties"].(map[string]any)
			closed := schema["additionalProperties"] == false
			seen := map[string]bool{}
			for _, entry := range names {
				name, _ := entry.(string)
				if name == "" || seen[name] {
					c.fail(at, "required has an empty or repeated name %q", name)
				}
				seen[name] = true
				if _, declared := properties[name]; closed && !declared {
					c.fail(at, "required property %q is not declared on a closed object", name)
				}
			}
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
			number, ok := value.(float64)
			if !ok || key == "multipleOf" && number <= 0 {
				c.fail(at, "%s must be a number", key)
			}
		case "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties":
			if number, ok := value.(float64); !ok || number < 0 || number != math.Trunc(number) {
				c.fail(at, "%s must be a non-negative integer", key)
			}
		}
	}
	for _, pair := range [][2]string{{"minimum", "maximum"}, {"minLength", "maxLength"}, {"minItems", "maxItems"}, {"minProperties", "maxProperties"}} {
		low, lowOK := schema[pair[0]].(float64)
		high, highOK := schema[pair[1]].(float64)
		if lowOK && highOK && low > high {
			c.fail(at, "%s %v exceeds %s %v", pair[0], low, pair[1], high)
		}
	}
}

// JSON Schema patterns are ECMA-262 expressions. RE2 lacks lookaround and
// spells a code point escape differently, so those two are rewritten into
// their nearest RE2 form before the rest of the syntax is compiled.
var (
	ecmaCodePoint  = regexp.MustCompile(`\\u([0-9A-Fa-f]{4})`)
	ecmaLookaround = regexp.MustCompile(`\(\?<?[=!]`)
)

func compileECMAPattern(pattern string) error {
	pattern = ecmaCodePoint.ReplaceAllString(pattern, `\x{$1}`)
	pattern = ecmaLookaround.ReplaceAllString(pattern, "(?:")
	_, err := regexp.Compile(pattern)
	return err
}

func (c *openAPIChecker) reference(at string, raw any) {
	ref, _ := raw.(string)
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		c.fail(at, "$ref %q is not a local component schema", ref)
		return
	}
	components, _ := c.document["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if _, ok := schemas[strings.TrimPrefix(ref, prefix)]; !ok {
		c.fail(at, "$ref %q has no target", ref)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
