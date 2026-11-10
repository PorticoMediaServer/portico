package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Condition is one filter on a list: a field, some values, and how the values combine.
// Conditions combine with each other as "all of them".
type Condition struct {
	Field string
	// Mode is ModeAny (the default), ModeAll or ModeNone: an item passes when it has any of
	// the values, every one of them, or none of them.
	Mode   string
	Values []string
}

// How the values of one condition combine.
const (
	ModeAny  = "any"
	ModeAll  = "all"
	ModeNone = "none"
)

// The most a list's filter may hold.
const (
	MaxConditions      = 40
	MaxConditionValues = 100
)

// ErrFilterTooLong: the filter has more conditions, or one has more values, than a list takes.
var ErrFilterTooLong = errors.New("filter too long")

// The fields a condition can name, by what a value looks like.
var (
	// A tag by its id, or for a kind by its name ("genre=Drama").
	conditionTags = map[string]string{"genre": TagGenre, "studio": TagStudio, "network": TagNetwork, "label": TagLabel, "tag": ""}
	// A person by id, in one role or (person) in any.
	conditionPeople = map[string]CreditRole{"actor": RoleActor, "director": RoleDirector, "writer": RoleWriter, "producer": RoleProducer,
		"composer": RoleComposer, "author": RoleAuthor, "narrator": RoleNarrator, "artist": RoleArtist, "person": 0}
	// A number or a range of numbers: "7", "7..9", "7..", "..9".
	conditionNumbers = map[string]string{
		"communityRating": "i.community_rating", "criticRating": "i.critic_rating", "durationMs": "i.duration_ms",
		"userRating": stateValue("rating"), "playCount": "ifnull(" + stateValue("play_count") + ", 0)",
		"versions": "(SELECT count(*) FROM media m WHERE m.item_id = i.id)",
	}
	// A day or a range of days: "2024-03-01", "2024-01-01..2024-12-31", "2024-01-01..".
	conditionDays = map[string]string{"added": "i.added_at", "lastPlayed": stateValue("last_played_at"), "released": "i.release_date"}
	// true or false.
	conditionFlags = map[string]string{
		"available":  "i.available = 1",
		"inProgress": "EXISTS (SELECT 1 FROM user_item_state p WHERE p.profile_id = @viewer_profile AND p.item_id = i.id AND p.played = 0 AND (p.position_ms > 0 OR p.position_text IS NOT NULL))",
	}
)

// stateValue reads one column of the viewer's own state for the item.
func stateValue(column string) string {
	return "(SELECT st." + column + " FROM user_item_state st WHERE st.profile_id = @viewer_profile AND st.item_id = i.id)"
}

// ConditionFields lists every field a condition can name.
var ConditionFields = []string{"genre", "studio", "network", "label", "tag", "actor", "director", "writer", "producer", "composer", "author",
	"narrator", "artist", "person", "year", "contentRating", "resolution", "dynamicRange", "audioLanguage", "subtitleLanguage",
	"communityRating", "criticRating", "durationMs", "userRating", "playCount", "versions", "added", "lastPlayed", "released",
	"available", "inProgress"}

func span(value string) (from, to string, ranged bool) {
	from, to, ranged = strings.Cut(value, "..")
	if !ranged {
		to = from
	}
	return from, to, ranged
}

// between writes "expr is within from..to" for a range either end of which may be open.
func between(w *query, expr string, from, to any, hasFrom, hasTo bool) string {
	var parts []string
	if hasFrom {
		parts = append(parts, expr+" >= "+w.arg(from))
	}
	if hasTo {
		parts = append(parts, expr+" <= "+w.arg(to))
	}
	if len(parts) == 0 {
		return expr + " IS NOT NULL"
	}
	return "(" + strings.Join(parts, " AND ") + ")"
}

// hasStream is the test that some version of the item has a track of this kind in a language,
// inside one of its files or as a file beside it.
const hasStream = `EXISTS (SELECT 1 FROM media m WHERE m.item_id = i.id AND (
	EXISTS (SELECT 1 FROM media_parts p JOIN media_streams t ON t.file_id = p.file_id WHERE p.media_id = m.id AND t.kind = %[1]d AND t.language = %[2]s)
	OR EXISTS (SELECT 1 FROM files f JOIN media_streams t ON t.file_id = f.id WHERE f.media_id = m.id AND t.kind = %[1]d AND t.language = %[2]s)))`

