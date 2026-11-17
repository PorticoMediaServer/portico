package playqueue

import "github.com/PorticoMediaServer/portico/server-next/internal/db"

// HomeRowSource names the same row and controls as GET /v1/home/rows/{id}.
type HomeRowSource struct {
	ID         string `json:"id"`
	Library    string `json:"library,omitempty"`
	Sort       string `json:"sort,omitempty"`
	Order      string `json:"order,omitempty"`
	Genre      string `json:"genre,omitempty"`
	Year       string `json:"year,omitempty"`
	Watched    *bool  `json:"watched,omitempty"`
	InProgress *bool  `json:"inProgress,omitempty"`
	Favorite   *bool  `json:"favorite,omitempty"`
	InList     *bool  `json:"inList,omitempty"`
	prepared   *db.HomeQueueSource
}

// Prepare keeps the server-checked source outside the app's request fields.
func (r *HomeRowSource) Prepare(source db.HomeQueueSource) { r.prepared = &source }

func (s QueueSource) viewSelection() (*selection, error) {
	if s.HomeRow != nil {
		if s.HomeRow.prepared == nil {
			return nil, ErrUnsupported
		}
		return &selection{Home: s.HomeRow.prepared}, nil
	}
	if s.View != nil {
		return &selection{View: *s.View}, nil
	}
	return nil, ErrUnsupported
}

func (s *selection) query() (db.ItemQuery, error) {
	if s.Home != nil {
		q := s.Home.ItemQuery()
		for _, kind := range q.Kinds {
			if !queueKind(kind) {
				return q, ErrUnsupported
			}
		}
		return q, nil
	}
	return s.View.query()
}

func queueKind(kind db.ItemKind) bool {
	return kind.Playable() || kind == db.KindShow || kind == db.KindSeason || kind == db.KindAlbum || kind == db.KindArtist
}
