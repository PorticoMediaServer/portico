package reading

import "errors"

// These are the resource caps enforced by the EPUB reader, in uncompressed
// bytes. Reject the publication before returning an unusable session manifest.
const (
	EPUBMemberBytes = 16 << 20
	EPUBTotalBytes  = 256 << 20
)

var ErrPublicationTooLarge = errors.New("reading publication exceeds reader limits")

type archiveLimits struct {
	member, total uint64
	exceeded      error
}

var publicationArchiveLimits = archiveLimits{EPUBMemberBytes, EPUBTotalBytes, ErrPublicationTooLarge}
var safetyArchiveLimits = archiveLimits{MaxMember, MaxExpanded, ErrLimit}
