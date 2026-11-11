package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A profile's picture. It is a person's own photograph more often than not, so unlike a
// poster it is never given an address that works without signing in: it is stored in the
// picture store under a random name no reply ever contains, and read only through the
// signed-in route below, by people of the same account and by the owner and admins.

// maxProfilePicture is the largest picture a person can upload for a profile.
const maxProfilePicture = 4 << 20

// This server keeps profile pictures only when its people sign in to it directly. Where they
// sign in with a Portico Account, a person's picture is part of that account and is kept by
// Portico's hosted service: nothing is stored here, and changing it here is refused.

// signInKindKey is the setting that says how people sign in to this server. Setup writes it
// when the owner chooses; until Portico Account sign-in is built it is never anything but local.
const (
	signInKindKey         = db.SignInKindKey
	signInPorticoAccounts = db.SignInPortico
)

func (s *Server) picturesKeptHere(r *http.Request) error {
	kind, _, err := s.db.Setting(r.Context(), signInKindKey)
	if err != nil {
		return err
	}
	if kind == signInPorticoAccounts {
		return ErrConflict("managed_by_portico_account", "This profile's picture is managed in the person's Portico Account.")
	}
	return nil
}

func (s *Server) profilePictureRoutes() {
	// The upload's body and the picture's reply are the picture itself, not JSON, so both
	// are registered by hand.
	upload := &Route{Pattern: "PUT /v1/profiles/{id}/avatar", Access: SignedIn, Scope: ScopeSession, Response: reflect.TypeFor[Profile]()}
	s.routes = append(s.routes, upload)
	s.mux.HandleFunc(upload.Pattern, func(w http.ResponseWriter, r *http.Request) {
		r, err := s.admit(r, upload)
		if err == nil {
			var out Profile
			if out, err = s.setProfilePicture(w, r); err == nil {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				json.NewEncoder(w).Encode(out)
				return
			}
		}
		s.writeError(w, r, err)
	})
	read := &Route{Pattern: "GET /v1/profiles/{id}/avatar", Access: SignedIn, Scope: ScopeRead}
	s.routes = append(s.routes, read)
	s.mux.HandleFunc(read.Pattern, func(w http.ResponseWriter, r *http.Request) {
		r, err := s.admit(r, read)
		if err == nil {
			err = s.profilePicture(w, r)
		}
		if err != nil {
			s.writeError(w, r, err)
		}
	})
	Handle(s, "DELETE /v1/profiles/{id}/avatar", SignedIn, func(r *http.Request, _ None) (Profile, error) {
		userID, _, err := s.profileOwner(r)
		if err != nil {
			return Profile{}, err
		}
		if err := s.picturesKeptHere(r); err != nil {
			return Profile{}, err
		}
		p, err := s.db.SetProfilePicture(r.Context(), userID, r.PathValue("id"), "", 0, 0)
		return profileOut(p), refuse(err)
	})
}

// setProfilePicture takes a JPEG, PNG, GIF (its first frame) or WebP of at most 4 MB as the
// request body, judged by its contents. Only the two profile-picture sizes made from it are
// stored, drawn afresh, so nothing embedded in the original, such as where it was taken, is kept.
func (s *Server) setProfilePicture(w http.ResponseWriter, r *http.Request) (Profile, error) {
	userID, _, err := s.profileOwner(r)
	if err != nil {
		return Profile{}, err
	}
	if err := s.picturesKeptHere(r); err != nil {
		return Profile{}, err
	}
	if _, err := s.db.Profile(r.Context(), userID, r.PathValue("id")); err != nil {
		return Profile{}, refuse(err)
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxProfilePicture))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return Profile{}, refusal(http.StatusRequestEntityTooLarge, "body_too_large", "A profile picture can be at most 4 MB.", false)
	case err != nil:
		return Profile{}, err
	}
	// A random name: the picture's own hash would be the same for anyone holding the same
	// photograph, and two profiles must not share one stored picture.
	random := make([]byte, 16)
	rand.Read(random)
	name := hex.EncodeToString(random)
	width, height, err := s.artwork.PutSized(name, data, artwork.ProfilePicture)
	if errors.Is(err, artwork.ErrNotAPicture) {
		return Profile{}, refusal(http.StatusUnsupportedMediaType, "unsupported_media_type", "Upload a JPEG, PNG, GIF or WebP picture.", false)
	}
	if err != nil {
		return Profile{}, err
	}
	p, err := s.db.SetProfilePicture(r.Context(), userID, r.PathValue("id"), name, width, height)
	return profileOut(p), refuse(err)
}

// profilePicture serves a profile's picture to the people who can see the profile: anyone
// signed in to its account, on any profile or still choosing one, and with ?member= the
// owner or an admin who manages that person.
func (s *Server) profilePicture(w http.ResponseWriter, r *http.Request) error {
	userID := CallerFrom(r.Context()).UserID
	if r.URL.Query().Has("member") {
		var err error
		if userID, _, err = s.profileOwner(r); err != nil {
			return err
		}
	}
	// Any width above the small size's is answered with the large one.
	width := 1
	switch r.URL.Query().Get("size") {
	case "", "small":
	case "large":
		width = artwork.ProfilePicture.Large
	default:
		return ErrInvalid("Give size as small or large.")
	}
	name, err := s.db.ProfilePictureName(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		return refuse(err)
	}
	data, contentType, err := s.artwork.Read(name, width, 0)
	if errors.Is(err, artwork.ErrNotFound) {
		return ErrNotFound()
	}
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The address carries the picture's version, so what was fetched can be kept, but only
	// by the person's own browser or app.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	return nil
}
