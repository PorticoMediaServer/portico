package read

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Bounded preparation choices. 4096 is a per-family key cap, never a truncated
// exact answer: one extra key is read and refused explicitly. Bytes are charged
// to the caller's preparation lease; this family invents no second ledger.
const (
	rsTechnicalRows     = 4096
	rsTechnicalKeyBytes = 128
)

// RSSelectedTechnicalReader composes the selected timed file's accepted
// technical facts. It is inert: no route, product wiring, probe, filesystem
// opening, playback grant or media-worker (MP group F ready-source) fact is
// created here. Selected disc media is display metadata from the current scan
// publication, never an alternate-title playback authority.
type RSSelectedTechnicalReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewRSSelectedTechnicalReader(s *governor.Snapshots) (*RSSelectedTechnicalReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &RSSelectedTechnicalReader{snapshots: s, pool: s}, nil
}

type RSSelectedTechnicalInput struct {
	View        BrowseInput
	Item, Asset int64
	Work        *RSTechnicalWork
}

// RSTechnicalFacts is the complete selected technical answer. The four basic
// fact heads (stream/chapter/subtitle/disc) and the versioned stream detail each
// report their own missing/stale/pending/ready state; a pending or stale head
// never borrows an older row and never publishes partial facts.
type RSTechnicalFacts struct {
	Preparing    bool
	PartIndex    int64
	Edition      string
	StartSeconds float64
	EndSeconds   *float64
	File         RSTechnicalAsset
	Path         *string // owner/member role "owner" only

	StreamState, ChapterState, SubtitleState, DiscState, MediaState string
	Streams                                                         []RSTechnicalStream
	Chapters                                                        []RSTechnicalChapter
	Subtitles                                                       []RSTechnicalSubtitle

	Disc *RSDiscFacts // owner/member role "owner" only
}

type RSDiscFacts struct {
	Chapters map[int64]int

	// Media is the current scan's selected display title, present only when
	// RSTechnicalFacts.MediaState is ready. It is never a playback grant.
	Media *RSTechnicalDisctitle

	Kind, Packaging, Encryption string
	Titles                      []RSTechnicalDisctitle
}

// RSTechnicalWork is one owner/query preparation. Polls attach to this same
// object and cannot renew its absolute deadline or replace its input. It keeps
// no native SQL handle, consumer reference or independent grant; Close drops all
// private values and physically retires the exact governor reservation.
type RSTechnicalWork struct {
	mu        sync.Mutex
	authority *governor.Authority
	lease     *governor.PreparationLease
	owner     governor.OwnerID
	in        RSSelectedTechnicalInput
	source    []any
	fence     browseFence
	bound     bool
	handed    bool
	closed    bool
	ownerRole bool
	failure   error
	phase     int
	index     int
	keys      [][]any
	family    string
	used      int64
	facts     RSTechnicalFacts
}

func validRSTechnical(in RSSelectedTechnicalInput) bool {
	v := in.View
	// Reading 20/21 is a separate accepted-publication family; it stays closed
	// here rather than sharing timed-media technical facts.
	return in.Item > 0 && in.Asset > 0 && v.Kind >= 1 && v.Kind <= 13 &&
		v.After == nil && !v.Descending && !v.KeepArticles && v.Library > 0 && v.Now != (time.Time{}) &&
		v.Limit >= 1 && v.Limit <= MaxPage && len(v.Database) > 0 && len(v.Database) <= 256 &&
		len(v.TokenHash) > 0 && len(v.TokenHash) <= 256 &&
		v.policy.recipe == 0 && v.policy.subject == 0 && v.policy.state == nil && !v.policy.nativeSearch
}

// BeginRSTechnicalWork admits only the existing F-04 memory/publication lease.
// ctx is the original server-owned job lifetime, not a poll lifetime. The key is
// the trusted composition's full canonical job identity; capacity is the exact
// caller reservation and refusal is explicit.
func BeginRSTechnicalWork(ctx context.Context, a *governor.Authority, owner governor.OwnerID, key governor.JobKey, deadline time.Time, reservedBytes int64, in RSSelectedTechnicalInput) (*RSTechnicalWork, *governor.Refusal, error) {
	if ctx == nil || a == nil || owner == 0 || key.ID == "" || len(key.ID) > 1024 ||
		reservedBytes < governor.PreparationMetadataBytes || !validRSTechnical(in) || in.Work != nil {
		return nil, nil, ErrInput
	}
	d := a.BeginPreparation(ctx, governor.PreparationRequest{Key: key, Owner: owner, Deadline: deadline, ReservedBytes: reservedBytes})
	if d.Refusal != nil {
		return nil, d.Refusal, governor.ErrBusy
	}
	w := &RSTechnicalWork{authority: a, lease: d.Lease, owner: owner, in: in}
	if e := w.charge(1024 + 4*int64(len(in.View.Database)+len(in.View.TokenHash))); e != nil {
		_ = a.RetirePreparation(d.Lease)
		return nil, nil, e
	}
	return w, nil, nil
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
	w.keys = nil
	w.source = nil
	w.facts = RSTechnicalFacts{}
	w.in = RSSelectedTechnicalInput{}
	return w.authority.RetirePreparation(w.lease)
}

