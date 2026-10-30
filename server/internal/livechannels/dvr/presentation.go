package dvr

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strconv"
	"strings"
	"time"
)

// Detail explains competition without disclosing another profile's recording
// identity, title, or rule. Demand/capacity still include all reservations.
type Overlap struct {
	Channel  RecordingChannel               `json:"channel"`
	RuleID   string                         `json:"ruleId"`
	Episode  *livechannels.ProgrammeEpisode `json:"episode,omitempty"`
	ID       string                         `json:"id"`
	Title    string                         `json:"title"`
	Start    string                         `json:"start"`
	End      string                         `json:"end"`
	Priority int                            `json:"priority"`
	State    string                         `json:"state"`
}
type Detail struct {
	Recording   Recording             `json:"recording"`
	Capacity    livechannels.Capacity `json:"capacity"`
	Overlaps    []Overlap             `json:"overlaps"`
	NextOverlap string                `json:"nextOverlap"`
}

func (s *Store) Detail(ctx context.Context, a livechannels.Authority, o livechannels.Owner, id, after string) (Detail, error) {
	out := Detail{Overlaps: []Overlap{}}
	if !canonicalID.MatchString(id) || after != "" && !canonicalID.MatchString(after) {
		return out, ErrInvalid
	}
	e := s.snapshot(ctx, a, o, func(tx *sql.Tx, _ func(string, string) bool) error {
		r, e := loadTx(ctx, tx, o, id)
		if e != nil {
			return e
		}
		programmeAllowed, err := programmePredicateTx(ctx, tx, o)
		if err != nil {
			return err
		}
		out.Recording = r
		restrictRecording(&out.Recording, programmeAllowed)
		if e = s.recordingChannelMarksTx(ctx, tx, a, o, []recordingChannelRef{{r.Occurrence.SourceID, &out.Recording.Channel}}); e != nil {
			return e
		}
		if r.State == "pending-delete" {
			var reason string
			if err := tx.QueryRowContext(ctx, `SELECT reason FROM dvr_delete_work WHERE recording_id=?`, r.ID).Scan(&reason); err != nil && err != sql.ErrNoRows {
				return err
			}
			switch reason {
			case "physical-reader-active", "artifact-remove-unavailable", "active-reader-grace":
				out.Recording.Reason = reason
			}
		}
		cap, e := livechannels.CapacityTx(ctx, tx, r.Occurrence.SourceID)
		if e == nil {
			out.Capacity = cap
		}
		if !mutable(r.State) && !capturing(r.State) {
			return nil
		}
		if e != nil {
			return e
		}
		out.Recording.Conflicts, e = conflictsTx(ctx, tx, r)
		if e != nil {
			return e
		}
		if len(out.Recording.Conflicts) > 0 && r.State == "scheduled" {
			out.Recording.State = "conflicted"
			out.Recording.Reason = "tuner-conflict"
		}
		start, _ := time.Parse(time.RFC3339Nano, r.Start)
		end, _ := time.Parse(time.RFC3339Nano, r.End)
		rows, e := tx.QueryContext(ctx, `SELECT id,programme_json,start_ms,end_ms,priority,state,rule_id,channel_id FROM dvr_recordings WHERE owner_key=? AND source_id=? AND id<>? AND id>? AND start_ms<? AND end_ms>? AND state IN('scheduled','conflicted','preparing','recording','finalizing') ORDER BY id LIMIT 21`, o.Key(), r.Occurrence.SourceID, id, after, end.UnixMilli(), start.UnixMilli())
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var v Overlap
			var programme string
			var start, end int64
			if e = rows.Scan(&v.ID, &programme, &start, &end, &v.Priority, &v.State, &v.RuleID, &v.Channel.ID); e != nil {
				return e
			}
			p, e := decodeProgramme(programme)
			if e != nil {
				return e
			}
			p.Admit(programmeAllowed)
			v.Title = p.Title
			v.Episode = p.Episode
			v.Start = time.UnixMilli(start).UTC().Format(time.RFC3339Nano)
			v.End = time.UnixMilli(end).UTC().Format(time.RFC3339Nano)
			out.Overlaps = append(out.Overlaps, v)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out.Overlaps) > 20 {
			out.Overlaps = out.Overlaps[:20]
			out.NextOverlap = out.Overlaps[19].ID
		}
		rows.Close()
		for i := range out.Overlaps {
			peer := Recording{Occurrence: Occurrence{SourceID: r.Occurrence.SourceID, ChannelID: out.Overlaps[i].Channel.ID, Generation: r.Occurrence.Generation}}
			if e = channelTx(ctx, tx, &peer); e != nil {
				return e
			}
			out.Overlaps[i].Channel = peer.Channel
		}
		refs := make([]recordingChannelRef, len(out.Overlaps))
		for i := range out.Overlaps {
			refs[i] = recordingChannelRef{r.Occurrence.SourceID, &out.Overlaps[i].Channel}
		}
		return s.recordingChannelMarksTx(ctx, tx, a, o, refs)
	})
	return out, e
}

