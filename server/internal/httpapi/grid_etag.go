package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Grid validators use the requested resource's catalogue and semantic personal
// revisions. Token rotation and unrelated write transactions keep the tag.
// The time bucket bounds reuse of signed continuations embedded in the body.
var gridProcessNonce = func() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}()

const gridTagBucket = 5 * time.Minute

// gridTag is computed after the request is authorized and before the page is
// read. extra folds any further facts the caller's bytes depend on.
func (d Dependencies) gridTag(r *http.Request, p identity.Principal, bucket time.Duration, extra ...string) string {
	library := r.PathValue("id")
	var content, personal, settings, policy int64
	contentFence := ""
	// Routes naming an entity resolve its own library; a library route already
	// has that public library id. Global surfaces include the catalogue sum.
	if d.DB != nil {
		var resolved string
		if err := d.DB.QueryRowContext(r.Context(), `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, library).Scan(&resolved); err == nil {
			library = resolved
		}
		if err := d.DB.QueryRowContext(r.Context(), `SELECT COALESCE(sum(r.revision),0),COALESCE(sum(v.revision),0) FROM library_revisions r LEFT JOIN viewer_revisions v ON v.library_id=r.library_id AND v.profile_id=? WHERE r.library_id=? OR NOT EXISTS(SELECT 1 FROM library_revisions WHERE library_id=?)`, identity.PersonalKey(p.Viewer), library, library).Scan(&content, &personal); err != nil {
			return ""
		}
		if err := d.DB.QueryRowContext(r.Context(), `SELECT revision FROM console_settings_revision WHERE singleton=1`).Scan(&settings); err != nil {
			return ""
		}
		if d.Identity != nil {
			var err error
			_, contentFence, err = d.viewerRestrictions(r, p)
			if err != nil {
				return ""
			}
			if p.Authority != "local" {
				if err = d.DB.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT revision FROM policy WHERE server_id=?),0)`, d.Identity.ID()).Scan(&policy); err != nil {
					return ""
				}
			}
		}
	}
	parts := []string{"grid", gridProcessNonce, strconv.FormatInt(content, 10), strconv.FormatInt(personal, 10), strconv.FormatInt(settings, 10), strconv.FormatInt(policy, 10), contentFence, r.URL.Path, canonicalQuery(r.URL.Query()), viewerScope(p), strconv.Itoa(p.Epoch), identity.PersonalKey(p.Viewer), strconv.FormatInt(time.Now().UTC().UnixNano()/int64(bucket), 10)}
	return responseTag(append(parts, extra...)...)
}

// canonicalQuery orders the query so parameter order does not split the cache.
func canonicalQuery(q url.Values) string { return q.Encode() }

// gridNotModified answers 304 when the client's validator matches. It sets no
// header otherwise: the validator goes only on a successful page (setGridTag),
// never on an error response.
func gridNotModified(w http.ResponseWriter, r *http.Request, tag string) bool {
	if tag == "" {
		return false
	}
	quoted := `"` + tag + `"`
	if !matchesETag(r.Header.Get("If-None-Match"), quoted) {
		return false
	}
	return conditional(w, r, tag)
}

// setGridTag marks a successful page with its validator.
func setGridTag(w http.ResponseWriter, tag string) {
	if tag == "" {
		return
	}
	w.Header().Set("ETag", `"`+tag+`"`)
	w.Header().Set("Cache-Control", revalidate)
}
