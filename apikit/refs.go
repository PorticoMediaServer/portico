package apikit

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
)

// Refs names the ids a request may carry in its query or body that the
// server may need to replace before the handler reads them, such as the id of
// a title that has since been merged into another. Resolve receives every
// such value a request carries and returns the replacements; nil or an empty
// map when nothing changes, which leaves the request byte for byte as sent.
type Refs struct {
	// Names are the query parameter and JSON member names whose string
	// values, or string-array elements, are ids. A request member tagged
	// `ref:"title"` counts whatever its name.
	Names   []string
	Resolve func(q *http.Request, values []string) map[string]string
}

// RewriteRefs installs the id replacement every route's query and JSON body
// pass before the handler reads them.
func (r *Registry) RewriteRefs(refs Refs) { r.refs = refs }

// refPath is one place in a request body that carries ids: its member names
// from the document root, "*" for an array's elements.
type refPath []string

// bodyRefPaths lists where a request type carries ids. It is worked out once
// per route at registration.
func (r *Registry) bodyRefPaths(request reflect.Type) []refPath {
	if r.refs.Resolve == nil || request == nil {
		return nil
	}
	var out []refPath
	var walk func(t reflect.Type, at refPath, depth int)
	walk = func(t reflect.Type, at refPath, depth int) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || depth > 8 {
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if f.Anonymous && name == "" {
				walk(ft, at, depth+1)
				continue
			}
			if name == "" {
				name = f.Name
			}
			here := append(append(refPath{}, at...), name)
			marked := f.Tag.Get("ref") == "title" || slices.Contains(r.refs.Names, name)
			switch {
			case ft.Kind() == reflect.String && marked:
				out = append(out, here)
			case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String && marked:
				out = append(out, append(here, "*"))
			case ft.Kind() == reflect.Slice:
				walk(ft.Elem(), append(here, "*"), depth+1)
			case ft.Kind() == reflect.Struct:
				walk(ft, here, depth+1)
			}
		}
	}
	walk(request, nil, 0)
	return out
}

// rewriteQuery replaces ids named in the query string.
func (r *Registry) rewriteQuery(q *http.Request) {
	if r.refs.Resolve == nil || q.URL.RawQuery == "" {
		return
	}
	query := q.URL.Query()
	var values []string
	for _, name := range r.refs.Names {
		for _, value := range query[name] {
			if value != "" {
				values = append(values, value)
			}
		}
	}
	if len(values) == 0 {
		return
	}
	replace := r.refs.Resolve(q, values)
	if len(replace) == 0 {
		return
	}
	for _, name := range r.refs.Names {
		for i, value := range query[name] {
			if next, ok := replace[value]; ok {
				query[name][i] = next
			}
		}
	}
	q.URL.RawQuery = query.Encode()
}

// rewriteBody returns raw with the ids at paths replaced, or raw itself when
// nothing changes or the document is not one a strict decoder would accept
// (the handler then refuses it as it would have anyway).
func (r *Registry) rewriteBody(q *http.Request, raw []byte, paths []refPath) []byte {
	if len(paths) == 0 || len(raw) == 0 {
		return raw
	}
	mentioned := false
	for _, path := range paths {
		leaf := path[len(path)-1]
		if leaf == "*" {
			leaf = path[len(path)-2]
		}
		if bytes.Contains(raw, []byte(`"`+leaf+`"`)) {
			mentioned = true
			break
		}
	}
	if !mentioned {
		return raw
	}
	tokens := json.NewDecoder(bytes.NewReader(raw))
	tokens.UseNumber()
	if uniqueValue(tokens) != nil {
		return raw
	}
	if _, err := tokens.Token(); err != io.EOF {
		return raw
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var document any
	if d.Decode(&document) != nil {
		return raw
	}
	var values []string
	visit := func(apply func(string) (string, bool)) {
		for _, path := range paths {
			refVisit(document, path, apply)
		}
	}
	visit(func(v string) (string, bool) {
		if v != "" {
			values = append(values, v)
		}
		return v, false
	})
	if len(values) == 0 {
		return raw
	}
	replace := r.refs.Resolve(q, values)
	if len(replace) == 0 {
		return raw
	}
	visit(func(v string) (string, bool) {
		next, ok := replace[v]
		return next, ok
	})
	var out bytes.Buffer
	e := json.NewEncoder(&out)
	e.SetEscapeHTML(false)
	if e.Encode(document) != nil {
		return raw
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n"))
}

// refVisit calls apply on every string at path inside a decoded document and
// stores what it returns when it says so.
func refVisit(node any, path refPath, apply func(string) (string, bool)) {
	if len(path) == 0 {
		return
	}
	switch value := node.(type) {
	case map[string]any:
		if path[0] == "*" {
			return
		}
		child, ok := value[path[0]]
		if !ok {
			return
		}
		if len(path) == 1 {
			if s, ok := child.(string); ok {
				if next, set := apply(s); set {
					value[path[0]] = next
				}
			}
			return
		}
		refVisit(child, path[1:], apply)
	case []any:
		if path[0] != "*" {
			return
		}
		for i, child := range value {
			if len(path) == 1 {
				if s, ok := child.(string); ok {
					if next, set := apply(s); set {
						value[i] = next
					}
				}
				continue
			}
			refVisit(child, path[1:], apply)
		}
	}
}

// rewriteHTTPBody replaces ids in the body an HTTP handler is about to read.
// Nothing is read until the handler reads: it checks who is asking and takes
// its own admission first, so a refused or waiting request never has its body
// read. A body larger than the route allows is passed on untouched for the
// handler to refuse. The declared Content-Length stays the client's.
func (r *Registry) rewriteHTTPBody(q *http.Request, limit int64, paths []refPath) {
	if len(paths) == 0 || q.Body == nil || q.Body == http.NoBody || limit <= 0 {
		return
	}
	q.Body = &refRewriteBody{source: q.Body, limit: limit, rewrite: func(raw []byte) []byte { return r.rewriteBody(q, raw, paths) }}
}

// refRewriteBody reads and rewrites the whole body on the handler's first read.
type refRewriteBody struct {
	source  io.ReadCloser
	limit   int64
	rewrite func([]byte) []byte
	reader  io.Reader
}

func (b *refRewriteBody) Read(p []byte) (int, error) {
	if b.reader == nil {
		raw, err := io.ReadAll(io.LimitReader(b.source, b.limit+1))
		switch {
		case err != nil:
			b.reader = io.MultiReader(bytes.NewReader(raw), errorReader{err})
		case int64(len(raw)) > b.limit:
			b.reader = io.MultiReader(bytes.NewReader(raw), b.source)
		default:
			b.reader = bytes.NewReader(b.rewrite(raw))
		}
	}
	return b.reader.Read(p)
}

func (b *refRewriteBody) Close() error { return b.source.Close() }

type errorReader struct{ err error }

func (e errorReader) Read([]byte) (int, error) { return 0, e.err }
