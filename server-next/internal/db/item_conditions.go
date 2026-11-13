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
	conditionTags = map[string]string{"genre": TagGenre, "studio": TagStudio, "network": TagNetwork, "label": TagLabel, "tag": "",
		"country": TagCountry, "mood": TagMood}
	// A person by id, in one role or (person) in any.
	conditionPeople = map[string]CreditRole{"actor": RoleActor, "director": RoleDirector, "writer": RoleWriter, "producer": RoleProducer,
		"composer": RoleComposer, "author": RoleAuthor, "narrator": RoleNarrator, "artist": RoleArtist, "person": 0}
	// A number or a range of numbers: "7", "7..9", "7..", "..9".
	conditionNumbers = map[string]string{
		"communityRating": "i.community_rating", "criticRating": "i.critic_rating", "durationMs": "i.duration_ms", "wordCount": "i.word_count",
		"userRating": stateValue("rating"), "playCount": "ifnull(" + stateValue("play_count") + ", 0)",
		"versions": "(SELECT count(*) FROM media m WHERE m.item_id = i.id)",
	}
	// A day or a range of days: "2024-03-01", "2024-01-01..2024-12-31", "2024-01-01..".
	conditionDays = map[string]string{"added": "i.added_at", "lastPlayed": stateValue("last_played_at"), "released": "i.release_date"}
	// true or false.
	conditionFlags = map[string]string{
		"available": "i.available = 1",
		"inProgress": fmt.Sprintf(`i.id IN (SELECT p.item_id FROM user_item_state p
            CROSS JOIN items r ON r.id = p.item_id
            WHERE p.profile_id = @viewer_profile AND p.played = 0 AND
            CASE WHEN r.kind IN (%d, %d, %d) THEN
                json_extract(p.position_text, '$.progression') > 0 AND json_extract(p.position_text, '$.progression') < 1
            ELSE p.position_ms > 0 END)`, KindBook, KindComic, KindIssue),
	}
)

// stateValue reads one column of the viewer's own state for the item.
func stateValue(column string) string {
	return "(SELECT st." + column + " FROM user_item_state st WHERE st.profile_id = @viewer_profile AND st.item_id = i.id)"
}

// ConditionFields lists every field a condition can name.
var ConditionFields = []string{"genre", "studio", "network", "label", "tag", "actor", "director", "writer", "producer", "composer", "author",
	"narrator", "artist", "person", "year", "contentRating", "resolution", "dynamicRange", "audioLanguage", "subtitleLanguage",
	"communityRating", "criticRating", "durationMs", "wordCount", "userRating", "playCount", "versions", "added", "lastPlayed", "released",
	"available", "inProgress", "country", "mood", "decade", "collection"}

// PersonalFields are the fields that read the viewer's own state. A Library Channel is shared,
// so its conditions never name one.
var PersonalFields = map[string]bool{"userRating": true, "playCount": true, "lastPlayed": true, "inProgress": true}

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

// tagValue finds the tag a condition's value names: by public id, or for a kind by its name.
// A value no tag has is 0 with no error: nothing carries it.
func tagValue(ctx context.Context, tx *sql.Tx, kind, value string) (tag int64, uses int, err error) {
	err = tx.QueryRowContext(ctx, `SELECT id, (SELECT count(*) FROM (SELECT 1 FROM item_tags WHERE tag_id = tags.id LIMIT ?))
		FROM tags WHERE (public_id = ?2 OR (?3 <> '' AND norm_name = ?4)) AND (?3 = '' OR kind = ?3)`,
		rareTag, value, kind, normTag(value)).Scan(&tag, &uses)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	return tag, uses, err
}

// tagTest is the test that an item, or the title it belongs to, carries a tag.
func tagTest(tag int64) string {
	if tag == 0 {
		return "0" // no such tag: nothing carries it
	}
	return fmt.Sprintf(`EXISTS (SELECT 1 FROM item_tags t WHERE t.tag_id = %d AND t.item_id IN (i.id, i.root_id))`, tag)
}

