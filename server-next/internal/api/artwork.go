package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// A title's artwork in the editor: the pictures it could show for each role, choosing one,
// uploading one, and asking the provider for more. Owner and administrators, like the rest of
// metadata.go, and a title hidden from the caller's own profile is not found.

// maxPictureUpload is the largest picture a person can upload (definition 04, rule 43).
const maxPictureUpload = 10 << 20

// ArtworkPicture is one picture a title could show for a role.
type ArtworkPicture struct {
	ID string `json:"id"`
	// Source is file (beside the media), embedded (inside the file), provider, upload or frame.
	Source   string `json:"source"`
	URL      string `json:"url"` // its address at GET /v1/images/{id}
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Language string `json:"language,omitempty"`
	Selected bool   `json:"selected"`
	Locked   bool   `json:"locked"`
}

// ArtworkChoice uses a picture for a role, which locks it, or unlocks the role.
type ArtworkChoice struct {
	ImageID string `json:"imageId,omitempty"`
	Unlock  bool   `json:"unlock,omitempty"`
}

func (s *Server) artworkRoutes() {
	library := func(r *Route) { r.Scope = ScopeLibrary }
	library(Handle(s, "GET /v1/admin/items/{id}/artwork", Admin, func(r *http.Request, _ None) (Page[ArtworkPicture], error) {
		id, role, err := s.artworkTarget(r, r.URL.Query().Get("role"))
		if err != nil {
			return Page[ArtworkPicture]{}, err
		}
		return s.pictures(r, id, role)
	}))
	library(Handle(s, "PUT /v1/admin/items/{id}/artwork/{role}", Admin, func(r *http.Request, req ArtworkChoice) (Page[ArtworkPicture], error) {
		id, role, err := s.artworkTarget(r, r.PathValue("role"))
		if err != nil {
			return Page[ArtworkPicture]{}, err
		}
		switch {
		case req.Unlock == (req.ImageID != ""):
			return Page[ArtworkPicture]{}, ErrInvalid("Send a picture to use, or unlock, not both.")
		case req.Unlock:
			err = s.db.UnlockPicture(r.Context(), id, role)
		default:
			err = s.db.ChoosePicture(r.Context(), id, role, req.ImageID)
		}
		if err != nil {
			return Page[ArtworkPicture]{}, refuse(err)
		}
		return s.pictures(r, id, role)
	}))
	library(Handle(s, "DELETE /v1/admin/items/{id}/artwork/{imageId}", Admin, func(r *http.Request, _ None) (None, error) {
		id, err := s.editable(r)
		if err != nil {
			return None{}, err
		}
		err = s.db.RemovePicture(r.Context(), id, r.PathValue("imageId"))
		switch {
		case errors.Is(err, db.ErrNotFound):
			return None{}, nil // already gone: a remove sent twice changes nothing
		case errors.Is(err, db.ErrConflict):
			return None{}, ErrConflict("not_an_upload", "Only an uploaded picture can be removed.")
		}
		return None{}, err
	}))
	library(Handle(s, "POST /v1/admin/items/{id}/artwork/find", Admin, func(r *http.Request, _ None) (Page[ArtworkPicture], error) {
		id, role, err := s.artworkTarget(r, r.URL.Query().Get("role"))
		if err != nil {
			return Page[ArtworkPicture]{}, err
		}
		if err := s.metadata.FindPictures(r.Context(), id, role.String()); errors.Is(err, metadata.ErrNotMatched) {
			return Page[ArtworkPicture]{}, ErrConflict("not_matched", "This title has no match to ask the provider about.")
		} else if err != nil {
			return Page[ArtworkPicture]{}, metadataError(err)
		}
		return s.pictures(r, id, role)
	}))
	// Re-fetch chosen: the provider's picture the role shows is downloaded again (a copy that
	// went bad or was cleared), in place, keeping its choice and lock (definition area 4, section 3).
	library(Handle(s, "POST /v1/admin/items/{id}/artwork/refetch", Admin, func(r *http.Request, _ None) (Page[ArtworkPicture], error) {
		id, role, err := s.artworkTarget(r, r.URL.Query().Get("role"))
		if err != nil {
			return Page[ArtworkPicture]{}, err
		}
		image, address, err := s.db.ChosenProviderPicture(r.Context(), id, role)
		if errors.Is(err, db.ErrNotFound) {
			return Page[ArtworkPicture]{}, ErrConflict("not_from_provider", "The picture in use is not the provider's, so there is nothing to fetch again.")
		}
		if err != nil {
			return Page[ArtworkPicture]{}, err
		}
		hash, err := s.metadata.CandidatePoster(r.Context(), address)
		if err != nil {
			return Page[ArtworkPicture]{}, refusal(http.StatusBadGateway, "provider_unavailable", "The provider did not give the picture. Try again later.", true)
		}
		if err := s.db.PictureRefetched(r.Context(), image, hash); err != nil {
			return Page[ArtworkPicture]{}, refuse(err)
		}
		return s.pictures(r, id, role)
	}))
	// Make from the video: an episode's picture made from a frame of its own video at the
	// owner's asking, whatever its library's Episode thumbnails says, and made the episode's
	// choice, as an upload is (definition area 4, rules 39, 40 and 46).
	library(Handle(s, "POST /v1/admin/items/{id}/artwork/make", Admin, func(r *http.Request, _ None) (Page[ArtworkPicture], error) {
		id, err := s.editable(r)
		if err != nil {
			return Page[ArtworkPicture]{}, err
		}
		e, err := s.db.EpisodeVideo(r.Context(), id)
		if errors.Is(err, db.ErrNotFound) {
			return Page[ArtworkPicture]{}, ErrConflict("not_an_episode", "Only an episode's picture can be made from its video.")
		}
		if err != nil {
			return Page[ArtworkPicture]{}, err
		}
		data, err := s.madeFrame(r.Context(), e)
		switch {
		case errors.Is(err, errNoDecoder):
			return Page[ArtworkPicture]{}, refusal(http.StatusServiceUnavailable, "no_decoder", "This server cannot read video to make a picture.", false)
		case errors.Is(err, errOutOfReach):
			return Page[ArtworkPicture]{}, refusal(http.StatusServiceUnavailable, "file_unavailable", "The episode's file cannot be read now. Try again later.", true)
		case err != nil:
			return Page[ArtworkPicture]{}, refusal(http.StatusUnprocessableEntity, "not_made", "No picture could be made from this video.", false)
		}
		hash, err := s.artwork.Hash(data)
		if err != nil {
			return Page[ArtworkPicture]{}, err
		}
		width, height, err := s.artwork.PutSized(hash, data, artwork.Artwork)
		if err != nil {
			return Page[ArtworkPicture]{}, err
		}
		if _, err := s.db.UploadPicture(r.Context(), id, db.NewPicture{Role: db.ImageThumb, Hash: hash, Width: width, Height: height,
			Source: db.ImageFromFrame}); err != nil {
			return Page[ArtworkPicture]{}, refuse(err)
		}
		return s.pictures(r, id, db.ImageThumb)
	}))
	// An upload's body is the picture itself, not JSON, so it is registered by hand.
	upload := &Route{Pattern: "POST /v1/admin/items/{id}/artwork", Access: Admin, Scope: ScopeLibrary, Response: reflect.TypeFor[ArtworkPicture]()}
	s.routes = append(s.routes, upload)
	s.mux.HandleFunc(upload.Pattern, func(w http.ResponseWriter, r *http.Request) {
		r, err := s.admit(r, upload)
		if err == nil {
			var out ArtworkPicture
			if out, err = s.upload(w, r); err == nil {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(out)
				return
			}
		}
		s.writeError(w, r, err)
	})
}