type ChannelChoice struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Number string `json:"number"`
}
type ChannelChoices struct {
	Channels   []ChannelChoice `json:"channels"`
	NextCursor string          `json:"nextCursor"`
	Generation string          `json:"generation"`
}

// The picker reads the current authoritative source generation. No provider
// locators, credentials, or inaccessible sources are part of this projection.
func (s *Store) ChannelChoices(ctx context.Context, a livechannels.Authority, o livechannels.Owner, source, search, cursor string) (ChannelChoices, error) {
	out := ChannelChoices{Channels: []ChannelChoice{}}
	if !hexID.MatchString(source) || len(search) > 120 || len(cursor) > 160 || strings.ContainsAny(search, "\x00\r\n") {
		return out, ErrInvalid
	}
	e := s.snapshot(ctx, a, o, func(tx *sql.Tx, allowed func(string, string) bool) error {
		if !allowed(source, "") {
			return ErrDenied
		}
		var state string
		if e := tx.QueryRowContext(ctx, `SELECT active_generation,state FROM live_sources WHERE id=?`, source).Scan(&out.Generation, &state); e != nil {
			return ErrDenied
		}
		after := -1
		if cursor != "" {
			parts := strings.Split(cursor, ".")
			if len(parts) != 2 || !hexID.MatchString(parts[0]) {
				return ErrInvalid
			}
			if parts[0] != out.Generation {
				return ErrConflict
			}
			position, err := strconv.Atoi(parts[1])
			if err != nil || position < 0 || strconv.Itoa(position) != parts[1] {
				return ErrInvalid
			}
			after = position
		}
		rows, e := tx.QueryContext(ctx, `SELECT channel_id,name,number,position FROM live_channel_versions WHERE generation_id=? AND position>? AND (?='' OR instr(lower(name),lower(?))>0 OR instr(lower(number),lower(?))>0) ORDER BY position LIMIT 257`, out.Generation, after, search, search, search)
		if e != nil {
			return e
		}
		defer rows.Close()
		examined := 0
		for rows.Next() {
			// Bound the raw walk as well as the visible page. A denied prefix
			// must still advance the cursor so later allowed channels are reachable.
			if examined == 256 || len(out.Channels) == 30 {
				out.NextCursor = out.Generation + "." + strconv.Itoa(after)
				break
			}
			var v ChannelChoice
			if e = rows.Scan(&v.ID, &v.Name, &v.Number, &after); e != nil {
				return e
			}
			examined++
			if allowed(source, v.ID) {
				out.Channels = append(out.Channels, v)
			}
		}
		if e = rows.Err(); e != nil {
			return e
		}
		return nil
	})
	return out, e
}

