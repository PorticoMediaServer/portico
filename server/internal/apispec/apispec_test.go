package apispec

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// The generated API document must parse before any contract checks run.
func TestEveryDocumentParses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	files, err := documentFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err = yaml.Unmarshal(raw, &doc); err != nil {
			t.Errorf("%s: %v", filepath.Base(file), err)
		}
	}
}

func TestArrayMaximum(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	doc, schema, err := Schema("FilesystemPage")
	if err != nil {
		t.Fatal(err)
	}
	root := map[string]any{"path": "/", "name": "/", "kind": "root", "description": "root"}
	roots := make([]any, 65)
	for i := range roots {
		roots[i] = root
	}
	page := map[string]any{"path": "", "parent": "", "separator": "/", "platform": "linux", "writable": false, "writableChecked": false, "suggestions": []any{}, "truncated": true, "roots": roots[:64], "entries": []any{}, "nextCursor": ""}
	if problems := doc.Validate(schema, page); len(problems) != 0 {
		t.Fatal(problems)
	}
	page["roots"] = roots
	if problems := doc.Validate(schema, page); len(problems) == 0 {
		t.Fatal("spec accepted 65 roots")
	}
}

// An optional member may be omitted; it is not nullable unless the schema says
// so. The Go wire types send an omitempty pointer by omission, so an explicit
// null is neither that producer's absent value nor a shape the schema declares.
func TestOptionalMemberNullIsNotAbsence(t *testing.T) {
	doc := Document{}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"needed": map[string]any{"type": "integer"},
			"note":   map[string]any{"type": "string"},
			"maybe":  map[string]any{"type": "string", "nullable": true},
		},
		"required":             []string{"needed"},
		"additionalProperties": false,
	}
	for name, body := range map[string]string{
		"omitted optional":  `{"needed":1}`,
		"declared nullable": `{"needed":1,"maybe":null}`,
	} {
		if issues := doc.ValidateJSON(schema, []byte(body)); len(issues) != 0 {
			t.Fatalf("%s: %v", name, issues)
		}
	}
	for name, body := range map[string]string{
		"optional null":    `{"needed":1,"note":null}`,
		"required null":    `{"needed":null}`,
		"missing required": `{}`,
	} {
		if issues := doc.ValidateJSON(schema, []byte(body)); len(issues) == 0 {
			t.Fatalf("%s accepted", name)
		}
	}
}

// pattern is part of the declared shape: a conforming value passes, a
// non-matching one fails, and a pattern that does not compile is reported
// rather than silently accepting every value.
func TestStringPattern(t *testing.T) {
	doc := Document{}
	schema := map[string]any{"type": "string", "pattern": `^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]{1,9})?Z$`}
	if issues := doc.Validate(schema, "2026-10-27T01:02:03.123Z"); len(issues) != 0 {
		t.Fatal(issues)
	}
	for _, value := range []string{"not-a-time", "2026-10-27T01:02:03+00:00", "2026-10-27T01:02:03.1234567890Z"} {
		if issues := doc.Validate(schema, value); len(issues) == 0 {
			t.Fatalf("%q accepted", value)
		}
	}
	if issues := doc.Validate(map[string]any{"type": "string", "pattern": "("}, "anything"); len(issues) == 0 {
		t.Fatal("an uncompilable pattern accepted every value")
	}
}
