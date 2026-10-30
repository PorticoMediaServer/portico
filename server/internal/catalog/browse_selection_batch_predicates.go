package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"slices"
	"strconv"
	"strings"
	"unsafe"
)

// Each source is driven by its raw owner-leading primary/index key, before its
// field, currentness or comparison is tested. The subsequent EXISTS is pinned
// to that exact source key and only joins unique keys. Negative predicates
// complement at source EOF, not after an arbitrary candidate cap.
type selectionBatchTest struct {
	search                                     *selectionBatchSearch
	rangeShow                                  string
	rangeStart                                 int64
	global                                     bool
	streams                                    []selectionBatchTest
	creditGuards                               []selectionBatchCreditGuard
	scalar                                     string
	sizeSQL                                    string
	value                                      string
	args                                       []any
	negative                                   bool
	owners                                     int
	table, owner, keys, guard, join, condition string
	keyZero                                    []any
	profileArgs                                []any
}

type selectionBatchCreditGuard struct {
	column, condition string
}

func (t selectionBatchTest) bytes() int {
	n := cap(t.streams)*int(unsafe.Sizeof(selectionBatchTest{})) + cap(t.creditGuards)*int(unsafe.Sizeof(selectionBatchCreditGuard{}))
	for _, guard := range t.creditGuards {
		n += len(guard.column) + len(guard.condition)
	}
	for _, stream := range t.streams {
		n += stream.bytes()
	}
	if t.search != nil {
		n += t.search.bytes()
	}
	return n + len(t.rangeShow) + len(t.value) + len(t.sizeSQL) + len(t.scalar) + len(t.table) + len(t.owner) + len(t.keys) + len(t.guard) + len(t.join) + len(t.condition) + selectionArgsBytes(t.args) + selectionArgsBytes(t.profileArgs) + selectionArgsBytes(t.keyZero)
}

type selectionBatchRelationCursor struct {
	previousAfter              [3]int64
	previousPending            [2]int64
	previousReady, previousEOF bool
	stream                     int
	creditGuard                int
	creditGuardMatched         bool
	phase                      int
	pending                    []any
	members                    selectionBatchMemberCursor
	member                     int64
	keys                       []any
	owner                      int
	rawOwner                   int64
	ownerReady                 bool
}

func (c selectionBatchRelationCursor) clone() selectionBatchRelationCursor {
	c.keys = slices.Clone(c.keys)
	c.pending = slices.Clone(c.pending)
	return c
}
func (c selectionBatchRelationCursor) bytes() int {
	return selectionArgsBytes(c.keys) + selectionArgsBytes(c.pending)
}

func selectionBatchTests(node *BrowseNode, profile string) ([]selectionBatchTest, error) {
	out := []selectionBatchTest{}
	var visit func(*BrowseNode) error
	visit = func(n *BrowseNode) error {
		if n == nil {
			return nil
		}
		if n.Field != "" {
			if n.Operator == "contains-all" {
				values, ok := n.Value.([]any)
				if !ok {
					return ErrSelectionBatchUnsupported
				}
				for _, value := range values {
					single := *n
					single.Operator = "equals"
					single.Value = value
					if err := visit(&single); err != nil {
						return err
					}
				}
				return nil
			}
			test, err := selectionBatchCompileTest(*n, profile, browseRequiresGroup(node))
			if err != nil {
				return err
			}
			out = append(out, test)
			// Bits keep continuation state fixed-size. This is an explicit backend
			// refusal; never truncate a legal larger expression into a false count.
			if len(out) > 4032 {
				return fmt.Errorf("%w: expanded predicate limit", ErrSelectionBatchUnsupported)
			}
			return nil
		}
		for i := range n.All {
			if err := visit(&n.All[i]); err != nil {
				return err
			}
		}
		for i := range n.Any {
			if err := visit(&n.Any[i]); err != nil {
				return err
			}
		}
		return visit(n.Not)
	}
	err := visit(node)
	return out, err
}

