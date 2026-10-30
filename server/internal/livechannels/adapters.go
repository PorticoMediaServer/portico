package livechannels

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// ReadSource bounds compressed and expanded bytes independently. The exact same
// pinned transport is used later for media and every nested manifest request.
func ReadSource(ctx context.Context, locator string, policy remotemedia.Policy, limit int64) ([]byte, error) {
	if limit < 1 || limit > 1<<30 {
		return nil, ErrInvalid
	}
	c, e := remotemedia.New(ctx, locator, policy)
	if e != nil {
		return nil, sourceFetchError(e)
	}
	defer c.Close()
	resp, e := c.Open(ctx, "GET", "")
	if e != nil {
		return nil, sourceFetchError(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength > limit {
		return nil, ErrSourceUnavailable
	}
	compressed := &io.LimitedReader{R: resp.Body, N: limit + 1}
	sniff := bufio.NewReader(compressed)
	prefix, _ := sniff.Peek(2)
	var reader io.Reader = sniff
	encoding := strings.TrimSpace(strings.ToLower(resp.Header.Get("Content-Encoding")))
	switch encoding {
	case "", "identity", "gzip":
		if encoding == "gzip" || len(prefix) == 2 && prefix[0] == 0x1f && prefix[1] == 0x8b {
			g, e := gzip.NewReader(sniff)
			if e != nil {
				return nil, ErrSourceUnavailable
			}
			defer g.Close()
			reader = g
		}
	default:
		return nil, ErrSourceUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(reader, limit+1))
	if e != nil || int64(len(raw)) > limit || compressed.N <= 0 {
		return nil, ErrSourceUnavailable
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return raw, nil
}

func (f SourceFetcher) fetch(ctx context.Context, c remoteConfig) (SourceInput, int, error) {
	d := c.Draft
	in := SourceInput{ID: d.ID, ExpectedRevision: d.ExpectedRevision, Name: d.Name, Mappings: append([]ChannelMapping{}, d.Mappings...), EnabledGroups: d.EnabledGroups, EnabledChannels: d.EnabledChannels}
	limits := f.defaults.ImportLimits.effective()
	p := remotemedia.Policy{UserAgent: f.defaults.UserAgent, Approvals: c.Approvals, Resolver: f.Resolver, ReadTimeout: 15 * time.Second}
	physical := 0
	switch d.Kind {
	case "m3u":
		b, e := ReadSource(ctx, d.Locator, p, int64(limits.Bytes))
		if e != nil {
			return in, 0, e
		}
		in.Playlist = string(b)
	case "xtream":
		base, e := url.Parse(d.Locator)
		if e != nil || base.RawQuery != "" || base.User != nil {
			return in, 0, ErrInvalid
		}
		api := *base
		api.Path = strings.TrimSuffix(api.Path, "/") + "/player_api.php"
		q := url.Values{"username": {d.Username}, "password": {d.Password}, "action": {"get_live_streams"}}
		api.RawQuery = q.Encode()
		b, e := ReadSource(ctx, api.String(), p, int64(limits.Bytes))
		if e != nil {
			return in, 0, e
		}
		var streams []struct {
			ID       json.Number     `json:"stream_id"`
			Name     string          `json:"name"`
			Number   json.Number     `json:"num"`
			EPG      string          `json:"epg_channel_id"`
			Category json.RawMessage `json:"category_id"`
		}
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.UseNumber()
		if dec.Decode(&streams) != nil || len(streams) == 0 || len(streams) > 100000 {
			return in, 0, ErrInvalid
		}
		var text strings.Builder
		text.WriteString("#EXTM3U\n")
		for _, s := range streams {
			sid, e := s.ID.Int64()
			if e != nil || sid <= 0 {
				text.WriteString("#EXTINF:-1,Skipped\nfile:///skipped\n")
				continue
			}
			key := "xtream-" + strconv.FormatInt(sid, 10)
			stream := *base
			stream.RawPath = ""
			stream.Path = strings.TrimSuffix(stream.Path, "/") + "/live/" + d.Username + "/" + d.Password + "/" + strconv.FormatInt(sid, 10) + ".ts"
			// Path components must not change route structure; URL's RawPath preserves
			// reserved characters in credentials without exposing them to consumers.
			stream.RawPath = strings.TrimSuffix(base.EscapedPath(), "/") + "/live/" + url.PathEscape(d.Username) + "/" + url.PathEscape(d.Password) + "/" + strconv.FormatInt(sid, 10) + ".ts"
			s.Name = strings.ReplaceAll(s.Name, "\"", "'")
			if !validText(s.Name, 256) {
				text.WriteString("#EXTINF:-1,Skipped\nfile:///skipped\n")
				continue
			}
			fmt.Fprintf(&text, "#EXTINF:-1 tvg-id=\"%s\" tvg-chno=\"%s\",%s\n%s\n", key, s.Number.String(), s.Name, stream.String())
			if !hasMapping(in.Mappings, key) && s.EPG != "" {
				in.Mappings = append(in.Mappings, ChannelMapping{key, s.EPG})
			}
		}
		in.Playlist = text.String()
		if d.GuideURL == "" {
			guide := *base
			guide.Path = strings.TrimSuffix(guide.Path, "/") + "/xmltv.php"
			q.Del("action")
			guide.RawQuery = q.Encode()
			d.GuideURL = guide.String()
		}
	case "hdhomerun":
		base, e := url.Parse(d.Locator)
		if e != nil || base.RawQuery != "" || base.User != nil {
			return in, 0, ErrInvalid
		}
		discover := *base
		discover.Path = strings.TrimSuffix(base.Path, "/") + "/discover.json"
		b, e := ReadSource(ctx, discover.String(), p, 64<<10)
		if e != nil {
			return in, 0, e
		}
		var info struct {
			TunerCount int `json:"TunerCount"`
		}
		if json.Unmarshal(b, &info) != nil || info.TunerCount < 0 || info.TunerCount > 256 {
			return in, 0, ErrInvalid
		}
		physical = info.TunerCount
		lineup := *base
		lineup.Path = strings.TrimSuffix(base.Path, "/") + "/lineup.json"
		b, e = ReadSource(ctx, lineup.String(), p, int64(limits.Bytes))
		if e != nil {
			return in, 0, e
		}
		var rows []struct {
			Number string `json:"GuideNumber"`
			Name   string `json:"GuideName"`
			URL    string `json:"URL"`
			DRM    int    `json:"DRM"`
		}
		if json.Unmarshal(b, &rows) != nil || len(rows) == 0 || len(rows) > 100000 {
			return in, 0, ErrInvalid
		}
		var text strings.Builder
		text.WriteString("#EXTM3U\n")
		for _, r := range rows {
			if r.DRM != 0 {
				continue
			}
			if !validText(r.Number, 32) || !validPlaylistText(r.Number) || !validText(r.Name, 256) || !validPlaylistText(r.Name) || !validLocator(r.URL) {
				continue
			}
			// A lineup URL is not trusted merely because the device returned it. Media
			// access later passes the same policy and exact confirmed LAN scope again.
			fmt.Fprintf(&text, "#EXTINF:-1 tvg-id=\"hdhr-%s\" tvg-chno=\"%s\",%s\n%s\n", r.Number, r.Number, r.Name, r.URL)
		}
		in.Playlist = text.String()
	default:
		return in, 0, ErrInvalid
	}
	if d.GuideURL != "" {
		remaining := int64(limits.Bytes - len(in.Playlist))
		if remaining <= 0 {
			return in, 0, ErrInvalid
		}
		if guideBytes := int64(f.guideLimits.Bytes); guideBytes > 0 && guideBytes < remaining {
			remaining = guideBytes
		}
		guidePolicy := p
		if f.guideUserAgent != "" {
			guidePolicy.UserAgent = f.guideUserAgent
		}
		file, digest, e := readGuideFile(ctx, d.GuideURL, guidePolicy, remaining)
		in.guideFile, in.guideDigest = file, digest
		if e != nil {
			return in, 0, e
		}
	}
	return in, physical, nil
}
func validPlaylistText(s string) bool { return !strings.ContainsAny(s, "\"\r\n\x00") }
func hasMapping(ms []ChannelMapping, key string) bool {
	for _, m := range ms {
		if m.ChannelKey == key {
			return true
		}
	}
	return false
}

// Input is server-private. Callers must acquire a current source allocation and
// authorize the viewer/job before requesting it. No HTTP DTO embeds this type.
type Input struct {
	StreamBufferSeconds, RetryWindowSeconds int
	Locator                                 string
	Policy                                  remotemedia.Policy
	SourceID, ChannelID, Generation         string
	Revision                                int64
}

// Fence identifies what a producer reading this input depends on: the
// channel's stream address and the network access the owner approved for the
// source, plus the actual admitted acquisition buffering/retry behavior.
// The guide generation and the source revision are deliberately not
// part of it, so republishing a line-up whose stream did not change yields the
// same fence, and a changed address or approval yields a different one.
func (in Input) Fence() string {
	raw, _ := json.Marshal(struct {
		Locator             string                 `json:"locator"`
		Approvals           []remotemedia.Approval `json:"approvals"`
		UserAgent           string                 `json:"userAgent"`
		StreamBufferSeconds int                    `json:"streamBufferSeconds"`
		RetryWindowSeconds  int                    `json:"retryWindowSeconds"`
	}{in.Locator, in.Policy.Approvals, in.Policy.UserAgent, in.StreamBufferSeconds, in.RetryWindowSeconds})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:16])
}

func (s *Store) InputTx(ctx context.Context, tx *sql.Tx, sourceID, channelID, generation string) (Input, error) {
	var in Input
	var sealed []byte
	var active string
	e := tx.QueryRowContext(ctx, `SELECT s.revision,s.active_generation,c.locator_sealed FROM live_sources s JOIN live_channel_versions c ON c.generation_id=s.active_generation WHERE s.id=? AND c.channel_id=? AND s.state='active' AND NOT EXISTS(SELECT 1 FROM admin_channel_numbers n WHERE n.source_id=s.id AND n.channel_id=c.channel_id AND n.hidden=1)`, sourceID, channelID).Scan(&in.Revision, &active, &sealed)
	if e != nil || active != generation {
		return in, ErrConflict
	}
	// AAD is exactly the binding used by the existing source store.
	in.Locator, e = s.unseal(sealed, channelID+":"+active)
	if e != nil {
		return in, e
	}
	var remote []byte
	e = tx.QueryRowContext(ctx, `SELECT sealed FROM live_remote_configs WHERE source_id=?`, sourceID).Scan(&remote)
	if e == nil {
		raw, err := s.unseal(remote, "remote:"+sourceID)
		if err != nil {
			return in, err
		}
		var c remoteConfig
		if json.Unmarshal([]byte(raw), &c) != nil {
			return in, ErrUnavailable
		}
		in.Policy.Approvals = c.Approvals
	} else if e != sql.ErrNoRows {
		return in, ErrUnavailable
	}
	in.SourceID, in.ChannelID, in.Generation = sourceID, channelID, generation
	d, _, e := RuntimeConfigurationTx(ctx, tx, sourceID)
	if e != nil {
		return in, e
	}
	in.StreamBufferSeconds, in.RetryWindowSeconds = d.StreamBufferMs/1000, d.RetryWindowMs/1000
	in.Policy.UserAgent = d.UserAgent
	in.Policy.ReadTimeout = 15 * time.Second
	return in, nil
}
