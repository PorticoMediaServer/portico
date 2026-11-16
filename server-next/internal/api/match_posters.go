package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
)

// A title offered as a match shows its poster (definition area 4, section 1: "each shows its
// poster, fetched by the server"). An app never contacts a provider, so a candidate carries the
// server's address for its poster instead of the provider's: /v1/match-posters/{token}, a long
// random token that only an answer to an owner or administrator contains. Like an image
// address it is its own permission, so an <img> can load it. The first request downloads the
// picture through the server's picture fetching and limits into the picture store and sends
// the app on to /v1/images/{hash}; nothing is downloaded for a poster nobody looks at. Tokens
// live in memory: after a restart an app reads the list again and gets new ones.

// matchPosterLimit bounds the remembered addresses; past it the book starts again.
const matchPosterLimit = 20_000

type matchPosters struct {
	mu    sync.Mutex
	url   map[string]string // token -> the provider's address
	token map[string]string // the provider's address -> token
	hash  map[string]string // token -> the picture's hash, once fetched
}

// posterAddress is the server's address for a candidate's poster, or "" when it has none.
func (s *Server) posterAddress(providerURL string) string {
	if providerURL == "" {
		return ""
	}
	p := &s.matchPosters
	p.mu.Lock()
	defer p.mu.Unlock()
	if token, known := p.token[providerURL]; known {
		return "/v1/match-posters/" + token
	}
	if len(p.url) >= matchPosterLimit || p.url == nil {
		p.url, p.token, p.hash = map[string]string{}, map[string]string{}, map[string]string{}
	}
	raw := make([]byte, 16)
	rand.Read(raw)
	token := hex.EncodeToString(raw)
	p.url[token], p.token[providerURL] = providerURL, token
	return "/v1/match-posters/" + token
}

func (s *Server) matchPosterRoutes() {
	route := &Route{Pattern: "GET /v1/match-posters/{token}", Access: Public, Scope: ScopeOpen}
	s.routes = append(s.routes, route)
	s.mux.HandleFunc(route.Pattern, func(w http.ResponseWriter, r *http.Request) {
		token, p := r.PathValue("token"), &s.matchPosters
		p.mu.Lock()
		address, hash := p.url[token], p.hash[token]
		p.mu.Unlock()
		if address == "" {
			s.writeError(w, r, ErrNotFound())
			return
		}
		if _, _, err := s.artwork.Read(hash, 1, 1); hash == "" || err != nil {
			if hash, err = s.metadata.CandidatePoster(r.Context(), address); err != nil {
				s.writeError(w, r, ErrNotFound()) // the provider has no picture to give now: the app draws its own cover
				return
			}
			p.mu.Lock()
			if p.url[token] == address {
				p.hash[token] = hash
			}
			p.mu.Unlock()
		}
		target := "/v1/images/" + hash
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
	})
}
