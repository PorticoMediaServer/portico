// Command portico-contracts writes what apps are built against, from the server's own route
// table and Go types: TypeScript types for every request and response with a typed route
// table, and an OpenAPI document. Run it from server-next after changing a route or a shape:
//
//	go run ./cmd/portico-contracts
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Where the two files go, relative to the server-next folder.
const (
	typesFile   = "../packages/contracts/src/next/api.ts"
	openAPIFile = "api/openapi.json"
)

func main() {
	dir := flag.String("dir", ".", "the server-next folder")
	flag.Parse()
	types, openAPI, err := generate()
	if err == nil {
		err = os.WriteFile(filepath.Join(*dir, typesFile), types, 0o644)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(*dir, openAPIFile), openAPI, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// generate builds a server on a throwaway database, only to read its route table.
func generate() (types, openAPI []byte, err error) {
	tmp, err := os.MkdirTemp("", "portico-contracts")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(tmp, "main.db"))
	if err != nil {
		return nil, nil, err
	}
	defer d.Close()
	srv, err := api.New(ctx, d, tmp, "dev", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return nil, nil, err
	}
	g := &generator{shapes: map[string]reflect.Type{}}
	var routes []route
	for _, r := range srv.Routes() {
		method, path, _ := strings.Cut(r.Pattern, " ")
		routes = append(routes, route{r.Pattern, method, path, accessNames[r.Access], g.use(r.Request), g.use(r.Response)})
	}
	// Every refusal has this one shape, whichever route it comes from.
	g.use(reflect.TypeFor[api.Error]())
	if g.err != nil {
		return nil, nil, g.err
	}
	return g.typeScript(routes), g.openAPI(routes), nil
}

var accessNames = map[api.Access]string{
	api.Public: "public", api.SignedIn: "signedIn", api.WithProfile: "withProfile", api.Admin: "admin", api.Owner: "owner",
}

type route struct {
	pattern, method, path, access string
	// request and response are shape names, or "" for none.
	request, response string
}

type generator struct {
	shapes map[string]reflect.Type
	err    error
}

var (
	noneType     = reflect.TypeFor[api.None]()
	timeType     = reflect.TypeFor[time.Time]()
	jsonType     = reflect.TypeFor[json.RawMessage]()
	unmarshaler  = reflect.TypeFor[json.Unmarshaler]()
	genericParts = regexp.MustCompile(`^(\w+)\[.*?(\w+)\]$`)
)

// name is what a struct is called in the contract. Page[Profile] is ProfilePage.
func name(t reflect.Type) string {
	if m := genericParts.FindStringSubmatch(t.Name()); m != nil {
		return m[2] + m[1]
	}
	return t.Name()
}

// use records a route's request or response shape, and every shape it holds, and returns
// its name. A route with no body or no reply has none.
func (g *generator) use(t reflect.Type) string {
	if t == nil || t == noneType {
		return ""
	}
	if t.Kind() != reflect.Struct || t.Name() == "" {
		g.fail("a route's request and response must be named structs, not %s", t)
		return ""
	}
	g.collect(t)
	return name(t)
}

func (g *generator) fail(format string, args ...any) {
	if g.err == nil {
		g.err = fmt.Errorf(format, args...)
	}
}

func (g *generator) collect(t reflect.Type) {
	if t == jsonType {
		return
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		g.collect(t.Elem())
	case reflect.Struct:
		if t == timeType || nullable(t) != nil {
			return
		}
		n := name(t)
		if n == "" {
			g.fail("the contract cannot name an anonymous struct: %s", t)
			return
		}
		if seen, ok := g.shapes[n]; ok {
			if seen != t {
				g.fail("two different shapes are both called %s", n)
			}
			return
		}
		g.shapes[n] = t
		for i := range t.NumField() {
			g.collect(t.Field(i).Type)
		}
	}
}

// nullable returns the value type of a struct that reads "a value, null or absent" from
// JSON by itself (an Unmarshaler with a Value field), or nil for any other type.
func nullable(t reflect.Type) reflect.Type {
	if t.Kind() != reflect.Struct || !reflect.PointerTo(t).Implements(unmarshaler) {
		return nil
	}
	f, ok := t.FieldByName("Value")
	if !ok {
		return nil
	}
	return f.Type
}

type field struct {
	name     string
	t        reflect.Type
	optional bool // may be left out
	null     bool // may be null
}

// fields lists a struct's JSON fields in declaration order, and the structs it embeds.
func fields(t reflect.Type) (embedded []reflect.Type, out []field) {
	for i := range t.NumField() {
		f := t.Field(i)
		tag, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case tag == "-" || !f.IsExported():
			continue
		case f.Anonymous && tag == "":
			embedded = append(embedded, f.Type)
			continue
		case tag == "":
			tag = f.Name
		}
		omit := strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero")
		ft, pointer := f.Type, f.Type.Kind() == reflect.Pointer
		if pointer {
			ft = ft.Elem()
		}
		if v := nullable(ft); v != nil {
			for v.Kind() == reflect.Pointer {
				v = v.Elem()
			}
			out = append(out, field{tag, v, true, true})
			continue
		}
		// A pointer that is always written is null when it has nothing to say.
		out = append(out, field{tag, ft, omit || pointer, pointer && !omit})
	}
	return embedded, out
}

