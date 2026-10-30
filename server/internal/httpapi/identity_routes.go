package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/imagework"
)

// identityRoutes registers the workstream-H identity surface: profile
// restrictions and avatars, PIN recovery, device records and
// remembered browser accounts, direct-server two-factor, self-registration and
// the pre-authentication capabilities descriptor.
//
// Routes under /v1/direct/* take the account session token (directBearer), the
// same credential the rest of the Direct Sign-In surface uses. Routes under
// /v1/auth/* are deliberately unauthenticated and say so in their handlers.
func (d Dependencies) identityRoutes(mux *http.ServeMux) {
	if d.Identity == nil {
		return
	}
	d.identityProfileRoutes(mux)
	d.identityDeviceRoutes(mux)
	d.identityAuthRoutes(mux)
	d.topShelfRoutes(mux)
}

func (d Dependencies) identityProfileRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "POST /v1/direct/profiles/{id}/avatar", func(w http.ResponseWriter, r *http.Request) {
		if e := d.Identity.AuthorizeProfileAvatarUpload(r.Context(), directBearer(r), r.PathValue("id")); e != nil {
			closeUnreadUpload(w, r)
			failure(w, e)
			return
		}
		raw, release, e := readAvatarUpload(w, r)
		if e != nil {
			failure(w, e)
			return
		}
		defer release()
		out, e := d.Identity.UploadProfileAvatar(r.Context(), directBearer(r), r.PathValue("id"), raw)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 201, out)
	})
	d.httpHandle(mux, "DELETE /v1/direct/profiles/{id}/avatar", func(w http.ResponseWriter, r *http.Request) {
		if e := d.Identity.DeleteProfileAvatar(r.Context(), directBearer(r), r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
	d.httpHandle(mux, "GET /v1/profiles/{id}/avatar", func(w http.ResponseWriter, r *http.Request) {
		// Reading an avatar needs a viewing session on the same account: a
		// profile picture is personal data, not public artwork.
		p, e := d.principal(r)
		if e != nil {
			failure(w, e)
			return
		}
		size := 0
		for key, values := range r.URL.Query() {
			if len(values) != 1 || (key != "v" && key != "size") || len(values[0]) > 16 {
				failure(w, requestinput.New("invalid avatar query"))
				return
			}
			if key == "size" {
				size, _ = strconv.Atoi(values[0])
			}
		}
		raw, version, updated, e := d.Identity.ReadProfileAvatar(r.Context(), p.AccountID, r.PathValue("id"), size)
		if e != nil {
			failure(w, e)
			return
		}
		requested := r.URL.Query().Get("v")
		if requested != "" && requested != strconv.FormatInt(version, 10) {
			// The client asked for a version this profile no longer has. Saying
			// so beats serving a different picture under the requested URL.
			w.Header().Set("Cache-Control", "no-store")
			failure(w, identity.ErrAvatarMissing)
			return
		}
		modified, _ := time.Parse(time.RFC3339, updated)
		w.Header().Set("Content-Type", http.DetectContentType(raw))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if requested == "" {
			w.Header().Set("Cache-Control", "private, no-cache")
		} else {
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		}
		http.ServeContent(w, r, "avatar", modified, bytes.NewReader(raw))
	})
}

// A refusal before consuming an HTTP/1 upload must not make net/http wait for
// the remaining body while finalizing the answer. HTTP/2 resets only the stream.
func closeUnreadUpload(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor == 1 && r.ContentLength != 0 {
		w.Header().Set("Connection", "close")
		// finishRequest closes the body even with Connection: close. Bound its
		// early-close drain after this handler has finished using the body.
		_ = http.NewResponseController(w).SetReadDeadline(time.Now())
	}
}

// avatarBody reads the uploaded picture from either a multipart form field named
// "image" or a raw body. Neither the declared type nor the file name is trusted;
// the bytes are sniffed in internal/identity.
func avatarBody(r *http.Request) ([]byte, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return nil, identity.ErrAvatarUpload
		}
		defer r.MultipartForm.RemoveAll()
		file, _, err := r.FormFile("image")
		if err != nil {
			return nil, identity.ErrAvatarUpload
		}
		defer file.Close()
		return io.ReadAll(io.LimitReader(file, identity.AvatarUploadBytes+1))
	}
	return io.ReadAll(io.LimitReader(r.Body, identity.AvatarUploadBytes+1))
}

