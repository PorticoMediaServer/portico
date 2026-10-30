package apikit

import (
	"bytes"
	_ "embed"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"reflect"
	"sort"
	"strings"
	"time"
)

//go:embed templates/runtime.ts.txt
var runtimeTemplate []byte

//go:embed templates/route-builders.ts.txt
var routeBuilderTemplate []byte

//go:embed templates/index.ts.txt
var indexTemplate []byte

var (
	timeType = reflect.TypeFor[time.Time]()
	rawType  = reflect.TypeFor[json.RawMessage]()
)

// Schema derives the wire shape from the exact Go type a registered handler uses.
// Unsupported encodings fail generation instead of publishing an invented type.
// time.Time is an RFC 3339 string; json.RawMessage is any JSON value, the empty schema.
func Schema(t reflect.Type) (map[string]any, error) { return schema(t, map[reflect.Type]bool{}, nil) }

// SchemaProvider lets a wire type state its own schema (an opaque JSON value is
// {}: any value, generated as TypeScript `unknown`).
type SchemaProvider interface{ JSONSchema() map[string]any }

// StatusSchemaProvider narrows a response union for a declared HTTP status.
// JSONSchema still describes all response variants for the shared SDK decoder.
// A nil status schema retains that union. This never declares a new status.
type StatusSchemaProvider interface {
	SchemaProvider
	JSONSchemaForStatus(int) map[string]any
}

func schemaForStatus(t reflect.Type, status int, fallback map[string]any) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var provider StatusSchemaProvider
	if t.Implements(reflect.TypeFor[StatusSchemaProvider]()) {
		provider = reflect.Zero(t).Interface().(StatusSchemaProvider)
	} else if reflect.PointerTo(t).Implements(reflect.TypeFor[StatusSchemaProvider]()) {
		provider = reflect.New(t).Interface().(StatusSchemaProvider)
	}
	if provider != nil {
		if shape := provider.JSONSchemaForStatus(status); shape != nil {
			return shape
		}
	}
	return fallback
}

func schema(t reflect.Type, stack map[reflect.Type]bool, overrides map[reflect.Type]map[string]any) (map[string]any, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if override, ok := overrides[t]; ok {
		return override, nil
	}
	switch t {
	case timeType:
		return map[string]any{"type": "string", "format": "date-time"}, nil
	case rawType:
		return map[string]any{}, nil
	}
	provider := reflect.TypeFor[SchemaProvider]()
	if t.Implements(provider) {
		return reflect.Zero(t).Interface().(SchemaProvider).JSONSchema(), nil
	}
	if reflect.PointerTo(t).Implements(provider) {
		return reflect.New(t).Interface().(SchemaProvider).JSONSchema(), nil
	}
	if stack[t] {
		return nil, fmt.Errorf("recursive type requires explicit schema: %s", t)
	}
	stack[t] = true
	defer delete(stack, t)
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer", "minimum": int64(-9007199254740991), "maximum": int64(9007199254740991)}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Interface:
		// Preference registry values have a published per-field type. Keep the
		// enclosing document typed while leaving those values open to that
		// registry, rather than pretending every value is a string.
		return map[string]any{"type": "any"}, nil
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return nil, fmt.Errorf("binary fields need an explicit wire type: %s", t)
		}
		item, err := schema(t.Elem(), stack, overrides)
		return map[string]any{"type": "array", "items": item}, err
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("map key must be string: %s", t)
		}
		value, err := schema(t.Elem(), stack, overrides)
		return map[string]any{"type": "object", "additionalProperties": value}, err
	case reflect.Struct:
		fields, err := structFields(t, stack, 0, overrides)
		if err != nil {
			return nil, err
		}
		props := map[string]any{}
		required := []string{}
		order := []string{}
		byName := map[string][]wireField{}
		for _, f := range fields {
			if byName[f.name] == nil {
				order = append(order, f.name)
			}
			byName[f.name] = append(byName[f.name], f)
		}
		for _, name := range order {
			f, err := dominant(byName[name])
			if err != nil {
				return nil, fmt.Errorf("%w: %s in %s", err, name, t)
			}
			props[name] = f.value
			if f.required {
				required = append(required, name)
			}
		}
		return map[string]any{"type": "object", "properties": props, "required": required}, nil
	}
	return nil, fmt.Errorf("unsupported wire type %s", t)
}

