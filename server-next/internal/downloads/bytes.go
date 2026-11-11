package downloads

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// GrantPrefix starts every download address's grant. It holds a character a stream's grant
// never has, so the media route tells the two apart at a glance.
const GrantPrefix = "dl."

// grantIdle is how long a download address works after it was last used, and grantMax how
// long at most after it was given, however often it is used. A device that comes back later,
// or after a restart, asks for the title's addresses again (which needs its sign-in) and
// carries on from where its file stopped.
const (
	grantIdle = 24 * time.Hour
	grantMax  = 7 * 24 * time.Hour
)

// grantKey is what an address is for: one sign-in, one request, one title.
type grantKey struct {
	session        int64
	download, item string
}

// grant is one title of one request, fetchable by whoever holds its address for as long as
// the sign-in it was given to lasts.
type grant struct {
	grantKey
	viewer       db.Viewer
	token        string
	given        time.Time
	expires      time.Time
	usedRecorded time.Time
}

// grants are in memory: an address is a convenience for a device fetching in the background,
// and nothing a person would miss after a restart.
type grants struct {
	mu      sync.Mutex
	byToken map[string]*grant
	byKey   map[grantKey]*grant
}

// Grant returns the address part for one title of a request, given to one sign-in: the same
// one while it works.
func (s *Service) Grant(v db.Viewer, session int64, download, item string) string {
	g := &s.grants
	g.mu.Lock()
	defer g.mu.Unlock()
	now := s.Now()
	if g.byToken == nil {
		g.byToken, g.byKey = map[string]*grant{}, map[grantKey]*grant{}
	}
	key := grantKey{session: session, download: download, item: item}
	if held := g.byKey[key]; held != nil && now.Before(held.expires) && now.Before(held.given.Add(grantMax-grantIdle)) {
		held.expires = now.Add(grantIdle)
		return held.token
	}
	// Expired addresses are dropped as new ones are given, so the maps stay as large as the
	// addresses in use.
	if len(g.byToken) > 1000 {
		for token, held := range g.byToken {
			if now.After(held.expires) {
				delete(g.byToken, token)
				delete(g.byKey, held.grantKey)
			}
		}
	}
	var b [18]byte
	rand.Read(b[:])
	token := GrantPrefix + base64.RawURLEncoding.EncodeToString(b[:])
	held := &grant{grantKey: key, viewer: v, token: token, given: now, expires: now.Add(grantIdle)}
	g.byToken[token], g.byKey[key] = held, held
	return token
}

func (s *Service) take(token string) (*grant, bool) {
	g := &s.grants
	g.mu.Lock()
	defer g.mu.Unlock()
	held := g.byToken[token]
	now := s.Now()
	if held == nil || now.After(held.expires) {
		return nil, false
	}
	held.expires = now.Add(grantIdle)
	if limit := held.given.Add(grantMax); held.expires.After(limit) {
		held.expires = limit
	}
	copied := *held
	return &copied, true
}

// recordUse notes that a title's copy was fetched through this address now.
func (s *Service) recordUse(key grantKey, now time.Time) {
	s.grants.mu.Lock()
	defer s.grants.mu.Unlock()
	if held := s.grants.byKey[key]; held != nil {
		held.usedRecorded = now
	}
}

// File is one file of a downloaded title, open for serving by range.
type File struct {
	io.ReadSeeker
	io.Closer
	Size     int64
	Modified time.Time
	// ETag is strong: the copy's hash, or the original's size and modified time.
	ETag        string
	ContentType string
	// Name is a file name for a browser to save it as.
	Name string
}

// ErrGone says the address no longer works: expired, or the title is no longer the
// holder's to fetch. Both answer the same hidden 404.
var ErrGone = errors.New("this download address no longer works")

// ErrNotReady says the copy is still being made, so there is nothing to fetch yet.
var ErrNotReady = errors.New("this copy is not ready yet")