// artworkTarget checks the title in the address and reads the role a request names.
func (s *Server) artworkTarget(r *http.Request, roleName string) (string, db.ImageRole, error) {
	id, err := s.editable(r)
	if err != nil {
		return "", 0, err
	}
	role, known := db.ImageRoleByName[roleName]
	if !known || role == db.ImageAvatar {
		return "", 0, ErrField("role", "Name a role: poster, backdrop, logo, thumb, banner, cover or portrait.")
	}
	return id, role, nil
}

func (s *Server) pictures(r *http.Request, id string, role db.ImageRole) (Page[ArtworkPicture], error) {
	out := Page[ArtworkPicture]{Items: []ArtworkPicture{}}
	pictures, err := s.db.Pictures(r.Context(), id, role)
	if err != nil {
		return out, refuse(err)
	}
	for _, p := range pictures {
		out.Items = append(out.Items, pictureOut(p))
	}
	return out, nil
}

func pictureOut(p db.Picture) ArtworkPicture {
	return ArtworkPicture{ID: p.ID, Source: p.Source.String(), URL: "/v1/images/" + p.Hash, Width: p.Width, Height: p.Height,
		Language: p.Language, Selected: p.Selected, Locked: p.Locked}
}

// upload takes a JPEG, PNG, GIF (its first frame) or WebP of at most 10 MB as the request body,
// judged by its contents and not its name or Content-Type. It is stored in the picture store's
// two sizes, as every picture is when it arrives, and made the role's choice. The same picture
// sent again is the same picture.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) (ArtworkPicture, error) {
	id, role, err := s.artworkTarget(r, r.URL.Query().Get("role"))
	if err != nil {
		return ArtworkPicture{}, err
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPictureUpload))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return ArtworkPicture{}, refusal(http.StatusRequestEntityTooLarge, "body_too_large", "A picture can be at most 10 MB.", false)
	case err != nil:
		return ArtworkPicture{}, err
	}
	hash, err := s.artwork.Hash(data)
	if err != nil {
		return ArtworkPicture{}, err
	}
	// Stored in its two sizes now; the original is not kept.
	width, height, err := s.artwork.PutSized(hash, data, artwork.Artwork)
	if errors.Is(err, artwork.ErrNotAPicture) {
		return ArtworkPicture{}, refusal(http.StatusUnsupportedMediaType, "unsupported_media_type", "Upload a JPEG, PNG, GIF or WebP picture.", false)
	}
	if err != nil {
		return ArtworkPicture{}, err
	}
	imageID, err := s.db.UploadPicture(r.Context(), id, db.NewPicture{Role: role, Hash: hash, Width: width, Height: height})
	if err != nil {
		return ArtworkPicture{}, refuse(err)
	}
	return ArtworkPicture{ID: imageID, Source: db.ImageFromUpload.String(), URL: "/v1/images/" + hash, Width: width, Height: height,
		Selected: true, Locked: true}, nil
}

// pictureGrace is how long a picture is kept after it is stored before the sweep may take it:
// a picture is put in the store a moment before the row that names it is written.
const pictureGrace = time.Hour

// SweepPictures removes from the picture store every picture nothing names any more (see
// db.PictureHashes): a removed upload, a picture beside the media that was replaced, those of
// titles that left, a channel logo replaced. It
// is one step of the daily tidy-up and safe to run at any time. It returns how many it removed.
func (s *Server) SweepPictures(ctx context.Context) (int, error) {
	return s.artwork.Sweep(s.now().Add(-pictureGrace), func(yield func(string) error) error {
		return s.db.PictureHashes(ctx, yield)
	})
}