// wireField is one JSON member of a struct, before encoding/json's rules pick
// between same-named members of the struct and its embedded structs.
type wireField struct {
	name     string
	tagged   bool
	depth    int
	value    map[string]any
	required bool
}

var (
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
)

// structFields lists a struct's JSON members. An embedded exported struct
// without a JSON name contributes its own members one level deeper, as
// encoding/json promotes them. Embedding anything else unnamed (a pointer, a
// non-struct, or a type with its own JSON encoding, which would take over the
// outer struct's) still needs an explicit name.
func structFields(t reflect.Type, stack map[reflect.Type]bool, depth int, overrides map[reflect.Type]map[string]any) ([]wireField, error) {
	fields := []wireField{}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		tag := strings.Split(field.Tag.Get("json"), ",")
		name := tag[0]
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			embedded := field.Type
			if embedded.Kind() != reflect.Struct || embedded.Implements(jsonMarshaler) || embedded.Implements(textMarshaler) || reflect.PointerTo(embedded).Implements(jsonMarshaler) || reflect.PointerTo(embedded).Implements(textMarshaler) {
				return nil, fmt.Errorf("embedded fields need explicit names: %s", t)
			}
			if stack[embedded] {
				return nil, fmt.Errorf("recursive type requires explicit schema: %s", embedded)
			}
			stack[embedded] = true
			promoted, err := structFields(embedded, stack, depth+1, overrides)
			delete(stack, embedded)
			if err != nil {
				return nil, err
			}
			fields = append(fields, promoted...)
			continue
		}
		tagged := name != ""
		if name == "" {
			name = field.Name
		}
		var value map[string]any
		var err error
		if strings.Contains(field.Tag.Get("json"), ",string") {
			value = map[string]any{"type": "string"}
		} else {
			value, err = schema(field.Type, stack, overrides)
		}
		if err != nil {
			return nil, err
		}
		required := !strings.Contains(field.Tag.Get("json"), ",omitempty")
		// A pointer that is always sent is null when unset: the member is
		// required and nullable. (An omitempty pointer is simply optional.)
		if required && field.Type.Kind() == reflect.Pointer && len(value) > 0 {
			nullable := map[string]any{"nullable": true}
			for k, v := range value {
				nullable[k] = v
			}
			value = nullable
		}
		fields = append(fields, wireField{name: name, tagged: tagged, depth: depth, value: value, required: required})
	}
	return fields, nil
}

// dominant applies encoding/json's rule to same-named members: the shallowest
// wins, then the only tagged one at that depth. Where encoding/json would drop
// every candidate, generation fails rather than publish a member that is never sent.
func dominant(candidates []wireField) (wireField, error) {
	shallowest := candidates[0].depth
	for _, f := range candidates {
		shallowest = min(shallowest, f.depth)
	}
	var top, tagged []wireField
	for _, f := range candidates {
		if f.depth == shallowest {
			top = append(top, f)
			if f.tagged {
				tagged = append(tagged, f)
			}
		}
	}
	if len(top) == 1 {
		return top[0], nil
	}
	if len(tagged) == 1 {
		return tagged[0], nil
	}
	return wireField{}, errors.New("ambiguous JSON member")
}

type Artifacts struct {
	Runtime    []byte
	Index      []byte
	Errors     []byte
	Limits     []byte
	Decoders   []byte
	OpenAPI    []byte
	Typescript []byte
	Routes     []byte
	RoutePaths []byte
	Schemas    []byte
}

