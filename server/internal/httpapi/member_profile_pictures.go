package httpapi

import (
	"bytes"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strconv"
	"time"
)

func (d Dependencies) memberProfilePictureRoutes(mux *http.ServeMux) {
	store := d.Access.Access
	if store == nil || d.Identity == nil {
		return
	}
	const path = "/v1/admin/access/members/{id}/profiles/{profileId}/avatar"
	d.httpHandle(mux, "POST "+path, func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			closeUnreadUpload(w, r)
			accessFailure(w, e)
			return
		}
		revision, e := pictureRevision(r)
		if e != nil {
			closeUnreadUpload(w, r)
			accessFailure(w, e)
			return
		}
		account, profile := r.PathValue("id"), r.PathValue("profileId")
		auth := d.accessTargetAuthority(p, account, false)
		if e = store.AuthorizeMemberProfilePicture(r.Context(), auth, account, profile, revision); e != nil {
			closeUnreadUpload(w, r)
			accessFailure(w, e)
			return
		}
		raw, release, e := readAvatarUpload(w, r)
		if e != nil {
			failure(w, e)
			return
		}
		defer release()
		prepared, e := identity.PrepareProfileAvatar(r.Context(), raw)
		if e != nil {
			failure(w, e)
			return
		}
		out, e := store.SetMemberProfilePicture(r.Context(), auth, d.Identity, account, profile, revision, &prepared)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "DELETE "+path, func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		var q identity.MemberProfilePINReset
		if e = decode(w, r, &q); e != nil {
			accessFailure(w, e)
			return
		}
		out, e := store.SetMemberProfilePicture(r.Context(), d.accessTargetAuthority(p, r.PathValue("id"), false), d.Identity, r.PathValue("id"), r.PathValue("profileId"), q.ExpectedRevision, nil)
		if e != nil {
			accessFailure(w, e)
			return
		}
		write(w, 200, out)
	})
	d.httpHandle(mux, "GET "+path, func(w http.ResponseWriter, r *http.Request) {
		p, e := d.administrator(r)
		if e != nil {
			accessFailure(w, e)
			return
		}
		size := 0
		for key, values := range r.URL.Query() {
			if len(values) != 1 || (key != "v" && key != "size") || len(values[0]) > 16 {
				failure(w, identity.ErrDirectInput)
				return
			}
			if key == "size" {
				size, e = strconv.Atoi(values[0])
				if e != nil || size < 0 {
					failure(w, identity.ErrDirectInput)
					return
				}
			}
		}
		raw, version, updated, e := store.MemberProfilePicture(r.Context(), d.accessTargetAuthority(p, r.PathValue("id"), false), r.PathValue("id"), r.PathValue("profileId"), size)
		if e != nil {
			accessFailure(w, e)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		if v := r.URL.Query().Get("v"); v != "" && v != strconv.FormatInt(version, 10) {
			failure(w, identity.ErrAvatarMissing)
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(raw))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		modified, _ := time.Parse(time.RFC3339, updated)
		http.ServeContent(w, r, "avatar", modified, bytes.NewReader(raw))
	})
}

func pictureRevision(r *http.Request) (int64, error) {
	values := r.URL.Query()
	if len(values) != 1 || len(values["expectedRevision"]) != 1 {
		return 0, identity.ErrDirectInput
	}
	revision, e := strconv.ParseInt(values.Get("expectedRevision"), 10, 64)
	if e != nil || revision < 1 {
		return 0, identity.ErrDirectInput
	}
	return revision, nil
}