// personValue finds the person a condition's value names, or 0 for none.
func personValue(ctx context.Context, tx *sql.Tx, value string) (int64, error) {
	var person int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM people WHERE public_id = ?`, value).Scan(&person)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return person, err
}

// conditionPerson finds the person a person condition's value names. For "artist" the value
// may also be an artist's own id, as an artist's page has it: the artist is then the person
// most often credited as artist on its albums, or failing those on its songs. A scan names
// an artist by its album artist and credits that person on each album, and no link is kept
// from the artist to the person.
func conditionPerson(ctx context.Context, tx *sql.Tx, field, value string) (int64, error) {
	person, err := personValue(ctx, tx, value)
	if err != nil || person != 0 || field != "artist" {
		return person, err
	}
	err = tx.QueryRowContext(ctx, `SELECT c.person_id FROM items a JOIN items t ON t.root_id = a.id AND t.kind IN (?2, ?3)
		JOIN credits c ON c.item_id = t.id AND c.role = ?4
		WHERE a.public_id = ?1 AND a.kind = ?5
		GROUP BY c.person_id ORDER BY sum(t.kind = ?2) DESC, count(*) DESC, c.person_id LIMIT 1`,
		value, KindAlbum, KindTrack, RoleArtist, KindArtist).Scan(&person)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return person, err
}

// personTest is the test that a person is credited on an item, in one role or (0) in any.
func personTest(person int64, role CreditRole) string {
	if person == 0 {
		return "0"
	}
	in := ""
	if role != 0 {
		in = fmt.Sprintf(" AND c.role = %d", role)
	}
	return fmt.Sprintf(`EXISTS (SELECT 1 FROM credits c WHERE c.item_id = i.id AND c.person_id = %d%s)`, person, in)
}

// joinTests combines one condition's value tests by its mode.
func joinTests(mode string, tests []string) string {
	switch mode {
	case ModeAll:
		return "(" + strings.Join(tests, " AND ") + ")"
	case ModeNone:
		// A title with no value for the field has none of the values.
		return "NOT ifnull((" + strings.Join(tests, " OR ") + "), 0)"
	}
	return "(" + strings.Join(tests, " OR ") + ")"
}

// checkMode refuses a mode a condition cannot have.
func checkMode(mode string) error {
	if mode != "" && mode != ModeAny && mode != ModeAll && mode != ModeNone {
		return invalid("A filter's choices combine as any, all or none of them.")
	}
	return nil
}

// test is one whole condition as a test on the items row i: its values found and combined by
// its mode, by the same code the list's filter uses.
func (c Condition) test(ctx context.Context, tx *sql.Tx, w *query) (string, error) {
	if err := checkMode(c.Mode); err != nil {
		return "", err
	}
	tests := make([]string, 0, len(c.Values))
	tagKind, isTag := conditionTags[c.Field]
	role, isPerson := conditionPeople[c.Field]
	for _, value := range c.Values {
		switch {
		case isTag:
			tag, _, err := tagValue(ctx, tx, tagKind, value)
			if err != nil {
				return "", err
			}
			tests = append(tests, tagTest(tag))
		case isPerson:
			person, err := conditionPerson(ctx, tx, c.Field, value)
			if err != nil {
				return "", err
			}
			tests = append(tests, personTest(person, role))
		default:
			test, err := conditionTest(w, c.Field, value)
			if err != nil {
				return "", err
			}
			tests = append(tests, test)
		}
	}
	if len(tests) == 0 {
		return "1", nil
	}
	return joinTests(c.Mode, tests), nil
}

// CheckValue refuses a value a condition's field cannot take, by the same code that turns it
// into SQL; a tag or a person is any value (one nobody has matches nothing).
func CheckValue(field, value string) error {
	if _, ok := conditionTags[field]; ok {
		return nil
	}
	if _, ok := conditionPeople[field]; ok {
		return nil
	}
	_, err := conditionTest(newQuery(Viewer{}), field, value)
	return err
}

// conditions adds every condition of the list to its statement. The first condition that
// asks for tags or people becomes what the list is read from, as a single tag does (see
// ItemQuery.filters), so a narrow filter on a large library reads what matches and not the
// library. Every other condition is a check on each row the list comes to: one that few
// titles of a very large library pass reads far into the library to fill a page.
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
		if err := checkMode(c.Mode); err != nil {
			return false, err
		}
		var tests []string
		tagKind, isTag := conditionTags[c.Field]
		role, isPerson := conditionPeople[c.Field]
		switch {
		case isTag:
			var tags []int64
			rarest, fewest, most := int64(0), rareTag, 0
			for _, value := range c.Values {
				tag, uses, err := tagValue(ctx, tx, tagKind, value)
				if err != nil {
					return false, err
				}
				// An item matches a tag it carries, or one the title it belongs to carries.
				tests = append(tests, tagTest(tag))
				if tag == 0 {
					continue
				}
				tags, most = append(tags, tag), max(most, uses)
				if uses < fewest {
					rarest, fewest = tag, uses
				}
			}
			// The first condition that asks for tags decides how the list is read, as a single
			// tag does in ItemQuery.filters. Its total always starts from the tags' own items,
			// so it costs what matches and not the library. Its page starts there too when
			// the tags are rare, and otherwise walks the list in order and skips.
			if !driven && c.Mode != ModeNone && len(tags) == len(c.Values) {
				from, rare := tags, len(tags)*most < rareTag
				if c.Mode == ModeAll {
					from, rare = []int64{rarest}, rarest != 0
					if rarest == 0 {
						from = tags[:1]
					}
				}
				list := intList(from)
				gathered := ` AND i.id IN (SELECT item_id FROM item_tags WHERE tag_id IN ` + list + ` UNION
					SELECT d.id FROM item_tags h CROSS JOIN items d WHERE h.tag_id IN ` + list + ` AND d.root_id = h.item_id)`
				w.add("{tag}")
				w.countFrom, w.tagCount, driven = "items i NOT INDEXED", gathered, true
				if rare {
					w.from, w.tagPage = "items i NOT INDEXED", gathered
				}
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
				person, err := conditionPerson(ctx, tx, c.Field, value)
				if err != nil {
					return false, err
				}
				tests = append(tests, personTest(person, role))
				if person != 0 {
					people = append(people, person)
				}
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
			// A few saved places must not scan a library's ordered index to fill a page.
			if !driven && c.Field == "inProgress" && c.Mode != ModeNone && len(c.Values) == 1 && c.Values[0] == "true" {
				w.from, w.countFrom, driven = "items i NOT INDEXED", "items i NOT INDEXED", true
			}
			for _, value := range c.Values {
				test, err := conditionTest(w, c.Field, value)
				if err != nil {
					return false, err
				}
				tests = append(tests, test)
			}
		}
		w.add(` AND `, joinTests(c.Mode, tests))
	}
	return true, nil
}

// conditionTest writes the test for one value of a field that is neither a tag nor a person.
func conditionTest(w *query, field, value string) (string, error) {
	from, to, _ := span(value)
	switch field {
	case "year":
		// A range may be open at either end: "1990.." or "..1989".
		first, errFrom := strconv.Atoi(from)
		last, errTo := strconv.Atoi(to)
		if from == "" {
			first, errFrom = 1, nil
		}
		if to == "" {
			last, errTo = 9999, nil
		}
		if errFrom != nil || errTo != nil || first > last || first < 1 || from == "" && to == "" {
			return "", invalid("Give a year such as 1999, or a range such as 1990..1999, 1990.. or ..1999.")
		}
		// "{+}" keeps a page from being found through the year index. See ItemQuery.filters.
		return `({+}i.year >= ` + w.arg(first) + ` AND {+}i.year <= ` + w.arg(last) + `)`, nil
	case "decade":
		decade, err := strconv.Atoi(value)
		if err != nil || decade < 1 || decade%10 != 0 {
			return "", invalid("Give a decade by its first year, such as 1980.")
		}
		return `({+}i.year >= ` + w.arg(decade) + ` AND {+}i.year <= ` + w.arg(decade+9) + `)`, nil
	case "collection":
		// A collection by its public id: the titles it holds.
		return fmt.Sprintf(`EXISTS (SELECT 1 FROM items co JOIN item_relations r ON r.item_id = co.id AND r.kind = %d
			WHERE co.public_id = %s AND r.related_item_id = i.id)`, RelationCollectionMember, w.arg(value)), nil
	case "contentRating":
		return `i.content_rating = ` + w.arg(value), nil
	case "resolution":
		class, ok := resolutionClasses[value]
		if !ok {
			return "", invalid("Choose a resolution of sd, 720p, 1080p or 4k.")
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
			return "", invalid("Give a number, or a range such as 7..9.")
		}
		return between(w, expr, low, high, from != "", to != ""), nil
	}
	if expr, ok := conditionDays[field]; ok {
		if field == "released" && (len(from) == 4 || len(from) == 7 || len(to) == 4 || len(to) == 7) {
			for _, value := range []string{from, to} {
				if value == "" {
					continue
				}
				layout := map[int]string{4: "2006", 7: "2006-01", 10: time.DateOnly}[len(value)]
				if _, err := time.Parse(layout, value); err != nil {
					return "", invalid("Give a day such as 2024-03-01, or a range such as 2024-01-01..2024-12-31.")
				}
			}
			upper := to
			if len(to) == 4 {
				upper += "-99-99"
			} else if len(to) == 7 {
				upper += "-99"
			}
			return between(w, expr, from, upper, from != "", to != ""), nil
		}
		var first, last time.Time
		var errFrom, errTo error
		if from != "" {
			first, errFrom = time.Parse(time.DateOnly, from)
		}
		if to != "" {
			last, errTo = time.Parse(time.DateOnly, to)
		}
		if errFrom != nil || errTo != nil || from == "" && to == "" {
			return "", invalid("Give a day such as 2024-03-01, or a range such as 2024-01-01..2024-12-31.")
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
		return "", invalid("Use true or false.")
	}
	return "", invalid("This list cannot be filtered that way.")
}