// conditions adds every condition of the list to its statement. The first condition that
// asks for a tag or a person becomes what the list is read from when it is rare, exactly as
// a single tag does (see ItemQuery.filters), so a narrow filter on a large library reads
// what matches and not the library. It reports false when nothing can match.
func (q ItemQuery) conditions(ctx context.Context, tx *sql.Tx, w *query, scoped *bool) (bool, error) {
	if len(q.Conditions) > MaxConditions {
		return false, ErrFilterTooLong
	}
	driven := q.Tag != "" || q.Genre != "" || q.Person != ""
	for _, c := range q.Conditions {
		if len(c.Values) > MaxConditionValues {
			return false, ErrFilterTooLong
		}
		if len(c.Values) == 0 {
			continue
		}
		if c.Mode == "" {
			c.Mode = ModeAny
		}
		if c.Mode != ModeAny && c.Mode != ModeAll && c.Mode != ModeNone {
			return false, invalid("a filter combines its values as any, all or none, not %q", c.Mode)
		}
		var tests []string
		tagKind, isTag := conditionTags[c.Field]
		role, isPerson := conditionPeople[c.Field]
		switch {
		case isTag:
			var tags []int64
			rarest, fewest, most := int64(0), rareTag, 0
			for _, value := range c.Values {
				var tag int64
				var uses int
				err := tx.QueryRowContext(ctx, `SELECT id, (SELECT count(*) FROM (SELECT 1 FROM item_tags WHERE tag_id = tags.id LIMIT ?))
					FROM tags WHERE (public_id = ?2 OR (?3 <> '' AND norm_name = ?4)) AND (?3 = '' OR kind = ?3)`,
					rareTag, value, tagKind, normTag(value)).Scan(&tag, &uses)
				if errors.Is(err, sql.ErrNoRows) {
					tests = append(tests, "0") // no such tag: nothing carries it
					continue
				}
				if err != nil {
					return false, err
				}
				tags, most = append(tags, tag), max(most, uses)
				if uses < fewest {
					rarest, fewest = tag, uses
				}
				// An item matches a tag it carries, or one the title it belongs to carries.
				tests = append(tests, fmt.Sprintf(`EXISTS (SELECT 1 FROM item_tags t WHERE t.tag_id = %d AND t.item_id IN (i.id, i.root_id))`, tag))
			}
			// Read from the tags when that is the shorter road: every tag of an "any" is
			// rare, or one tag of an "all" is.
			from := tags
			if c.Mode == ModeAll && rarest != 0 {
				from = []int64{rarest}
			}
			if !driven && c.Mode != ModeNone && len(tags) == len(c.Values) && (c.Mode == ModeAll && rarest != 0 || c.Mode == ModeAny && len(tags)*most < rareTag) {
				list := intList(from)
				w.add(` AND i.id IN (SELECT item_id FROM item_tags WHERE tag_id IN `, list, ` UNION
					SELECT d.id FROM item_tags h CROSS JOIN items d WHERE h.tag_id IN `, list, ` AND d.root_id = h.item_id)`)
				w.from, w.countFrom, driven = "items i NOT INDEXED", "items i NOT INDEXED", true
			}
			if c.Mode != ModeNone {
				*scoped = true
			}
		case isPerson:
			var people []int64
			in := ""
			if role != 0 {
				in = fmt.Sprintf(" AND c.role = %d", role)
			}
			for _, value := range c.Values {
				var person int64
				err := tx.QueryRowContext(ctx, `SELECT id FROM people WHERE public_id = ?`, value).Scan(&person)
				if errors.Is(err, sql.ErrNoRows) {
					tests = append(tests, "0")
					continue
				}
				if err != nil {
					return false, err
				}
				people = append(people, person)
				tests = append(tests, fmt.Sprintf(`EXISTS (SELECT 1 FROM credits c WHERE c.item_id = i.id AND c.person_id = %d%s)`, person, in))
			}
			// One person's credits are few beside a library, so the list is read from them.
			if !driven && c.Mode != ModeNone && len(people) > 0 && len(people) == len(c.Values) {
				from := people
				if c.Mode == ModeAll {
					from = people[:1]
				}
				w.add(` AND i.id IN (SELECT c.item_id FROM credits c WHERE c.person_id IN `, intList(from), in, `)`)
				w.from, w.countFrom, driven = "items i NOT INDEXED", "items i NOT INDEXED", true
			}
			if c.Mode != ModeNone {
				*scoped = true
			}
		default:
			for _, value := range c.Values {
				test, err := conditionTest(w, c.Field, value)
				if err != nil {
					return false, err
				}
				tests = append(tests, test)
			}
		}
		switch c.Mode {
		case ModeAll:
			w.add(` AND (`, strings.Join(tests, " AND "), `)`)
		case ModeNone:
			w.add(` AND NOT (`, strings.Join(tests, " OR "), `)`)
		default:
			w.add(` AND (`, strings.Join(tests, " OR "), `)`)
		}
	}
	return true, nil
}

