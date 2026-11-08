package read

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"slices"
	"sync"
	"time"
)

const rsTechnicalBytes int64 = 8 << 20
const rsTechnicalRows = 4096

type RSSelectedTechnicalReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewRSSelectedTechnicalReader(s *governor.Snapshots) (*RSSelectedTechnicalReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &RSSelectedTechnicalReader{s, s}, nil
}

type RSSelectedTechnicalInput struct {
	View        BrowseInput
	Item, Asset int64
	Work        *RSTechnicalWork
}
type RSTechnicalFacts struct {
	Preparing                                           bool
	PartIndex                                           int64
	Edition                                             string
	StartSeconds                                        float64
	EndSeconds                                          *float64
	File                                                RSTechnicalAsset
	Path                                                *string
	StreamState, ChapterState, SubtitleState, DiscState string
	Streams                                             []RSTechnicalStream
	Chapters                                            []RSTechnicalChapter
	Subtitles                                           []RSTechnicalSubtitle
	Disc                                                *RSDiscFacts
}
type RSDiscFacts struct {
	Chapters map[int64]int

	Media      *RSTechnicalDisctitle
	MediaState string

	Kind, Packaging, Encryption string
	Titles                      []RSTechnicalDisctitle
}

// Work owns no native handle or independent grant. Close after consumption.
type RSTechnicalWork struct {
	mu                    sync.Mutex
	authority             *governor.Authority
	lease                 *governor.PreparationLease
	owner                 governor.OwnerID
	in                    RSSelectedTechnicalInput
	source                []any
	fence                 browseFence
	bound, handed, closed bool
	failure               error
	phase, index          int
	keys                  [][]any
	family                string
	bytes                 int64
	facts                 RSTechnicalFacts
}

