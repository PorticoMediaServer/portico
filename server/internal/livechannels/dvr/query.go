package dvr

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

type Query struct {
	Sort   string
	Group  string
	State  string
	Cursor string
	Limit  int
}
type continuation struct {
	Owner, Fence, State, AfterID string
	Sort, Group, AfterText       string
	AfterBucket                  int
	AfterMS, Revision, Expiry    int64
	Limit                        int
}

func (s *Store) cursor(v continuation) string {
	b, _ := json.Marshal(v)
	h := hmac.New(sha256.New, s.live.ContinuationKey("dvr-pages"))
	h.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (s *Store) decodeCursor(text string) (continuation, error) {
	var v continuation
	if len(text) > 4096 {
		return v, ErrInvalid
	}
	parts := strings.Split(text, ".")
	if len(parts) != 2 {
		return v, ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return v, ErrInvalid
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return v, ErrInvalid
	}
	h := hmac.New(sha256.New, s.live.ContinuationKey("dvr-pages"))
	h.Write(b)
	if !hmac.Equal(sig, h.Sum(nil)) || json.Unmarshal(b, &v) != nil {
		return v, ErrInvalid
	}
	return v, nil
}
func (s *Store) List(ctx context.Context, a livechannels.Authority, o livechannels.Owner, q Query) (Page, error) {
	if q.Sort == "" {
		q.Sort = "recent"
	}
	out := Page{DeletionAvailable: s.driver != nil && s.driver.RetirementAvailable(), Recordings: []Recording{}, Rules: []Rule{}, CaptureAvailable: s.captureAvailable}
	if !s.captureAvailable {
		out.CaptureUnavailableReason = "capture-unavailable"
	}
	if (q.Sort != "recent" && q.Sort != "oldest" && q.Sort != "title") || len(q.Group) > 1200 {
		return out, ErrInvalid
	}
	if q.Limit < 1 || q.Limit > 50 || (q.State != "all" && q.State != "upcoming" && q.State != "recorded" && q.State != "history" && q.State != "rules") {
		return out, ErrInvalid
	}
	e := s.snapshot(ctx, a, o, func(tx *sql.Tx, allowed func(string, string) bool) error {
		sources, scopeErr := recordingSourcesTx(ctx, tx, o, allowed)
		if scopeErr != nil {
			return scopeErr
		}
		var usageErr error
		out.Usage, usageErr = usageTx(ctx, tx, o)
		if usageErr != nil {
			return usageErr
		}
		fence, _, e := a(ctx, tx, false)
		if e != nil {
			return e
		}
		if q.State == "upcoming" {
			reservationFence, err := reservationFenceTx(ctx, tx, sources)
			if err != nil {
				return err
			}
			fence += ":" + reservationFence
		}
		e = tx.QueryRowContext(ctx, `SELECT revision FROM dvr_owner_revisions WHERE owner_key=?`, o.Key()).Scan(&out.Revision)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		c := continuation{Owner: o.Key(), Fence: fence, State: q.State, Limit: q.Limit, Revision: out.Revision, Expiry: s.now().Add(15 * time.Minute).UnixMilli(), Sort: q.Sort, Group: q.Group, AfterMS: -(1 << 62), AfterBucket: -1}
		if q.Cursor != "" {
			old, e := s.decodeCursor(q.Cursor)
			if e != nil {
				return e
			}
			if old.Owner != c.Owner || old.Fence != c.Fence || old.State != c.State || old.Sort != q.Sort || old.Group != q.Group || old.Limit != c.Limit || old.Revision != c.Revision || old.Expiry <= s.now().UnixMilli() {
				return livechannels.ErrCursor
			}
			c = old
		}
		if q.State == "rules" {
			rows, e := tx.QueryContext(ctx, `SELECT `+ruleColumns+` FROM dvr_rules r LEFT JOIN dvr_rule_work w ON w.rule_id=r.id WHERE r.owner_key=? AND r.deleted=0 AND r.source_id IN(SELECT value FROM json_each(?)) AND r.id>? ORDER BY r.id LIMIT ?`, o.Key(), sources, c.AfterID, q.Limit+1)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				r, e := scanRule(rows)
				if e != nil {
					return e
				}
				out.Rules = append(out.Rules, r)
			}
			if e = rows.Err(); e != nil {
				return e
			}
			if len(out.Rules) > q.Limit {
				out.Rules = out.Rules[:q.Limit]
				c.AfterID = out.Rules[len(out.Rules)-1].ID
				out.NextCursor = s.cursor(c)
			}
			if e = s.ruleSummariesTx(ctx, tx, o, out.Rules, allowed); e != nil {
				return e
			}
			refs := make([]recordingChannelRef, len(out.Rules))
			for i := range out.Rules {
				refs[i] = recordingChannelRef{out.Rules[i].Config.SourceID, &out.Rules[i].Summary.Channel}
			}
			return s.recordingChannelMarksTx(ctx, tx, a, o, refs)
		}
		where := `state!='deleted'`
		switch q.State {
		case "upcoming":
			where = `state IN('scheduled','conflicted','waiting-source','waiting-guide','preparing','recording','finalizing')`
		case "recorded":
			where = `state IN('completed','incomplete-playable')`
		case "history":
			where = `state IN('failed','cancelled','pending-delete')`
		}
		bucket := "0"
		sortMS := "-r.created_ms"
		if q.State == "recorded" {
			sortMS = "-COALESCE(NULLIF(r.finished_ms,0),r.created_ms)"
		}
		sortText := "''"
		if q.Sort == "oldest" {
			sortMS = "r.created_ms"
			if q.State == "recorded" {
				sortMS = "COALESCE(NULLIF(r.finished_ms,0),r.created_ms)"
			}
		}
		if q.Sort == "title" {
			sortMS = "0"
			sortText = "lower(json_extract(r.programme_json,'$.title'))"
		}
		if q.State == "upcoming" {
			bucket = "CASE WHEN " + currentConflictSQL + " THEN 0 ELSE 1 END"
			sortMS = "r.start_ms"
			sortText = "''"
		}
		rows, e := tx.QueryContext(ctx, `WITH dvr_recordings AS (SELECT r.*,`+bucket+` sort_bucket,`+sortMS+` sort_ms,`+sortText+` sort_text,`+recordingGroupKeySQL+` group_key FROM main.dvr_recordings r WHERE owner_key=? AND source_id IN(SELECT value FROM json_each(?))) SELECT `+recordingColumns+`,sort_ms,sort_bucket,sort_text FROM dvr_recordings WHERE `+where+` AND (?='' OR group_key=?) AND (sort_bucket,sort_ms,sort_text,id)>(?,?,?,?) ORDER BY sort_bucket,sort_ms,sort_text,id LIMIT ?`, o.Key(), sources, q.Group, q.Group, c.AfterBucket, c.AfterMS, c.AfterText, c.AfterID, q.Limit+1)
		if e != nil {
			return e
		}
		// Include the sort key without inventing client-computed wall-time ordering.
		created := []int64{}
		buckets := []int{}
		texts := []string{}
		for rows.Next() {
			var r Recording
			var p, options string
			var start, end, ca, cb, at int64
			var bucket int
			var sortText string
			e = rows.Scan(&r.ID, &r.Revision, &r.Occurrence.SourceID, &r.Occurrence.ChannelID, &r.Occurrence.Generation, &r.Occurrence.ProgrammeID, &p, &r.RuleID, &options, &start, &end, &r.State, &r.Reason, &r.Keep, &r.ItemID, &ca, &cb, &r.Bytes, &at, &bucket, &sortText)
			if e != nil {
				rows.Close()
				return e
			}
			if json.Unmarshal([]byte(p), &r.Programme) != nil || json.Unmarshal([]byte(options), &r.Options) != nil {
				rows.Close()
				return ErrUnavailable
			}
			setSeriesGrouping(&r)
			r.Start = time.UnixMilli(start).UTC().Format(time.RFC3339Nano)
			r.End = time.UnixMilli(end).UTC().Format(time.RFC3339Nano)
			if ca > 0 {
				r.CoverageStart = time.UnixMilli(ca).UTC().Format(time.RFC3339Nano)
			}
			if cb > 0 {
				r.CoverageEnd = time.UnixMilli(cb).UTC().Format(time.RFC3339Nano)
			}
			if r.ItemID != "" {
				r.LibraryID = digest([]string{"private-recorded-library-v1", o.Key()})
			}
			r.Conflicts = []livechannels.LosingInterval{}
			out.Recordings = append(out.Recordings, r)
			created = append(created, at)
			buckets = append(buckets, bucket)
			texts = append(texts, sortText)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(out.Recordings) > q.Limit {
			out.Recordings = out.Recordings[:q.Limit]
			c.AfterID = out.Recordings[q.Limit-1].ID
			c.AfterMS = created[q.Limit-1]
			c.AfterBucket = buckets[q.Limit-1]
			c.AfterText = texts[q.Limit-1]
			out.NextCursor = s.cursor(c)
		}
		for i := range out.Recordings {
			if e = decorateTx(ctx, tx, o, &out.Recordings[i]); e != nil {
				return e
			}
		}
		programmeAllowed, err := programmePredicateTx(ctx, tx, o)
		if err != nil {
			return err
		}
		for i := range out.Recordings {
			restrictRecording(&out.Recordings[i], programmeAllowed)
		}
		for i, r := range out.Recordings {
			if mutable(r.State) {
				intervals, e := conflictsTx(ctx, tx, r)
				if errors.Is(e, livechannels.ErrConflict) {
					continue
				}
				if e != nil {
					return e
				}
				out.Recordings[i].Conflicts = intervals
				if len(intervals) > 0 && r.State == "scheduled" {
					out.Recordings[i].State = "conflicted"
					out.Recordings[i].Reason = "tuner-conflict"
				}
			}
		}
		for i := range out.Recordings {
			if len(out.Recordings[i].Conflicts) > 0 {
				overlaps, e := overlapsTx(ctx, tx, o, out.Recordings[i], programmeAllowed, 3)
				if e != nil {
					return e
				}
				out.Recordings[i].Overlaps = overlaps
			}
		}
		return s.recordingChannelMarksTx(ctx, tx, a, o, recordingChannelRefs(out.Recordings))
	})
	return out, e
}

// Other owners can change a shared tuner's competition without changing this
// owner's revision. Bind upcoming paging to that exact reservation projection.
func reservationFenceTx(ctx context.Context, tx *sql.Tx, sources string) (string, error) {
	rows, e := tx.QueryContext(ctx, `SELECT v.id,v.source_id,v.start_ms,v.end_ms,v.priority,v.enabled,COALESCE(r.channel_id,v.id) FROM live_reservations v LEFT JOIN dvr_recordings r ON r.id=v.id WHERE v.source_id IN(SELECT value FROM json_each(?)) ORDER BY v.id`, sources)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var id, source, channel string
		var start, end, priority, enabled int64
		if e = rows.Scan(&id, &source, &start, &end, &priority, &enabled, &channel); e != nil {
			return "", e
		}
		raw, _ := json.Marshal([]any{id, source, start, end, priority, enabled, channel})
		h.Write(raw)
	}
	if e = rows.Err(); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)), nil
}