func selectionBatchCompileTest(node BrowseNode, profile string, groupSearch bool) (selectionBatchTest, error) {
	field, ok := browseFieldByID(node.Field)
	if !ok {
		return selectionBatchTest{}, ErrSelectionBatchUnsupported
	}
	c := browseCompiler{profile: profile, itemPivot: true, perTitle: true}
	test := selectionBatchTest{owners: 1, sizeSQL: "0"}
	if field.Entity != "" && field.ID != "search" {
		var err error
		test.scalar, err = c.comparison(field, field.Entity, node.Operator, node.Value)
		test.sizeSQL = "COALESCE(length(CAST((" + field.Entity + ") AS BLOB)),0)"
		test.args = c.args
		return test, err
	}
	if field.ID == "search" {
		text, ok := node.Value.(string)
		if !ok {
			return test, ErrSelectionBatchUnsupported
		}
		normalized, _, err := NormalizeSearch(text)
		if err != nil {
			return test, err
		}
		// Follow the ORIGINAL compiler's branch, including unqualified MATCH's
		// kind column and raw CJK post-check. Rowid-correlated FTS is not bounded.
		if _, indexed := facettext.IndexedSearchTerms(normalized); !groupSearch || !indexed {
			test.search, err = newSelectionBatchSearch(normalized)
			return test, err
		}
		c.groupSearch = true
		test.scalar, err = c.entityPredicate(field, node)
		test.args = c.args
		return test, err
	}

	if field.ID == "match" {
		attention := `(EXISTS(SELECT 1 FROM screen_metadata_attention a JOIN screen_metadata_work w ON w.target_id=a.entity_id AND w.target_kind=a.target_kind AND w.generation=a.generation WHERE a.entity_id=e.entity_id AND a.retired=0 AND a.reason NOT IN('pending','searching','pending_search','pending_episodes','pending_children','pending_apply')) OR EXISTS(SELECT 1 FROM listening_metadata_attention a WHERE a.entity_id=e.entity_id AND a.retired=0 AND a.kind IN('album','song') AND a.reason NOT IN('pending','searching','pending_search','pending_episodes','pending_children','pending_apply')))`
		value := `CASE WHEN EXISTS(SELECT 1 FROM catalog_entities target WHERE target.id=e.entity_id AND target.retired=0) AND ` + attention + ` THEN 'unmatched' ELSE 'matched' END`
		condition, err := c.comparison(field, value, node.Operator, node.Value)
		if err != nil {
			return test, err
		}
		test.scalar = `e.kind IN(1,4,7) AND EXISTS(SELECT 1 FROM catalog_libraries cl JOIN libraries l ON l.id=cl.library_id LEFT JOIN library_metadata_agents agent ON agent.library_id=l.id WHERE cl.id=e.library_id AND l.kind IN('movie','tv','anime','music') AND COALESCE(agent.agent,'online')='online') AND (` + condition + `)`
		test.args = c.args
		return test, nil
	}
	if field.ID == "episodeGroup" {
		show, group, ok := splitEpisodeGroup(node.Value)
		if !ok {
			return test, browseIssue("query.value", "invalid episode group")
		}
		if strings.HasPrefix(group, "range:") {
			start, err := strconv.ParseInt(strings.TrimPrefix(group, "range:"), 10, 64)
			if err != nil {
				return test, err
			}
			test.rangeShow = show
			test.rangeStart = start
			return test, nil
		}
		condition := "1"
		switch group {
		case "specials":
			condition = "sd.number=0"
		case "unassigned_absolute":
			condition = "ep.season_id IS NULL AND ep.numbering='absolute'"
		}
		test.scalar = `EXISTS(SELECT 1 FROM catalog_episodes ep LEFT JOIN catalog_seasons sd ON sd.entity_id=ep.season_id WHERE ep.entity_id=e.entity_id AND ep.show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND (` + condition + `))`
		test.args = []any{show}
		return test, nil
	}
	// These personal predicates are explicitly title-scoped for leaf pivots.
	// Correlating their item primary key avoids the ordinary value-side IN plan.
	if field.ID == "favorite" || field.ID == "watchlisted" || field.ID == "personalRating" || field.ID == "lastPlayedAt" {
		if field.Type == valueBoolean {
			flag, _ := node.Value.(bool)
			test.negative = (node.Operator == "equals") != flag
			node.Operator = "equals"
			node.Value = true
		} else if positive := selectionBatchPositive(node.Operator); positive != "" {
			test.negative = true
			node.Operator = positive
		}
		condition, err := c.comparison(field, field.Value, node.Operator, node.Value)
		if err != nil {
			return test, err
		}
		test.scalar = `EXISTS(SELECT 1 FROM personal_items pi WHERE pi.profile_id=? AND pi.item_id=e.entity_id AND (` + condition + `))`
		if test.negative {
			test.scalar = "NOT (" + test.scalar + ")"
		}
		test.args = append([]any{profile}, c.args...)
		return test, nil
	}
	if field.ID == "playState" {
		test.negative = node.Operator == "not-in"
		node.Operator = "equals"
		if _, ok := node.Value.([]any); ok {
			node.Operator = "in"
		}
	}
	if positive := selectionBatchPositive(node.Operator); positive != "" {
		test.negative = true
		node.Operator = positive
	}
	if field.ID == "subtitleLanguage" {
		sources := []struct {
			table, owner, keys, guard, join, value string
			global                                 bool
			profileArgs                            []any
		}{
			{"catalog_item_attribute_edges", "item_id", "term_id", "ca.term_id=raw.term_id", attributeJoin("subtitleLanguage"), "ca.source_value", false, nil},
			{"asset_subtitles", "id", "id", "track.id=raw.id", `JOIN asset_subtitles track ON track.id=raw.id JOIN catalog_assets asset ON asset.token=track.asset_id JOIN catalog_asset_links link ON link.entity_id=bei.item_id AND link.asset_id=asset.id JOIN asset_subtitle_facts facts ON facts.asset_id=asset.token AND facts.size=asset.size AND facts.modified_ns=asset.modified_ns AND facts.status='known'`, "track.language", true, nil},
			{"subtitle_resources", "id", "id", "resource.id=raw.id", `JOIN subtitle_resources resource ON resource.id=raw.id AND resource.item_id=bei.item_id AND resource.deleted=0 AND (resource.scope='shared' OR resource.owner=?) JOIN subtitle_revisions revision ON revision.resource_id=resource.id AND revision.revision=resource.current_revision JOIN catalog_assets asset ON asset.token=resource.source_id AND asset.size=revision.source_size AND asset.modified_ns=revision.source_modified_ns`, "revision.language", true, []any{profile}},
		}
		for _, source := range sources {
			comp := browseCompiler{}
			condition, err := comp.comparison(field, source.value, node.Operator, node.Value)
			if err != nil {
				return test, err
			}
			child := selectionBatchTest{owners: 1, global: source.global, table: source.table, owner: source.owner, keys: source.keys, guard: source.guard, join: source.join, value: source.value, condition: condition, args: comp.args, profileArgs: source.profileArgs, keyZero: []any{int64(0)}}
			if source.global {
				child.owner = ""
				child.keyZero = []any{""}
			}
			test.streams = append(test.streams, child)
		}
		return test, nil
	}
	if field.ID == "savedCollection" {
		owner, ok := profileParts(profile)
		if !ok {
			return selectionBatchTest{scalar: map[bool]string{true: "1", false: "0"}[test.negative], sizeSQL: "0"}, nil
		}
		values := []any{node.Value}
		if list, ok := node.Value.([]any); ok {
			values = list
		}
		test.global = true
		test.table = "saved_resource_entries"
		test.keys = "id"
		test.keyZero = []any{""}
		test.owners = 1
		test.join = `JOIN saved_resource_entries sre ON sre.id=raw.id JOIN saved_resources res ON res.id=sre.resource_id`
		test.guard = `sre.id=raw.id`
		test.value = `res.id`
		test.condition = `res.id IN (` + placeholders(len(values)) + `) AND res.kind='collection' AND res.deleted=0 AND sre.item_id IS NOT NULL AND (res.owner_key=? OR res.visibility='server' OR EXISTS(SELECT 1 FROM saved_resource_shares share WHERE share.resource_id=res.id AND share.authority=? AND share.account_id=? AND share.profile_id=?)) AND (sre.item_id=bei.entity_id OR EXISTS(SELECT 1 FROM catalog_browse_memberships held WHERE held.entity_id=sre.item_id AND held.item_id=bei.entity_id))`
		test.args = append(test.args, values...)
		test.args = append(test.args, profile, owner[0], owner[1], owner[2])
		return test, nil
	}
	// Maintained version counts and fixed item/container links are bounded
	// singleton facts, not an enumeration of asset/version histories.
	if field.ID == "playState" || field.ID == "versions" || field.ID == "discNumber" || field.ID == "album" || field.ID == "author" || field.ID == "narrator" {
		test.table = "catalog_entities"
		test.owner = "id"
		test.keys = "id"
		test.keyZero = []any{int64(0)}
		test.guard = "bei.item_id=raw.id"
	} else {
		switch {
		case strings.HasPrefix(field.Join, "JOIN catalog_item_attribute_edges"):
			test.table = "catalog_item_attribute_edges"
			test.owner = "item_id"
			test.keys = "term_id"
			test.keyZero = []any{int64(0)}
			test.guard = "ca.term_id=raw.term_id"
		case field.ID == "resolution" || field.ID == "durationMs":
			test.table = "catalog_asset_links"
			test.owner = "entity_id"
			test.keys = "asset_id"
			test.keyZero = []any{int64(0)}
			test.guard = "ia.asset_id=raw.asset_id"
			if field.ID == "durationMs" {
				if value, ok := node.Value.(float64); ok {
					node.Value = value / 1000
				}
				if values, ok := node.Value.([]any); ok {
					values = slices.Clone(values)
					for i, v := range values {
						if n, ok := v.(float64); ok {
							values[i] = n / 1000
						}
					}
					node.Value = values
				}
			}
		case field.ID == "actor" || field.ID == "director" || field.ID == "writer":
			test.table = "catalog_credits"
			test.owner = "entity_id"
			test.keys = "ord"
			test.keyZero = []any{int64(-1)}
			test.guard = "cc.ord=raw.ord"
		case field.ID == "person" || field.ID == "cast" || field.ID == "crew":
			// Leaf identity is the displayed entity itself. The ordinary IN has
			// the same identity twice; avoid expanding another title's credits.
			test.table = "catalog_credits"
			test.owner = "entity_id"
			test.keys = "ord"
			test.keyZero = []any{int64(-1)}
			test.guard = "credit.ord=raw.ord AND credit.entity_id=bei.item_id"
		case field.ID == "list":
			owner, ok := profileParts(profile)
			if !ok {
				return selectionBatchTest{scalar: map[bool]string{true: "1", false: "0"}[test.negative], sizeSQL: "0"}, nil
			}
			test.table = "catalog_playlist_entries"
			test.owner = "item_id"
			test.keys = "id"
			test.keyZero = []any{int64(0)}
			test.guard = "entry.id=raw.id"
			for range 2 {
				for _, part := range owner {
					test.profileArgs = append(test.profileArgs, part)
				}
			}
		case field.ID == "genre":
			test.table = "catalog_term_sources"
			test.owner = "entity_id"
			test.keys = "provider,source_id"
			test.keyZero = []any{"", ""}
			test.guard = "ts.entity_id=raw.entity_id AND ts.provider=raw.provider AND ts.source_id=raw.source_id"
			test.owners = 2
		case field.ID == "communityRating" || field.ID == "criticRating":
			test.table = "metadata_ratings"
			test.owner = "item_id"
			test.keys = "provider"
			test.keyZero = []any{""}
			test.guard = "mr.provider=raw.provider"
		case field.ID == "collection":
			test.table = "catalog_collection_members"
			test.owner = "item_id"
			test.keys = "collection_id"
			test.keyZero = []any{int64(0)}
			test.guard = "cim.collection_id=raw.collection_id"
		case field.ID == "memberOf":
			test.table = "catalog_browse_memberships"
			test.owner = "item_id"
			test.keys = "entity_id,source"
			test.keyZero = []any{int64(0), int64(-1)}
			test.guard = "parent.entity_id=raw.entity_id AND parent.source=raw.source"
		case field.ID == "artist":
			test.table = "catalog_song_artists"
			test.owner = "song_id"
			test.keys = "artist_id"
			test.keyZero = []any{int64(0)}
			test.guard = "sar.artist_id=raw.artist_id"
		default:
			return test, fmt.Errorf("%w: %s", ErrSelectionBatchUnsupported, field.ID)
		}
	}
	condition, err := c.comparison(field, field.Value, node.Operator, node.Value)
	if err != nil {
		return test, err
	}
	if field.ID == "playState" {
		test.profileArgs = []any{profile, profile}
	}
	test.value = field.Value
	test.join = field.Join
	test.condition = condition
	test.args = c.args
	// Evaluate each role/department label in its own resumable raw unit. Keep
	// guards out of the value join: rejected labels still consume their UTF-8
	// bytes, and two large guard labels cannot share one scalar overrun.
	switch field.ID {
	case "cast", "crew":
		condition := "lower(label.label) IN('acting','cast')"
		if field.ID == "crew" {
			condition = "NOT (" + condition + ")"
		}
		test.creditGuards = []selectionBatchCreditGuard{{"department_id", condition}}
		test.join = `JOIN catalog_credits credit ON credit.entity_id=bei.item_id AND credit.ord=raw.ord JOIN catalog_people person ON person.id=credit.person_id`
	case "actor", "director", "writer":
		test.join = `JOIN catalog_credits cc ON cc.entity_id=bei.item_id AND cc.ord=raw.ord`
		switch field.ID {
		case "actor":
			test.creditGuards = []selectionBatchCreditGuard{{"department_id", "label.label='Acting'"}}
		case "director":
			test.creditGuards = []selectionBatchCreditGuard{{"role_id", "label.label='Director'"}, {"department_id", "label.label='Directing'"}}
		case "writer":
			test.creditGuards = []selectionBatchCreditGuard{{"role_id", "label.label IN('Writer','Screenplay')"}, {"department_id", "label.label='Writing'"}}
		}
	}
	return test, nil
}