// Options names a generated contract. The zero value is the server's contract.
type Options struct {
	// Title is the OpenAPI info title; empty means "Portico API".
	Title string
	// Errors is the published error catalogue. Nil publishes apikit's catalogue
	// (the server's); a non-nil slice, even an empty one, publishes exactly those
	// definitions, for a service with its own error envelope.
	Errors []apierror.Definition
	// WireSchemas adds bounds published by the service owning a wire type.
	WireSchemas map[reflect.Type]map[string]any
	// PendingTaggedUnions lists routes whose response unions still lack a
	// required const discriminator (O-159 R-09b migration; ends empty).
	PendingTaggedUnions map[string]bool
	// GrandfatheredUnionTags keeps a pre-R-09b domain tag ("route path" → tag);
	// every other response union is tagged by variant.
	GrandfatheredUnionTags map[string]string
	// RequireGrandfatheredUse fails generation on a grandfathered entry that
	// matches no union. Set only by the full generator, not route subsets.
	RequireGrandfatheredUse bool
	// Components gives reusable wire types descriptive names in OpenAPI.
	Components map[string]reflect.Type
	// ServiceAccessRules lists access labels this service authenticates itself,
	// outside Registry. Generation accepts a route with one of these labels only
	// as an authenticated route: its metadata still passes every other check,
	// including a non-open scope. The label is published exactly as declared. It
	// never reaches Registry admission, so declaring one cannot widen what a
	// request may call. Nil keeps generation closed to undeclared labels.
	//
	// A label must be non-empty, unique, and distinct from apikit's own access
	// rules; a service cannot redefine public, device, viewer, owner or admin.
	ServiceAccessRules []Access
}

// Generate emits the server's contract, titled "Portico API".
func Generate(definitions []Definition) (Artifacts, error) {
	return GenerateWith(definitions, Options{})
}

