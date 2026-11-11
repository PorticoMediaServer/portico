package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg" // uploads are judged by their contents
	_ "image/png"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// A title's artwork in the editor: the pictures it could show for each role, choosing one,
// uploading one, and asking the provider for more. Owner and administrators, like the rest of
// metadata.go, and a title hidden from the caller's own profile is not found.

// maxUpload is the largest picture a person can upload (definition 04, rule 43).
const maxUpload = 10 << 20

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

// upload takes a JPEG, PNG or WebP of at most 10 MB as the request body, judged by its
// contents and not its name or Content-Type, keeps it in the picture store and makes it the
// role's choice. The same picture sent again is the same picture.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) (ArtworkPicture, error) {
	id, role, err := s.artworkTarget(r, r.URL.Query().Get("role"))
	if err != nil {
		return ArtworkPicture{}, err
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUpload))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return ArtworkPicture{}, refusal(http.StatusRequestEntityTooLarge, "body_too_large", "A picture can be at most 10 MB.", false)
	case err != nil:
		return ArtworkPicture{}, err
	}
	width, height, ok := pictureSize(data)
	if !ok {
		return ArtworkPicture{}, refusal(http.StatusUnsupportedMediaType, "unsupported_media_type", "Upload a JPEG, PNG or WebP picture.", false)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if err := s.artwork.Put(hash, data); err != nil {
		return ArtworkPicture{}, err
	}
	imageID, err := s.db.UploadPicture(r.Context(), id, db.NewPicture{Role: role, Hash: hash, Width: width, Height: height})
	if err != nil {
		return ArtworkPicture{}, refuse(err)
	}
	return ArtworkPicture{ID: imageID, Source: db.ImageFromUpload.String(), URL: "/v1/images/" + hash, Width: width, Height: height,
		Selected: true, Locked: true}, nil
}

// pictureSize says whether data is a JPEG, PNG or WebP picture, and its size where it can
// be read without decoding it (not for WebP).
func pictureSize(data []byte) (width, height int, ok bool) {
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return 0, 0, true
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") || config.Width <= 0 || config.Height <= 0 {
		return 0, 0, false
	}
	return config.Width, config.Height, true
}

// pictureGrace is how long a picture is kept after it is stored before the sweep may take it:
// a picture is put in the store a moment before the row that names it is written.
const pictureGrace = time.Hour

// SweepPictures removes from the picture store every picture no images row names any more: a
// removed upload, a picture beside the media that was replaced, those of titles that left. It
// is one step of the daily tidy-up and safe to run at any time. It returns how many it removed.
func (s *Server) SweepPictures(ctx context.Context) (int, error) {
	return s.artwork.Sweep(s.now().Add(-pictureGrace), func(yield func(string) error) error {
		return s.db.PictureHashes(ctx, yield)
	})
}