func selectionBatchPositive(operator string) string {
	switch operator {
	case "not-equals":
		return "equals"
	case "not-in":
		return "in"
	case "is-missing":
		return "is-present"
	}
	return ""
}

func (s *Service) selectionBatchRelation(test selectionBatchTest, entity int64, cursor *selectionBatchRelationCursor, scalarBytes *int) (bool, bool, error) {
	if len(test.streams) > 0 {
		done, yes, err := s.selectionBatchRelation(test.streams[cursor.stream], entity, cursor, scalarBytes)
		if err != nil || yes {
			return false, yes, err
		}
		if done && cursor.stream+1 < len(test.streams) {
			cursor.stream++
			cursor.keys = nil
			cursor.pending = nil
			cursor.owner = 0
			cursor.ownerReady = false
			cursor.phase = 0
			return false, false, nil
		}
		return done, false, nil
	}

	if !cursor.ownerReady {
		cursor.rawOwner = cursor.member
		if cursor.owner == 1 {
			var show sql.NullInt64
			err := s.read().QueryRow(`SELECT show_id FROM catalog_episodes WHERE entity_id=?`, cursor.member).Scan(&show)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return false, false, err
			}
			cursor.rawOwner = show.Int64
		}
		cursor.ownerReady = true
		return false, false, nil
	}
	keys := strings.Split(test.keys, ",")
	if cursor.phase == 1 {
		return s.selectionBatchPreviousMember(test, cursor, scalarBytes)
	}
	if cursor.phase == 0 {
		after := ""
		args := []any{}
		where := "1"
		if !test.global {
			where = test.owner + "=?"
			args = append(args, cursor.rawOwner)
		}
		if cursor.keys != nil {
			after = " AND (" + test.keys + ")>(" + placeholders(len(keys)) + ")"
			args = append(args, cursor.keys...)
		}
		hint := " NOT INDEXED"
		if index := selectionBatchRawIndex(test.table); index != "" {
			hint = " INDEXED BY " + index
		}
		query := `SELECT ` + test.keys + ` FROM ` + test.table + hint + ` WHERE ` + where + after + ` ORDER BY ` + test.keys + ` LIMIT 1`
		got := make([]any, len(keys))
		dest := make([]any, len(keys))
		for i := range got {
			dest[i] = &got[i]
		}
		err := dbwork.Snapshot(s.Context()).QueryRowContext(s.Context(), query, args...).Scan(dest...)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, false, err
		}
		cursor.pending = nil
		if err == nil {
			cursor.pending = got
			*scalarBytes += selectionBatchKeyBytes(got)
		}
		if test.table == "catalog_browse_memberships" {
			cursor.phase = 1
			return false, false, nil
		}
		cursor.phase = 2
		if cursor.pending == nil {
			cursor.owner++
			cursor.ownerReady = false
			cursor.keys = nil
			cursor.phase = 0
			return cursor.owner >= test.owners, false, nil
		}
		return false, false, nil
	}

	if cursor.creditGuard < len(test.creditGuards) {
		guard := test.creditGuards[cursor.creditGuard]
		var encoded int64
		err := s.read().QueryRow(`SELECT 2*COALESCE(length(CAST(label.label AS BLOB)),0)+CASE WHEN (`+guard.condition+`) THEN 1 ELSE 0 END FROM catalog_credits cc LEFT JOIN catalog_credit_labels label ON label.id=cc.`+guard.column+` WHERE cc.entity_id=? AND cc.ord=?`, cursor.rawOwner, cursor.pending[0]).Scan(&encoded)
		if err != nil {
			return false, false, err
		}
		*scalarBytes = int(encoded / 2)
		cursor.creditGuardMatched = cursor.creditGuardMatched || encoded%2 != 0
		cursor.creditGuard++
		return false, false, nil
	}
	if len(test.creditGuards) > 0 && !cursor.creditGuardMatched {
		cursor.keys, cursor.pending = cursor.pending, nil
		cursor.creditGuard, cursor.phase = 0, 0
		return false, false, nil
	}

	aliases := make([]string, 0, len(keys)+1)
	args := slices.Clone(cursor.pending)
	for _, key := range keys {
		aliases = append(aliases, "? AS "+key)
	}
	if !test.global {
		aliases = append(aliases, "? AS "+test.owner)
		args = append(args, cursor.rawOwner)
	}
	args = append(args, test.args...)
	args = append(args, cursor.member, entity)
	args = append(args, test.profileArgs...)
	query := `WITH raw AS (SELECT ` + strings.Join(aliases, ",") + `) SELECT COALESCE((SELECT 2*COALESCE(length(CAST((` + test.value + `) AS BLOB)),0)+CASE WHEN (` + test.condition + `) THEN 1 ELSE 0 END FROM raw CROSS JOIN (SELECT ? AS item_id,? AS entity_id) bei ` + test.join + ` WHERE (` + test.guard + `) LIMIT 1),0)`
	var encoded int64
	err := s.read().QueryRow(query, args...).Scan(&encoded)
	*scalarBytes = int(encoded/2) + selectionBatchKeyBytes(cursor.pending)
	yes := encoded%2 != 0
	if err != nil {
		return false, false, err
	}
	cursor.keys = cursor.pending
	cursor.pending = nil
	cursor.creditGuard = 0
	cursor.creditGuardMatched = false
	cursor.phase = 0
	return false, yes, nil
}

