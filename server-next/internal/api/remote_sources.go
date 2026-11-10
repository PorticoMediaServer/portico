package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// Remote sources: storage Portico reaches over the network and never mounts. Only the owner
// adds, tests, changes or disconnects one, and nothing secret a source signs in with is ever
// sent back.

// RemoteSource is a remote source as the owner sees it.
type RemoteSource struct {
	ID string `json:"id"`
	// Kind is webdav, rclone or drive.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Preset is list-only, read-once (the default), everything or on-request.
	Preset string       `json:"preset"`
	Limits RemoteLimits `json:"limits"`
	WebDAV *WebDAVShown `json:"webdav,omitempty"`
	Rclone *RcloneShown `json:"rclone,omitempty"`
	Drive  *DriveShown  `json:"drive,omitempty"`
	// State is connected, unreachable, sign_in (needs its sign-in again), waiting (it asked
	// Portico to slow down, until WaitingUntil) or setup (no library folder is on it yet).
	State        string     `json:"state"`
	WaitingUntil *time.Time `json:"waitingUntil,omitempty"`
	Problem      string     `json:"problem,omitempty"`
	// Folders is how many library folders are on the source.
	Folders int `json:"folders"`
}

// RemoteLimits are a source's limits: connections at once and requests a second. Zero takes
// the preset's.
type RemoteLimits struct {
	Connections int     `json:"connections"`
	PerSecond   float64 `json:"perSecond"`
}

// WebDAVShown is a WebDAV source's settings as shown: never its password.
type WebDAVShown struct {
	Address     string `json:"address"`
	User        string `json:"user,omitempty"`
	PasswordSet bool   `json:"passwordSet"`
}

