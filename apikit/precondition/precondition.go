package precondition

import (
	"net/http"
	"strings"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

// Match requires exactly one strong validator. Weak validators never authorize writes.
func Match(r *http.Request, revision string, current any) error {
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		return &apierror.Error{Code: "revision_required"}
	}
	if len(values) != 1 || revision == "" || strings.ContainsAny(revision, "\"\r\n") || values[0] != "\""+revision+"\"" {
		return &apierror.Error{Code: "revision_mismatch", Current: current}
	}
	return nil
}
func Create(r *http.Request, exists bool) error {
	values := r.Header.Values("If-None-Match")
	if len(values) != 1 || values[0] != "*" {
		return &apierror.Error{Code: "revision_required"}
	}
	if exists {
		return &apierror.Error{Code: "revision_mismatch"}
	}
	return nil
}