// Pin the raw owner/order path. Letting the planner pick a value-side index and
// sort an owner's entire history before LIMIT would defeat the work budget.
// These are existing baseline primary keys or owner-leading secondary indexes.
func selectionBatchRawIndex(table string) string {
	switch table {
	case "catalog_collection_members":
		return "catalog_collection_members_item"
	case "catalog_browse_memberships":
		return "catalog_browse_membership_item"
	case "catalog_playlist_entries":
		return "catalog_playlist_entries_item"
	case "catalog_entities":
		return "" // INTEGER PRIMARY KEY; handled below.
	default:
		return "sqlite_autoindex_" + table + "_1"
	}
}

func selectionBatchKeyLess(a, b []any) bool {
	for i := range a {
		switch v := a[i].(type) {
		case int64:
			other := b[i].(int64)
			if v != other {
				return v < other
			}
		case string:
			other := b[i].(string)
			if v != other {
				return v < other
			}
		}
	}
	return false
}

func selectionBatchKeyBytes(values []any) int {
	n := 0
	for _, value := range values {
		if text, ok := value.(string); ok {
			n += len(text)
		}
	}
	return n
}

const selectionBatchPreviousMembershipRawSQL = `SELECT entity_id,item_id,source FROM catalog_browse_memberships_previous INDEXED BY sqlite_autoindex_catalog_browse_memberships_previous_1 WHERE generation=(SELECT generation FROM catalog_generations WHERE singleton=1 AND retaining=1) AND (entity_id,item_id,source)>(?,?,?) ORDER BY entity_id,item_id,source LIMIT 1`

