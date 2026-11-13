package api

import (
	"crypto/sha256"
	"fmt"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/segmentdetect"
	"strconv"
	"strings"
)

type SegmentFileCounts struct {
	Pending     int `json:"pending"`
	Complete    int `json:"complete"`
	Failed      int `json:"failed"`
	Unsupported int `json:"unsupported"`
}
type SegmentReviewCounts struct {
	Automatic int `json:"automatic"`
	Suggested int `json:"suggested"`
	Approved  int `json:"approved"`
	Dismissed int `json:"dismissed"`
}
type SegmentLibraryStatus struct {
	LibraryID string            `json:"libraryId"`
	Name      string            `json:"name"`
	ItemKind  string            `json:"itemKind"`
	Choice    string            `json:"choice"`
	Mode      string            `json:"mode"`
	Files     SegmentFileCounts `json:"files"`
}
type SegmentReadFailure struct {
	ItemID    string `json:"itemId"`
	Title     string `json:"title"`
	State     string `json:"state"`
	ErrorCode string `json:"errorCode"`
	UpdatedMS int64  `json:"updatedMS"`
}
type SegmentDetectionStatus struct {
	Mode            string                 `json:"mode"`
	Libraries       []SegmentLibraryStatus `json:"libraries"`
	Files           SegmentFileCounts      `json:"files"`
	ShowsWaiting    int                    `json:"showsWaiting"`
	CatchUpComplete bool                   `json:"catchUpComplete"`
	LastFullPassMS  int64                  `json:"lastFullPassMS"`
	Failures        []SegmentReadFailure   `json:"failures"`
}
type SegmentTitleSummary struct {
	ID      string              `json:"id"`
	Title   string              `json:"title"`
	Kind    string              `json:"kind"`
	Files   SegmentFileCounts   `json:"files"`
	Markers SegmentReviewCounts `json:"markers"`
}
type SegmentTitlePage struct {
	LibraryID  string                `json:"libraryId"`
	Titles     []SegmentTitleSummary `json:"titles"`
	NextCursor string                `json:"nextCursor"`
}
type SegmentReviewMarker struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	StartUS       string  `json:"startUS"`
	EndUS         string  `json:"endUS"`
	Confidence    float64 `json:"confidence"`
	Provenance    string  `json:"provenance"`
	Basis         string  `json:"basis"`
	Approved      bool    `json:"approved"`
	Edited        bool    `json:"edited"`
	AutomaticSafe bool    `json:"automaticSafe"`
	ViewerVisible bool    `json:"viewerVisible"`
}
type SegmentReviewEntry struct {
	ItemID          string                `json:"itemId"`
	Title           string                `json:"title"`
	Number          int                   `json:"number"`
	DurationUS      string                `json:"durationUS"`
	State           string                `json:"state"`
	ErrorCode       string                `json:"errorCode"`
	SourceID        string                `json:"sourceId"`
	SourceRevision  string                `json:"sourceRevision"`
	MappingRevision string                `json:"mappingRevision"`
	MarkerRevision  int64                 `json:"markerRevision"`
	Markers         []SegmentReviewMarker `json:"markers"`
	VersionName     string                `json:"versionName,omitempty"`
}
type SegmentReviewSeason struct {
	Number  int                  `json:"number"`
	Title   string               `json:"title"`
	Entries []SegmentReviewEntry `json:"entries"`
}
type SegmentTitleReview struct {
	NextCursor string                `json:"nextCursor,omitempty"`
	ID         string                `json:"id"`
	Title      string                `json:"title"`
	Kind       string                `json:"kind"`
	LibraryID  string                `json:"libraryId"`
	Mode       string                `json:"mode"`
	Seasons    []SegmentReviewSeason `json:"seasons"`
}
type SegmentMarkerEdit struct {
	SourceID         string `json:"sourceId"`
	SourceRevision   string `json:"sourceRevision"`
	ExpectedRevision int64  `json:"expectedRevision"`
	RequestID        string `json:"requestId"`
	Action           string `json:"action"`
	MarkerID         string `json:"markerId"`
	Kind             string `json:"kind"`
	StartUS          string `json:"startUS"`
	EndUS            string `json:"endUS"`
}
type SegmentApprove struct {
	Season *int     `json:"season,omitempty"`
	Kinds  []string `json:"kinds"`
}
type SegmentChanged struct {
	Changed int `json:"changed"`
}

func segmentRevision(e db.SegmentEntry) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(e.Key())))
}
func segmentFiles(c db.SegmentCounts) SegmentFileCounts {
	return SegmentFileCounts{c.Pending, c.Complete, c.Failed, c.Unsupported}
}
func segmentCounts(c db.SegmentMarkerCounts) SegmentReviewCounts {
	return SegmentReviewCounts{c.Automatic, c.Suggested, c.Approved, c.Dismissed}
}
func segmentMarker(m db.SegmentMarker, policy segmentdetect.ConfidencePolicy, enabled bool) SegmentReviewMarker {
	kind := m.Kind.String()
	if m.Kind == db.MarkerAdvert {
		kind = "commercial"
	}
	basis := "earlier"
	switch {
	case m.Source == db.MarkerFromPerson:
		basis = "owner"
	case strings.HasPrefix(m.Provenance, segmentdetect.SeasonProvenance):
		basis = "season"
	case m.Provenance == segmentdetect.ChapterProvenance:
		basis = "chapter"
	case strings.Contains(m.Provenance, "visual"):
		basis = "picture"
	}
	approved := m.Source == db.MarkerFromPerson
	visible := policy.Visible(approved, m.Confidence, m.Provenance)
	automatic := policy.AutomaticSafe(approved, m.Confidence, m.Provenance)
	if m.Provenance == "" {
		visible, automatic = true, m.Source.Trusted()
	}
	return SegmentReviewMarker{ID: strconv.FormatInt(m.ID, 10), Kind: kind, StartUS: fmt.Sprint(m.StartMS * 1000), EndUS: fmt.Sprint(m.EndMS * 1000),
		Confidence: m.Confidence, Provenance: m.Provenance, Basis: basis, Approved: m.Source == db.MarkerFromPerson && !m.Dismissed,
		Edited: m.Provenance == "owner_edit", AutomaticSafe: enabled && automatic && !m.Dismissed, ViewerVisible: enabled && visible && !m.Dismissed}
}
func segmentKind(kind string) (db.MarkerKind, bool) {
	switch kind {
	case "intro":
		return db.MarkerIntro, true
	case "recap":
		return db.MarkerRecap, true
	case "credits":
		return db.MarkerCredits, true
	case "commercial":
		return db.MarkerAdvert, true
	case "outro":
		return db.MarkerOutro, true
	}
	return 0, false
}

func playbackMarkerType(kind db.MarkerKind) string {
	if kind == db.MarkerAdvert {
		return "commercial"
	}
	return kind.String()
}
