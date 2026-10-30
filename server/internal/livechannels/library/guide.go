package librarychannels

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// Guide uses the same consumer DTO/window model, but never shares a tuner source.
func (s *Store) Guide(ctx context.Context, a Authority, q livechannels.GuideQuery, key []byte) (livechannels.Guide, error) {
	out := livechannels.Guide{Sources: []livechannels.GuideSource{}, Channels: []livechannels.Channel{}, Timezone: q.Timezone, Start: q.Start.UTC().Format(time.RFC3339), End: q.End.UTC().Format(time.RFC3339), ObservedAt: s.now().UTC().Format(time.RFC3339)}
	if q.Kind != livechannels.LibraryChannel || q.Start.IsZero() || !q.End.After(q.Start) || (!q.Compact && q.End.Sub(q.Start) > 24*time.Hour || q.End.Sub(q.Start) > 14*24*time.Hour) || q.Limit < 1 || q.Limit > 50 || len(q.ChannelIDs) > 50 || len(q.Group) > 120 || len(q.Search) > 120 || len(key) < 32 {
		return out, ErrInvalid
	}
	if _, e := time.LoadLocation(q.Timezone); e != nil {
		return out, ErrInvalid
	}
	selected := make(map[string]bool, len(q.ChannelIDs))
	for _, id := range q.ChannelIDs {
		if id == "" || len(id) > 256 {
			return out, ErrInvalid
		}
		selected[id] = true
	}
	e := s.snapshot(ctx, a, false, func(tx *sql.Tx, scope Scope) error {
		out.ViewerFence = scope.Fence
		rows, e := tx.QueryContext(ctx, `SELECT id,revision,active_generation FROM lc_channels WHERE enabled=1 AND removed=0 ORDER BY position,id`)
		if e != nil {
			return unavailable(e)
		}
		type identity struct {
			id, gen  string
			revision int64
		}
		all := []identity{}
		boundQuery := q
		boundQuery.Cursor = ""
		binding := scope.Fence + ":" + encode(boundQuery)
		for rows.Next() {
			var c identity
			if cause := rows.Scan(&c.id, &c.revision, &c.gen); cause != nil {
				rows.Close()
				return unavailable(cause)
			}
			all = append(all, c)
			binding += fmt.Sprintf(":%s:%d:%s", c.id, c.revision, c.gen)
			if len(all) > MaxChannels {
				rows.Close()
				return ErrUnavailable
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return unavailable(e)
		}
		var prefRev int64
		if q.Viewer.Valid() {
			if cause := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(revision),0) FROM live_channel_preferences WHERE authority=? AND account_id=? AND profile_id=?`, q.Viewer.Authority, q.Viewer.AccountID, q.Viewer.ProfileID).Scan(&prefRev); cause != nil {
				return unavailable(cause)
			}
		}
		binding = digest(binding, fmt.Sprint(prefRev))
		offset := 0
		if q.Cursor != "" {
			decoded, e := parseCursor(q.Cursor, key)
			if e != nil || decoded.Binding != binding {
				return livechannels.ErrCursor
			}
			offset = decoded.Offset
		}
		type pageChannel struct {
			channel Channel
			ch      livechannels.Channel
		}
		page := []pageChannel{}
		for index := offset; index < len(all); index++ {
			if len(page) >= q.Limit {
				out.NextCursor = signCursor(libraryCursor{binding, index}, key)
				break
			}
			if (len(selected) > 0 && !selected[all[index].id]) || (q.Group != "" && q.Group != "Library Channels") {
				continue
			}
			c, e := readChannel(ctx, tx, all[index].id)
			if e != nil {
				return e
			}
			if (len(selected) > 0 && !selected[c.Config.ID]) || (q.Group != "" && q.Group != "Library Channels") || !scope.permits(c.Config) || (q.SourceID != "" && q.SourceID != c.Config.ID) || (q.Search != "" && !strings.Contains(strings.ToLower(c.Config.Name), strings.ToLower(q.Search))) {
				continue
			}
			ch := livechannels.Channel{ID: c.Config.ID, SourceID: c.Config.ID, Provenance: livechannels.LibraryChannel, TuneUnavailableReason: "delivery-unavailable", RecordUnavailableReason: "not-recordable", Name: c.Config.Name, Number: fmt.Sprint(c.Config.Position + 1), Group: "Library Channels", Generation: c.Generation, Programmes: []livechannels.Programme{}}
			if c.Config.LogoItemID != "" {
				var logoLibrary string
				if tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?) AND e.retired=0 AND cl.retired=0`, c.Config.LogoItemID).Scan(&logoLibrary) == nil && scope.AllowsLibrary(logoLibrary) && contentaccess.VisibleItemTx(ctx, tx, scope.Principal, c.Config.LogoItemID) == nil {
					ch.LogoPath = "/v1/items/" + c.Config.LogoItemID + "/art/poster"
				}
			}
			if c.Generation == "" {
				continue
			}
			if q.Viewer.Valid() {
				e = tx.QueryRowContext(ctx, `SELECT favorite,hidden,revision FROM live_channel_preferences WHERE authority=? AND account_id=? AND profile_id=? AND source_id=? AND channel_id=?`, q.Viewer.Authority, q.Viewer.AccountID, q.Viewer.ProfileID, "library:"+c.Config.ID, c.Config.ID).Scan(&ch.Favorite, &ch.Hidden, &ch.PreferenceRevision)
				ch.Hidden = false // only the owner hides channels, for everyone
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return unavailable(e)
				}
			}
			if ch.Hidden && !q.IncludeHidden || q.FavoritesOnly && !ch.Favorite {
				continue
			}
			page = append(page, pageChannel{channel: c, ch: ch})
		}
		// PERF-13: one entries query per guide page, not one per channel. A
		// generation belongs to exactly one channel, so IN over the page's
		// generations returns the same rows as N per-channel window queries.
		byGeneration := map[string][]Entry{}
		combined := []Entry{}
		if len(page) > 0 && !q.NoProgrammes {
			gens := make([]string, 0, len(page))
			for _, p := range page {
				gens = append(gens, p.channel.Generation)
			}
			marks := strings.TrimSuffix(strings.Repeat("?,", len(gens)), ",")
			args := make([]any, 0, len(gens)+3)
			for _, g := range gens {
				args = append(args, g)
			}
			args = append(args, q.End.UnixMilli(), q.Start.UnixMilli())
			query := `SELECT occurrence_id,channel_id,start_ms,end_ms,COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=lc_entries.item_id),''),asset_id,library_id,source_fence,title,rule_id,block_id,source_offset_ms,slate_reason,generation_id FROM lc_entries WHERE generation_id IN (` + marks + `) AND start_ms<? AND end_ms>? ORDER BY generation_id,start_ms`
			if q.Compact {
				query = ""
				args = []any{}
				for _, p := range page {
					if query != "" {
						query += " UNION ALL "
					}
					query += `SELECT * FROM (SELECT occurrence_id,channel_id,start_ms,end_ms,COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=lc_entries.item_id),''),asset_id,library_id,source_fence,title,rule_id,block_id,source_offset_ms,slate_reason,generation_id FROM lc_entries WHERE generation_id=? AND start_ms<? AND end_ms>? ORDER BY start_ms LIMIT 3)`
					args = append(args, p.channel.Generation, q.End.UnixMilli(), q.Start.UnixMilli())
				}
			}
			rows, e := tx.QueryContext(ctx, query, args...)
			if e != nil {
				return unavailable(e)
			}
			for rows.Next() {
				var v Entry
				var gen string
				if cause := rows.Scan(&v.ID, &v.ChannelID, &v.StartMS, &v.EndMS, &v.ItemID, &v.AssetID, &v.LibraryID, &v.SourceFence, &v.Title, &v.RuleID, &v.BlockID, &v.SourceOffsetMS, &v.SlateReason, &gen); cause != nil {
					rows.Close()
					return unavailable(cause)
				}
				byGeneration[gen] = append(byGeneration[gen], v)
				combined = append(combined, v)
			}
			if cause := rows.Err(); cause != nil {
				rows.Close()
				return unavailable(cause)
			}
			rows.Close()
		}
		// One access check for the whole page: the viewer's restrictions are
		// read once and the page's distinct items checked in one set query,
		// with candidate validation cached per distinct source (windowAccess).
		access := func(v Entry) string { return "" }
		if len(combined) > 0 {
			var e error
			access, e = windowAccess(ctx, tx, scope, combined)
			if e != nil {
				return unavailable(e)
			}
		}
		// One generation-status query per page instead of one per channel.
		genStatus := map[string][3]int64{}
		if len(page) > 0 {
			gens := make([]string, 0, len(page))
			for _, p := range page {
				gens = append(gens, p.channel.Generation)
			}
			marks := strings.TrimSuffix(strings.Repeat("?,", len(gens)), ",")
			args := make([]any, 0, len(gens))
			for _, g := range gens {
				args = append(args, g)
			}
			rows, e := tx.QueryContext(ctx, `SELECT id,published_ms,start_ms,end_ms FROM lc_generations WHERE id IN (`+marks+`) AND status='published'`, args...)
			if e != nil {
				return unavailable(e)
			}
			for rows.Next() {
				var id string
				var published, start, end int64
				if cause := rows.Scan(&id, &published, &start, &end); cause != nil {
					rows.Close()
					return unavailable(cause)
				}
				genStatus[id] = [3]int64{published, start, end}
			}
			if cause := rows.Err(); cause != nil {
				rows.Close()
				return unavailable(cause)
			}
			rows.Close()
		}
		// Programme facts for the items the viewer may see (guide facts), looked
		// up once for the page below.
		visible := map[string]string{}
		for _, p := range page {
			ch := p.ch
			for _, v := range byGeneration[p.channel.Generation] {
				title := v.Title
				switch access(v) {
				case "restricted":
					title = restrictedTitle
				case "unavailable":
					title = unavailableTitle
				default:
					visible[v.ID] = v.ItemID
				}
				ch.Programmes = append(ch.Programmes, livechannels.Programme{ID: v.ID, ChannelID: p.channel.Config.ID, Title: title, Start: time.UnixMilli(v.StartMS).UTC().Format(time.RFC3339Nano), End: time.UnixMilli(v.EndMS).UTC().Format(time.RFC3339Nano), Lineage: "generated"})
			}
			status, ok := genStatus[p.channel.Generation]
			if !ok {
				return unavailable(errors.New("generation not published"))
			}
			published, start, end := status[0], status[1], status[2]
			out.Channels = append(out.Channels, ch)
			out.Sources = append(out.Sources, livechannels.GuideSource{ID: p.channel.Config.ID, Name: p.channel.Config.Name, Generation: p.channel.Generation, PublishedAt: time.UnixMilli(published).UTC().Format(time.RFC3339), AvailableStart: time.UnixMilli(start).UTC().Format(time.RFC3339Nano), AvailableEnd: time.UnixMilli(end).UTC().Format(time.RFC3339Nano), Provenance: livechannels.LibraryChannel, RefreshState: p.channel.State})
		}
		if q.MarkChannels != nil {
			if e := q.MarkChannels(ctx, tx, out.Channels); e != nil {
				return e
			}
		}
		if len(visible) > 0 {
			flat := []livechannels.Programme{}
			for _, c := range out.Channels {
				flat = append(flat, c.Programmes...)
			}
			if e := libraryFacts(ctx, tx, flat, visible); e != nil {
				return unavailable(e)
			}
			k := 0
			for i := range out.Channels {
				n := len(out.Channels[i].Programmes)
				copy(out.Channels[i].Programmes, flat[k:k+n])
				k += n
			}
		}
		out.State = "ready"
		if len(out.Channels) == 0 {
			out.State = "filter-empty"
			if len(all) == 0 {
				out.State = "no-sources"
			}
		} else {
			count := 0
			stale := false
			for _, c := range out.Channels {
				count += len(c.Programmes)
			}
			for _, src := range out.Sources {
				if src.RefreshState == "stale" || src.RefreshState == "source-failure" {
					stale = true
				}
			}
			if count == 0 {
				out.State = "no-guide-data"
			} else if stale {
				out.State = "guide-stale"
			}
		}
		out.Days = livechannels.GuideDays(out.Sources, s.now())
		return nil
	})
	return out, e
}

