package playback

type ChapterScope struct {
	OffersScope
	SessionID  string `json:"sessionId"`
	Generation int    `json:"generation"`
	SourceID   string `json:"sourceId"`
}
type ChapterSeek struct {
	ID              string  `json:"id"`
	PositionSeconds float64 `json:"positionSeconds"`
}
type PlayingChapter struct {
	ID           string      `json:"id"`
	Index        int         `json:"index"`
	Title        string      `json:"title"`
	StartSeconds float64     `json:"startSeconds"`
	EndSeconds   float64     `json:"endSeconds"`
	Action       ChapterSeek `json:"action"`
	// ThumbnailURL is present only when a current chapter image exists for this
	// exact source revision. A missing image publishes no URL, never a link
	// that would 404 in a player.
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
}
type ChapterProjection struct {
	Scope      ChapterScope     `json:"scope"`
	Revision   string           `json:"revision"`
	Status     string           `json:"status"`
	Reason     *string          `json:"reason"`
	Duration   float64          `json:"duration"`
	Chapters   []PlayingChapter `json:"chapters"`
	TotalCount int              `json:"totalCount"`
	NextCursor string           `json:"nextCursor"`
}