// charge funds every retained key, payload and handoff copy BEFORE it grows.
// PayloadBytes is the exact remaining job payload; there is no hidden cache.
func (w *RSTechnicalWork) charge(n int64) error {
	if n < 0 || n > w.lease.PayloadBytes()-w.used {
		return governor.ErrSnapshotBudget
	}
	w.used += n
	return nil
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

// rsTechnicalHead classifies one basic fact head. status is nil where the head
// has no status column. A head whose stored size/mtime is not the exact accepted
// asset frame is stale, not an older ready answer.
func rsTechnicalHead(s []any, revision, size, mtime int, status any) string {
	if s[revision] == nil {
		return "missing"
	}
	if s[size] != s[5] || s[mtime] != s[6] {
		return "stale"
	}
	if status != nil && status != "known" {
		return "pending"
	}
	return "ready"
}

// rsTechnicalMediaState names the current selected disc media display state. It
// needs the scan's own selected title key and matching ready stream/chapter head
// revisions; it does not consult, infer or replace MP's ready-source ABI.
func rsTechnicalMediaState(s []any, stream, chapter string) string {
	if s[30] == "" || s[28] != "none" {
		return "missing"
	}
	if stream != "ready" || chapter != "ready" || s[31] != s[11] || s[32] != s[15] {
		return "pending"
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
	if w.closed || w.handed || w.owner != request.Owner || output.Authority != w.authority || original != in {
		return RSTechnicalFacts{}, BrowseReadResult{}, ErrInput
	}
	// A failed preparation stays failed with the same error class; a later poll
	// of this job must not reclassify it as bad input.
	if w.failure != nil {
		return RSTechnicalFacts{}, BrowseReadResult{}, w.failure
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
		// Two current observations are ambiguous: this family will not choose
		// one source/probe to stand in for the other.
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
		n := int64(1024)
		for _, v := range s {
			switch x := v.(type) {
			case string:
				n += 4 * int64(len(x))
			case []byte:
				n += 4 * int64(len(x))
			}
		}
		if e := w.charge(n); e != nil {
			return e
		}
		w.source = slices.Clone(s)
		w.fence = f
		w.bound = true
		w.ownerRole = f.role == "owner"
		// s[7], s[8] and s[43] are reserved source positions the reader does not consume today.
		w.facts.PartIndex = s[39].(int64)
		w.facts.Edition = s[40].(string)
		w.facts.StartSeconds = rsTechnicalFloat(s[41])
		if s[42] != nil {
			x := rsTechnicalFloat(s[42])
			w.facts.EndSeconds = &x
		}
		w.facts.StreamState = rsTechnicalHead(s, 11, 12, 13, nil)
		w.facts.ChapterState = rsTechnicalHead(s, 15, 16, 17, s[18])
		w.facts.SubtitleState = rsTechnicalHead(s, 19, 20, 21, s[22])
		// Owner-only inventory: a member never receives the accepted path,
		// ordered disc titles or selected media display.
		w.facts.DiscState, w.facts.MediaState = "missing", "missing"
		if w.ownerRole && s[23] != nil {
			w.facts.DiscState = "stale"
			if s[23] == s[2] && s[24] == s[4] && s[25] == s[3] && s[33] == "local" {
				w.facts.DiscState = "ready"
				w.facts.Disc = &RSDiscFacts{Kind: s[26].(string), Packaging: s[27].(string), Encryption: s[28].(string), Chapters: map[int64]int{}}
				w.facts.MediaState = rsTechnicalMediaState(s, w.facts.StreamState, w.facts.ChapterState)
			}
		}
		return nil
	}, func(_ context.Context, q browseQuery, _ browseFence, _ *RSTechnicalFacts) error {
		// One bounded key inventory per public poll. Wide payloads follow on
		// later polls, so no poll exceeds the existing read boundary.
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
				if !w.ownerRole {
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
				if !w.ownerRole || w.facts.DiscState != "ready" {
					continue
				}
				id = w.source[1]
			case 7:
				w.family = "media"
				if !w.ownerRole || w.facts.MediaState != "ready" {
					continue
				}
				w.keys = [][]any{{w.source[30]}}
				return nil
			default:
				return nil
			}
			if int64(rsTechnicalRows+1)*rsTechnicalKeyBytes > w.lease.PayloadBytes()-w.used {
				return governor.ErrSnapshotBudget
			}
			rows, e := q("rs.technical."+w.family+"_keys", id, int64(rsTechnicalRows+1))
			if errors.Is(e, governor.ErrSnapshotBudget) && request.Budget.RawRows >= rsTechnicalRows {
				// The shared read window admits at most the family cap. A key
				// inventory bounded to one extra key can only exhaust that window
				// by exceeding the cap, so refuse the family's own typed capacity
				// error instead of publishing the 4096-row prefix as complete.
				return ErrUnqualified
			}
			if e != nil {
				return e
			}
			if len(rows) > rsTechnicalRows {
				return ErrUnqualified
			}
			if e := w.charge(int64(len(rows)) * rsTechnicalKeyBytes); e != nil {
				return e
			}
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
			// Asset token and subtitle identity are both text keys.
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
		// Retained values, row metadata and handoff copies are funded BEFORE
		// the payload window and decode.
		if n > (w.lease.PayloadBytes()-w.used-1024)/4 {
			return governor.ErrSnapshotBudget
		}
		n = n*4 + 1024
		scratch := int64(0)
		if w.family == "media" || w.family == "disctitle" {
			scratch = int64(rsTechnicalRows+1) * rsTechnicalKeyBytes
		}
		if e := w.charge(n + scratch); e != nil {
			return e
		}
		return windows.arm(max(n+scratch, 1), 3)
	}, func(_ context.Context, q browseQuery, _ browseFence, _ *RSTechnicalFacts) error {
		// The point row gets its own window. A disc title's chapter inventory
		// needs the whole shared row allowance at the 4096 boundary, so it must
		// not share that window with the title row it describes.
		selected = nil
		if w.index >= len(w.keys) || point == "" {
			return nil
		}
		rows, e := q(point, args...)
		if e != nil {
			return e
		}
		if len(rows) != 1 {
			return ErrStale
		}
		selected = rows[0]
		return nil
	}, func(_ context.Context, q browseQuery, _ browseFence, p *RSTechnicalFacts) error {
		if selected != nil {
			if w.family == "media" || w.family == "disctitle" {
				v := rsTechnicalDecodeDisctitle(selected)
				chapters, err := q("rs.technical.discchapters", w.source[1], v.key, int64(rsTechnicalRows+1))
				if errors.Is(err, governor.ErrSnapshotBudget) && request.Budget.RawRows >= rsTechnicalRows {
					// One chapter beyond the family cap is a typed refusal, never
					// a chapter count silently capped at 4096.
					return ErrUnqualified
				}
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
				// Music facts are valid only for the exact observation revision
				// that produced them; a moved/re-scanned file starts unobserved.
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
					if w.source[14] == int64(rsTechnicalStreamDetailVersion) {
						detail, hearing, commentary, e := rsTechnicalStreamDetail(raw)
						if e != nil {
							return e
						}
						v.Detail, v.HearingImpaired, v.Commentary = detail, hearing, commentary
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
	facts, result, e := rsTechnicalPublish(ctx, windows, request, budget, output, in.View, steps, root)
	if e != nil {
		w.failure = e
		_ = w.authority.CancelPreparation(w.lease)
		return RSTechnicalFacts{}, result, e
	}
	if !facts.Preparing {
		if e = w.authority.ReadyPreparation(w.lease); e != nil {
			return RSTechnicalFacts{}, result, e
		}
		if e = w.authority.HandoffPreparation(w.lease); e != nil {
			return RSTechnicalFacts{}, result, e
		}
		w.handed = true
	}
	return facts, result, nil
}

// rsTechnicalPublish is ONE attempt. A preparation reducer that already advanced
// its key/fact cursors must never be replayed inside the same poll, so the shared
// double-attempt reader is deliberately not used here.
func rsTechnicalPublish[T any](ctx context.Context, s browseSnapshots, q governor.Request, b time.Duration, out BrowseOutput, in BrowseInput, steps []readStep[T], roots ...rootRead) (T, BrowseReadResult, error) {
	var copied int64
	page, cost, err := publishAttempt(ctx, s, q, b, out, in, steps, &copied, roots...)
	cost.Attempts = 1
	if !cost.Publication.Started.IsZero() {
		cost.PublicationWindows = append(cost.PublicationWindows, cost.Publication)
	}
	return page, cost, err
}
