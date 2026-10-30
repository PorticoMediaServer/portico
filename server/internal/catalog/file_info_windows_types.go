package catalog

const (
	FileInfoDefaultLimit = 40
	FileInfoMaximumLimit = 100
)

// FileInfoDetailCapture is the original accepted Detail, echoed unchanged by
// every lane. Scope below belongs to the separately admitted FileInfo audience;
// neither continuation nor preparation may silently adopt a newer Detail.
type FileInfoDetailCapture struct {
	Scope    DetailScope     `json:"scope"`
	Revision ContentRevision `json:"revision"`
}

type FileInfoWindow struct {
	QueryID        string `json:"queryId"`
	Limit          int    `json:"limit"`
	Returned       int    `json:"returned"`
	NextCursor     string `json:"nextCursor,omitempty"`
	PreviousCursor string `json:"previousCursor,omitempty"`
	ReloadCursor   string `json:"reloadCursor,omitempty"`
}

type FileInfoWindowBasis struct {
	Scope  DetailScope           `json:"scope"`
	Detail FileInfoDetailCapture `json:"detail"`
	Basis  string                `json:"basis"`
	Window FileInfoWindow        `json:"window"`
}

// Totals come from the complete accepted surface, never the resident window.
// OwnerDiscOccurrence names the whole-surface first successful owner-disc file.
// It is absent for non-owners and when the accepted surface has no such probe.
type FileInfoSurface struct {
	FileCount           int64           `json:"fileCount"`
	EligibleFileCount   int64           `json:"eligibleFileCount"`
	GroupCount          int64           `json:"groupCount"`
	CanSplit            bool            `json:"canSplit"`
	HasDiscColumns      bool            `json:"hasDiscColumns"`
	OwnerDiscOccurrence string          `json:"ownerDiscOccurrence,omitempty"`
	Manage              *FileInfoManage `json:"manage,omitempty"`
}

// FileInfoFile has no embedded all-track or title arrays. Occurrence binds this
// exact parent/child/link/asset to the opening and private preparation lease.
// It grants no byte/action authority. Every command revalidates current rights.
type FileInfoFile struct {
	Occurrence    string          `json:"occurrence"`
	ItemID        string          `json:"itemId"`
	ID            string          `json:"id"`
	Path          string          `json:"path,omitempty"`
	Size          *int64          `json:"size,omitempty"`
	SizeExact     string          `json:"sizeExact,omitempty"`
	Container     string          `json:"container"`
	Duration      *float64        `json:"duration,omitempty"`
	DurationState string          `json:"durationState,omitempty"`
	BitRate       int64           `json:"bitRate,omitempty"`
	Available     bool            `json:"available"`
	PartIndex     int             `json:"partIndex"`
	Edition       string          `json:"edition,omitempty"`
	Video         *FileInfoVideo  `json:"video,omitempty"`
	Disc          *DiscMediaFacts `json:"disc,omitempty"`
	AudioCount    int64           `json:"audioCount"`
	SubtitleCount int64           `json:"subtitleCount"`
}

type FileInfoFilesPage struct {
	FileInfoWindowBasis
	Kind     string          `json:"kind"`
	ItemKind string          `json:"itemKind"`
	Surface  FileInfoSurface `json:"surface"`
	Files    []FileInfoFile  `json:"files"`
}

type FileInfoVideo struct {
	TitleFileVideo
	Profile string `json:"profile,omitempty"`
}

type FileInfoRawTrack struct {
	Ordinal int64 `json:"ordinal"`
	TitleFileTrack
	Profile         string `json:"profile,omitempty"`
	SampleRate      int    `json:"sampleRate,omitempty"`
	BitDepth        int    `json:"bitDepth,omitempty"`
	Commentary      bool   `json:"commentary,omitempty"`
	HearingImpaired bool   `json:"hearingImpaired,omitempty"`
}

// Offered tracks use the same informational fields as raw tracks. ID names the
// exact accepted head track; it is not a byte delivery or playback grant.
type FileInfoHeadTrack struct {
	FileInfoRawTrack
	ID string `json:"id"`
}
type FileInfoHeadTracksPage struct {
	FileInfoWindowBasis
	Kind       string              `json:"kind"`
	Lane       string              `json:"lane"`
	GroupID    string              `json:"groupId"`
	Occurrence string              `json:"occurrence"`
	Total      int64               `json:"total"`
	Tracks     []FileInfoHeadTrack `json:"tracks"`
}