type libraryCursor struct {
	Binding string `json:"b"`
	Offset  int    `json:"o"`
}

func signCursor(c libraryCursor, key []byte) string {
	raw := []byte(encode(c))
	h := hmac.New(sha256.New, key)
	h.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func parseCursor(raw string, key []byte) (libraryCursor, error) {
	var c libraryCursor
	if len(raw) > 2048 {
		return c, ErrInvalid
	}
	payload, signature, ok := strings.Cut(raw, ".")
	if !ok {
		return c, ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(payload)
	if e != nil {
		return c, ErrInvalid
	}
	sig, e := base64.RawURLEncoding.DecodeString(signature)
	if e != nil {
		return c, ErrInvalid
	}
	h := hmac.New(sha256.New, key)
	h.Write(b)
	if !hmac.Equal(sig, h.Sum(nil)) || json.Unmarshal(b, &c) != nil || c.Offset < 0 || c.Offset > MaxChannels {
		return c, ErrInvalid
	}
	return c, nil
}
func entriesWindow(ctx context.Context, tx *sql.Tx, gen string, start, end int64, limit int) ([]Entry, error) {
	rows, e := tx.QueryContext(ctx, `SELECT occurrence_id,channel_id,start_ms,end_ms,COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=lc_entries.item_id),''),asset_id,library_id,source_fence,title,rule_id,block_id,source_offset_ms,slate_reason FROM lc_entries WHERE generation_id=? AND start_ms<? AND end_ms>? ORDER BY start_ms LIMIT ?`, gen, end, start, limit)
	if e != nil {
		return nil, unavailable(e)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var v Entry
		if cause := rows.Scan(&v.ID, &v.ChannelID, &v.StartMS, &v.EndMS, &v.ItemID, &v.AssetID, &v.LibraryID, &v.SourceFence, &v.Title, &v.RuleID, &v.BlockID, &v.SourceOffsetMS, &v.SlateReason); cause != nil {
			return nil, unavailable(cause)
		}
		out = append(out, v)
	}
	if cause := rows.Err(); cause != nil {
		return nil, unavailable(cause)
	}
	return out, nil
}
func entryCandidate(ctx context.Context, tx *sql.Tx, v Entry) (Candidate, error) {
	var duration float64
	if tx.QueryRowContext(ctx, `SELECT duration FROM catalog_assets WHERE token=?`, v.AssetID).Scan(&duration) != nil {
		return Candidate{}, ErrConflict
	}
	c := Candidate{ItemID: v.ItemID, LibraryID: v.LibraryID, AssetID: v.AssetID, SourceFence: v.SourceFence, DurationMS: int64(duration*1000 + 0.5), Title: v.Title}
	return c, ValidateCandidateTx(ctx, tx, c)
}

// Slate titles for a viewer who can't watch an entry. The lineup is shared, so a
// restricted profile sees the same schedule with a slate in place of the title.
const (
	unavailableTitle = "Unavailable program"
	restrictedTitle  = livechannels.RestrictedProgrammeTitle
)

// entryAccess is "" when the viewer may watch the entry, else "restricted" (the
// profile's content restrictions) or "unavailable" (no item, no library access,
// or the source changed).
func entryAccess(ctx context.Context, tx *sql.Tx, scope Scope, v Entry) string {
	if v.ItemID == "" {
		return "unavailable"
	}
	if scope.AllowsLibrary == nil || !scope.AllowsLibrary(v.LibraryID) {
		return "unavailable"
	}
	// Integration merge: lane C's member-aware contentaccess check (59300f8) and
	// BE-channels' profile restriction callback both turn the title into a slate.
	if contentaccess.VisibleItemTx(ctx, tx, scope.Principal, v.ItemID) != nil {
		return "restricted"
	}
	if scope.AllowsItem != nil && !scope.AllowsItem(ctx, tx, v.ItemID) {
		return "restricted"
	}
	if _, e := entryCandidate(ctx, tx, v); e != nil {
		return "unavailable"
	}
	return ""
}

// windowAccess is entryAccess for a whole guide window (C55): the viewer's
// restrictions are read once and the window's distinct item ids are checked in
// one set query, instead of several statements per programme. Candidate
// validation runs once per distinct source, not once per programme.
func windowAccess(ctx context.Context, tx *sql.Tx, scope Scope, entries []Entry) (func(Entry) string, error) {
	ids := []string{}
	seen := map[string]bool{}
	for _, v := range entries {
		if v.ItemID != "" && scope.AllowsLibrary != nil && scope.AllowsLibrary(v.LibraryID) && !seen[v.ItemID] {
			seen[v.ItemID] = true
			ids = append(ids, v.ItemID)
		}
	}
	visible := map[string]bool{}
	if len(ids) > 0 {
		predicate, args, err := contentaccess.VisibleItemsSQL(ctx, tx, scope.Principal, "i.id", nil)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(ids)
		rows, err := tx.QueryContext(ctx, `SELECT pid(i.public_id) FROM catalog_entities i WHERE i.public_id IN (SELECT pid_blob(value) FROM json_each(?)) AND i.retired=0 AND `+predicate, append([]any{string(raw)}, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			visible[id] = true
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
		// A scope may add its own per-item rule; production's is the same
		// content fence, so this only runs for ids the set query admitted.
		if scope.AllowsItem != nil {
			for id := range visible {
				if !scope.AllowsItem(ctx, tx, id) {
					delete(visible, id)
				}
			}
		}
	}
	type source struct{ item, library, asset, fence string }
	valid := map[source]bool{}
	return func(v Entry) string {
		if v.ItemID == "" || scope.AllowsLibrary == nil || !scope.AllowsLibrary(v.LibraryID) {
			return "unavailable"
		}
		if !visible[v.ItemID] {
			return "restricted"
		}
		key := source{v.ItemID, v.LibraryID, v.AssetID, v.SourceFence}
		ok, known := valid[key]
		if !known {
			_, e := entryCandidate(ctx, tx, v)
			ok = e == nil
			valid[key] = ok
		}
		if !ok {
			return "unavailable"
		}
		return ""
	}, nil
}

// Tune is private server selection evidence. It is not a media grant and exposes
// neither a path nor a provider URL. The shared playback runtime consumes it.
type Tune struct {
	Number     string
	Name       string
	Overlay    Overlay
	ChannelID  string
	Generation string
	Entry      Entry
	Candidate  Candidate
	OffsetMS   int64
	ClipEndMS  int64
	Next       *Entry
	Quality    Quality
	LogoItemID string
}

func (s *Store) ResolveTuneTx(ctx context.Context, tx *sql.Tx, scope Scope, id, expectedGeneration, existingPlayback string, now time.Time) (Tune, error) {
	var out Tune
	c, e := readChannel(ctx, tx, id)
	if e != nil {
		return out, e
	}
	if !scope.permits(c.Config) {
		return out, ErrDenied
	}
	gen := c.Generation
	if existingPlayback == "" {
		if !c.Config.Enabled || gen == "" || expectedGeneration != "" && expectedGeneration != gen {
			return out, ErrConflict
		}
	} else {
		var referenceGen string
		var expiry int64
		if tx.QueryRowContext(ctx, `SELECT generation_id,lease_until_ms FROM lc_playback_refs WHERE playback_id=? AND channel_id=?`, existingPlayback, id).Scan(&referenceGen, &expiry) != nil || expiry <= now.UnixMilli() {
			return out, ErrDenied
		}
		if !c.Config.Enabled {
			gen = referenceGen
		}
	}
	values, e := entriesWindow(ctx, tx, gen, now.UnixMilli(), now.UnixMilli()+1, 1)
	if e != nil {
		return out, e
	}
	if len(values) != 1 {
		return out, ErrConflict
	}
	v := values[0]
	switch entryAccess(ctx, tx, scope, v) {
	case "":
	case "restricted":
		// Restricted is absent (SEC-02): the caller answers not_found, never a
		// permission error that confirms the programme exists (INT P26).
		return out, identity.ErrContentRestricted
	default:
		return out, ErrDenied
	}
	candidate, e := entryCandidate(ctx, tx, v)
	if e != nil {
		return out, e
	}
	next, e := entriesWindow(ctx, tx, gen, v.EndMS, v.EndMS+1, 1)
	if e != nil {
		return out, e
	}
	var upcoming *Entry
	if len(next) == 1 {
		safe := next[0]
		if access := entryAccess(ctx, tx, scope, safe); access != "" {
			safe.ItemID = ""
			safe.Title = unavailableTitle
			safe.SlateReason = "unavailable"
			if access == "restricted" {
				safe.Title, safe.SlateReason = restrictedTitle, "restricted"
			}
		}
		upcoming = &safe
	}
	out = Tune{Number: fmt.Sprint(c.Config.Position + 1), Name: c.Config.Name, Overlay: c.Config.Overlay, ChannelID: id, Generation: gen, Entry: v, Candidate: candidate, OffsetMS: v.SourceOffsetMS + now.UnixMilli() - v.StartMS, ClipEndMS: v.SourceOffsetMS + v.EndMS - v.StartMS, Next: upcoming, Quality: c.Config.Quality, LogoItemID: c.Config.LogoItemID}
	return out, nil
}
func BindPlaybackTx(ctx context.Context, tx *sql.Tx, playback string, t Tune, expiry time.Time) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO lc_playback_refs VALUES(?,?,?,?,?,?) ON CONFLICT(playback_id) DO UPDATE SET generation_id=excluded.generation_id,occurrence_id=excluded.occurrence_id,end_ms=excluded.end_ms,lease_until_ms=excluded.lease_until_ms`, playback, t.ChannelID, t.Generation, t.Entry.ID, t.Entry.EndMS, expiry.UnixMilli())
	if e != nil {
		return unavailable(e)
	}
	return nil
}

// CheckRetainedTx authorizes a specific retained programme, rather than silently
// substituting the programme currently on air. Disabling removes new admission;
// accepted viewers may finish while ownership and all selected libraries remain
// authorized. Removed channels and changed assets fail closed.
func (s *Store) CheckRetainedTx(ctx context.Context, tx *sql.Tx, scope Scope, id string, candidate Candidate) (Config, error) {
	c, e := readChannel(ctx, tx, id)
	if e != nil {
		return Config{}, e
	}
	if !scope.permits(c.Config) || scope.AllowsLibrary == nil || !scope.AllowsLibrary(candidate.LibraryID) {
		return Config{}, ErrDenied
	}
	if e = ValidateCandidateTx(ctx, tx, candidate); e != nil {
		return Config{}, e
	}
	return c.Config, nil
}

// ProjectAiringsTx is the guide's item-level admission seam for programme
// Search. It runs in the caller's directory snapshot; denied entries are omitted.
func (s *Store) ProjectAiringsTx(ctx context.Context, tx *sql.Tx, a Authority, values []livechannels.Airing) ([]livechannels.Airing, error) {
	scope, e := a(ctx, tx, false)
	if e != nil {
		return nil, e
	}
	out := []livechannels.Airing{}
	for _, v := range values {
		if v.Channel.Provenance != livechannels.LibraryChannel {
			out = append(out, v)
			continue
		}
		c, e := readChannel(ctx, tx, v.Channel.ID)
		if e != nil {
			return nil, e
		}
		if !scope.permits(c.Config) || c.Generation != v.Channel.Generation {
			continue
		}
		entries, e := entriesWindow(ctx, tx, c.Generation, mustAiringTime(v.Programme.Start), mustAiringTime(v.Programme.End), 4)
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			if entry.ID != v.Programme.ID {
				continue
			}
			access, e := windowAccess(ctx, tx, scope, []Entry{entry})
			if e != nil {
				return nil, e
			}
			if access(entry) != "" {
				continue
			}
			programmes := []livechannels.Programme{v.Programme}
			if e = libraryFacts(ctx, tx, programmes, map[string]string{v.Programme.ID: entry.ItemID}); e != nil {
				return nil, e
			}
			v.Programme = programmes[0]
			out = append(out, v)
		}
	}
	current, e := a(ctx, tx, false)
	if e != nil {
		return nil, e
	}
	if current.Fence != scope.Fence {
		return nil, livechannels.ErrCursor
	}
	return out, nil
}
func mustAiringTime(raw string) int64 {
	v, _ := time.Parse(time.RFC3339Nano, raw)
	return v.UnixMilli()
}
