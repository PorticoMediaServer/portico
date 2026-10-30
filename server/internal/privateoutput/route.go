// Package privateoutput mounts decoder output on the SAME confined loopback
// endpoint as its input. No decoder receives filesystem write permission.
package privateoutput

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

type Route struct {
	Prefix  string
	handler http.Handler
}

func New(handler http.Handler) (*Route, error) {
	if handler == nil {
		return nil, errors.New("missing private output")
	}
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return nil, e
	}
	return &Route{Prefix: "/output/" + hex.EncodeToString(b[:]) + "/", handler: handler}, nil
}
func (r *Route) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r == nil || len(req.URL.Path) < len(r.Prefix) || subtle.ConstantTimeCompare([]byte(req.URL.Path[:len(r.Prefix)]), []byte(r.Prefix)) != 1 || req.URL.RawQuery != "" || req.URL.RawPath != "" {
		http.NotFound(w, req)
		return
	}
	tail := req.URL.Path[len(r.Prefix):]
	if tail == "" || strings.Contains(tail, "/") || req.Method != "PUT" {
		http.NotFound(w, req)
		return
	}
	child := req.Clone(req.Context())
	u := *req.URL
	u.Path = tail
	child.URL = &u
	r.handler.ServeHTTP(w, child)
}
