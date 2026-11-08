package read

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// MG guide reads are inert: this package composes bounded GV snapshots only and
// has no writer, product registration or runtime cutover. One committed guide
// generation is read at its exact source head; imports, logos and staging work
// never gate the published view. Guide visibility requires the current viewer
// to see every configured library, so no fabricated service viewer, truncated
// library set or caller-supplied EOF is accepted.
const (
	mgGuideLibraryMax   = 1024
	mgGuideProgrammeMax = 1000
	mgGuideSpanMax      = 14 * 24 * time.Hour
)

const mgGuidePolicy readPolicyID = 32600

// mgGuidePurpose is a private compiled marker. It selects the guide's
// complete library/viewer obligation and cannot be supplied by a caller.
type mgGuidePurpose struct{ marker uint8 }

func (*mgGuidePurpose) readPolicyState() {}

var mgGuideReadPurpose = &mgGuidePurpose{marker: 1}

func init() {
	registerReadPolicy(readPolicyRecipe{
		id: mgGuidePolicy, domain: "mg-guide-viewer", kinds: []int64{1},
		stateful: true, scope: readPrincipalScope,
		validate: validateMGGuidePolicy,
	})
}

// validateMGGuidePolicy runs in BOTH the lookup and the fresh publication
// snapshot. It proves the current viewer may see every configured catalogue
// library; owner authority covers the whole server. The bounded witness joins
// the final fence, including the viewer's current rating restriction values.
func validateMGGuidePolicy(q browseQuery, in BrowseInput, f browseFence) (string, error) {
	if in.policy.state != mgGuideReadPurpose || in.policy.subject != 0 || f.library != "" || f.libraryID != 0 {
		return "", ErrInput
	}
	rows, err := q(mgGuideQuery("libraries"), "", int64(mgGuideLibraryMax+1))
	if err != nil {
		return "", err
	}
	if len(rows) > mgGuideLibraryMax {
		return "", ErrUnqualified
	}
	var allowed, profileAllowed []string
	if err = json.Unmarshal([]byte(f.memberLibraries), &allowed); err != nil || allowed == nil {
		return "", ErrUnqualified
	}
	if f.profileRestricted {
		if err = json.Unmarshal([]byte(f.profileLibraries), &profileAllowed); err != nil || profileAllowed == nil {
			return "", ErrUnqualified
		}
	}
	contains := func(values []string, want string) bool {
		for _, v := range values {
			if v == want {
				return true
			}
		}
		return false
	}
	active := 0
	for _, row := range rows {
		if len(row) != 2 {
			return "", ErrUnqualified
		}
		id, ok := row[0].(string)
		if !ok || id == "" || !utf8.ValidString(id) {
			return "", ErrUnqualified
		}
		retired, ok := row[1].(int64)
		if !ok || retired != 0 && retired != 1 {
			return "", ErrUnqualified
		}
		if retired != 0 {
			continue
		}
		active++
		if f.role != "owner" && !contains(allowed, id) {
			return "", ErrDenied
		}
		if f.profileRestricted && !contains(profileAllowed, id) {
			return "", ErrDenied
		}
	}
	return fmt.Sprintf("mg-guide-v1:libraries=%d:max=%d:unrated=%d:restriction=%d", active, f.maximumAge, f.allowUnrated, f.restrictionRevision), nil
}

// MGGuideInput selects one source's committed generation over a whole-second
// half-open interval [Start,End). Library and kind are the reader's private
// principal anchor, never caller choices. After continues the channel keyset
// only at the exact fence and generation that issued it.
type MGGuideInput struct {
	View       BrowseInput
	Source     string
	Start, End string
	After      *MGGuideCursor
}

// MGGuideCursor is an opaque channel keyset position. Its private fields stop
// callers fabricating a different viewer's or generation's anchor.
type MGGuideCursor struct {
	fence              browseFence
	source, generation string
	position           int64
}

type MGGuideSource struct {
	ID, Generation string
	Revision       int64
}

type MGGuideChannel struct {
	Position   int64
	ID, Name   string
	Number     string
	Group      string
	LogoURL    string
	HasLogo    bool
	Programmes []MGGuideProgramme
}

// MGGuideProgramme is one complete stored guide row. Metadata facts are
// returned verbatim because playback, not guide visibility, is admitted per
// viewer; the final publication fence still pins the viewer's rating
// restriction revision and values.
type MGGuideProgramme struct {
	ID, ChannelID                    string
	Title, Start, End                string
	SeriesID, EpisodeID, NewEvidence string
	Description, Facts, IconURL      string
	HasMetadata, HasIcon             bool
}

