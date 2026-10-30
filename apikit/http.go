package apikit

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
)

// RegisterHTTP declares the exact request and response types of an HTTP handler.
// The handler keeps its own access checks, decoding, headers and status selection.
// This is useful for multipart uploads, byte streams, and JSON handlers whose
// response semantics must survive a registration change. Credential scopes are
// still enforced by the registry before the handler is reached.
func RegisterHTTP[Request, Response any](r *Registry, metadata Metadata) error {
	metadata.HTTP = true
	if err := r.define(metadata, reflect.TypeFor[Request](), reflect.TypeFor[Response](), true); err != nil {
		return err
	}
	r.handlers = append(r.handlers, http.NotFound)
	return nil
}

// MountHTTP binds a declared HTTP contract to its existing handler. Undeclared
// or duplicate bindings fail at construction, before the server accepts traffic.
func (r *Registry) MountHTTP(mux *http.ServeMux, pattern string, handler http.Handler) error {
	return r.MountHTTPAt(mux, pattern, pattern, handler)
}

// MountHTTPAt retains the declared contract and its scope/rewrite wrapper while
// using an adapter-owned mux pattern. The adapter must reject paths outside the
// contract. This permits dispatch around crossing ServeMux wildcard patterns
// without changing a public route or skipping the registry's access checks.
func (r *Registry) MountHTTPAt(mux *http.ServeMux, pattern, mountPattern string, handler http.Handler) error {
	if handler == nil {
		return fmt.Errorf("missing HTTP handler for %s", pattern)
	}
	for i, d := range r.routes {
		if !d.HTTP || d.Method+" "+d.Path != pattern {
			continue
		}
		if r.bound == nil {
			r.bound = make(map[string]bool)
		}
		if r.bound[d.ID] {
			return fmt.Errorf("HTTP contract already bound: %s", pattern)
		}
		r.bound[d.ID] = true
		m := d.Metadata
		names := pathNames(d.Path)
		refs := r.bodyRefPaths(d.Request)
		serve := func(w http.ResponseWriter, q *http.Request) {
			if r.scopes != nil {
				if err := r.scopes(q, m.Requires); err != nil {
					r.errors.Write(w, "", err)
					return
				}
			}
			if r.rewrite != nil {
				for _, name := range names {
					if value := q.PathValue(name); value != "" {
						q.SetPathValue(name, r.rewrite(q, name, value))
					}
				}
			}
			r.rewriteQuery(q)
			r.rewriteHTTPBody(q, m.BodyLimit, refs)
			handler.ServeHTTP(w, q)
		}
		r.handlers[i] = serve
		mux.Handle(mountPattern, http.HandlerFunc(serve))
		return nil
	}
	return fmt.Errorf("HTTP handler has no typed contract: %s", pattern)
}

// pathNames lists a route path's wildcard names.
func pathNames(path string) []string {
	var names []string
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, "{") {
			if end := strings.IndexByte(part, '}'); end > 1 {
				names = append(names, strings.TrimSuffix(part[1:end], "..."))
			}
		}
	}
	return names
}

// MetadataForPattern reads an operation declaration by its registered pattern.
// It also covers HTTP path parameters whose syntax belongs to net/http.
func (r *Registry) MetadataForPattern(pattern string) (Metadata, bool) {
	for _, route := range r.routes {
		if route.Method+" "+route.Path == pattern {
			return route.Metadata, true
		}
	}
	return Metadata{}, false
}
