package subtitlevideo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

var companionName = regexp.MustCompile(`^[\pL\pN _().-]+\.(?i:srt|vtt|ass|ssa|sup|idx)$`)

func companionURL(locator, name string) (string, error) {
	if !utf8.ValidString(name) || len(name) > 255 || !companionName.MatchString(name) || strings.Contains(name, "..") || strings.TrimSpace(name) != name {
		return "", subtitles.ErrInput
	}
	u, e := url.Parse(locator)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return "", subtitles.ErrInput
	}
	// A media query token is not authorization to send that credential to another
	// resource. A companion needing separate credentials must be uploaded instead.
	u.Path = path.Join(path.Dir(u.Path), name)
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.ForceQuery = false
	return u.String(), nil
}
func (r *Runtime) AcquireCompanion(ctx context.Context, item, source, name, format string) ([]byte, []byte, string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	input, e := r.OpenSubtitleInput(ctx, item, source, "")
	if e != nil {
		return nil, nil, "", e
	}
	defer input.Close()
	remote, ok := input.(*remoteInput)
	if !ok {
		return nil, nil, "", subtitles.ErrUnsupported
	}
	descriptor, e := remote.descriptor.ReadExtent(ctx, 0, remote.descriptor.Size())
	if e != nil {
		return nil, nil, "", e
	}
	locator, e := remotemedia.ParseDescriptor(descriptor)
	if e != nil {
		return nil, nil, "", e
	}
	companion, e := companionURL(locator, name)
	if e != nil {
		return nil, nil, "", e
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	canonicalFormat := func(v string) string {
		switch v {
		case "sup":
			return "pgs"
		case "idx":
			return "vobsub"
		}
		return v
	}
	if canonicalFormat(ext) != canonicalFormat(format) {
		return nil, nil, "", subtitles.ErrInput
	}
	var approvals string
	policy := remotemedia.Policy{}
	e = r.db.QueryRowContext(ctx, `SELECT approvals_json FROM library_network_policy WHERE library_id=? AND revision=?`, remote.source.library, remote.source.networkRevision).Scan(&approvals)
	if e != nil {
		if remote.source.networkRevision != 0 {
			return nil, nil, "", e
		}
		approvals = "[]"
	}
	if json.Unmarshal([]byte(approvals), &policy.Approvals) != nil {
		return nil, nil, "", subtitles.ErrInput
	}
	read := func(locator string, limit int64) ([]byte, error) {
		client, e := remotemedia.New(ctx, locator, policy)
		if e != nil {
			return nil, e
		}
		defer client.Close()
		response, e := client.Open(ctx, http.MethodGet, "")
		if e != nil {
			return nil, e
		}
		defer response.Body.Close()
		if response.StatusCode != 200 || response.ContentLength > limit || response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
			return nil, subtitles.ErrUnavailable
		}
		data, e := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if e != nil {
			return nil, e
		}
		if len(data) < 1 || int64(len(data)) > limit {
			return nil, subtitles.ErrCapacity
		}
		return data, nil
	}
	limit := int64(subtitles.MaxInputBytes)
	if ext == "sup" {
		limit = int64(subtitles.BinaryLimitBytes())
	}
	data, e := read(companion, limit)
	if e != nil {
		return nil, nil, "", e
	}
	var paired []byte
	if ext == "idx" {
		u, _ := url.Parse(companion)
		u.Path = strings.TrimSuffix(u.Path, path.Ext(u.Path)) + ".sub"
		paired, e = read(u.String(), int64(subtitles.BinaryLimitBytes())-int64(len(data)))
		if e != nil {
			return nil, nil, "", e
		}
	}
	if e = input.Validate(ctx); e != nil {
		return nil, nil, "", e
	}
	return data, paired, input.Evidence(), nil
}