// WebDAVSettings are what a WebDAV source needs. On an edit an empty password keeps the
// one stored.
type WebDAVSettings struct {
	Address  string `json:"address"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
}

// RcloneShown is an rclone source as shown: the remote's type and the folder in it, never
// its configuration, which holds its tokens.
type RcloneShown struct {
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
}

// RcloneSettings are one remote from an rclone configuration, pasted as rclone config show
// prints it ("[name]" then "key = value" lines; the remote's own name is not used), and the
// folder within the remote. On an edit an empty configuration keeps the one stored.
type RcloneSettings struct {
	Config string `json:"config,omitempty"`
	Path   string `json:"path,omitempty"`
}

// DriveShown is a Google Drive source as shown: the folder in it, and what it signs in with
// (service-account or oauth, with the OAuth client's id). Never a key, secret or token.
type DriveShown struct {
	Path         string `json:"path,omitempty"`
	SignedInWith string `json:"signedInWith"`
	ClientID     string `json:"clientId,omitempty"`
}

// DriveSettings are what a Google Drive source signs in with, with the owner's own Google
// credentials: a service account key (the JSON file, pasted), or a finished sign-in with the
// owner's OAuth client (SignIn from POST /v1/admin/remote-sources/drive-sign-in and the
// address the browser was sent to). On an edit, leaving both out keeps the stored sign-in.
type DriveSettings struct {
	ServiceAccountKey string `json:"serviceAccountKey,omitempty"`
	SignIn            string `json:"signIn,omitempty"`
	RedirectedURL     string `json:"redirectedUrl,omitempty"`
	Path              string `json:"path,omitempty"`
}

// DriveSignInStart starts a sign-in with the owner's own Google OAuth client.
type DriveSignInStart struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// DriveSignInStarted is the sign-in to finish: open Address, allow Portico to read Drive, and
// paste the address the browser is sent to (a page that does not load) as redirectedUrl.
type DriveSignInStarted struct {
	SignIn  string `json:"signIn"`
	Address string `json:"address"`
}

// RemoteSourceCreate adds a source. Its connection is tested first and nothing is saved
// unless the test succeeds. Send an Idempotency-Key.
type RemoteSourceCreate struct {
	Kind   string          `json:"kind"`
	Name   string          `json:"name"`
	Preset string          `json:"preset,omitempty"`
	Limits *RemoteLimits   `json:"limits,omitempty"`
	WebDAV *WebDAVSettings `json:"webdav,omitempty"`
	Rclone *RcloneSettings `json:"rclone,omitempty"`
	Drive  *DriveSettings  `json:"drive,omitempty"`
}

// RemoteSourceEdit changes a source. Fields left out stay. A change to how it is reached
// or signs in to is tested first and not saved unless the test succeeds.
type RemoteSourceEdit struct {
	Name   *string         `json:"name,omitempty"`
	Preset *string         `json:"preset,omitempty"`
	Limits *RemoteLimits   `json:"limits,omitempty"`
	WebDAV *WebDAVSettings `json:"webdav,omitempty"`
	Rclone *RcloneSettings `json:"rclone,omitempty"`
	Drive  *DriveSettings  `json:"drive,omitempty"`
}

// RemoteFolder is a folder on a source, for choosing a library's folders.
type RemoteFolder struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (s *Server) remoteSourceRoutes() {
	Handle(s, "GET /v1/admin/remote-sources", Owner, func(r *http.Request, _ None) (Page[RemoteSource], error) {
		rows, err := s.db.RemoteSources(r.Context())
		out := Page[RemoteSource]{Items: []RemoteSource{}}
		for _, row := range rows {
			out.Items = append(out.Items, s.remoteSourceOut(row))
		}
		return out, err
	})
	Handle(s, "POST /v1/admin/remote-sources", Owner, s.createRemoteSource)
	Handle(s, "GET /v1/admin/remote-sources/{id}", Owner, func(r *http.Request, _ None) (RemoteSource, error) {
		row, err := s.remoteSource(r)
		return s.remoteSourceOut(row), err
	})
	Handle(s, "PATCH /v1/admin/remote-sources/{id}", Owner, s.editRemoteSource)
	Handle(s, "DELETE /v1/admin/remote-sources/{id}", Owner, func(r *http.Request, _ None) (None, error) {
		row, err := s.db.RemoteSource(r.Context(), r.PathValue("id"))
		if errors.Is(err, db.ErrNotFound) {
			return None{}, nil
		}
		if err != nil {
			return None{}, err
		}
		if err := s.db.DeleteRemoteSource(r.Context(), row.PublicID); errors.Is(err, db.ErrConflict) {
			return None{}, ErrConflict("source_in_use", "Remove this source's folders from their libraries first.")
		} else if err != nil {
			return None{}, err
		}
		s.remote.Forget(row.ID)
		return None{}, nil
	})
	Handle(s, "POST /v1/admin/remote-sources/{id}/test", Owner, func(r *http.Request, _ None) (RemoteSource, error) {
		row, err := s.remoteSource(r)
		if err != nil {
			return RemoteSource{}, err
		}
		if err := s.reach(r.Context(), row.ID, ""); err != nil {
			return RemoteSource{}, err
		}
		row, err = s.remoteSource(r)
		return s.remoteSourceOut(row), err
	})
	Handle(s, "GET /v1/admin/remote-sources/{id}/folders", Owner, s.remoteFolders)
	Handle(s, "POST /v1/admin/remote-sources/drive-sign-in", Owner, func(r *http.Request, req DriveSignInStart) (DriveSignInStarted, error) {
		req.ClientID, req.ClientSecret = strings.TrimSpace(req.ClientID), strings.TrimSpace(req.ClientSecret)
		if req.ClientID == "" || req.ClientSecret == "" || len(req.ClientID) > 512 || len(req.ClientSecret) > 512 {
			return DriveSignInStarted{}, ErrInvalid("Give your Google OAuth client's id and secret.")
		}
		id, address := s.driveSignIns.Start(req.ClientID, req.ClientSecret)
		return DriveSignInStarted{SignIn: id, Address: address}, nil
	})
}

func (s *Server) remoteSource(r *http.Request) (db.RemoteSource, error) {
	row, err := s.db.RemoteSource(r.Context(), r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		return row, ErrNotFound()
	}
	return row, err
}

func (s *Server) remoteSourceOut(row db.RemoteSource) RemoteSource {
	var c remote.Config
	_ = json.Unmarshal([]byte(row.Config), &c)
	preset, _ := remote.PresetNamed(c.Preset)
	out := RemoteSource{ID: row.PublicID, Kind: row.Kind, Name: row.Name, Preset: preset.Name, Problem: row.LastError, Folders: row.Folders,
		Limits: RemoteLimits{c.Limits.Connections, c.Limits.PerSecond}}
	switch row.Kind {
	case "webdav":
		out.WebDAV = &WebDAVShown{Address: c.Address, User: c.User, PasswordSet: c.SecretSet}
	case "rclone":
		out.Rclone = &RcloneShown{Type: c.RcloneType, Path: c.Path}
	case "drive":
		out.Drive = &DriveShown{Path: c.Path, SignedInWith: "service-account", ClientID: c.DriveClientID}
		if c.DriveClientID != "" {
			out.Drive.SignedInWith = "oauth"
		}
	}
	switch until, waiting := s.remote.Waiting(row.ID); {
	case waiting:
		out.State, out.WaitingUntil = "waiting", &until
	case row.State == "unreachable", row.State == "sign_in":
		out.State = row.State
	case row.Folders == 0:
		out.State = "setup"
	default:
		out.State = "connected"
	}
	return out
}

// settings checks a request's settings and folds them into a source's config and secret.
func remoteSettings(kind string, c *remote.Config, secret *remote.Secret, preset *string, limits *RemoteLimits, dav *WebDAVSettings, rc *RcloneSettings) error {
	if preset != nil {
		if _, ok := remote.PresetNamed(*preset); !ok {
			return ErrField("preset", "The preset is list-only, read-once, everything or on-request.")
		}
		c.Preset = *preset
	}
	if limits != nil {
		if limits.Connections < 0 || limits.Connections > 64 || limits.PerSecond < 0 || limits.PerSecond > 1000 {
			return ErrField("limits", "Connections are 1 to 64 and requests 1 to 1,000 a second.")
		}
		c.Limits = remote.Limits{Connections: limits.Connections, PerSecond: limits.PerSecond}
	}
	switch kind {
	case "webdav":
		if dav == nil {
			return nil
		}
		address := strings.TrimSpace(dav.Address)
		if u, err := url.Parse(address); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return ErrField("webdav.address", "Give the WebDAV address starting with https:// or http://, without a user name in it.")
		}
		c.Address, c.User = address, dav.User
		if dav.Password != "" {
			secret.Password = dav.Password
			c.SecretSet = true
		}
	case "rclone":
		if rc == nil {
			return nil
		}
		c.Path = strings.Trim(strings.TrimSpace(rc.Path), "/")
		if rc.Config != "" {
			options, err := remote.ParseRcloneConfig(rc.Config)
			if err != nil {
				return ErrField("rclone.config", "Paste one remote from the rclone configuration: "+err.Error()+".")
			}
			secret.Rclone, c.RcloneType, c.SecretSet = rc.Config, options["type"], true
		}
	case "drive":
	default:
		return ErrField("kind", "The kind of source is webdav, rclone or drive.")
	}
	return nil
}

// driveSettings folds a Drive source's settings in, finishing its sign-in if one is given.
func (s *Server) driveSettings(ctx context.Context, c *remote.Config, secret *remote.Secret, d *DriveSettings) error {
	if d == nil {
		return nil
	}
	c.Path = strings.Trim(strings.TrimSpace(d.Path), "/")
	switch {
	case d.ServiceAccountKey != "":
		if err := remote.CheckServiceAccountKey(d.ServiceAccountKey); err != nil {
			return ErrField("drive.serviceAccountKey", "Paste the JSON key Google gives for a service account.")
		}
		*secret, c.DriveClientID, c.SecretSet = remote.Secret{DriveKey: d.ServiceAccountKey}, "", true
	case d.SignIn != "":
		id, clientSecret, token, err := s.driveSignIns.Finish(ctx, d.SignIn, d.RedirectedURL)
		if err != nil {
			return refusal(http.StatusUnprocessableEntity, "drive_sign_in", "The Google sign-in did not finish: "+err.Error()+".", false)
		}
		*secret, c.DriveClientID, c.SecretSet = remote.Secret{DriveClientSecret: clientSecret, DriveToken: token}, id, true
	}
	return nil
}

func (s *Server) createRemoteSource(r *http.Request, req RemoteSourceCreate) (RemoteSource, error) {
	ctx := r.Context()
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 {
		return RemoteSource{}, ErrField("name", "A source's name has 1 to 200 characters.")
	}
	if req.Kind == "webdav" && req.WebDAV == nil {
		return RemoteSource{}, ErrField("webdav", "Give the WebDAV address.")
	}
	if req.Kind == "rclone" && (req.Rclone == nil || req.Rclone.Config == "") {
		return RemoteSource{}, ErrField("rclone.config", "Paste the remote from the rclone configuration.")
	}
	var c remote.Config
	var secret remote.Secret
	preset := req.Preset
	if err := remoteSettings(req.Kind, &c, &secret, &preset, req.Limits, req.WebDAV, req.Rclone); err != nil {
		return RemoteSource{}, err
	}
	if req.Kind == "drive" {
		if req.Drive == nil || req.Drive.ServiceAccountKey == "" && req.Drive.SignIn == "" {
			return RemoteSource{}, ErrField("drive", "Paste a service account key, or sign in with your own Google OAuth client.")
		}
		if err := s.driveSettings(ctx, &c, &secret, req.Drive); err != nil {
			return RemoteSource{}, err
		}
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return RemoteSource{}, ErrInvalid("The Idempotency-Key is at most 128 characters.")
	}
	if err := s.testSource(ctx, req.Kind, c, secret); err != nil {
		return RemoteSource{}, err
	}
	config, _ := json.Marshal(c)
	publicID := db.NewID("src")
	sealed, err := s.remote.Seal(publicID, secret)
	if err != nil {
		return RemoteSource{}, err
	}
	// The secret is left out of what identifies a repeat, so no trace of it is kept elsewhere.
	hashed := req
	if hashed.WebDAV != nil {
		dav := *hashed.WebDAV
		dav.Password = ""
		hashed.WebDAV = &dav
	}
	if hashed.Rclone != nil {
		rc := *hashed.Rclone
		rc.Config = ""
		hashed.Rclone = &rc
	}
	if hashed.Drive != nil {
		d := *hashed.Drive
		d.ServiceAccountKey, d.RedirectedURL = "", ""
		hashed.Drive = &d
	}
	row, err := s.db.CreateRemoteSource(ctx, publicID, db.NewRemoteSource{UserID: CallerFrom(ctx).UserID, Kind: req.Kind, Name: req.Name,
		Config: string(config), Secret: sealed, IdempotencyKey: key, RequestID: requestHash(hashed), Now: s.now()})
	if errors.Is(err, db.ErrKeyReused) {
		return RemoteSource{}, errKeyReused()
	}
	return s.remoteSourceOut(row), err
}

func (s *Server) editRemoteSource(r *http.Request, req RemoteSourceEdit) (RemoteSource, error) {
	ctx := r.Context()
	row, err := s.remoteSource(r)
	if err != nil {
		return RemoteSource{}, err
	}
	var c remote.Config
	if err := json.Unmarshal([]byte(row.Config), &c); err != nil {
		return RemoteSource{}, err
	}
	if req.Name != nil {
		if *req.Name = strings.TrimSpace(*req.Name); *req.Name == "" || len(*req.Name) > 200 {
			return RemoteSource{}, ErrField("name", "A source's name has 1 to 200 characters.")
		}
	}
	oldAddress := c.Address
	var secret remote.Secret
	if err := remoteSettings(row.Kind, &c, &secret, req.Preset, req.Limits, req.WebDAV, req.Rclone); err != nil {
		return RemoteSource{}, err
	}
	if row.Kind == "drive" {
		if err := s.driveSettings(ctx, &c, &secret, req.Drive); err != nil {
			return RemoteSource{}, err
		}
	}
	edit := db.RemoteSourceEdit{Name: req.Name, Now: s.now()}
	if req.Rclone != nil || req.Drive != nil {
		// A new configuration or folder: test it, with the stored configuration unless a new
		// one was given. The configuration names its own hosts, so it is kept with them.
		if secret == (remote.Secret{}) {
			if secret, err = s.remote.Secret(ctx, row.ID); err != nil {
				return RemoteSource{}, ErrConflict("source_secret_lost", "Paste the remote's configuration again.")
			}
		}
		if err := s.testSource(ctx, row.Kind, c, secret); err != nil {
			return RemoteSource{}, err
		}
		if edit.Secret, err = s.remote.Seal(row.PublicID, secret); err != nil {
			return RemoteSource{}, err
		}
	}
	if req.WebDAV != nil {
		// How the source is reached changed: test it, with the stored password unless a new
		// one was given, and keep nothing unless the test succeeds.
		if secret == (remote.Secret{}) {
			// A stored password goes only to the place it was given for: a new host needs it
			// typed again, so changing the address cannot be used to read it.
			before, _ := url.Parse(oldAddress)
			after, _ := url.Parse(c.Address)
			if before == nil || after == nil || before.Scheme != after.Scheme || before.Host != after.Host {
				return RemoteSource{}, ErrField("webdav.password", "Give the password again for a new address.")
			}
			if secret, err = s.remote.Secret(ctx, row.ID); err != nil {
				return RemoteSource{}, ErrConflict("source_secret_lost", "Give the source's password again.")
			}
		}
		if err := s.testSource(ctx, row.Kind, c, secret); err != nil {
			return RemoteSource{}, err
		}
		if edit.Secret, err = s.remote.Seal(row.PublicID, secret); err != nil {
			return RemoteSource{}, err
		}
	}
	config, _ := json.Marshal(c)
	text := string(config)
	edit.Config = &text
	row, err = s.db.UpdateRemoteSource(ctx, row.PublicID, edit)
	if errors.Is(err, db.ErrNotFound) {
		return RemoteSource{}, ErrNotFound()
	}
	return s.remoteSourceOut(row), err
}

// testSource tries a source's settings before anything is kept.
func (s *Server) testSource(ctx context.Context, kind string, c remote.Config, secret remote.Secret) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return reachRefusal(remote.Test(ctx, kind, c, secret))
}

// reach lists a folder of a saved source, which also records how the source was found.
func (s *Server) reach(ctx context.Context, id int64, dir string) error {
	_, err := s.listRemote(ctx, id, dir)
	return err
}

func (s *Server) listRemote(ctx context.Context, id int64, dir string) ([]remote.Entry, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	storage, _, err := s.remote.Open(ctx, id)
	if err != nil {
		return nil, reachRefusal(err)
	}
	entries, err := storage.List(ctx, dir)
	return entries, reachRefusal(err)
}

// reachRefusal says in a sentence why a source could not be reached.
func reachRefusal(err error) error {
	var unreachable *url.Error
	switch {
	case err == nil:
		return nil
	case errors.Is(err, remote.ErrSignIn):
		return refusal(http.StatusUnprocessableEntity, "source_sign_in", "The source refused that user name and password.", false)
	case errors.Is(err, context.DeadlineExceeded):
		return refusal(http.StatusUnprocessableEntity, "source_unreachable", "The source did not answer in time. Check the address.", true)
	case errors.As(err, &unreachable):
		return refusal(http.StatusUnprocessableEntity, "source_unreachable", "Portico could not reach that address: "+unreachable.Err.Error(), true)
	default:
		return refusal(http.StatusUnprocessableEntity, "source_unusable", "The source answered, but not as Portico needs: "+err.Error(), false)
	}
}

// remoteFolders lists the folders in one folder of a source, for choosing a library's folders.
func (s *Server) remoteFolders(r *http.Request, _ None) (Page[RemoteFolder], error) {
	row, err := s.remoteSource(r)
	if err != nil {
		return Page[RemoteFolder]{}, err
	}
	dir := strings.Trim(r.URL.Query().Get("path"), "/")
	entries, err := s.listRemote(r.Context(), row.ID, dir)
	if err != nil {
		return Page[RemoteFolder]{}, err
	}
	out := Page[RemoteFolder]{Items: []RemoteFolder{}}
	for _, e := range entries {
		if e.Dir && !strings.HasPrefix(e.Name, ".") {
			out.Items = append(out.Items, RemoteFolder{Name: e.Name, Path: strings.TrimPrefix(dir+"/"+e.Name, "/")})
		}
	}
	return out, nil
}