func (s *Service) selectionBatchPreviousMember(test selectionBatchTest, c *selectionBatchRelationCursor, scalarBytes *int) (bool, bool, error) {
	if !c.previousReady && !c.previousEOF {
		var entity, item, source int64
		err := dbwork.Snapshot(s.Context()).QueryRowContext(s.Context(), selectionBatchPreviousMembershipRawSQL, c.previousAfter[0], c.previousAfter[1], c.previousAfter[2]).Scan(&entity, &item, &source)
		if errors.Is(err, sql.ErrNoRows) {
			c.previousEOF = true
		} else if err != nil {
			return false, false, err
		} else {
			c.previousAfter = [3]int64{entity, item, source}
			if item != c.rawOwner {
				return false, false, nil
			}
			c.previousPending = [2]int64{entity, source}
			c.previousReady = true
		}
	}
	if c.previousReady {
		previous := []any{c.previousPending[0], c.previousPending[1]}
		if c.pending == nil || !selectionBatchKeyLess(c.pending, previous) {
			c.pending = previous
			c.previousReady = false
		}
	}
	c.phase = 2
	if c.pending == nil {
		c.owner++
		c.ownerReady = false
		c.keys = nil
		c.phase = 0
		return c.owner >= test.owners, false, nil
	}
	return false, false, nil
}

// Admission checks search qualification without constructing another reducer plan.
func selectionBatchSearchQualified(node *BrowseNode) error {
	grouped := browseRequiresGroup(node)
	var visit func(*BrowseNode) error
	visit = func(n *BrowseNode) error {
		if n == nil {
			return nil
		}
		if n.Field == "search" {
			test, err := selectionBatchCompileTest(*n, "", grouped)
			if err != nil {
				return err
			}
			if test.search != nil && !selectionBatchLocalSearchEnabled {
				return fmt.Errorf("%w: local search consumer qualification pending", ErrSelectionBatchUnsupported)
			}
		}
		for i := range n.All {
			if err := visit(&n.All[i]); err != nil {
				return err
			}
		}
		for i := range n.Any {
			if err := visit(&n.Any[i]); err != nil {
				return err
			}
		}
		return visit(n.Not)
	}
	return visit(node)
}