// MGGuidePage is complete for its requested channel page and interval, or the
// read is refused. Exhausted proves no further configured channel row remains.
type MGGuidePage struct {
	Source     MGGuideSource
	Channels   []MGGuideChannel
	Exhausted  bool
	Next       *MGGuideCursor
	Programmes int
}

type MGGuideReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewMGGuideReader(s *governor.Snapshots) (*MGGuideReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &MGGuideReader{snapshots: s, pool: s}, nil
}

// Channels is the complete inert authorized guide reader. Every denial, head
// drift, capacity overflow, cancellation or admission error returns ZERO page.
func (r *MGGuideReader) Channels(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MGGuideInput) (MGGuidePage, BrowseReadResult, error) {
	if r == nil {
		return MGGuidePage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (MGGuidePage, BrowseReadResult, error) {
		in.View.Now = at
		return r.page(ctx, request, budget, output, in)
	})
}

func (r *MGGuideReader) page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MGGuideInput) (MGGuidePage, BrowseReadResult, error) {
	start, end, err := mgGuideBounds(in.Start, in.End)
	if err != nil || !mgGuideName(in.Source, 128) || in.After != nil && !validMGGuideCursor(*in.After) || in.After != nil && in.After.source != in.Source {
		return MGGuidePage{}, BrowseReadResult{}, ErrInput
	}
	view := in.View
	view.Library, view.Kind = 0, 1
	view.After, view.Descending, view.KeepArticles = nil, false, false
	view.policy = readPolicyBinding{recipe: mgGuidePolicy, state: mgGuideReadPurpose}
	var head []any
	root := func(q browseQuery) ([]any, error) {
		rows, err := q(mgGuideQuery("source"), in.Source)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		head = rows[0]
		return head, nil
	}
	return publishRead(ctx, &mgGuideWindows{source: r.snapshots}, request, budget, output, view, []readStep[MGGuidePage]{
		func(_ context.Context, q browseQuery, f browseFence, page *MGGuidePage) error {
			return readMGGuidePage(q, f, in, start, end, head, page)
		},
	}, root)
}

func readMGGuidePage(q browseQuery, f browseFence, in MGGuideInput, start, end string, head []any, page *MGGuidePage) error {
	if len(head) != 5 {
		return ErrUnqualified
	}
	revision, err := mgGuideInt(head[0])
	if err != nil {
		return err
	}
	state, err := mgGuideString(head[1], 32)
	if err != nil {
		return err
	}
	generation, err := mgGuideString(head[2], 128)
	if err != nil {
		return err
	}
	access, err := mgGuideString(head[3], 32)
	if err != nil {
		return err
	}
	removals, err := mgGuideInt(head[4])
	if err != nil {
		return err
	}
	// A retired or removing source is never presented as a readable guide.
	if state != "active" || removals != 0 {
		return ErrDenied
	}
	switch access {
	case "server-members":
		if f.role != "owner" && f.role != "admin" && f.role != "member" {
			return ErrDenied
		}
	case "owner-only":
		if f.role != "owner" {
			return ErrDenied
		}
	default:
		return ErrUnqualified
	}
	if in.After != nil && (in.After.fence != f || in.After.generation != generation) {
		return ErrStale
	}
	after := int64(-1)
	if in.After != nil {
		after = in.After.position
	}
	rows, err := q(mgGuideQuery("channels"), generation, after, int64(in.View.Limit+1))
	if err != nil {
		return err
	}
	truncated := len(rows) > in.View.Limit
	if truncated {
		rows = rows[:in.View.Limit]
	}
	page.Source = MGGuideSource{ID: in.Source, Generation: generation, Revision: revision}
	page.Channels = make([]MGGuideChannel, 0, len(rows))
	total := 0
	last := after
	for _, row := range rows {
		if len(row) != 2 {
			return ErrUnqualified
		}
		position, err := mgGuideInt(row[0])
		if err != nil || position <= last {
			return ErrUnqualified
		}
		id, err := mgGuideString(row[1], 128)
		if err != nil || id == "" {
			return ErrUnqualified
		}
		value, err := q(mgGuideQuery("channel"), generation, id)
		if err != nil {
			return err
		}
		if len(value) != 1 {
			return ErrStale
		}
		channel, err := mgGuideChannelValue(value[0], position, generation, id)
		if err != nil {
			return err
		}
		remaining := mgGuideProgrammeMax - total
		if remaining <= 0 {
			return ErrUnqualified
		}
		channel.Programmes, err = mgGuideProgrammes(q, generation, id, start, end, remaining)
		if err != nil {
			return err
		}
		total += len(channel.Programmes)
		page.Channels = append(page.Channels, channel)
		last = position
	}
	if truncated {
		page.Next = &MGGuideCursor{fence: f, source: in.Source, generation: generation, position: last}
	}
	page.Exhausted = !truncated
	page.Programmes = total
	return nil
}