// GenerateWith emits a contract for another Portico service from its route definitions.
func GenerateWith(definitions []Definition, options Options) (Artifacts, error) {
	if options.Title == "" {
		options.Title = "Portico API"
	}
	definitions = append([]Definition{}, definitions...)
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	serviceAccess, err := serviceAccessAllowlist(options.ServiceAccessRules)
	if err != nil {
		return Artifacts{}, err
	}
	paths := map[string]any{}
	schemas := map[string]any{}
	routes := []Metadata{}
	var ts strings.Builder
	var decoders strings.Builder
	decoders.WriteString("// Generated by apigen. Do not edit.\nimport { decode } from './runtime.ts';\nimport type * as types from './types.ts';\n")
	ts.WriteString("// Generated by apigen. Do not edit.\n")
	var contractTypes strings.Builder
	contractTypes.WriteString("export type RouteContracts = {\n")
	anyJSON := false
	nullable := false
	grandfatherUsed := map[string]bool{}
	for _, d := range definitions {
		// Generation must refuse incomplete metadata even when a caller supplies
		// definitions directly rather than going through Registry registration.
		// A service access label is authenticated outside Registry, so check it
		// against the generic authenticated rule; the label itself is published
		// unchanged below and never becomes an admission rule.
		metadata := d.Metadata
		if serviceAccess[metadata.Access] {
			metadata.Access = Device
		}
		if err := validate(metadata); err != nil {
			return Artifacts{}, fmt.Errorf("route %s: %w", d.ID, err)
		}
		request, err := schema(d.Request, map[reflect.Type]bool{}, options.WireSchemas)
		if err != nil {
			return Artifacts{}, err
		}
		response, err := schema(d.Response, map[reflect.Type]bool{}, options.WireSchemas)
		if err != nil {
			return Artifacts{}, err
		}
		anyJSON = anyJSON || usesAny(request) || usesAny(response)
		nullable = nullable || usesNullable(request) || usesNullable(response)
		name := exportName(d.ID)
		fmt.Fprintf(&decoders, "export function decode%sResponse(input: unknown): types.%sResponse { return decode(%q, input) as types.%sResponse; }\n", name, name, name+"Response", name)
		fmt.Fprintf(&contractTypes, "%q: { request: %sRequest; response: %sResponse };\n", d.ID, name, name)
		schemas[name+"Request"] = request
		if err := discriminateResponse(d.ID, response, options.PendingTaggedUnions, options.GrandfatheredUnionTags, grandfatherUsed); err != nil {
			return Artifacts{}, err
		}
		schemas[name+"Response"] = response
		ts.WriteString("export type " + name + "Request = " + tsType(request) + ";\n")
		ts.WriteString("export type " + name + "Response = " + tsType(response) + ";\n")
		contentType := d.ResponseContentType
		if contentType == "" {
			contentType = "application/json"
		}
		operation := map[string]any{"operationId": d.ID, "summary": d.Summary, "tags": []string{string(d.Audience)}, "x-audience": d.Audience, "x-access": d.Access, "x-scope": d.Requires, "x-lane": d.Lane, "x-cost": d.Cost, "responses": map[string]any{fmt.Sprint(d.Status): map[string]any{"description": "Success", "content": map[string]any{contentType: map[string]any{"schema": schemaForStatus(d.Response, d.Status, response)}}}}}
		if d.Protocol != "" {
			operation["x-protocol"] = d.Protocol
		}
		parameters := []any{}
		for _, part := range strings.Split(d.Path, "/") {
			if strings.HasPrefix(part, "{") {
				end := strings.IndexByte(part, '}')
				if end > 1 {
					parameters = append(parameters, map[string]any{"name": part[1:end], "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
				}
			}
		}
		for _, key := range d.Query {
			required := false
			for _, integer := range d.QueryIntegers {
				if integer.Name == key {
					required = integer.Required
				}
			}
			parameters = append(parameters, map[string]any{"name": key, "in": "query", "required": required, "schema": querySchema(d.Metadata, key)})
		}
		for _, key := range d.Headers {
			parameters = append(parameters, map[string]any{"name": key, "in": "header", "required": false, "schema": map[string]any{"type": "string"}})
		}
		if len(parameters) > 0 {
			operation["parameters"] = parameters
		}
		operation["x-error-codes"] = d.Errors
		operation["responses"].(map[string]any)["default"] = map[string]any{"description": "Request refused or unavailable", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "required": []string{"error"}, "properties": map[string]any{"error": map[string]any{"type": "object", "required": []string{"code", "message"}, "properties": map[string]any{"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}}}}}}}}
		operation["x-admission"] = d.Admission
		if d.Status == 204 || d.NoResponseBody {
			operation["responses"].(map[string]any)[fmt.Sprint(d.Status)] = map[string]any{"description": "Success"}
		}
		if d.SSE {
			field, ok := d.Response.FieldByName("Events")
			if !ok || field.Type.Kind() != reflect.Slice {
				return Artifacts{}, fmt.Errorf("SSE response %s must expose Events", d.ID)
			}
			event, err := Schema(field.Type.Elem())
			if err != nil {
				return Artifacts{}, err
			}
			operation["responses"].(map[string]any)[fmt.Sprint(d.Status)].(map[string]any)["content"].(map[string]any)["text/event-stream"] = map[string]any{"schema": map[string]any{"type": "string"}, "x-event-schema": event}
		}
		for _, status := range append(append([]int{}, d.SuccessStatuses...), d.ResponseStatuses...) {
			reply := map[string]any{"description": "Success"}
			if status != 204 && !d.NoResponseBody {
				reply["content"] = map[string]any{contentType: map[string]any{"schema": schemaForStatus(d.Response, status, response)}}
			}
			operation["responses"].(map[string]any)[fmt.Sprint(status)] = reply
		}
		for _, header := range d.ResponseHeaders {
			reply, ok := operation["responses"].(map[string]any)[fmt.Sprint(header.Status)].(map[string]any)
			if !ok {
				return Artifacts{}, fmt.Errorf("response header %s names undeclared status %d", header.Name, header.Status)
			}
			headers, ok := reply["headers"].(map[string]any)
			if !ok {
				headers = map[string]any{}
				reply["headers"] = headers
			}
			headers[header.Name] = map[string]any{"description": header.Description, "required": header.Required, "schema": map[string]any{"type": "string"}}
		}
		if d.BodyLimit > 0 {
			operation["requestBody"] = map[string]any{"required": !d.OptionalBody, "content": map[string]any{"application/json": map[string]any{"schema": request}}}
			if len(d.RequestContentTypes) > 0 {
				content := map[string]any{}
				for _, typ := range d.RequestContentTypes {
					shape := request
					if typ != "application/json" && typ != "multipart/form-data" {
						shape = map[string]any{"type": "string", "format": "binary"}
					}
					content[typ] = map[string]any{"schema": shape}
				}
				operation["requestBody"].(map[string]any)["content"] = content
			}
			operation["x-max-body-bytes"] = d.BodyLimit
		}
		if paths[d.Path] == nil {
			paths[d.Path] = map[string]any{}
		}
		paths[d.Path].(map[string]any)[strings.ToLower(d.Method)] = operation
		routes = append(routes, d.Metadata)
	}
	for key := range options.GrandfatheredUnionTags {
		if options.RequireGrandfatheredUse && !grandfatherUsed[key] {
			return Artifacts{}, fmt.Errorf("grandfathered union tag %q matches no response union; remove it", key)
		}
	}
	components := map[string]any{}
	for name, schema := range schemas {
		components[name] = schema
	}
	for name, typ := range options.Components {
		component, err := schema(typ, map[reflect.Type]bool{}, options.WireSchemas)
		if err != nil {
			return Artifacts{}, err
		}
		components[name] = component
	}
	spec := map[string]any{"openapi": "3.1.0", "info": map[string]any{"title": options.Title, "version": "1"}, "tags": []map[string]any{
		{"name": string(Integrations), "description": "Operations intended for external integrations and automation. Audience is documentation; access rules and credential scopes still apply."},
		{"name": string(Clients), "description": "Operations intended for Portico clients. Audience is documentation; access rules and credential scopes still apply."},
	}, "paths": paths, "components": map[string]any{"schemas": components}}
	openapi, err := json.Marshal(spec)
	if err != nil {
		return Artifacts{}, err
	}
	var document any
	if err = json.Unmarshal(openapi, &document); err != nil {
		return Artifacts{}, err
	}
	document = openAPISchema(document)
	openapi, err = json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Artifacts{}, err
	}
	if err = CheckOpenAPI(openapi); err != nil {
		return Artifacts{}, err
	}
	rawSchemas, _ := json.MarshalIndent(schemas, "", "  ")
	rawRoutes, _ := json.MarshalIndent(routes, "", "  ")
	pathTable := map[string]string{}
	for _, route := range definitions {
		pathTable[route.ID] = route.Path
	}
	rawPaths, _ := json.MarshalIndent(pathTable, "", "  ")
	contractTypes.WriteString("};\n")
	ts.WriteString(contractTypes.String())
	definitionsErrors := options.Errors
	if definitionsErrors == nil {
		catalogue, _ := apierror.New()
		definitionsErrors = catalogue.Definitions()
	} else {
		definitionsErrors = append([]apierror.Definition{}, definitionsErrors...)
	}
	sort.Slice(definitionsErrors, func(i, j int) bool { return definitionsErrors[i].Code < definitionsErrors[j].Code })
	rawErrors, _ := json.MarshalIndent(definitionsErrors, "", "  ")
	runtime := append([]byte{}, runtimeTemplate...)
	if anyJSON {
		if runtime, err = anyRuntime(runtime); err != nil {
			return Artifacts{}, err
		}
	}
	if nullable {
		if runtime, err = nullableRuntime(runtime); err != nil {
			return Artifacts{}, err
		}
	}
	return Artifacts{Runtime: runtime, Index: append([]byte{}, indexTemplate...), OpenAPI: append(openapi, '\n'), Typescript: []byte(ts.String()), Routes: []byte("// Generated by apigen. Do not edit.\nexport const routes = " + string(rawRoutes) + " as const;\nexport * from './route-paths.ts';\n"), RoutePaths: []byte("// Generated by apigen. Do not edit.\nexport const routePaths = " + string(rawPaths) + " as const;\n" + string(routeBuilderTemplate)), Schemas: []byte("// Generated by apigen. Do not edit.\nexport const schemas = " + string(rawSchemas) + " as const;\n"), Errors: []byte("// Generated by apigen. Do not edit.\nexport const errors = " + string(rawErrors) + " as const;\n"), Limits: []byte("// Generated by apigen. Do not edit.\nexport const limits = { responseBytes: 524288, cursorChars: 4096, itemIds: 500 } as const;\n"), Decoders: []byte(decoders.String())}, nil
}

// serviceAccessAllowlist validates an explicit set of authenticated service
// access labels. Generation accepts a route with one of these labels only while
// holding its metadata to the generic authenticated (Device) rule. Registry
// admission never consults this list: the returned set is local to generation.
func serviceAccessAllowlist(rules []Access) (map[Access]bool, error) {
	allowed := make(map[Access]bool, len(rules))
	for _, label := range rules {
		if label == "" {
			return nil, errors.New("service access label required")
		}
		switch label {
		case Public, Device, ViewerLibrary, ViewerItem, Owner, Admin:
			return nil, fmt.Errorf("service access %q collides with a built-in access rule", label)
		}
		if allowed[label] {
			return nil, fmt.Errorf("duplicate service access %q", label)
		}
		allowed[label] = true
	}
	return allowed, nil
}

// querySchema is a query parameter's published schema: a string unless the
// route gives it integer bounds. A clamped maximum is an extension, because a
// JSON-Schema maximum would promise that larger values are refused.
func querySchema(m Metadata, name string) map[string]any {
	for _, integer := range m.QueryIntegers {
		if integer.Name != name {
			continue
		}
		out := map[string]any{"type": "integer"}
		if !integer.Required && !integer.NoDefault {
			out["default"] = integer.Default
		}
		if integer.DefaultBelowMinimum {
			out["x-default-below-minimum"] = integer.Minimum
		} else {
			out["minimum"] = integer.Minimum
		}
		if integer.Clamped {
			out["x-clamp-maximum"] = integer.Maximum
		} else if integer.DefaultAboveMaximum {
			out["x-default-above-maximum"] = integer.Maximum
		} else {
			out["maximum"] = integer.Maximum
		}
		if integer.ClampMaximum != 0 {
			out["x-clamp-maximum"] = integer.ClampMaximum
		}
		if integer.Description != "" {
			out["description"] = integer.Description
		}
		if integer.DefaultOnInvalid {
			out["type"] = []string{"integer", "string"}
			out["x-default-on-invalid"] = true
		} else if integer.EmptyIsDefault {
			// An explicitly empty query value is accepted by Get-based parsers.
			delete(out, "type")
			out["anyOf"] = []any{map[string]any{"type": "integer"}, map[string]any{"type": "string", "const": ""}}
		}
		if integer.EmptyIsDefault {
			out["x-empty-is-default"] = true
		}
		return out
	}
	return map[string]any{"type": "string"}
}

func exportName(s string) string {
	var b strings.Builder
	upper := true
	for _, r := range s {
		if r == '_' || r == '-' || r == '.' {
			upper = true
			continue
		}
		if upper {
			b.WriteString(strings.ToUpper(string(r)))
			upper = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// usesAny reports whether a schema contains the empty (any JSON value) schema.
func usesAny(s map[string]any) bool {
	if len(s) == 0 {
		return true
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if alternatives, ok := s[key].([]any); ok {
			for _, alternative := range alternatives {
				if usesAny(alternative.(map[string]any)) {
					return true
				}
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok && usesAny(items) {
		return true
	}
	if additional, ok := s["additionalProperties"].(map[string]any); ok && usesAny(additional) {
		return true
	}
	if props, ok := s["properties"].(map[string]any); ok {
		for _, p := range props {
			if usesAny(p.(map[string]any)) {
				return true
			}
		}
	}
	return false
}

// usesNullable reports whether a schema has a nullable member.
func usesNullable(s map[string]any) bool {
	if s["nullable"] == true {
		return true
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if alternatives, ok := s[key].([]any); ok {
			for _, alternative := range alternatives {
				if usesNullable(alternative.(map[string]any)) {
					return true
				}
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok && usesNullable(items) {
		return true
	}
	if additional, ok := s["additionalProperties"].(map[string]any); ok && usesNullable(additional) {
		return true
	}
	if props, ok := s["properties"].(map[string]any); ok {
		for _, p := range props {
			if usesNullable(p.(map[string]any)) {
				return true
			}
		}
	}
	return false
}

// nullableRuntime lets the decoder accept null for nullable members. Like
// anyRuntime it applies only when a contract has one, so other contracts keep
// the original runtime byte for byte.
func nullableRuntime(runtime []byte) ([]byte, error) {
	for _, edit := range [][2]string{
		{"minimum?: number; maximum?: number}", "minimum?: number; maximum?: number; nullable?: boolean}"},
		{"  const reject = (): never => { throw new Error(`Invalid contract value at ${path}`); };\n", "  const reject = (): never => { throw new Error(`Invalid contract value at ${path}`); };\n  if (input === null && schema.nullable) return null;\n"},
	} {
		if bytes.Count(runtime, []byte(edit[0])) != 1 {
			return nil, errors.New("runtime template changed; update nullableRuntime")
		}
		runtime = bytes.Replace(runtime, []byte(edit[0]), []byte(edit[1]), 1)
	}
	return runtime, nil
}

// anyRuntime lets the decoder pass through values of the empty schema. It applies
// only when a contract uses one, so contracts without free-form JSON keep the
// original runtime byte for byte.
func anyRuntime(runtime []byte) ([]byte, error) {
	for _, edit := range [][2]string{
		{"type Schema = {type: string;", "type Schema = {type?: string;"},
		{"  switch (schema.type) {\n", "  // The empty schema is any JSON value.\n  if (schema.type === undefined) return input;\n  switch (schema.type) {\n"},
	} {
		if bytes.Count(runtime, []byte(edit[0])) != 1 {
			return nil, errors.New("runtime template changed; update anyRuntime")
		}
		runtime = bytes.Replace(runtime, []byte(edit[0]), []byte(edit[1]), 1)
	}
	return runtime, nil
}
func tsType(s map[string]any) string {
	if s["nullable"] == true {
		plain := map[string]any{}
		for k, v := range s {
			if k != "nullable" {
				plain[k] = v
			}
		}
		return tsType(plain) + " | null"
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if alternatives, ok := s[key].([]any); ok {
			types := make([]string, 0, len(alternatives))
			tag, tagged := s["x-discriminator"].(string)
			for _, alternative := range alternatives {
				rendered := tsType(alternative.(map[string]any))
				// A tagged alternative carries its tag as a literal, so consumers narrow on it.
				if value, ok := singleTag(alternative.(map[string]any), tag); tagged && ok {
					rendered = fmt.Sprintf("(%s & { %q: %s })", rendered, tag, value)
				}
				types = append(types, rendered)
			}
			if len(types) == 0 {
				return "never"
			}
			// O-159 R-09b: a response tag this client does not know reads as an
			// unknown variant, so every consumer has to say what it does with one.
			if tag, ok := s["x-discriminator"].(string); ok {
				types = append(types, fmt.Sprintf("{ readonly unknownVariant: true; readonly %q: string }", tag))
			}
			return strings.Join(types, " | ")
		}
	}
	switch s["type"] {
	case "null":
		return "null"
	case nil:
		return "unknown"
	case "string":
		return "string"
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "any":
		return "unknown"
	case "array":
		return "Array<" + tsType(s["items"].(map[string]any)) + ">"
	case "object":
		if additional, ok := s["additionalProperties"].(map[string]any); ok {
			return "Record<string, " + tsType(additional) + ">"
		}
		props := s["properties"].(map[string]any)
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		required := map[string]bool{}
		switch names := s["required"].(type) {
		case nil:
		case []string:
			for _, name := range names {
				required[name] = true
			}
		case []any:
			// Custom schemas may have passed through encoding/json, whose
			// arrays are []any. Preserve their required-property semantics.
			for _, value := range names {
				name, ok := value.(string)
				if !ok {
					panic("required property name must be a string")
				}
				required[name] = true
			}
		default:
			panic("required properties must be an array")
		}
		fields := []string{}
		for _, key := range keys {
			suffix := "?"
			if required[key] {
				suffix = ""
			}
			fields = append(fields, fmt.Sprintf("%q%s: %s", key, suffix, tsType(props[key].(map[string]any))))
		}
		return "{ " + strings.Join(fields, "; ") + " }"
	}
	panic("unsupported schema")
}

// openAPISchema translates internal decoder annotations into JSON Schema 2020-12.
func openAPISchema(value any) any {
	switch v := value.(type) {
	case []any:
		for i := range v {
			v[i] = openAPISchema(v[i])
		}
		return v
	case map[string]any:
		for key, field := range v {
			v[key] = openAPISchema(field)
		}
		// An object with no required members says so by omitting the keyword.
		if required, ok := v["required"].([]any); ok && len(required) == 0 {
			delete(v, "required")
		}
		if v["type"] == "any" {
			delete(v, "type")
			v["not"] = map[string]any{"type": "null"}
		}
		if v["nullable"] == true {
			delete(v, "nullable")
			return map[string]any{"anyOf": []any{v, map[string]any{"type": "null"}}}
		}
		return v
	}
	return value
}