func (g *generator) names() []string {
	names := make([]string, 0, len(g.shapes))
	for n := range g.shapes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// tsName keeps the error shape from hiding JavaScript's own Error.
func tsName(n string) string {
	if n == "Error" {
		return "ApiError"
	}
	return n
}

func tsType(t reflect.Type) string {
	if t == jsonType {
		return "unknown" // any JSON value, passed through as it is
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16,
		reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Pointer:
		return tsType(t.Elem())
	case reflect.Slice:
		return tsType(t.Elem()) + "[]"
	case reflect.Map:
		return "Record<string, " + tsType(t.Elem()) + ">"
	case reflect.Struct:
		if t == timeType {
			return "string"
		}
		return tsName(name(t))
	}
	panic(fmt.Sprintf("the contract has no TypeScript type for %s", t))
}

func (g *generator) typeScript(routes []route) []byte {
	var b bytes.Buffer
	b.WriteString("// Generated by server-next/cmd/portico-contracts from the server's Go types. Do not edit.\n")
	b.WriteString("// Times are RFC 3339 strings. Every string that names a kind, role or code is open: ignore values you do not know.\n")
	for _, n := range g.names() {
		embedded, fs := fields(g.shapes[n])
		fmt.Fprintf(&b, "\nexport interface %s", tsName(n))
		for i, e := range embedded {
			fmt.Fprintf(&b, "%s%s", []string{" extends ", ", "}[min(i, 1)], tsName(name(e)))
		}
		b.WriteString(" {\n")
		for _, f := range fs {
			fmt.Fprintf(&b, "  %s%s: %s%s;\n", f.name, map[bool]string{true: "?"}[f.optional], tsType(f.t), map[bool]string{true: " | null"}[f.null])
		}
		b.WriteString("}\n")
	}
	b.WriteString("\n/** The body of every refusal. */\nexport interface ErrorBody {\n  error: ApiError;\n}\n")
	b.WriteString("\n/** Every route: who may call it, what it takes and what it answers. */\nexport interface Routes {\n")
	for _, r := range routes {
		fmt.Fprintf(&b, "  '%s': {access: '%s'; request: %s; response: %s};\n", r.pattern, r.access, tsOrNone(r.request), tsOrNone(r.response))
	}
	b.WriteString("}\n")
	return b.Bytes()
}

func tsOrNone(shape string) string {
	if shape == "" {
		return "undefined"
	}
	return tsName(shape)
}

type object = map[string]any

func ref(n string) object { return object{"$ref": "#/components/schemas/" + n} }

func schema(t reflect.Type) object {
	if t == jsonType {
		return object{}
	}
	switch t.Kind() {
	case reflect.String:
		return object{"type": "string"}
	case reflect.Bool:
		return object{"type": "boolean"}
	case reflect.Float32, reflect.Float64:
		return object{"type": "number"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16,
		reflect.Uint32, reflect.Uint64:
		return object{"type": "integer"}
	case reflect.Pointer:
		return schema(t.Elem())
	case reflect.Slice:
		return object{"type": "array", "items": schema(t.Elem())}
	case reflect.Map:
		return object{"type": "object", "additionalProperties": schema(t.Elem())}
	case reflect.Struct:
		if t == timeType {
			return object{"type": "string", "format": "date-time"}
		}
		return ref(name(t))
	}
	panic(fmt.Sprintf("the contract has no schema for %s", t))
}

var pathParam = regexp.MustCompile(`\{(\w+)\}`)

func (g *generator) openAPI(routes []route) []byte {
	schemas := object{"ErrorBody": object{"type": "object", "required": []string{"error"}, "properties": object{"error": ref("Error")}}}
	for n, t := range g.shapes {
		embedded, fs := fields(t)
		props, required := object{}, []string{}
		for _, f := range fs {
			s := schema(f.t)
			if f.null {
				s = object{"anyOf": []object{s, {"type": "null"}}}
			}
			props[f.name] = s
			if !f.optional {
				required = append(required, f.name)
			}
		}
		s := object{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		if len(embedded) > 0 {
			all := []object{}
			for _, e := range embedded {
				all = append(all, ref(name(e)))
			}
			s = object{"allOf": append(all, s)}
		}
		schemas[n] = s
	}
	paths := object{}
	for _, r := range routes {
		op := object{"x-access": r.access}
		params := []object{}
		for _, m := range pathParam.FindAllStringSubmatch(r.path, -1) {
			params = append(params, object{"name": m[1], "in": "path", "required": true, "schema": object{"type": "string"}})
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if r.access == "public" {
			op["security"] = []object{}
		}
		if r.request != "" {
			op["requestBody"] = object{"required": true, "content": object{"application/json": object{"schema": ref(r.request)}}}
		}
		answer := object{"204": object{"description": "Done"}}
		if r.response != "" {
			answer = object{"200": object{"description": "OK", "content": object{"application/json": object{"schema": ref(r.response)}}}}
		}
		answer["default"] = object{"description": "A refusal", "content": object{"application/json": object{"schema": ref("ErrorBody")}}}
		op["responses"] = answer
		item, _ := paths[r.path].(object)
		if item == nil {
			item = object{}
			paths[r.path] = item
		}
		item[strings.ToLower(r.method)] = op
	}
	doc := object{
		"openapi":  "3.1.0",
		"info":     object{"title": "Portico", "version": fmt.Sprint(api.APIMajor)},
		"paths":    paths,
		"security": []object{{"bearer": []string{}}},
		"components": object{
			"schemas":         schemas,
			"securitySchemes": object{"bearer": object{"type": "http", "scheme": "bearer"}},
		},
	}
	// Maps are written with sorted keys, so the same routes always give the same file.
	out, _ := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n')
}