// Annotate only programmes already authorized by the guide, owned by this exact
// durable viewer. The guide's generation remains the occurrence authority.
func (s *Store) AnnotateGuide(ctx context.Context, a livechannels.Authority, o livechannels.Owner, g *livechannels.Guide) error {
	if g == nil {
		return ErrInvalid
	}
	return s.snapshot(ctx, a, o, func(tx *sql.Tx, allowed func(string, string) bool) error {
		type channelKey struct{ source, channel string }
		channels := map[channelKey]map[string][]*livechannels.Programme{}
		pairs := []string{}
		args := []any{o.Key(), mustTime(g.Start).UnixMilli(), mustTime(g.End).UnixMilli()}
		for i := range g.Channels {
			ch := &g.Channels[i]
			if len(ch.Programmes) == 0 || ch.Provenance != livechannels.LiveSource || !allowed(ch.SourceID, ch.ID) {
				continue
			}
			key := channelKey{ch.SourceID, ch.ID}
			if _, exists := channels[key]; !exists {
				pairs = append(pairs, "(?,?)")
				args = append(args, ch.SourceID, ch.ID)
				channels[key] = map[string][]*livechannels.Programme{}
			}
			for j := range ch.Programmes {
				p := &ch.Programmes[j]
				channels[key][p.ID] = append(channels[key][p.ID], p)
			}
		}
		if len(pairs) == 0 {
			return nil
		}
		// Scan this owner's overlapping recordings once for the authorized page,
		// rather than once per channel. Pair membership also keeps equal channel
		// or programme IDs from another source out of the annotation.
		rows, e := tx.QueryContext(ctx, `SELECT source_id,channel_id,programme_id,id,state FROM dvr_recordings WHERE owner_key=? AND end_ms>? AND start_ms<? AND state NOT IN('cancelled','deleted') AND (source_id,channel_id) IN (VALUES `+strings.Join(pairs, ",")+`)`, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var key channelKey
			var programme, id, state string
			if e = rows.Scan(&key.source, &key.channel, &programme, &id, &state); e != nil {
				return e
			}
			for _, p := range channels[key][programme] {
				p.RecordingID = id
				p.RecordingState = state
			}
		}
		return rows.Err()
	})
}
func mustTime(s string) time.Time { v, _ := time.Parse(time.RFC3339Nano, s); return v }

func decodeProgramme(raw string) (livechannels.Programme, error) {
	var p livechannels.Programme
	if json.Unmarshal([]byte(raw), &p) != nil {
		return p, ErrUnavailable
	}
	return p, nil
}

type RuleSummary struct {
	Upcoming        int                     `json:"upcoming"`
	Recorded        int                     `json:"recorded"`
	Conflicts       int                     `json:"conflicts"`
	MatchingTonight int                     `json:"matchingTonight"`
	Next            *livechannels.Programme `json:"next,omitempty"`
	Channel         RecordingChannel        `json:"channel"`
}
type Summary struct {
	Upcoming  int `json:"upcoming"`
	Recorded  int `json:"recorded"`
	Rules     int `json:"rules"`
	Conflicts int `json:"conflicts"`
	Groups    int `json:"groups"`
	Unwatched int `json:"unwatched"`
}
type RecordingGroup struct {
	Key        string    `json:"key"`
	Title      string    `json:"title"`
	Recordings int       `json:"recordings"`
	Unwatched  int       `json:"unwatched"`
	Kept       int       `json:"kept"`
	Exceptions int       `json:"exceptions"`
	Latest     string    `json:"latest"`
	Recording  Recording `json:"recording"`
}
type GroupPage struct {
	Groups     []RecordingGroup `json:"groups"`
	NextCursor string           `json:"nextCursor"`
	Revision   int64            `json:"revision"`
}

const recordingGroupKeySQL = `CASE WHEN COALESCE(json_extract(r.programme_json,'$.seriesId'),'')<>'' THEN 'series:'||r.source_id||':'||json_extract(r.programme_json,'$.seriesId') WHEN json_type(r.programme_json,'$.episode')='object' OR (r.rule_id<>'' AND COALESCE((SELECT json_extract(config_json,'$.matchKind') FROM dvr_rules WHERE id=r.rule_id),'')='title') THEN 'title:'||r.source_id||':'||r.channel_id||':'||lower(trim(json_extract(r.programme_json,'$.title'))) ELSE 'recording:'||r.id END`