// conditionTest writes the test for one value of a field that is neither a tag nor a person.
func conditionTest(w *query, field, value string) (string, error) {
	from, to, _ := span(value)
	switch field {
	case "year":
		first, errFrom := strconv.Atoi(from)
		last, errTo := strconv.Atoi(to)
		if errFrom != nil || errTo != nil || first > last || first < 1 {
			return "", invalid(`a year is "1999" or a range such as "1990..1999", not %q`, value)
		}
		// "{+}" keeps a page from being found through the year index. See ItemQuery.filters.
		return `({+}i.year >= ` + w.arg(first) + ` AND {+}i.year <= ` + w.arg(last) + `)`, nil
	case "contentRating":
		return `i.content_rating = ` + w.arg(value), nil
	case "resolution":
		class, ok := resolutionClasses[value]
		if !ok {
			return "", invalid("unknown resolution %q", value)
		}
		return `EXISTS (SELECT 1 FROM media m WHERE m.item_id = i.id AND m.width IS NOT NULL AND ` + resolutionSQL + ` = ` + fmt.Sprint(class) + `)`, nil
	case "dynamicRange":
		// A version with no range recorded is standard range.
		return `EXISTS (SELECT 1 FROM media m WHERE m.item_id = i.id AND ifnull(nullif(m.hdr, ''), 'sdr') = ` + w.arg(strings.ToLower(value)) + `)`, nil
	case "audioLanguage":
		return fmt.Sprintf(hasStream, StreamAudio, w.arg(value)), nil
	case "subtitleLanguage":
		return fmt.Sprintf(hasStream, StreamSubtitle, w.arg(value)), nil
	}
	if expr, ok := conditionNumbers[field]; ok {
		low, errFrom := strconv.ParseFloat(from, 64)
		high, errTo := strconv.ParseFloat(to, 64)
		if from != "" && errFrom != nil || to != "" && errTo != nil || from == "" && to == "" {
			return "", invalid(`%s takes a number or a range such as "7..9", not %q`, field, value)
		}
		return between(w, expr, low, high, from != "", to != ""), nil
	}
	if expr, ok := conditionDays[field]; ok {
		var first, last time.Time
		var errFrom, errTo error
		if from != "" {
			first, errFrom = time.Parse(time.DateOnly, from)
		}
		if to != "" {
			last, errTo = time.Parse(time.DateOnly, to)
		}
		if errFrom != nil || errTo != nil || from == "" && to == "" {
			return "", invalid(`%s takes a day or a range such as "2024-01-01..2024-12-31", not %q`, field, value)
		}
		if field == "released" {
			return between(w, expr, from, to, from != "", to != ""), nil // stored as a day
		}
		// Stored as a moment: the range runs to the end of its last day.
		return between(w, expr, first.UnixMilli(), last.AddDate(0, 0, 1).UnixMilli()-1, from != "", to != ""), nil
	}
	if test, ok := conditionFlags[field]; ok {
		switch value {
		case "true":
			return test, nil
		case "false":
			return "NOT " + test, nil
		}
		return "", invalid("%s is true or false, not %q", field, value)
	}
	return "", invalid("unknown filter %q", field)
}
