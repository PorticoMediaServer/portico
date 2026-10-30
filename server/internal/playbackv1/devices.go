package playbackv1

import (
	"strconv"
	"strings"
)

// ParseRevision reads an If-Match value: the quoted strong ETag ("12") or the
// bare revision (12), per spec §17.3. Weak validators never authorize a write.
func ParseRevision(v string) (int64, bool) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "W/") {
		return 0, false
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n >= 0
}

// ETag is the quoted strong ETag of a revision.
func ETag(revision int64) string { return `"` + strconv.FormatInt(revision, 10) + `"` }

// RevisionError is a stale If-Match: the caller gets 412 with the current
// resource (spec §17.3).
type RevisionError struct {
	Current  any
	Revision int64
}

func (e *RevisionError) Error() string { return "revision mismatch" }
func (e *RevisionError) Unwrap() error { return ErrRevision }
