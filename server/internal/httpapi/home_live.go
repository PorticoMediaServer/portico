package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"sort"
	"strings"
	"time"
)

type homeLiveCursor struct {
	Revision string `json:"revision"`
	Fence    string `json:"fence"`
	Group    string `json:"group"`
	SourceID string `json:"sourceId"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
	At       int64  `json:"at"`
	Until    int64  `json:"until"`
}

func (d Dependencies) decodeHomeLiveCursor(raw string) (homeLiveCursor, error) {
	var out homeLiveCursor
	payload, signature, ok := strings.Cut(raw, ".")
	if !ok || len(raw) > 4096 {
		return out, catalog.ErrCursor
	}
	data, e := base64.RawURLEncoding.DecodeString(payload)
	if e != nil {
		return out, catalog.ErrCursor
	}
	sig, e := base64.RawURLEncoding.DecodeString(signature)
	if e != nil {
		return out, catalog.ErrCursor
	}
	mac := hmac.New(sha256.New, d.LiveChannels.ContinuationKey("home-on-now"))
	mac.Write(data)
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(data, &out) != nil {
		return out, catalog.ErrCursor
	}
	return out, nil
}
func (d Dependencies) encodeHomeLiveCursor(value homeLiveCursor) string {
	raw, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, d.LiveChannels.ContinuationKey("home-on-now"))
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// homeOnNow uses Live TV's authority, directory and guide. Ordering precedes
// LIMIT; programme reads name only the selected <=50 channels, for one second.
func (d Dependencies) homeOnNow(r *http.Request, p identity.Principal, page catalog.HomeRowPage) (catalog.HomeRow, error) {
	out := catalog.HomeRow{}
	if page.AnchorID != "" {
		return out, catalog.ErrCursor
	}
	if d.LiveChannels == nil {
		return out, catalog.ErrHomeRowUnknown
	}
	if err := d.featureAllowed(r.Context(), p, "live_tv"); err != nil {
		return out, catalog.ErrHomeRowUnknown
	}
	restrictions, fence, err := d.viewerRestrictions(r, p)
	if err != nil {
		return out, err
	}
	limit := page.Limit
	if limit <= 0 {
		limit = catalog.HomeRowDefaultLimit
	}
	limit = min(limit, 50)
	now := time.Now().UTC().Truncate(time.Second)
	cursor := homeLiveCursor{At: now.Unix(), Until: now.Truncate(time.Minute).Add(time.Minute).Unix(), Limit: limit, Offset: page.Start, Group: page.Group, SourceID: page.SourceID}
	if page.Cursor != "" {
		if page.Start != 0 || page.AnchorID != "" {
			return out, catalog.ErrCursor
		}
		cursor, err = d.decodeHomeLiveCursor(page.Cursor)
		if err != nil {
			return out, err
		}
		if cursor.Limit != limit || cursor.Group != page.Group || cursor.SourceID != page.SourceID {
			return out, catalog.ErrCursor
		}
		if now.Unix() >= cursor.Until {
			return out, catalog.ErrStaleContinuation
		}
	}
	query := livechannels.DirectoryQuery{Kind: "all", Sort: "favorites", Limit: limit, Offset: cursor.Offset, Group: page.Group, SourceID: page.SourceID}
	directory, err := d.LiveChannels.ChannelDirectory(r.Context(), d.guideDirectoryAuthority(p, fence), query)
	if err != nil {
		return out, err
	}
	if page.Cursor != "" && (cursor.Revision != directory.Revision || cursor.Fence != directory.ViewerFence) {
		return out, catalog.ErrStaleContinuation
	}
	if page.Revision != "" && page.Revision != directory.Revision {
		return out, catalog.ErrStaleContinuation
	}
	if err = d.annotateViewerDirectory(r.Context(), p, directory.Channels); err != nil {
		return out, err
	}
	at := time.Unix(cursor.At, 0).UTC()
	byID := map[string]livechannels.Channel{}
	for _, kind := range []livechannels.Provenance{livechannels.LiveSource, livechannels.LibraryChannel} {
		ids := []string{}
		for _, channel := range directory.Channels {
			if channel.Provenance == kind {
				ids = append(ids, channel.ID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		q := livechannels.GuideQuery{Viewer: channelSubject(p), ChannelIDs: ids, Start: at, End: at.Add(time.Second), Timezone: "UTC", Kind: kind, Limit: 50, SourceID: page.SourceID}
		if restrictions.Active() {
			q.ProgrammeAllowed = func(rating string) bool { return catalog.RatingAllowed(restrictions, rating) }
		}
		var guide livechannels.Guide
		if kind == livechannels.LibraryChannel {
			if d.LibraryChannels == nil {
				return out, livechannels.ErrUnavailable
			}
			guide, err = d.LibraryChannels.Guide(r.Context(), d.libraryAuthority(p), q, d.LiveChannels.ContinuationKey("library-guide-v1"))
		} else {
			guide, err = d.LiveChannels.Guide(r.Context(), d.liveAuthority(p), q)
		}
		if err != nil {
			return out, err
		}
		for _, channel := range guide.Channels {
			byID[string(channel.Provenance)+":"+channel.SourceID+":"+channel.ID] = channel
		}
	}
	sources := map[string]livechannels.GuideSource{}
	for _, source := range directory.Sources {
		sources[string(source.Provenance)+":"+source.ID] = source
	}
	live := &catalog.HomeLive{Revision: directory.Revision, ObservedAt: at.Format(time.RFC3339), Sort: "favorites", Group: page.Group, Channels: []catalog.HomeChannel{}, Groups: []livechannels.GuideGroupCount{}}
	for _, channel := range directory.Channels {
		if guideChannel, ok := byID[string(channel.Provenance)+":"+channel.SourceID+":"+channel.ID]; ok {
			for _, programme := range guideChannel.Programmes {
				start, e1 := time.Parse(time.RFC3339, programme.Start)
				end, e2 := time.Parse(time.RFC3339, programme.End)
				if e1 == nil && e2 == nil && !start.After(at) && end.After(at) {
					channel.Programmes = []livechannels.Programme{programme}
					if end.Unix() < cursor.Until {
						cursor.Until = end.Unix()
					}
					break
				}
			}
		}
		live.Channels = append(live.Channels, catalog.HomeChannel{
			Channel: channel, Source: sources[string(channel.Provenance)+":"+channel.SourceID],
			Actions: catalog.HomeChannelActions{Play: channel.TuneAvailable && d.v1 != nil, Favorite: channel.Provenance == livechannels.LiveSource || channel.Provenance == livechannels.LibraryChannel && d.LibraryChannels != nil},
		})
	}

	summary, err := d.LiveChannels.ChannelSourceSummary(r.Context(), d.guideDirectoryAuthority(p, fence), false, now)
	if err != nil {
		return out, err
	}
	groups := map[string]int{}
	for _, source := range summary.Sources {
		if page.SourceID == "" || source.ID == page.SourceID {
			for _, group := range source.GroupCounts {
				groups[group.Name] += group.Count
			}
		}
	}
	for name, count := range groups {
		live.Groups = append(live.Groups, livechannels.GuideGroupCount{Name: name, Count: count})
	}
	sort.Slice(live.Groups, func(i, j int) bool { return live.Groups[i].Name < live.Groups[j].Name })
	// Fence guide generations/preferences/source policy changed during guide reads.
	query.Limit = 1
	query.Offset = 0
	query.Revision = directory.Revision
	if _, err = d.LiveChannels.ChannelDirectory(r.Context(), d.guideDirectoryAuthority(p, fence), query); err != nil {
		return out, catalog.ErrStaleContinuation
	}
	if err = d.featureAllowed(r.Context(), p, "live_tv"); err != nil {
		return out, catalog.ErrHomeRowUnknown
	}
	_, after, err := d.viewerRestrictions(r, p)
	if err != nil {
		return out, err
	}
	if fence != after || time.Now().Unix() >= cursor.Until {
		return out, catalog.ErrStaleContinuation
	}
	live.ExpiresAt = time.Unix(cursor.Until, 0).UTC().Format(time.RFC3339)
	out.Live = live
	total := directory.Total
	out.Total = &total
	out.Start = directory.Offset
	out.Limit = limit
	out.HasMore = out.Start+len(live.Channels) < total
	if out.HasMore {
		cursor.Offset = out.Start + len(live.Channels)
		cursor.Revision = directory.Revision
		cursor.Fence = directory.ViewerFence
		out.NextCursor = d.encodeHomeLiveCursor(cursor)
	}
	if len(directory.Sources) == 1 {
		out.SubtitleText = &catalog.ServerText{Code: "next.home.sourceName", Params: map[string]string{"source": directory.Sources[0].Name}, Fallback: directory.Sources[0].Name}
	}
	return out, nil
}