func mgGuideChannelValue(row []any, position int64, generation, id string) (MGGuideChannel, error) {
	if len(row) != 7 {
		return MGGuideChannel{}, ErrUnqualified
	}
	channelID, err := mgGuideString(row[0], 128)
	if err != nil || channelID != id {
		return MGGuideChannel{}, ErrStale
	}
	name, err := mgGuideText(row[1], 0)
	if err != nil {
		return MGGuideChannel{}, err
	}
	number, err := mgGuideText(row[2], 64)
	if err != nil {
		return MGGuideChannel{}, err
	}
	group, err := mgGuideText(row[3], 0)
	if err != nil {
		return MGGuideChannel{}, err
	}
	got, err := mgGuideInt(row[4])
	if err != nil || got != position {
		return MGGuideChannel{}, ErrStale
	}
	hasLogo, err := mgGuideInt(row[5])
	if err != nil || hasLogo != 0 && hasLogo != 1 {
		return MGGuideChannel{}, ErrUnqualified
	}
	logo, err := mgGuideNullableText(row[6], 0)
	if err != nil || hasLogo == 0 && logo != "" {
		return MGGuideChannel{}, ErrUnqualified
	}
	return MGGuideChannel{Position: position, ID: id, Name: name, Number: number, Group: group, LogoURL: logo, HasLogo: hasLogo == 1}, nil
}

