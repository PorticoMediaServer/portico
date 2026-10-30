package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"strings"
)

// Unchanged items are unplayed. Evaluate canonical watched state only for items
// whose explicit intent, progress or container default can change that answer.
// This preserves inherited intent and eligible-member semantics while keeping
// sparse profiles from running the canonical state expression for every title.
func (c *browseCompiler) playStatePredicate(node BrowseNode) (string, error) {
	if c.bounded {
		values := browseNodeValues(node)
		restriction, args := ItemRestrictionSQL("member.item_id", c.restrictions)
		c.args = append(c.args, c.profile)
		c.args = append(c.args, values...)
		c.args = append(c.args, args...)
		state := `CASE WHEN ` + personalstate.CompactSQL("scope.profile", "member.item_id") + `=1 THEN 'played' WHEN COALESCE(p.position,0)>0 THEN 'in-progress' ELSE 'unplayed' END`
		clause := `EXISTS(SELECT 1 FROM (SELECT ? AS profile) scope CROSS JOIN catalog_browse_memberships member LEFT JOIN progress p ON p.profile_id=scope.profile AND p.item_id=member.item_id WHERE member.entity_id=e.entity_id AND (` + state + `) IN(` + placeholders(len(values)) + `) AND (` + restriction + `))`
		if node.Operator == "not-in" {
			clause = "NOT (" + clause + ")"
		}
		return clause, nil
	}
	values := []any{node.Value}
	if list, ok := node.Value.([]any); ok {
		values = list
	}
	wanted := map[string]bool{}
	for _, v := range values {
		if text, ok := v.(string); ok {
			wanted[text] = true
		}
	}
	restriction, restrictionArgs := ItemRestrictionSQL("member.item_id", c.restrictions)
	eligible := `EXISTS(SELECT 1 FROM catalog_browse_memberships member WHERE member.entity_id=e.entity_id AND (` + restriction + `))`
	negate := node.Operator == "not-in"
	finish := func(clause string) (string, error) {
		if negate {
			return "NOT (" + clause + ")", nil
		}
		return clause, nil
	}
	if len(wanted) == 3 {
		c.args = append(c.args, restrictionArgs...)
		return finish(eligible)
	}
	if wanted["unplayed"] {
		c.args = append(c.args, restrictionArgs...)
	}
	for i := 0; i < 7; i++ {
		c.args = append(c.args, c.profile)
	}
	c.args = append(c.args, values...)
	c.args = append(c.args, restrictionArgs...)
	active := `WITH candidates AS MATERIALIZED (
 SELECT item_id FROM personal_watched_intents WHERE profile_id=? AND watched=1
 UNION SELECT item_id FROM progress WHERE profile_id=? AND position>0
 UNION SELECT ep.entity_id FROM container_personal_state state CROSS JOIN catalog_episodes ep ON ep.show_id=state.container_id WHERE state.profile_id=? AND state.kind='show' AND state.watched=1
 UNION SELECT ep.entity_id FROM container_personal_state state CROSS JOIN catalog_episodes ep ON ep.season_id=state.container_id WHERE state.profile_id=? AND state.kind='season' AND state.watched=1
 UNION SELECT song.entity_id FROM container_personal_state state CROSS JOIN catalog_songs song ON song.album_id=state.container_id WHERE state.profile_id=? AND state.kind='album' AND state.watched=1
 UNION SELECT file.entity_id FROM container_personal_state state CROSS JOIN catalog_book_files file ON file.book_id=state.container_id WHERE state.profile_id=? AND state.kind='book' AND state.watched=1
 ), active AS MATERIALIZED (
 SELECT candidates.item_id,CASE WHEN ` + personalstate.CompactSQL("scope.profile", "candidates.item_id") + `=1 THEN 'played'
 WHEN COALESCE(progress.position,0)>0 THEN 'in-progress' ELSE 'unplayed' END AS state
 FROM candidates CROSS JOIN (SELECT ? AS profile) scope LEFT JOIN progress ON progress.profile_id=scope.profile AND progress.item_id=candidates.item_id
 ) `
	if !wanted["unplayed"] {
		return finish(`e.entity_id IN(` + active + ` SELECT member.entity_id FROM active CROSS JOIN catalog_browse_memberships member ON member.item_id=active.item_id
 WHERE active.state IN (` + placeholders(len(values)) + `) AND (` + restriction + `))`)
	}
	// An entity lacks a wanted member only if all its eligible members belong to
	// the sparse unwanted set. Start that check from affected entities, rather
	// than from every entity in the library. Empty entities still fail eligible.
	return finish(`(` + eligible + ` AND e.entity_id NOT IN (` + strings.TrimSpace(active) + `, unwanted AS MATERIALIZED (
 SELECT item_id FROM active WHERE state NOT IN (` + placeholders(len(values)) + `))
 SELECT candidate.entity_id FROM unwanted CROSS JOIN catalog_browse_memberships candidate ON candidate.item_id=unwanted.item_id
 WHERE NOT EXISTS(SELECT 1 FROM catalog_browse_memberships member WHERE member.entity_id=candidate.entity_id
 AND (` + restriction + `) AND member.item_id NOT IN(SELECT item_id FROM unwanted))))`)
}