func validRSTechnical(in RSSelectedTechnicalInput) bool {
	return in.Item > 0 && in.Asset > 0 && in.View.Kind >= 1 && in.View.Kind <= 13 && in.View.After == nil && in.View.Library > 0 && in.View.Limit >= 1 && in.View.Limit <= MaxPage && len(in.View.Database) > 0 && len(in.View.Database) <= 256 && len(in.View.TokenHash) > 0 && len(in.View.TokenHash) <= 256
}
func BeginRSTechnicalWork(ctx context.Context, a *governor.Authority, owner governor.OwnerID, key governor.JobKey, deadline time.Time, in RSSelectedTechnicalInput) (*RSTechnicalWork, *governor.Refusal, error) {
	if ctx == nil || a == nil || !validRSTechnical(in) || in.Work != nil {
		return nil, nil, ErrInput
	}
	d := a.BeginPreparation(ctx, governor.PreparationRequest{Key: key, Owner: owner, Deadline: deadline, ReservedBytes: rsTechnicalBytes + governor.PreparationMetadataBytes})
	if d.Refusal != nil {
		return nil, d.Refusal, governor.ErrBusy
	}
	return &RSTechnicalWork{authority: a, lease: d.Lease, owner: owner, in: in}, nil, nil
}
func (w *RSTechnicalWork) Close() error {
	if w == nil {
		return ErrInput
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	e := w.authority.CancelPreparation(w.lease)
	w.keys = nil
	w.source = nil
	w.facts = RSTechnicalFacts{}
	w.in = RSSelectedTechnicalInput{}
	return errors.Join(e, w.authority.RetirePreparation(w.lease))
}
func rsTechnicalInt(v any) *int64 {
	if v == nil {
		return nil
	}
	n := v.(int64)
	return &n
}
func rsTechnicalText(v any) *string {
	if v == nil {
		return nil
	}
	n := v.(string)
	return &n
}
func rsTechnicalFloat(v any) float64 {
	if n, ok := v.(int64); ok {
		return float64(n)
	}
	return v.(float64)
}
func rsTechnicalState(s []any, head, size, mtime int) string {
	if s[head] == nil {
		return "missing"
	}
	if s[size] != s[5] || s[mtime] != s[6] {
		return "stale"
	}
	return "ready"
}
func (r *RSSelectedTechnicalReader) Poll(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RSSelectedTechnicalInput) (RSTechnicalFacts, BrowseReadResult, error) {
	if r == nil || !validRSTechnical(in) || in.Work == nil {
		return RSTechnicalFacts{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RSTechnicalFacts, BrowseReadResult, error) {
		in.View.Now = at
		return r.poll(ctx, request, budget, output, in)
	})
}
func (r *RSSelectedTechnicalReader) poll(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RSSelectedTechnicalInput) (RSTechnicalFacts, BrowseReadResult, error) {
	w := in.Work
	w.mu.Lock()
	defer w.mu.Unlock()
	original := w.in
	original.View.Now = in.View.Now
	in.Work = nil
	if w.closed || w.handed || w.failure != nil || w.owner != request.Owner || output.Authority != w.authority || original != in {
		return RSTechnicalFacts{}, BrowseReadResult{}, ErrInput
	}
	if e := w.authority.CheckPreparation(w.lease); e != nil {
		return RSTechnicalFacts{}, BrowseReadResult{}, e
	}
	in.View.policy = readPolicyBinding{recipe: rsDetailSubjectPolicy, subject: in.Item}
	windows := &rsByteWindow{source: r.snapshots, preflightCalls: 16, goCopyBytes: output.Request.Need[governor.MemoryBytes] / 2}
	var current []any
	root := func(q browseQuery) ([]any, error) {
		if e := w.authority.CheckPreparation(w.lease); e != nil {
			return nil, e
		}
		lib, e := q("rs.technical.library", in.View.Library)
		if e != nil {
			return nil, e
		}
		if len(lib) != 1 {
			return nil, ErrDenied
		}
		rows, e := q("rs.technical.source", in.Item, in.Asset, in.View.Library, in.Asset, in.View.Library, lib[0][0])
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		observations, err := q("rs.technical.observations", rows[0][0])
		if err != nil {
			return nil, err
		}
		if len(observations) != 1 {
			return nil, ErrDenied
		}
		if w.bound && !equalSource(rows[0], w.source) {
			return nil, ErrStale
		}
		current = rows[0]
		return rows[0], nil
	}
	var selected []any
	var point string
	var args []any
	steps := []readStep[RSTechnicalFacts]{func(_ context.Context, q browseQuery, f browseFence, _ *RSTechnicalFacts) error {
		if w.bound {
			if f != w.fence {
				return ErrStale
			}
			return nil
		}
		s := current
		n := int64(8192 + len(in.View.Database) + len(in.View.TokenHash))
		for _, v := range s {
			switch x := v.(type) {
			case string:
				n += 4 * int64(len(x))
			case []byte:
				n += 4 * int64(len(x))
			}
		}
		if n > min(rsTechnicalBytes, output.Request.Need[governor.MemoryBytes]/4) {
			return governor.ErrSnapshotBudget
		}
		w.source = slices.Clone(s)
		w.fence = f
		w.bound = true
		w.bytes = n
		w.facts.PartIndex = s[39].(int64)
		w.facts.Edition = s[40].(string)
		w.facts.StartSeconds = rsTechnicalFloat(s[41])
		if s[42] != nil {
			x := rsTechnicalFloat(s[42])
			w.facts.EndSeconds = &x
		}
		w.facts.StreamState = rsTechnicalState(s, 11, 12, 13)
		w.facts.ChapterState = rsTechnicalState(s, 15, 16, 17)
		w.facts.SubtitleState = rsTechnicalState(s, 19, 20, 21)
		if w.facts.ChapterState == "ready" && s[18] != "known" {
			w.facts.ChapterState = "pending"
		}
		if w.facts.SubtitleState == "ready" && s[22] != "known" {
			w.facts.SubtitleState = "pending"
		}
		w.facts.DiscState = "missing"
		if s[23] != nil {
			w.facts.DiscState = "stale"
			if s[23] == s[2] && s[24] == s[4] && s[25] == s[3] && s[33] == "local" {
				w.facts.DiscState = "ready"
				w.facts.Disc = &RSDiscFacts{Kind: s[26].(string), Packaging: s[27].(string), Encryption: s[28].(string), Chapters: map[int64]int{}, MediaState: "missing"}
			}
		}
		if w.facts.Disc != nil && s[30] != "" {
			w.facts.Disc.MediaState = "stale"
			if w.facts.ChapterState == "pending" {
				w.facts.Disc.MediaState = "pending"
			}
		}
		return nil
	}, func(_ context.Context, q browseQuery, _ browseFence, _ *RSTechnicalFacts) error {
		// One bounded key inventory per public poll. Wide payloads follow on later polls.
		for w.index >= len(w.keys) {
			w.keys = nil
			w.index = 0
			w.phase++
			var id any = w.source[0]
			switch w.phase {
			case 1:
				w.family = "asset"
				w.keys = [][]any{{in.Asset}}
				return nil
			case 2:
				if w.fence.role != "owner" {
					continue
				}
				w.family = "path"
				w.keys = [][]any{{in.Asset}}
				return nil
			case 3:
				w.family = "stream"
				if w.facts.StreamState != "ready" {
					continue
				}
			case 4:
				w.family = "chapter"
				if w.facts.ChapterState != "ready" {
					continue
				}
			case 5:
				w.family = "subtitle"
				if w.facts.SubtitleState != "ready" {
					continue
				}
			case 6:
				w.family = "disctitle"
				if w.fence.role != "owner" || w.facts.DiscState != "ready" {
					continue
				}
				id = w.source[1]
			case 7:
				if w.facts.DiscState != "ready" || w.source[33] != "local" || w.source[28] != "none" || w.facts.StreamState != "ready" || w.facts.ChapterState != "ready" || w.source[31] != w.source[11] || w.source[32] != w.source[15] || w.source[30] == "" {
					continue
				}
				w.family = "media"
				w.keys = [][]any{{w.source[30]}}
				return nil

			default:
				return nil
			}
			if int64(rsTechnicalRows+1)*128 > min(rsTechnicalBytes-w.bytes, output.Request.Need[governor.MemoryBytes]/4-w.bytes) {
				return governor.ErrSnapshotBudget
			}
			rows, e := q("rs.technical."+w.family+"_keys", id, int64(rsTechnicalRows+1))
			if e != nil {
				return e
			}
			if len(rows) > rsTechnicalRows {
				return ErrUnqualified
			}
			n := int64(len(rows)) * 128
			if n > rsTechnicalBytes-w.bytes {
				return governor.ErrSnapshotBudget
			}
			w.bytes += n
			w.keys = rows
			return nil
		}
		return nil
	}, func(_ context.Context, q browseQuery, _ browseFence, _ *RSTechnicalFacts) error {
		if w.index >= len(w.keys) {
			return nil
		}
		point = "rs.technical." + w.family
		key := w.keys[w.index][0]
		switch w.family {
		case "asset", "path":
			args = []any{key, in.View.Library}
		case "subtitle":
			args = []any{key, w.source[0]}
		case "media", "disctitle":
			args = []any{w.source[1], key}
		default:
			args = []any{w.source[0], key}
		}
		sizes, e := q(point+"_octets", args...)
		if e != nil {
			return e
		}
		if len(sizes) != 1 {
			return ErrStale
		}
		n, e := rsPointBytes(sizes[0])
		if e != nil {
			return e
		}
		// Retained values, row metadata and handoff copies are funded BEFORE decode.
		if n > (rsTechnicalBytes-w.bytes-8192)/4 {
			return governor.ErrSnapshotBudget
		}
		n = n*4 + 8192
		scratch := int64(0)
		if w.family == "media" || w.family == "disctitle" {
			scratch = int64(rsTechnicalRows+1) * 128
		}
		if n+scratch > rsTechnicalBytes-w.bytes || w.bytes+n+scratch > output.Request.Need[governor.MemoryBytes]/4 {
			return governor.ErrSnapshotBudget
		}
		w.bytes += n
		return windows.arm(max(n+scratch, 1), 3)
	}, func(_ context.Context, q browseQuery, _ browseFence, p *RSTechnicalFacts) error {
		if point != "" {
			rows, e := q(point, args...)
			if e != nil {
				return e
			}
			if len(rows) != 1 {
				return ErrStale
			}
			selected = rows[0]
			if w.family == "media" || w.family == "disctitle" {
				v := rsTechnicalDecodeDisctitle(selected)
				chapters, err := q("rs.technical.discchapters", w.source[1], v.key, int64(rsTechnicalRows+1))
				if err != nil {
					return err
				}
				if len(chapters) > rsTechnicalRows {
					return ErrUnqualified
				}
				w.facts.Disc.Chapters[v.Number] = len(chapters)
			}
			switch w.family {
			case "asset":
				w.facts.File = rsTechnicalDecodeAsset(selected)
				if w.source[37] != w.source[2] {
					w.facts.File.SampleRate = nil
					w.facts.File.BitDepth = nil
					w.facts.File.Channels = nil
					w.facts.File.Lossless = nil
				}
			case "path":
				w.facts.Path = rsTechnicalText(selected[0])
			case "stream":
				v := rsTechnicalDecodeStream(selected)
				v.DetailState = "missing"
				raw := selected[len(selected)-1].(string)
				if raw != "" {
					v.DetailState = "stale"
					if w.source[14] == int64(assets.StreamDetailVersion) {
						var d struct {
							assets.StreamDetail
							HearingImpaired bool `json:"hearingImpaired"`
							Commentary      bool `json:"commentary"`
						}
						if json.Unmarshal([]byte(raw), &d) != nil {
							return ErrUnqualified
						}
						d.StreamBreadth = assets.StreamBreadth{}
						v.Detail = &d.StreamDetail
						v.HearingImpaired = &d.HearingImpaired
						v.Commentary = &d.Commentary
						v.DetailState = "ready"
					}
				}
				w.facts.Streams = append(w.facts.Streams, v)
			case "chapter":
				w.facts.Chapters = append(w.facts.Chapters, rsTechnicalDecodeChapter(selected))
			case "subtitle":
				w.facts.Subtitles = append(w.facts.Subtitles, rsTechnicalDecodeSubtitle(selected))
			case "media":
				x := rsTechnicalDecodeDisctitle(selected)
				w.facts.Disc.Media = &x
				w.facts.Disc.MediaState = "ready"
			case "disctitle":
				w.facts.Disc.Titles = append(w.facts.Disc.Titles, rsTechnicalDecodeDisctitle(selected))
			}
			w.index++
		}
		if w.phase <= 7 {
			p.Preparing = true
			return nil
		}
		if w.facts.Disc != nil {
			slices.SortFunc(w.facts.Disc.Titles, func(a, b RSTechnicalDisctitle) int {
				if x := cmp.Compare(a.Order, b.Order); x != 0 {
					return x
				}
				return cmp.Compare(a.Number, b.Number)
			})
		}
		*p = w.facts
		return nil
	}}
	facts, result, e := publishRead(ctx, windows, request, budget, output, in.View, steps, root)
	if e != nil {
		w.failure = e
		_ = w.authority.CancelPreparation(w.lease)
		return RSTechnicalFacts{}, result, e
	}
	if !facts.Preparing {
		e = w.authority.ReadyPreparation(w.lease)
		if e == nil {
			e = w.authority.HandoffPreparation(w.lease)
		}
		if e != nil {
			return RSTechnicalFacts{}, result, e
		}
		w.handed = true
	}
	return facts, result, nil
}
