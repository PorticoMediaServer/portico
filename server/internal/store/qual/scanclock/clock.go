// Package scanclock retains harness-only acceptance observations. Filename
// visibility remains unmeasured without a current public-facade witness joining
// the original receipt's object, asset and accepted revision to reader state.
package scanclock

import (
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

var ErrObservation = errors.New("invalid or unjoined scan timing observation")

// Manifest identifies one first-visibility observation window. Source/build/
// fixture/host evidence is supplied by the owning run, never inferred here.
type Manifest struct {
	Run, Database, ClockDomain, FixtureSHA256, SourceSHA, BinarySHA256, HostEpoch, MediaProfile string
}

type Report struct {
	OriginalReference      *writer.ReceiptReference `json:"original_reference"`
	TargetLibraryID        int64                    `json:"target_library_id"`
	TargetSubjectID        int64                    `json:"target_subject_id"`
	Population             string                   `json:"population"`
	Label                  string                   `json:"label"`
	Qualification          bool                     `json:"qualification"`
	Manifest               Manifest                 `json:"manifest"`
	AppliedObserved        bool                     `json:"applied_observed"`
	FilenameObserved       bool                     `json:"filename_observed"`
	AppliedObservedNS      *int64                   `json:"applied_observed_ns"`
	FirstFilenameVisibleNS *int64                   `json:"first_filename_visible_ns"`
	AllFilenameVisibleNS   *int64                   `json:"all_filename_visible_ns"`
	FirstBasicNS           *int64                   `json:"first_basic_ns"`
	AllBasicNS             *int64                   `json:"all_basic_ns"`
	Missing                []string                 `json:"missing"`
	Rejected               uint64                   `json:"rejected_observations"`
}

// Clock retains ONE exact N1 Applied target and fixed-size receipt values.
// A browse row alone cannot prove that target's current visibility. It is
// single-owner only and supplies no writer, read or completion authority.
type Clock struct {
	manifest                              Manifest
	start                                 time.Time
	reference                             writer.ScanReceiptRequest
	member                                writer.InventoryMember
	appliedAt                             time.Time
	rejected                              uint64
	nonPositiveOrigin, nonPositiveApplied bool
}

// New's start is the actual offer of the scan's first supervised native
// traversal operation, before starting that request, in the owning process.
// This constructor does not observe or attest that externally supplied event.
func New(start time.Time, m Manifest) (*Clock, error) {
	if !monotonic(start) || m.Run == "" || m.Database == "" || len(m.Database) > 1024 || m.ClockDomain == "" || m.HostEpoch == "" || m.MediaProfile == "" ||
		len(m.Run) > 1024 || len(m.ClockDomain) > 1024 || len(m.HostEpoch) > 1024 || len(m.MediaProfile) > 128 ||
		!hex(m.FixtureSHA256, 64) || !hex(m.SourceSHA, 40) || !hex(m.BinarySHA256, 64) {
		return nil, ErrObservation
	}
	m.Run = strings.Clone(m.Run)
	m.Database = strings.Clone(m.Database)
	m.ClockDomain = strings.Clone(m.ClockDomain)
	m.FixtureSHA256 = strings.Clone(m.FixtureSHA256)
	m.SourceSHA = strings.Clone(m.SourceSHA)
	m.BinarySHA256 = strings.Clone(m.BinarySHA256)
	m.HostEpoch = strings.Clone(m.HostEpoch)
	m.MediaProfile = strings.Clone(m.MediaProfile)
	return &Clock{start: start, manifest: m}, nil
}

// Applied observes an ACTUAL successful facade call with its saved ORIGINAL
// request and complete N1 result. Errors/zero states never become milestones.
// Replayed receipt time cannot reconstruct first acceptance in an earlier run.
// This copies no live evidence pointer or result slice and changes no ACK floor.
func (c *Clock) Applied(at time.Time, original writer.ScanReceiptRequest, result writer.InventoryResult, callErr error) error {
	if c == nil {
		return ErrObservation
	}
	if callErr != nil || !c.appliedAt.IsZero() || !c.at(at) || original.RunID != c.manifest.Run || original.LibraryID <= 0 ||
		original.LibraryKey == "" || len(original.LibraryKey) > 1024 || original.Receipt.Identity.Sequence <= 0 ||
		original.Receipt.Identity.Incarnation == ([16]byte{}) || original.Receipt.Identity.OperationID == ([16]byte{}) || original.Receipt.RequestDigest == (writer.RequestDigest{}) || result.Outcome.State != writer.Applied || result.Outcome.Replayed ||
		result.Outcome.Reference != original.Receipt || result.RunID != original.RunID || result.ResultCount != 1 || len(result.Members) != 1 ||
		result.SourceID == "" || result.SourceIncarnation == "" || result.SourceRevision == "" || result.NextCursor == "" || result.AcceptedRunRevision <= 0 || result.Digest == (writer.ResultDigest{}) {
		return c.reject()
	}
	member := result.Members[0]
	if member.LibraryID != original.LibraryID || member.SubjectID <= 0 || member.AssetID <= 0 || member.ObjectID == "" || len(member.ObjectID) > 1024 ||
		member.AcceptedAssetRevision <= 0 || member.InputOrdinal != 0 || member.ChildKind != 0 || member.ChildOrdinal != 0 {
		return c.reject()
	}
	// Keep native scalar IDs/reference exactly. Receipt timing is only the
	// observation below; it never substitutes for a later serving/read result.
	original.LibraryKey = strings.Clone(original.LibraryKey)
	original.RunID = strings.Clone(original.RunID)
	member.ObjectID = strings.Clone(member.ObjectID)
	c.reference, c.member, c.appliedAt = original, member, at
	return nil
}

// Browse checks timing and scope of a supplied released page, but returns
// ErrObservation: no supported current public-facade witness joins it to the
// original object's asset and accepted revision, or revalidates that receipt.
// Subject/title/public ID alone cannot prove this. Browse.Revision is an entity
// revision, not AcceptedAssetRevision (asset info_epoch); they are never equated.
// Visibility stays null until that witness contract exists. No hook is minted.
func (c *Clock) Browse(at time.Time, input read.BrowseInput, page read.BrowsePage, callErr error) error {
	if c == nil {
		return ErrObservation
	}
	if !c.at(at) {
		return c.reject()
	}
	if !c.appliedAt.IsZero() && !at.After(c.appliedAt) {
		c.nonPositiveApplied = true
		return c.reject()
	}
	if callErr != nil || c.appliedAt.IsZero() || input.Library != c.member.LibraryID ||
		input.TokenHash == "" || input.Database != c.manifest.Database || len(page.Items) > read.MaxPage {
		return c.reject()
	}
	return c.reject() // current object/asset/revision/receipt witness unavailable
}

func (c *Clock) Report() Report {
	r := Report{Label: "DIRECTIONAL", Qualification: false, Population: "one_fresh_n1_target",
		Missing: []string{"authoritative population/frontier completion unavailable; all-filename clock unmeasured", "Basic accepting/readiness hook unavailable"}}
	if c == nil {
		r.Missing = append(r.Missing, "timing composition unavailable")
		return r
	}
	r.Manifest = c.manifest
	r.Rejected = c.rejected
	if c.nonPositiveOrigin {
		r.Missing = append(r.Missing, "non-positive traversal-offer-to-observation interval refused")
	}
	if c.nonPositiveApplied {
		r.Missing = append(r.Missing, "non-positive Applied-to-browse interval refused")
	}
	if !c.appliedAt.IsZero() {
		r.AppliedObserved = true
		reference := c.reference.Receipt
		r.OriginalReference = &reference
		r.TargetLibraryID = c.member.LibraryID
		r.TargetSubjectID = c.member.SubjectID
		n := c.appliedAt.Sub(c.start).Nanoseconds()
		r.AppliedObservedNS = &n
	} else {
		r.Missing = append(r.Missing, "actual fresh Applied observation unavailable")
	}
	r.Missing = append(r.Missing, "current public-facade witness joining original receipt/object/asset/info_epoch to reader visibility unavailable")
	return r
}
func (c *Clock) at(at time.Time) bool {
	if !monotonic(at) {
		return false
	}
	if !at.After(c.start) {
		c.nonPositiveOrigin = true
		return false
	}
	return true
}
func (c *Clock) reject() error   { c.rejected++; return ErrObservation }
func monotonic(t time.Time) bool { return !t.IsZero() && t != t.Round(0) }
func hex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
