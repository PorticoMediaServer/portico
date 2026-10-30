package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
)

// Every response leaves this server with `Cache-Control: no-store`, set once at
// the top of the handler chain. That is the right default for a catalogue whose
// contents are per-viewer authorised, and it is the wrong answer for artwork: a
// poster is bytes that never change, and `no-store` forbids a client from
// keeping it at all. A fifty-poster grid therefore re-downloaded fifty images on
// every render, every scroll-back and every app launch, and each of them paid a
// full authentication and an availability check on the way.
//
// The fix is not a max-age. A max-age means a viewer can be shown an image the
// owner replaced in the metadata editor five minutes ago, which is a visible
// behaviour change. `private, no-cache` means the opposite: store it, and
// revalidate it every single time. An unchanged image is then a bodyless 304,
// an edited one is served immediately, and nothing is ever stale.
//
// `private` is not decoration. These bytes are authorised per viewer and must
// never be held by a shared cache.
const revalidate = "private, no-cache"

// conditional sets the validator and the caching policy and reports whether the
// request can be answered with 304.
//
// The caller has already authorised the request when this runs, and that
// ordering is deliberate: whether a private image or a private page exists, and
// whether it has changed, are not public facts. A 304 is an answer, so it is
// only given to someone entitled to the answer.
func conditional(w http.ResponseWriter, r *http.Request, tag string) bool {
	if tag == "" {
		return false
	}
	quoted := `"` + tag + `"`
	w.Header().Set("ETag", quoted)
	w.Header().Set("Cache-Control", revalidate)
	if !matchesETag(r.Header.Get("If-None-Match"), quoted) {
		return false
	}
	// A 304 carries no body and no headers that describe one.
	w.Header().Del("Content-Type")
	w.Header().Del("Content-Length")
	w.WriteHeader(http.StatusNotModified)
	return true
}

// matchesETag implements the `If-None-Match` comparison: a list of entity tags,
// or `*`. Weak prefixes compare equal to their strong form, because the weak
// comparison function is the one this header uses.
func matchesETag(header, tag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		if weakETag(strings.TrimSpace(candidate)) == weakETag(tag) {
			return true
		}
	}
	return false
}

func weakETag(tag string) string { return strings.TrimPrefix(tag, "W/") }

// number formats an integer part of a validator.
func number(v int64) string { return strconv.FormatInt(v, 10) }

// responseTag folds the facts that identify a composed response into one
// validator. Anything that can change the bytes has to be in here — the viewer's
// authority fence as well as the catalogue revision — or a restriction change
// could be answered from a cache with the page the viewer used to be allowed to
// see.
func responseTag(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}