// The HTTP source policy is source-level. Read its small distinct source set,
// retaining recordings of a retired source only when that authority permits it.
func recordingSourcesTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, allowed func(string, string) bool) (string, error) {
	rows, e := tx.QueryContext(ctx, `SELECT DISTINCT source_id FROM dvr_recordings WHERE owner_key=? UNION SELECT DISTINCT source_id FROM dvr_rules WHERE owner_key=?`, o.Key(), o.Key())
	if e != nil {
		return "", e
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return "", e
		}
		if allowed(id, "") {
			ids = append(ids, id)
		}
	}
	raw, _ := json.Marshal(ids)
	return string(raw), rows.Err()
}
func (s *Store) Summary(ctx context.Context, a livechannels.Authority, o livechannels.Owner) (Summary, error) {
	out := Summary{}
	e := s.snapshot(ctx, a, o, func(tx *sql.Tx, allowed func(string, string) bool) error {
		sources, e := recordingSourcesTx(ctx, tx, o, allowed)
		if e != nil {
			return e
		}
		e = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(state IN('scheduled','conflicted','waiting-source','waiting-guide','preparing','recording','finalizing')),0),COALESCE(sum(state IN('completed','incomplete-playable')),0),COALESCE(sum(`+currentConflictSQL+`),0),count(DISTINCT CASE WHEN state IN('completed','incomplete-playable') THEN `+recordingGroupKeySQL+` END),COALESCE(sum(state IN('completed','incomplete-playable') AND item_id<>0 AND COALESCE((SELECT watched FROM personal_items WHERE profile_id=? AND item_id=r.item_id),0)=0),0) FROM dvr_recordings r WHERE owner_key=? AND source_id IN(SELECT value FROM json_each(?))`, personalOwner(o), o.Key(), sources).Scan(&out.Upcoming, &out.Recorded, &out.Conflicts, &out.Groups, &out.Unwatched)
		if e != nil {
			return e
		}
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM dvr_rules WHERE owner_key=? AND deleted=0 AND source_id IN(SELECT value FROM json_each(?))`, o.Key(), sources).Scan(&out.Rules)
	})
	return out, e
}
func personalOwner(o livechannels.Owner) string {
	return persistence.PersonalOwnerKey(o.Authority, o.AccountID, o.ProfileID)
}
func (s *Store) Groups(ctx context.Context, a livechannels.Authority, o livechannels.Owner, sort, cursor string, limit int) (GroupPage, error) {
	out := GroupPage{Groups: []RecordingGroup{}}
	if sort == "" {
		sort = "recent"
	}
	if sort != "recent" && sort != "title" && sort != "oldest" || limit < 1 || limit > 50 {
		return out, ErrInvalid
	}
	e := s.snapshot(ctx, a, o, func(tx *sql.Tx, allowed func(string, string) bool) error {
		sources, e := recordingSourcesTx(ctx, tx, o, allowed)
		if e != nil {
			return e
		}
		fence, _, e := a(ctx, tx, false)
		if e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM dvr_owner_revisions WHERE owner_key=?),0)`, o.Key()).Scan(&out.Revision); e != nil {
			return e
		}
		c := continuation{Owner: o.Key(), Fence: fence, State: "groups:" + sort, Limit: limit, Revision: out.Revision, Expiry: s.now().Add(15 * time.Minute).UnixMilli()}
		if cursor != "" {
			old, e := s.decodeCursor(cursor)
			if e != nil {
				return e
			}
			if old.Owner != c.Owner || old.Fence != fence || old.State != c.State || old.Limit != limit || old.Revision != out.Revision || old.Expiry <= s.now().UnixMilli() {
				return livechannels.ErrCursor
			}
			c = old
		}
		// SQL groups the full collection before LIMIT. The only decoded programmes
		// are the representative rows of the requested group page.
		query := `WITH eligible AS (SELECT r.*,` + recordingGroupKeySQL + ` group_key FROM dvr_recordings r WHERE owner_key=? AND source_id IN(SELECT value FROM json_each(?)) AND state IN('completed','incomplete-playable')), grouped AS (SELECT group_key,json_extract(programme_json,'$.title') title,count(*) total,sum(item_id<>0 AND COALESCE((SELECT watched FROM personal_items WHERE profile_id=? AND item_id=eligible.item_id),0)=0) unwatched,sum(keep) kept,sum(state<>'completed') exceptions,max(COALESCE(NULLIF(finished_ms,0),created_ms)) latest,min(COALESCE(NULLIF(finished_ms,0),created_ms)) oldest FROM eligible GROUP BY group_key) SELECT group_key,title,total,unwatched,kept,exceptions,latest,(SELECT id FROM eligible e WHERE e.group_key=grouped.group_key ORDER BY COALESCE(NULLIF(finished_ms,0),created_ms) DESC,id DESC LIMIT 1) FROM grouped ORDER BY `
		order := "latest DESC,group_key"
		if sort == "oldest" {
			order = "oldest,group_key"
		}
		if sort == "title" {
			order = "title COLLATE NOCASE,group_key"
		}
		query += order + ` LIMIT ? OFFSET ?`
		rows, e := tx.QueryContext(ctx, query, o.Key(), sources, personalOwner(o), limit+1, c.AfterMS)
		if e != nil {
			return e
		}
		ids := []string{}
		for rows.Next() {
			var g RecordingGroup
			var latest int64
			var id string
			if e = rows.Scan(&g.Key, &g.Title, &g.Recordings, &g.Unwatched, &g.Kept, &g.Exceptions, &latest, &id); e != nil {
				rows.Close()
				return e
			}
			g.Latest = time.UnixMilli(latest).UTC().Format(time.RFC3339Nano)
			out.Groups = append(out.Groups, g)
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(out.Groups) > limit {
			out.Groups = out.Groups[:limit]
			ids = ids[:limit]
			c.AfterMS += int64(limit)
			out.NextCursor = s.cursor(c)
		}
		predicate, e := programmePredicateTx(ctx, tx, o)
		if e != nil {
			return e
		}
		for i, id := range ids {
			r, e := loadTx(ctx, tx, o, id)
			if e != nil {
				return e
			}
			if e = decorateTx(ctx, tx, o, &r); e != nil {
				return e
			}
			restrictRecording(&r, predicate)
			out.Groups[i].Recording = r
			out.Groups[i].Title = r.Programme.Title
			if r.Programme.Title == livechannels.RestrictedProgrammeTitle {
				out.Groups[i].Key = "restricted:" + r.ID
			}
		}
		refs := make([]recordingChannelRef, len(out.Groups))
		for i := range out.Groups {
			r := &out.Groups[i].Recording
			refs[i] = recordingChannelRef{r.Occurrence.SourceID, &r.Channel}
		}
		return s.recordingChannelMarksTx(ctx, tx, a, o, refs)
	})
	return out, e
}

// One query for this page of rules: no per-rule preview HTTP calls. Paused
// rules retain tomorrow's matching candidates without enabling capture.
func (s *Store) ruleSummariesTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, rules []Rule, allowed func(string, string) bool) error {
	if len(rules) == 0 {
		return nil
	}
	byID := map[string]*Rule{}
	ids := []string{}
	for i := range rules {
		rules[i].Summary.Channel = RecordingChannel{}
		byID[rules[i].ID] = &rules[i]
		ids = append(ids, rules[i].ID)
	}
	raw, _ := json.Marshal(ids)
	rows, e := tx.QueryContext(ctx, `SELECT rule_id,sum(state IN('scheduled','conflicted','waiting-source','waiting-guide','preparing','recording','finalizing')),sum(state IN('completed','incomplete-playable')),sum(state='conflicted') FROM dvr_recordings WHERE owner_key=? AND rule_id IN(SELECT value FROM json_each(?)) GROUP BY rule_id`, o.Key(), string(raw))
	if e != nil {
		return e
	}
	for rows.Next() {
		var id string
		var up, rec, conf int
		if e = rows.Scan(&id, &up, &rec, &conf); e != nil {
			rows.Close()
			return e
		}
		r := byID[id]
		r.Summary.Upcoming = up
		r.Summary.Recorded = rec
		r.Summary.Conflicts = conf
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	predicate, e := programmePredicateTx(ctx, tx, o)
	if e != nil {
		return e
	}
	now := s.now()
	end := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()).UTC()
	// Each rule uses its indexed identity/channel relation. At most fifty rules
	// are on a page, and only one matching programme becomes a next-airing row.
	for i := range rules {
		r := &rules[i]
		if !allowed(r.Config.SourceID, "") {
			continue
		}
		where, args := r.Config.identitySQL()
		args = append([]any{r.Config.SourceID}, args...)
		args = append(args, now.UTC().Format(time.RFC3339))
		rows, e := tx.QueryContext(ctx, programmeQuery+` JOIN live_sources src ON src.active_generation=p.generation_id WHERE src.id=? AND src.state='active' AND `+where+` AND p.end_utc>? ORDER BY p.start_utc,p.id`, args...)
		if e != nil {
			return e
		}
		config := r.Config
		config.Enabled = true
		for rows.Next() {
			p, e := scanProgramme(rows)
			if e != nil {
				rows.Close()
				return e
			}
			if !allowed(r.Config.SourceID, p.ChannelID) || !predicate(programmeRating(p)) || !config.Matches(p) {
				continue
			}
			if r.Summary.Next == nil {
				v := p
				r.Summary.Next = &v
				r.Summary.Channel.ID = p.ChannelID
			}
			start, _ := time.Parse(time.RFC3339Nano, p.Start)
			if start.Before(end) {
				r.Summary.MatchingTonight++
			} else {
				break
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if r.Summary.Next != nil {
			record := Recording{Occurrence: Occurrence{SourceID: r.Config.SourceID, ChannelID: r.Summary.Next.ChannelID}}
			if e = channelTx(ctx, tx, &record); e != nil {
				return e
			}
			r.Summary.Channel = record.Channel
		}
	}
	return nil
}

func overlapsTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, r Recording, allowed func(string) bool, limit int) ([]Overlap, error) {
	out := []Overlap{}
	start, _ := time.Parse(time.RFC3339Nano, r.Start)
	end, _ := time.Parse(time.RFC3339Nano, r.End)
	rows, e := tx.QueryContext(ctx, `SELECT id,programme_json,start_ms,end_ms,priority,state,rule_id,channel_id FROM dvr_recordings WHERE owner_key=? AND source_id=? AND id<>? AND start_ms<? AND end_ms>? AND state IN('scheduled','conflicted','preparing','recording','finalizing') ORDER BY priority DESC,start_ms,id LIMIT ?`, o.Key(), r.Occurrence.SourceID, r.ID, end.UnixMilli(), start.UnixMilli(), limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var v Overlap
		var raw string
		var start, end int64
		if e = rows.Scan(&v.ID, &raw, &start, &end, &v.Priority, &v.State, &v.RuleID, &v.Channel.ID); e != nil {
			return nil, e
		}
		p, e := decodeProgramme(raw)
		if e != nil {
			return nil, e
		}
		p.Admit(allowed)
		v.Title = p.Title
		v.Episode = p.Episode
		v.Start = time.UnixMilli(start).UTC().Format(time.RFC3339Nano)
		v.End = time.UnixMilli(end).UTC().Format(time.RFC3339Nano)
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	rows.Close()
	for i := range out {
		r := Recording{Occurrence: Occurrence{SourceID: r.Occurrence.SourceID, ChannelID: out[i].Channel.ID, Generation: r.Occurrence.Generation}}
		if e = channelTx(ctx, tx, &r); e != nil {
			return nil, e
		}
		out[i].Channel = r.Channel
	}
	return out, nil
}

// ImageRendition names the stored small or large rendition of a recording's
// programme image after the recording's authority has been checked.
func (s *Store) ImageRendition(ctx context.Context, a livechannels.Authority, o livechannels.Owner, id string, small bool) (string, string, error) {
	digest, mime := "", ""
	e := s.snapshot(ctx, a, o, func(tx *sql.Tx, allowed func(string, string) bool) error {
		r, e := loadTx(ctx, tx, o, id)
		if e != nil {
			return e
		}
		if !allowed(r.Occurrence.SourceID, r.Occurrence.ChannelID) {
			return ErrDenied
		}
		if e = programmeVisibleTx(ctx, tx, o, r.Programme); e != nil {
			return e
		}
		if r.State == "deleted" {
			return ErrDenied
		}
		return tx.QueryRowContext(ctx, `SELECT CASE WHEN ? THEN img.small_digest ELSE img.large_digest END,img.media_type FROM dvr_programme_images d JOIN live_programme_images img ON img.url=d.url WHERE d.recording_id=?`, small, id).Scan(&digest, &mime)
	})
	return digest, mime, e
}