// Open opens one file of a title by its address. Who may fetch it is checked again on every
// request, so a person who lost the title or downloads altogether stops at the next range.
// name is "file/N" for the Nth part, or "subtitles/ID" for a subtitle file beside it.
func (s *Service) Open(ctx context.Context, token, name string) (*File, error) {
	held, ok := s.take(token)
	if !ok {
		return nil, ErrGone
	}
	// The sign-in it was given to must still be live, on the same profile, on the request's
	// own device: signing out, being signed out, or moving to another profile ends it.
	session, err := s.DB.DownloadSession(ctx, held.session, s.Now())
	if err != nil || session.ProfileID != held.viewer.ProfileID {
		return nil, ErrGone
	}
	if err := s.May(ctx, held.viewer, session.Platform); err != nil {
		return nil, ErrGone
	}
	dl, title, err := s.DB.DownloadedTitle(ctx, held.viewer.ProfileID, held.download, held.item)
	if err != nil || title.State != db.DownloadWanted || dl.DeviceID != session.DeviceID {
		return nil, ErrGone
	}
	if visible, err := s.DB.Visible(ctx, held.viewer, title.ItemID); err != nil || !visible {
		return nil, ErrGone
	}
	kind, rest, _ := strings.Cut(name, "/")
	switch kind {
	case "file":
		n, err := strconv.Atoi(rest)
		if err != nil || n < 1 {
			return nil, ErrGone
		}
		if dl.Quality != Original {
			return s.openCopy(ctx, held, title, n)
		}
		return s.openOriginal(ctx, title, n)
	case "subtitles":
		return s.openSubtitle(ctx, held.viewer, title, rest)
	}
	return nil, ErrGone
}

func (s *Service) openCopy(ctx context.Context, held *grant, title db.DownloadTitle, n int) (*File, error) {
	p := title.Prepared
	if p == nil {
		return nil, ErrGone
	}
	if p.State != db.PreparedReady {
		return nil, ErrNotReady
	}
	files := s.Prepare.Files(*p)
	hashes := strings.Fields(p.ContentHash)
	if n > len(files) || len(hashes) != len(files) {
		return nil, ErrGone
	}
	// Fetching keeps the copy: its keeping time starts again, at most once an hour per title.
	if now := s.Now(); now.Sub(held.usedRecorded) > time.Hour {
		s.DB.UsePrepared(ctx, p.ID, now)
		s.recordUse(held.grantKey, now)
	}
	file, err := os.Open(files[n-1])
	if err != nil {
		return nil, ErrGone
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, ErrGone
	}
	contentType := "video/mp4"
	if p.Container == "m4a" {
		contentType = "audio/mp4"
	}
	return &File{ReadSeeker: file, Closer: file, Size: info.Size(), Modified: info.ModTime(), ETag: `"` + hashes[n-1] + `"`,
		ContentType: contentType, Name: saveName(title.Title, n, len(files), p.Container)}, nil
}

func (s *Service) openOriginal(ctx context.Context, title db.DownloadTitle, n int) (*File, error) {
	v, _, err := s.DB.PreparedVersion(ctx, title.MediaID)
	if err != nil || n > len(v.Parts) {
		return nil, ErrGone
	}
	part := v.Parts[n-1]
	source, size, modified, err := s.Prepare.OpenFile(ctx, part.FileID)
	if err != nil {
		return nil, err
	}
	container := strings.TrimPrefix(strings.ToLower(extension(part.Name)), ".")
	video := ""
	if v.VideoCodec != "" {
		video = v.VideoCodec
	}
	return &File{ReadSeeker: io.NewSectionReader(source, 0, size), Closer: source, Size: size, Modified: modified,
		ETag: fmt.Sprintf(`"%x-%x"`, size, modified.UnixNano()), ContentType: mediafacts.OriginalContentType(v.Container, video),
		Name: saveName(title.Title, n, len(v.Parts), container)}, nil
}

func (s *Service) openSubtitle(ctx context.Context, v db.Viewer, title db.DownloadTitle, trackID string) (*File, error) {
	item, err := s.DB.PlaybackItem(ctx, v, title.ItemPublicID)
	if err != nil {
		return nil, ErrGone
	}
	for _, version := range item.Versions {
		if version.MediaID() != title.MediaID {
			continue
		}
		for _, track := range version.Tracks {
			if track.Kind != db.StreamSubtitle || !track.Sidecar || playback.TrackID(track) != trackID {
				continue
			}
			source, size, modified, err := s.Prepare.OpenFile(ctx, track.FileID)
			if err != nil {
				return nil, err
			}
			return &File{ReadSeeker: io.NewSectionReader(source, 0, size), Closer: source, Size: size, Modified: modified,
				ETag: fmt.Sprintf(`"%x-%x"`, size, modified.UnixNano()), ContentType: "application/octet-stream",
				Name: saveName(title.Title, 0, 0, track.Codec)}, nil
		}
	}
	return nil, ErrGone
}

func extension(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i:]
	}
	return ""
}

// saveName is what a browser saves a file as: the title, the part when there are several,
// and the extension. Characters no file system takes become spaces.
func saveName(title string, n, of int, ext string) string {
	name := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 32 {
			return ' '
		}
		return r
	}, title)
	if of > 1 {
		name += fmt.Sprintf(" (part %d)", n)
	}
	if ext != "" {
		name += "." + ext
	}
	return strings.TrimSpace(name)
}
