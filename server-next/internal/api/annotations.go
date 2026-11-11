package api

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Annotation is a bookmark or highlight, with its note, in one book. Only the profile that
// made it can see it; nobody else can, the owner included.
type Annotation struct {
	ID string `json:"id"`
	// Kind is "bookmark" or "highlight". Comics take bookmarks only.
	Kind string `json:"kind"`
	// Color is a highlight's: yellow, green, blue or pink.
	Color string `json:"color,omitempty"`
	// Location is the exact spot in the file it was made on (MadeOn, a book fingerprint), in
	// the reader's notation. Progress (0 to 1) places it in another file. Text is the words it
	// marked, so one that cannot be placed can still be listed, under "From an earlier copy of
	// this book".
	Location  string  `json:"location"`
	MadeOn    string  `json:"madeOn,omitempty"`
	Progress  float64 `json:"progress"`
	Chapter   string  `json:"chapter,omitempty"`
	Text      string  `json:"text,omitempty"`
	Note      string  `json:"note,omitempty"`
	Extra     JSON    `json:"extra,omitempty"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

// AnnotationBody adds or changes one annotation. MadeAgoMs is how long ago the person made the
// edit, for one that waited while the device was out of reach: of two edits to one
// annotation, the one made later wins.
type AnnotationBody struct {
	Kind      string  `json:"kind"`
	Color     string  `json:"color"`
	Location  string  `json:"location"`
	MadeOn    string  `json:"madeOn"`
	Progress  float64 `json:"progress"`
	Chapter   string  `json:"chapter"`
	Text      string  `json:"text"`
	Note      string  `json:"note"`
	Extra     JSON    `json:"extra"`
	MadeAgoMs int64   `json:"madeAgoMs"`
}

// AnnotationChange is one change in a batch: an annotation added or changed, or with Delete,
// deleted.
type AnnotationChange struct {
	ItemID string `json:"itemId"`
	ID     string `json:"id"`
	Delete bool   `json:"delete"`
	AnnotationBody
}

// AnnotationBatch is what a device made while out of reach, sent at once.
type AnnotationBatch struct {
	Changes []AnnotationChange `json:"changes"`
}

// AnnotationResults answers a batch, one result per change in its order: the annotation as it
// now stands, or, for a deletion, nothing; or why that change was refused.
type AnnotationResults struct {
	Results []AnnotationResult `json:"results"`
}

type AnnotationResult struct {
	ID         string      `json:"id"`
	Annotation *Annotation `json:"annotation,omitempty"`
	Error      *Error      `json:"error,omitempty"`
}

const maxAnnotationBatch = 500

func (s *Server) annotationRoutes() {
	Handle(s, "GET /v1/items/{id}/annotations", WithProfile, func(r *http.Request, _ None) (Page[Annotation], error) {
		limit, err := limitParam(r.URL.Query())
		if err != nil {
			return Page[Annotation]{}, err
		}
		if limit == 0 || limit > 500 {
			limit = 500
		}
		list, next, err := s.db.Annotations(r.Context(), viewer(r), r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
		if err != nil {
			return Page[Annotation]{}, refuse(err)
		}
		out := Page[Annotation]{Items: make([]Annotation, len(list)), NextCursor: next}
		for n, a := range list {
			out.Items[n] = annotationShape(a)
		}
		return out, nil
	})
	Handle(s, "PUT /v1/items/{id}/annotations/{annotationId}", WithProfile, func(r *http.Request, req AnnotationBody) (Annotation, error) {
		out, err := s.saveAnnotations(r, []AnnotationChange{{ItemID: r.PathValue("id"), ID: r.PathValue("annotationId"), AnnotationBody: req}})
		if err != nil {
			return Annotation{}, err
		}
		if out[0].Error != nil {
			return Annotation{}, out[0].Error
		}
		return *out[0].Annotation, nil
	})
	Handle(s, "DELETE /v1/items/{id}/annotations/{annotationId}", WithProfile, func(r *http.Request, _ None) (None, error) {
		out, err := s.saveAnnotations(r, []AnnotationChange{{ItemID: r.PathValue("id"), ID: r.PathValue("annotationId"), Delete: true}})
		if err != nil {
			return None{}, err
		}
		if e := out[0].Error; e != nil && e.Code != "annotation_deleted" {
			return None{}, e
		}
		return None{}, nil
	})
	Handle(s, "POST /v1/annotations:batch", WithProfile, func(r *http.Request, req AnnotationBatch) (AnnotationResults, error) {
		if len(req.Changes) == 0 || len(req.Changes) > maxAnnotationBatch {
			return AnnotationResults{}, ErrField("changes", "Send 1 to 500 changes.")
		}
		out, err := s.saveAnnotations(r, req.Changes)
		return AnnotationResults{Results: out}, err
	})
}

func (s *Server) saveAnnotations(r *http.Request, changes []AnnotationChange) ([]AnnotationResult, error) {
	edits := make([]db.AnnotationEdit, len(changes))
	for n, c := range changes {
		edits[n] = db.AnnotationEdit{ItemID: c.ItemID, Delete: c.Delete, Ago: time.Duration(c.MadeAgoMs) * time.Millisecond,
			Annotation: db.Annotation{PublicID: c.ID, Location: c.Location, MadeOn: c.MadeOn, Progress: c.Progress, Chapter: c.Chapter,
				Text: c.Text, Note: c.Note, Extra: string(c.Extra)}}
		if string(c.Extra) == "null" {
			edits[n].Extra = ""
		}
		switch c.Kind {
		case "bookmark":
			edits[n].Kind = db.AnnotationBookmark
		case "highlight":
			edits[n].Kind = db.AnnotationHighlight
		}
		if c.Color != "" {
			edits[n].Color = slices.Index(db.AnnotationColors, c.Color) + 1
			if edits[n].Color == 0 {
				edits[n].Color = -1 // not one of the four: refused with the others' reasons
			}
		}
	}
	stored, refusals, err := s.db.SaveAnnotations(r.Context(), viewer(r), edits)
	if err != nil {
		return nil, err
	}
	out := make([]AnnotationResult, len(changes))
	for n, c := range changes {
		out[n].ID = c.ID
		switch err := refusals[n]; {
		case err == nil && !c.Delete:
			a := annotationShape(stored[n])
			out[n].Annotation = &a
		case errors.Is(err, db.ErrAnnotationsFull):
			out[n].Error = ErrConflict("annotations_full", err.Error())
		case errors.Is(err, db.ErrAnnotationDeleted):
			out[n].Error = refusal(http.StatusGone, "annotation_deleted", err.Error(), false)
		case err != nil:
			out[n].Error, _ = asError(refuse(err))
		}
	}
	return out, nil
}

func annotationShape(a db.Annotation) Annotation {
	out := Annotation{ID: a.PublicID, Kind: "bookmark", Location: a.Location, MadeOn: a.MadeOn, Progress: a.Progress, Chapter: a.Chapter,
		Text: a.Text, Note: a.Note, CreatedAt: timeString(a.CreatedAt), UpdatedAt: timeString(a.UpdatedAt)}
	if a.Kind == db.AnnotationHighlight {
		out.Kind = "highlight"
	}
	if a.Color > 0 && a.Color <= len(db.AnnotationColors) {
		out.Color = db.AnnotationColors[a.Color-1]
	}
	if strings.TrimSpace(a.Extra) != "" {
		out.Extra = JSON(a.Extra)
	}
	return out
}