func mgGuideProgrammes(q browseQuery, generation, channel, start, end string, remaining int) ([]MGGuideProgramme, error) {
	if remaining <= 0 || remaining > mgGuideProgrammeMax {
		return nil, ErrUnqualified
	}
	rows, err := q(mgGuideQuery("programmes"), generation, channel, end, start, int64(remaining+1))
	if err != nil {
		return nil, err
	}
	// max+1 proves the interval is complete; a truncated interval is refused,
	// never returned as a short guide page.
	if len(rows) > remaining {
		return nil, ErrUnqualified
	}
	out := make([]MGGuideProgramme, 0, len(rows))
	for _, row := range rows {
		if len(row) != 3 {
			return nil, ErrUnqualified
		}
		id, err := mgGuideString(row[0], 128)
		if err != nil || id == "" {
			return nil, ErrUnqualified
		}
		idStart, err := mgGuideString(row[1], 64)
		if err != nil {
			return nil, err
		}
		idEnd, err := mgGuideString(row[2], 64)
		if err != nil {
			return nil, err
		}
		value, err := q(mgGuideQuery("programme"), generation, id)
		if err != nil {
			return nil, err
		}
		if len(value) != 1 {
			return nil, ErrStale
		}
		programme, err := mgGuideProgrammeValue(value[0], generation, channel, id, idStart, idEnd)
		if err != nil {
			return nil, err
		}
		out = append(out, programme)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		if out[i].End != out[j].End {
			return out[i].End < out[j].End
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func mgGuideProgrammeValue(row []any, generation, channel, id, idStart, idEnd string) (MGGuideProgramme, error) {
	if len(row) != 13 {
		return MGGuideProgramme{}, ErrUnqualified
	}
	gotID, err := mgGuideString(row[0], 128)
	if err != nil || gotID != id {
		return MGGuideProgramme{}, ErrStale
	}
	gotChannel, err := mgGuideString(row[1], 128)
	if err != nil || gotChannel != channel {
		return MGGuideProgramme{}, ErrStale
	}
	title, err := mgGuideText(row[2], 0)
	if err != nil {
		return MGGuideProgramme{}, err
	}
	start, err := mgGuideString(row[3], 64)
	if err != nil || start != idStart {
		return MGGuideProgramme{}, ErrStale
	}
	end, err := mgGuideString(row[4], 64)
	if err != nil || end != idEnd {
		return MGGuideProgramme{}, ErrStale
	}
	hasMetadata, err := mgGuideInt(row[5])
	if err != nil || hasMetadata != 0 && hasMetadata != 1 {
		return MGGuideProgramme{}, ErrUnqualified
	}
	series, err := mgGuideNullableText(row[6], 0)
	if err != nil {
		return MGGuideProgramme{}, err
	}
	episode, err := mgGuideNullableText(row[7], 0)
	if err != nil {
		return MGGuideProgramme{}, err
	}
	evidence, err := mgGuideNullableText(row[8], 0)
	if err != nil {
		return MGGuideProgramme{}, err
	}
	description, err := mgGuideNullableText(row[9], 0)
	if err != nil {
		return MGGuideProgramme{}, err
	}
	facts, err := mgGuideNullableText(row[10], 0)
	if err != nil {
		return MGGuideProgramme{}, err
	}
	if facts != "" && !json.Valid([]byte(facts)) {
		return MGGuideProgramme{}, ErrUnqualified
	}
	hasIcon, err := mgGuideInt(row[11])
	if err != nil || hasIcon != 0 && hasIcon != 1 {
		return MGGuideProgramme{}, ErrUnqualified
	}
	icon, err := mgGuideNullableText(row[12], 0)
	if err != nil || hasIcon == 0 && icon != "" {
		return MGGuideProgramme{}, ErrUnqualified
	}
	return MGGuideProgramme{
		ID: id, ChannelID: channel, Title: title, Start: start, End: end,
		SeriesID: series, EpisodeID: episode, NewEvidence: evidence,
		Description: description, Facts: facts, IconURL: icon,
		HasMetadata: hasMetadata == 1, HasIcon: hasIcon == 1,
	}, nil
}

func mgGuideBounds(start, end string) (string, string, error) {
	first, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return "", "", ErrInput
	}
	last, err := time.Parse(time.RFC3339Nano, end)
	if err != nil {
		return "", "", ErrInput
	}
	first, last = first.UTC(), last.UTC()
	if !first.Before(last) || last.Sub(first) > mgGuideSpanMax || first.Nanosecond() != 0 || last.Nanosecond() != 0 {
		return "", "", ErrInput
	}
	// Stored programme instants are UTC whole seconds from the import boundary;
	// normalized bounds keep the interval a native indexed text range.
	return first.Format(time.RFC3339), last.Format(time.RFC3339), nil
}

func mgGuideName(s string, max int) bool {
	return s != "" && (max == 0 || len(s) <= max) && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}

func mgGuideNullableText(v any, max int) (string, error) {
	if v == nil {
		return "", nil
	}
	return mgGuideText(v, max)
}

func mgGuideText(v any, max int) (string, error) {
	s, ok := v.(string)
	if !ok || !utf8.ValidString(s) || max > 0 && len(s) > max {
		return "", ErrUnqualified
	}
	return s, nil
}

func mgGuideString(v any, max int) (string, error) {
	s, ok := v.(string)
	if !ok || !utf8.ValidString(s) || max > 0 && len(s) > max || strings.ContainsAny(s, "\x00\r\n") {
		return "", ErrUnqualified
	}
	return s, nil
}

func mgGuideInt(v any) (int64, error) {
	n, ok := v.(int64)
	if !ok {
		return 0, ErrUnqualified
	}
	return n, nil
}

func validMGGuideCursor(c MGGuideCursor) bool {
	return c.fence.database != "" && c.fence.token != "" && c.source != "" && c.generation != "" && c.position >= -1
}

// mgGuideWindows raises only the finite statement count of one guide window.
// Native bytes stay the pool's whole-row reservation and every wide value still
// requires its exact same-snapshot octet preflight before payload hydration.
type mgGuideWindows struct{ source browseSnapshots }

func (w *mgGuideWindows) NativeRowReservation() int64 { return w.source.NativeRowReservation() }

type mgGuideForecastSource interface {
	browseSnapshots
	ReadForecasted(context.Context, governor.Request, time.Duration, governor.SnapshotForecast, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
}

func (w *mgGuideWindows) Read(ctx context.Context, request governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	source, ok := w.source.(mgGuideForecastSource)
	if !ok {
		return w.source.Read(ctx, request, budget, read)
	}
	calls := request.Budget.RawRows
	if calls == 0 {
		return governor.SnapshotResult{}, governor.ErrSnapshotBudget
	}
	if calls > 4096 {
		calls = 4096
	}
	return source.ReadForecasted(ctx, request, budget, governor.SnapshotForecast{
		NativeRowBytes: w.source.NativeRowReservation(),
		MaxQueries:     uint16(calls),
	}, read)
}