// Raw lanes preserve stored audio/subtitle order and evidence, including raw
// tracks that are not offered for playback. Offered head tracks are a separate
// lane with an exact group-head occurrence; clients must not substitute it.
type FileInfoTracksPage struct {
	FileInfoWindowBasis
	Kind       string             `json:"kind"`
	Lane       string             `json:"lane"`
	Occurrence string             `json:"occurrence"`
	Total      int64              `json:"total"`
	Tracks     []FileInfoRawTrack `json:"tracks"`
}

type FileInfoSource struct {
	Occurrence    string   `json:"occurrence"`
	ItemID        string   `json:"itemId"`
	ID            string   `json:"id"`
	Container     string   `json:"container"`
	VideoCodec    string   `json:"videoCodec"`
	AudioCodec    string   `json:"audioCodec"`
	Width         int      `json:"width"`
	Height        int      `json:"height"`
	Duration      *float64 `json:"duration,omitempty"`
	DurationState string   `json:"durationState,omitempty"`
}

type FileInfoSourcesPage struct {
	FileInfoWindowBasis
	Kind    string           `json:"kind"`
	Total   int64            `json:"total"`
	Sources []FileInfoSource `json:"sources"`
}

type FileInfoDiscPage struct {
	FileInfoWindowBasis
	Kind       string              `json:"kind"`
	Occurrence string              `json:"occurrence"`
	DiscKind   FileInfoDiscKind    `json:"discKind"`
	Total      int64               `json:"total"`
	Titles     []FileInfoDiscTitle `json:"titles"`
}

// Groups use the exact shape-first Options grouping, but contain informational
// scalars and bounded lane counts only. No Options call or cursor exhaustion is
// necessary to render the directory. Group/head IDs are opening-bound proofs.
type FileInfoGroup struct {
	ID             string          `json:"id"`
	HeadOccurrence string          `json:"headOccurrence"`
	Ordinal        int64           `json:"ordinal"`
	MemberCount    int64           `json:"memberCount"`
	Edition        string          `json:"edition,omitempty"`
	Container      string          `json:"container"`
	SizeBytes      *int64          `json:"sizeBytes,omitempty"`
	SizeBytesExact string          `json:"sizeBytesExact,omitempty"`
	DurationMs     *int64          `json:"durationMs,omitempty"`
	DurationState  string          `json:"durationState,omitempty"`
	Video          *FileInfoVideo  `json:"video,omitempty"`
	Disc           *DiscMediaFacts `json:"disc,omitempty"`
	AudioCount     int64           `json:"audioCount"`
	SubtitleCount  int64           `json:"subtitleCount"`
}

type FileInfoGroupsPage struct {
	FileInfoWindowBasis
	Kind    string          `json:"kind"`
	Surface FileInfoSurface `json:"surface"`
	Groups  []FileInfoGroup `json:"groups"`
}

type FileInfoMembersPage struct {
	FileInfoWindowBasis
	Kind    string         `json:"kind"`
	GroupID string         `json:"groupId"`
	Total   int64          `json:"total"`
	Members []FileInfoFile `json:"members"`
}

type FileInfoPreparing struct {
	Kind         string                 `json:"kind"`
	State        string                 `json:"state"`
	QueryID      string                 `json:"queryId"`
	RetryAfterMs int                    `json:"retryAfterMs"`
	Detail       *FileInfoDetailCapture `json:"detail,omitempty"`
}

// Typed results are registered as explicit200/202 unions at the HTTP boundary.
// Exactly one payload is populated. HTTP retains Release through serialization;
// ordinary transient hydration is charged and one legal oversized scalar takes
// the explicit uncached path. A preparation never carries guessed rows/counts.
type FileInfoWindowResult struct {
	Opening    *FileInfoOpeningReady
	Files      *FileInfoFilesPage
	Tracks     *FileInfoTracksPage
	HeadTracks *FileInfoHeadTracksPage
	Sources    *FileInfoSourcesPage
	Disc       *FileInfoDiscPage
	Groups     *FileInfoGroupsPage
	Members    *FileInfoMembersPage
	Preparing  *FileInfoPreparing
	release    func()
}

func (r FileInfoWindowResult) Release() {
	if r.release != nil {
		r.release()
	}
}

// FileInfoActionProof supplements existing action admission; it never replaces
// live owner, source custody, selection or preview/confirmation checks.
type FileInfoActionProof struct {
	Opening    string `json:"opening"`
	Occurrence string `json:"occurrence"`
}
