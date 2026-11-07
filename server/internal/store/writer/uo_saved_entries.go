package writer

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type uoSavedAddition struct {
	subject, library, revision, admission int64
	public                                [16]byte
	id                                    string
}
type uoSavedEntry struct {
	id, key string
	item    sql.NullInt64
}

// Every text copy is preceded by the exact same point's octet_length in this
// turn. Parent must reserve the returned actual bytes through GV/W admission.
// The cap bounds pathological rank growth rather than silently truncating it.
func readUOSavedEntry(ctx context.Context, t *commandTurn, budget uoSavedAuthority, point string, args ...any) (out uoSavedEntry, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if budget == nil {
		return out, ErrInvalid
	}
	switch point {
	case "entry", "last", "next", "undo":
	default:
		return out, ErrInvalid
	}
	n, e := wc008DraftInteger(ctx, t, stmt.ID("uo.saved."+point+"_bytes"), args...)
	if e != nil {
		return out, e
	}
	if n < 0 || n > 4224 {
		return out, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	if err = budget.reserveSavedBytes(ctx, t, n); err != nil {
		return out, err
	}
	row, e := wc008DraftQuery(ctx, t, stmt.ID("uo.saved."+point+"_value"), args...)
	if e != nil {
		return out, e
	}
	err = row.Scan(&out.id, &out.key, &out.item)
	if err == nil && (!uoPersonalText(out.id, 128) || !uoSavedRankValid(out.key)) {
		return out, ErrResolveRequired
	}
	return out, err
}
func uoSavedRankValid(s string) bool {
	if len(s) > 4096 {
		return false
	}
	for i := range s {
		if !strings.ContainsRune(uoSavedDigits, rune(s[i])) {
			return false
		}
	}
	return s == "" || s[len(s)-1] != '0'
}

const uoSavedDigits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func uoSavedBetween(lower, upper string) string {
	prefix := ""
	for len(prefix) <= 4096 {
		lo, hi := 0, len(uoSavedDigits)
		if lower != "" {
			lo = strings.IndexByte(uoSavedDigits, lower[0])
		}
		if upper != "" {
			hi = strings.IndexByte(uoSavedDigits, upper[0])
		}
		if hi-lo > 1 {
			return prefix + string(uoSavedDigits[(lo+hi)/2])
		}
		prefix += string(uoSavedDigits[lo])
		if lower != "" {
			lower = lower[1:]
		}
		if hi == lo {
			upper = upper[1:]
		} else {
			upper = ""
		}
	}
	return ""
}
func uoSavedRank(lo, hi string, i, n int) string {
	m := n / 2
	k := uoSavedBetween(lo, hi)
	if i == m {
		return k
	}
	if i < m {
		return uoSavedRank(lo, k, i, m)
	}
	return uoSavedRank(k, hi, i-m-1, n-m-1)
}
func applyUOSavedEntries(ctx context.Context, t *commandTurn, q uoSavedInput) (out uoSavedEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out.id = q.id
	if q.action == uoSavedReorder {
		if len(q.order) < 1 || len(q.order) > 200 {
			return out, ErrInvalid
		}
		seen := map[string]bool{}
		keys := make([]string, len(q.order))
		for i, id := range q.order {
			if !uoPersonalText(id, 128) || seen[id] {
				return out, ErrInvalid
			}
			seen[id] = true
			r, e := readUOSavedEntry(ctx, t, q.security, "entry", q.id, id)
			if e != nil {
				return out, e
			}
			keys[i] = r.key
		}
		if q.after == nil {
			sort.Strings(keys)
			for i := 1; i < len(keys); i++ {
				if keys[i] == keys[i-1] {
					return out, ErrResolveRequired
				}
			}
		} else {
			if seen[*q.after] {
				return out, ErrInvalid
			}
			lower, upper := "", ""
			if *q.after != "" {
				r, e := readUOSavedEntry(ctx, t, q.security, "entry", q.id, *q.after)
				if e != nil {
					return out, e
				}
				lower = r.key
			}
			cursor := lower
			cursorID := *q.after
			// At most 200 moved rows plus one surviving neighbor. No collection scan.
			for i := 0; i <= len(q.order); i++ {
				r, e := readUOSavedEntry(ctx, t, q.security, "next", q.id, cursor, cursorID)
				if errors.Is(e, ErrConflict) {
					break
				}
				if e != nil {
					return out, e
				}
				if r.key < cursor || r.key == cursor && r.id <= cursorID {
					return out, ErrResolveRequired
				}
				if !seen[r.id] {
					upper = r.key
					if upper <= lower {
						return out, ErrResolveRequired
					}
					break
				}
				cursor = r.key
				cursorID = r.id
			}
			for i := range keys {
				keys[i] = uoSavedRank(lower, upper, i, len(keys))
				if !uoSavedRankValid(keys[i]) || keys[i] == "" {
					return out, ErrInvalid
				}
			}
		}
		for i, id := range q.order {
			if _, err = wc008DraftInteger(ctx, t, "uo.saved.order", keys[i], q.id, id); err != nil {
				return out, err
			}
		}
		return out, nil
	}
	if q.action == uoSavedUndo {
		if !uoPersonalText(q.token, 128) || !uoPersonalText(q.owner, 256) {
			return out, ErrInvalid
		}
		cursor := ""
		for i := 0; i <= 100; i++ {
			r, e := readUOSavedEntry(ctx, t, q.security, "undo", q.token, q.owner, q.id, q.nowMS, cursor)
			if errors.Is(e, ErrConflict) {
				if i == 0 {
					return out, ErrConflict
				}
				break
			}
			if e != nil {
				return out, e
			}
			if i == 100 {
				return out, ErrResolveRequired
			}
			cursor = r.id
			status := "unchanged"
			// NULL/retired subjects cannot be resurrected. US rechecks restrictions for
			// each retained item; no historical grant is treated as current authority.
			if !r.item.Valid {
				_, e = wc008DraftInteger(ctx, t, "uo.saved.insert", r.id, q.id, nil, r.key)
				if e == nil {
					status = "added"
				} else if !errors.Is(e, ErrConflict) {
					return out, e
				}
			} else {
				live, e := wc008DraftInteger(ctx, t, "uo.saved.undo_live", r.item.Int64)
				if e != nil {
					return out, e
				}
				if live != 1 {
					return out, ErrConflict
				}
				a := uoSavedAddition{subject: r.item.Int64, id: r.id}
				if err = q.security.checkSavedItem(ctx, t, a); err != nil {
					return out, err
				}
				_, e = wc008DraftInteger(ctx, t, "uo.saved.insert", r.id, q.id, r.item.Int64, r.key)
				if e == nil {
					status = "added"
				} else if !errors.Is(e, ErrConflict) {
					return out, e
				}
			}
			if _, err = wc008DraftInteger(ctx, t, "uo.saved.undo_spend", q.token, r.id, q.owner, q.id, q.nowMS); err != nil {
				return out, err
			}
			out.outcomes = append(out.outcomes, uoSavedOutcome{r.id, r.id, status})
		}
		return out, nil
	}
	if len(q.additions) > 100 || len(q.removals) > 100 || len(q.additions)+len(q.removals) == 0 {
		return out, ErrInvalid
	}
	if len(q.removals) > 0 && (!uoPersonalText(q.token, 128) || !uoPersonalText(q.owner, 256)) {
		return out, ErrInvalid
	}
	for _, a := range q.additions {
		if a.subject <= 0 || a.library <= 0 || a.revision <= 0 || a.admission <= 0 || !uoPersonalText(a.id, 128) {
			return out, ErrInvalid
		}
		if err = q.security.checkSavedItem(ctx, t, a); errors.Is(err, sql.ErrNoRows) {
			out.outcomes = append(out.outcomes, uoSavedOutcome{input: hex.EncodeToString(a.public[:]), status: "not_found"})
			err = nil
			continue
		} else if err != nil {
			return out, err
		}
		live, e := wc008DraftInteger(ctx, t, "uo.saved.subject", a.subject, a.public[:], a.library, a.revision, a.admission)
		if e != nil {
			return out, e
		}
		status := "not_found"
		if live == 1 {
			n, e := wc008DraftInteger(ctx, t, "uo.saved.member", q.id, a.subject)
			if e != nil {
				return out, e
			}
			status = "unchanged"
			if n == 0 {
				last, e := readUOSavedEntry(ctx, t, q.security, "last", q.id)
				if e != nil && !errors.Is(e, ErrConflict) {
					return out, e
				}
				key := uoSavedBetween(last.key, "")
				if key == "" || len(key) > 4096 {
					return out, ErrInvalid
				}
				if _, err = wc008DraftInteger(ctx, t, "uo.saved.insert", a.id, q.id, a.subject, key); err != nil {
					return out, err
				}
				status = "added"
			}
		}
		generated := ""
		if status == "added" {
			generated = a.id
		}
		out.outcomes = append(out.outcomes, uoSavedOutcome{input: hex.EncodeToString(a.public[:]), id: generated, status: status})
	}
	// One live token per actor/list. Retire at most the public 100-row prior
	// token; refuse corrupt oversized debt rather than sweeping it in this turn.
	if len(q.removals) > 0 {
		for i := 0; i <= 100; i++ {
			n, e := wc008DraftInteger(ctx, t, "uo.saved.old_undo_bytes", q.owner, q.id)
			if errors.Is(e, ErrConflict) {
				break
			}
			if e != nil {
				return out, e
			}
			if i == 100 || n < 0 {
				return out, ErrResolveRequired
			}
			if _, err = wc008DraftInteger(ctx, t, "uo.saved.old_undo_remove", q.owner, q.id); err != nil {
				return out, err
			}
			out.reclaimedRows++
			out.reclaimedBytes += n
		}
	}
	for _, id := range q.removals {
		if !uoPersonalText(id, 128) {
			return out, ErrInvalid
		}
		r, e := readUOSavedEntry(ctx, t, q.security, "entry", q.id, id)
		if errors.Is(e, ErrConflict) {
			out.outcomes = append(out.outcomes, uoSavedOutcome{id, id, "not_found"})
			continue
		}
		if e != nil {
			return out, e
		}
		if q.nowMS > math.MaxInt64-30000 {
			return out, ErrInvalid
		}
		if _, err = wc008DraftInteger(ctx, t, "uo.saved.undo_put", q.token, q.owner, q.id, r.id, uoSavedItem(r.item), r.key, q.nowMS+30000); err != nil {
			return out, err
		}
		if _, err = wc008DraftInteger(ctx, t, "uo.saved.remove", q.id, id); err != nil {
			return out, err
		}
		out.undoToken = q.token
		out.undoUntil = q.nowMS + 30000
		out.outcomes = append(out.outcomes, uoSavedOutcome{id, id, "removed"})
	}
	return out, nil
}

func uoSavedItem(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}