// Remaining direct session and remembered-account routes. Device records use
// the shared /v1/devices typed registry in device_contract.go.
func (d Dependencies) identityDeviceRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "POST /v1/direct/profile-switch", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			InstallationID    string `json:"installationId"`
			OutgoingProfileID string `json:"outgoingProfileId"`
		}
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.SwitchProfileFence(r.Context(), directBearer(r), body.InstallationID, body.OutgoingProfileID); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
	d.httpHandle(mux, "POST /v1/direct/remembered-accounts", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			InstallationID  string `json:"installationId"`
			AutomaticSignIn bool   `json:"automaticSignIn"`
		}
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.RememberBrowserAccount(r.Context(), directBearer(r), body.InstallationID, body.AutomaticSignIn)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, map[string]any{"items": out})
	})
}

func (d Dependencies) identityAuthRoutes(mux *http.ServeMux) {
	d.httpHandle(mux, "POST /v1/auth/refresh", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			RefreshToken   string `json:"refreshToken"`
			InstallationID string `json:"installationId"`
			RequestID      string `json:"requestId"`
		}
		if e := decode(w, r, &q); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.RefreshSession(r.Context(), q.RefreshToken, q.InstallationID, q.RequestID)
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var body identity.SelfRegistrationRequest
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.Register(r.Context(), body, d.privateSetupPeer(r))
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeAuth(w, r, 201, out)
	})
	d.httpHandle(mux, "POST /v1/auth/two-factor/challenge", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
			Code  string `json:"code"`
		}
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.CompleteSignInChallenge(r.Context(), body.Token, body.Code, d.privateSetupPeer(r))
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeAuth(w, r, 200, out)
	})
	d.httpHandle(mux, "GET /v1/direct/two-factor", func(w http.ResponseWriter, r *http.Request) {
		out, e := d.Identity.TwoFactor(r.Context(), directBearer(r))
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/direct/two-factor/enrol", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.EnrolTwoFactor(r.Context(), directBearer(r), body.Password)
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 201, out)
	})
	d.httpHandle(mux, "POST /v1/direct/two-factor/verify", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.VerifyTwoFactor(r.Context(), directBearer(r), body.Password, body.Code)
		if e != nil {
			failure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "POST /v1/direct/two-factor/recovery-codes", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		out, e := d.Identity.ReplaceRecoveryCodes(r.Context(), directBearer(r), body.Password, body.Code)
		if e != nil {
			failure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		write(w, 200, out)
	})

	d.httpHandle(mux, "DELETE /v1/direct/two-factor", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if e := decode(w, r, &body); e != nil {
			failure(w, e)
			return
		}
		if e := d.Identity.DisableTwoFactor(r.Context(), directBearer(r), body.Password, body.Code); e != nil {
			failure(w, e)
			return
		}
		w.WriteHeader(204)
	})
}

func readAvatarUpload(w http.ResponseWriter, r *http.Request) ([]byte, func(), error) {
	release, e := imagework.AcquireUploadBody(r.Context())
	if e != nil {
		closeUnreadUpload(w, r)
		return nil, nil, e
	}
	if deadline, ok := r.Context().Deadline(); ok {
		_ = http.NewResponseController(w).SetReadDeadline(deadline)
		defer func() {
			if r.Context().Err() == nil && w.Header().Get("Connection") != "close" {
				_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
			}
		}()
	}
	// A picture is not JSON, so this route reads the body itself rather than
	// through decode. The envelope allowance covers multipart framing only.
	r.Body = http.MaxBytesReader(w, r.Body, identity.AvatarUploadBytes+(1<<20))
	raw, e := avatarBody(r)
	if e != nil {
		closeUnreadUpload(w, r)
		release()
		return nil, nil, e
	}
	return raw, release, nil
}
