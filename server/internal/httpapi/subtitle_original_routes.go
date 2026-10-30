package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// Binary payloads use the same grant registry and byte-range semantics as media.
type SubtitleBytesContract string

func (SubtitleBytesContract) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "format": "binary"}
}

type SubtitleRefreshRequest struct {
	SourceID string `json:"sourceId"`
}

func registerSubtitleRaw[Req, Resp any](registry *apikit.Registry, pattern string, handler http.HandlerFunc) error {
	method, path, _ := strings.Cut(pattern, " ")
	access := apikit.Device
	requires := apikit.ScopeRead
	if strings.Contains(path, "/{itemId}/") {
		access = apikit.ViewerItem
	}
	if method != "GET" && method != "HEAD" {
		requires = apikit.ScopeLibrary
	}
	if method == "PUT" && strings.Contains(path, "/playback/") {
		requires = apikit.ScopePlayback
	}
	lane := apikit.Default
	if strings.Contains(path, "/media/") {
		access = apikit.Public
		requires = apikit.ScopeOpen
		lane = apikit.Media
	}
	body := int64(0)
	if method != "GET" && method != "HEAD" {
		body = subtitles.MaxAssetBytes
	}
	admission := laneDefault
	if method == "GET" && strings.Contains(path, "/{itemId}/") {
		admission = laneBrowsing
	}
	if strings.Contains(path, "/playback/") {
		admission = lanePlayback
	}
	if strings.Contains(path, "/media/") {
		admission = laneMediaBody
	}
	if strings.HasSuffix(path, "/{tile}.jpg") || strings.HasSuffix(path, "/{chapter}/image") {
		admission = laneMedia
	}
	contentType := ""
	switch {
	case strings.HasSuffix(path, ".vtt"):
		contentType = "text/vtt"
	case strings.HasSuffix(path, "original.ass"), strings.HasSuffix(path, "original.ssa"):
		contentType = "text/x-ssa"
	case strings.HasSuffix(path, "original.idx"):
		contentType = "text/plain"
	case strings.Contains(path, "/original."):
		contentType = "application/octet-stream"
	case strings.HasSuffix(path, "/fonts/{font}"):
		contentType = "font/ttf"
	case strings.HasSuffix(path, "/{tile}.jpg"), strings.HasSuffix(path, "/{chapter}/image"):
		contentType = "image/jpeg"
	}

	identifier := strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_", ".", "_").Replace(strings.ToLower(method) + path)
	// Saving a file also accepts a multipart upload (subtitleMutationDecode).
	var requestTypes []string
	if method == "POST" && path == "/v1/items/{itemId}/subtitles" || method == "PUT" && path == "/v1/items/{itemId}/subtitles/{resource}" {
		requestTypes = []string{"application/json", "multipart/form-data"}
	}
	return apikit.RegisterRaw(registry, apikit.RawRoute[Req, Resp]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: admission, ID: identifier, Method: method, Path: path, Summary: "Read or change authorized subtitles", Access: access, Requires: requires, Lane: lane, Cost: apikit.Constant, BodyLimit: body, Query: []string{"segment"}, Status: 200, ResponseContentType: contentType, RequestContentTypes: requestTypes, Errors: subtitleRouteErrors()}, Serve: handler})
}
func (d Dependencies) originalSubtitleRoute(w http.ResponseWriter, r *http.Request) {
	subtitleHeaders(w)
	if d.Subtitles == nil || d.Playback == nil {
		subtitleFailure(w, subtitles.ErrUnavailable)
		return
	}
	revision, e := strconv.ParseInt(r.PathValue("revision"), 10, 64)
	if e != nil || revision < 1 || strconv.FormatInt(revision, 10) != r.PathValue("revision") {
		subtitleFailure(w, subtitles.ErrInput)
		return
	}
	_, p, item, e := d.Playback.ResolveGrantContext(r.Context(), r.PathValue("grant"))
	if e != nil {
		subtitleFailure(w, e)
		return
	}
	var session string
	e = d.DB.QueryRowContext(r.Context(), `SELECT id FROM playback_sessions WHERE grant_hash=?`, identity.Digest(r.PathValue("grant"))).Scan(&session)
	if e != nil {
		subtitleFailure(w, e)
		return
	}
	id := r.PathValue("resource")

	check := func() error {
		if _, _, _, e := d.Playback.ResolveGrantContext(r.Context(), r.PathValue("grant")); e != nil {
			return e
		}
		return d.Subtitles.CheckOriginal(r.Context(), p, item, session, id, revision)
	}
	if e = check(); e != nil {
		subtitleFailure(w, e)
		return
	}
	base := subtitles.MediaBaseURL(r.PathValue("grant"), id, revision)
	if strings.HasSuffix(r.URL.Path, "/lines") {
		lines, e := d.Subtitles.OriginalLines(r.Context(), p, item, session, id, revision)
		if e != nil {
			subtitleFailure(w, e)
			return
		}
		if e = check(); e != nil {
			subtitleFailure(w, e)
			return
		}
		write(w, 200, lines)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/fonts") {
		fonts, e := d.Subtitles.OriginalFonts(r.Context(), p, item, session, id, revision, base)
		if e != nil {
			subtitleFailure(w, e)
			return
		}
		if e = check(); e != nil {
			subtitleFailure(w, e)
			return
		}
		write(w, 200, fonts)
		return
	}
	name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	part := "data"
	contentType := "application/octet-stream"
	font := r.PathValue("font")
	if font != "" {
		n, e := strconv.Atoi(font)
		if e != nil || n < 0 || n >= 16 || strconv.Itoa(n) != font {
			subtitleFailure(w, subtitles.ErrInput)
			return
		}
		part = "font:" + font
		name = "font-" + font
		contentType = "font/ttf"
	} else if name == "original.sub" {
		part = "companion"
	}
	reader, size, format, encoding, e := d.Subtitles.OpenOriginalFile(r.Context(), p, item, session, id, revision, part)
	if e != nil {
		subtitleFailure(w, e)
		return
	}
	defer reader.Close()
	if font == "" {
		switch name {
		case "original.ass", "original.ssa":
			if format != "ass" && format != "ssa" {
				subtitleFailure(w, subtitles.ErrUnsupported)
				return
			}
			contentType = "text/x-ssa"
			if encoding != "" {
				contentType += "; charset=" + encoding
			}
		case "original.sup":
			if format != "pgs" {
				subtitleFailure(w, subtitles.ErrUnsupported)
				return
			}
		case "original.idx":
			if format != "vobsub" {
				subtitleFailure(w, subtitles.ErrUnsupported)
				return
			}
			contentType = "text/plain"
		case "original.sub":
			if format != "vobsub" {
				subtitleFailure(w, subtitles.ErrUnsupported)
				return
			}
		case "original.mks":
			if format != "mks" && format != "dvb" {
				subtitleFailure(w, subtitles.ErrUnsupported)
				return
			}
		default:
			subtitleFailure(w, subtitles.ErrInput)
			return
		}
	}
	if e = check(); e != nil {
		subtitleFailure(w, e)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", `"subtitle-original-`+id+`-`+strconv.FormatInt(revision, 10)+`-`+name+`"`)
	http.ServeContent(w, r, name, time.Time{}, &authorizedSubtitleReader{ReadSeeker: io.NewSectionReader(reader, 0, size), check: check})
}

type authorizedSubtitleBytes struct {
	*bytes.Reader
	check func() error
}

func (r *authorizedSubtitleBytes) Read(b []byte) (int, error) {
	if e := r.check(); e != nil {
		return 0, e
	}
	return r.Reader.Read(b[:min(len(b), 32<<10)])
}

var _ io.ReadSeeker = (*authorizedSubtitleBytes)(nil)

type authorizedSubtitleReader struct {
	io.ReadSeeker
	check func() error
}

func (r *authorizedSubtitleReader) Read(b []byte) (int, error) {
	if e := r.check(); e != nil {
		return 0, e
	}
	return r.ReadSeeker.Read(b[:min(len(b), 32<<10)])
}
