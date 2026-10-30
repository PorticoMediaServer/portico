package librarychannels

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	workerloop "github.com/PorticoMediaServer/portico/server/internal/worker"
	"sort"
	"strconv"
	"strings"
	"time"
)

type generation struct {
	ID                                               string
	ChannelID                                        string
	Revision                                         int64
	Config                                           Config
	Fence, Seed, Phase, Base, After                  string
	Start, End, Boundary, CopyAfter, Cursor, Created int64
	HistoryFence                                     int64
	Rule                                             int
	Count, Unresolved                                int
	stmts                                            *stmtCache
}

func loadGeneration(ctx context.Context, tx *sql.Tx, id string) (generation, error) {
	g := generation{ID: id}
	var raw string
	e := tx.QueryRowContext(ctx, `SELECT channel_id,config_revision,config_json,catalog_fence,seed,phase,base_generation,start_ms,end_ms,boundary_ms,copy_after_ms,scan_rule,scan_after,cursor_ms,candidate_count,unresolved_count,created_ms,play_history_fence FROM lc_generations WHERE id=? AND status='pending'`, id).Scan(&g.ChannelID, &g.Revision, &raw, &g.Fence, &g.Seed, &g.Phase, &g.Base, &g.Start, &g.End, &g.Boundary, &g.CopyAfter, &g.Rule, &g.After, &g.Cursor, &g.Count, &g.Unresolved, &g.Created, &g.HistoryFence)
	if e != nil || json.Unmarshal([]byte(raw), &g.Config) != nil {
		return g, unavailable(e)
	}
	return g, nil
}

// RunBatch performs at most one bounded candidate/copy batch, or up to
// EntryBatch schedule slots within scheduleQuantum, each committed on its own.
// All external playback/IO remains outside this database worker, which is
// background work.
func (s *Store) RunBatch(ctx context.Context) (bool, error) {
	class := dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia)
	gated, e := dbwork.Begin(ctx, s.db, class)
	if e != nil {
		return false, unavailable(e)
	}
	tx := gated.Tx()
	defer gated.Rollback()
	now := s.now()
	var id string
	e = tx.QueryRowContext(ctx, `SELECT g.id FROM lc_generations g JOIN lc_channels c ON c.id=g.channel_id WHERE g.status='pending' AND g.lease_until_ms<=? AND c.enabled=1 AND c.removed=0 ORDER BY g.created_ms,g.id LIMIT 1`, now.UnixMilli()).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, unavailable(e)
	}
	tokenBytes := make([]byte, 16)
	if _, e = rand.Read(tokenBytes); e != nil {
		return false, unavailable(e)
	}
	token := hex.EncodeToString(tokenBytes)
	if _, e = tx.ExecContext(ctx, `UPDATE lc_generations SET lease_token=?,lease_until_ms=? WHERE id=? AND lease_until_ms<=?`, token, now.Add(20*time.Second).UnixMilli(), id, now.UnixMilli()); e != nil {
		return false, unavailable(e)
	}
	g, e := loadGeneration(ctx, tx, id)
	if e != nil {
		return false, e
	}
	var revision int64
	if cause := tx.QueryRowContext(ctx, `SELECT revision FROM lc_channels WHERE id=?`, g.ChannelID).Scan(&revision); cause != nil {
		return false, unavailable(cause)
	}
	current, e := catalogFence(ctx, tx, g.Config)
	if revision != g.Revision || e != nil || current != g.Fence {
		if _, e = tx.ExecContext(ctx, `UPDATE lc_generations SET status='superseded',error_code='catalog-or-configuration-changed',lease_until_ms=0 WHERE id=? AND lease_token=?`, g.ID, token); e != nil {
			return false, unavailable(e)
		}
		if _, e = tx.ExecContext(ctx, `UPDATE lc_channels SET state='stale',health_code='catalog-changed' WHERE id=? AND revision=?`, g.ChannelID, g.Revision); e != nil {
			return false, unavailable(e)
		}
		return true, gated.Commit()
	}
	switch g.Phase {
	case "copying":
		e = copyEntries(ctx, tx, &g)
	case "copy-used":
		e = copyUsed(ctx, tx, &g)
	case "candidates":
		e = snapshotCandidates(ctx, tx, &g)
	case "scheduling":
		// This transaction only takes the lease: each slot reads outside the
		// writer and commits on its own (scheduleSlots).
		if e = gated.Commit(); e != nil {
			return true, unavailable(e)
		}
		return true, s.scheduleSlots(ctx, class, &g, token)
	case "validating":
		e = publishGeneration(ctx, tx, &g, now)
	default:
		e = ErrInvalid
	}
	if e != nil {
		return true, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE lc_generations SET phase=?,copy_after_ms=?,scan_rule=?,scan_after=?,cursor_ms=?,candidate_count=?,unresolved_count=?,lease_until_ms=0,lease_token='' WHERE id=? AND lease_token=?`, g.Phase, g.CopyAfter, g.Rule, g.After, g.Cursor, g.Count, g.Unresolved, g.ID, token); e != nil {
		return true, unavailable(e)
	}
	return true, gated.Commit()
}
func copyEntries(ctx context.Context, tx *sql.Tx, g *generation) error {
	rows, e := tx.QueryContext(ctx, `SELECT occurrence_id,channel_id,start_ms,end_ms,COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=lc_entries.item_id),''),asset_id,library_id,source_fence,title,rule_id,block_id,source_offset_ms,slate_reason FROM lc_entries WHERE generation_id=? AND end_ms>? AND end_ms<=? AND start_ms>? ORDER BY start_ms LIMIT ?`, g.Base, g.Start, g.Boundary, g.CopyAfter, CandidateBatch)
	if e != nil {
		return unavailable(e)
	}
	entries := []Entry{}
	for rows.Next() {
		var v Entry
		if cause := rows.Scan(&v.ID, &v.ChannelID, &v.StartMS, &v.EndMS, &v.ItemID, &v.AssetID, &v.LibraryID, &v.SourceFence, &v.Title, &v.RuleID, &v.BlockID, &v.SourceOffsetMS, &v.SlateReason); cause != nil {
			rows.Close()
			return unavailable(cause)
		}
		entries = append(entries, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return unavailable(e)
	}
	for _, v := range entries {
		if e = insertEntry(ctx, g.statements(tx), g.ID, v); e != nil {
			return e
		}
		g.CopyAfter = v.StartMS
	}
	if len(entries) < CandidateBatch {
		// Only carry cursors for unchanged rules. A config edit never silently
		// interprets an old cursor under different selection semantics.
		var raw string
		var old Config
		cause := tx.QueryRowContext(ctx, `SELECT config_json FROM lc_generations WHERE id=?`, g.Base).Scan(&raw)
		if cause == nil {
			cause = json.Unmarshal([]byte(raw), &old)
		}
		if cause != nil {
			return unavailable(cause)
		}
		oldRules := map[string]string{}
		for _, r := range old.Rules {
			oldRules[r.ID] = encode(r)
		}
		for _, r := range g.Config.Rules {
			if oldRules[r.ID] == encode(r) {
				if _, e = tx.ExecContext(ctx, `INSERT INTO lc_rule_state SELECT ?,rule_id,draw,cycle,last_sort,last_show,last_block,done FROM lc_rule_state WHERE generation_id=? AND rule_id=? ON CONFLICT DO NOTHING`, g.ID, g.Base, r.ID); e != nil {
					return unavailable(e)
				}
			}
		}
		var last sql.NullInt64
		if cause := tx.QueryRowContext(ctx, `SELECT max(end_ms) FROM lc_entries WHERE generation_id=?`, g.ID).Scan(&last); cause != nil {
			return unavailable(cause)
		}
		if last.Valid && last.Int64 < g.Boundary {
			gap := Entry{ID: digest(g.ChannelID, "gap", fmt.Sprint(last.Int64), fmt.Sprint(g.Boundary))[:48], ChannelID: g.ChannelID, StartMS: last.Int64, EndMS: g.Boundary, Title: unavailableTitle, SlateReason: "schedule-unavailable"}
			if e = insertEntry(ctx, g.statements(tx), g.ID, gap); e != nil {
				return e
			}
		}
		g.Phase = "copy-used"
		g.After = ""
	}
	return nil
}
func copyUsed(ctx context.Context, tx *sql.Tx, g *generation) error {
	afterRule, afterRaw, _ := strings.Cut(g.After, ":")
	var afterItem int64
	if afterRaw != "" {
		afterItem, _ = strconv.ParseInt(afterRaw, 10, 64)
	}
	rows, e := tx.QueryContext(ctx, `SELECT u.rule_id,u.item_id,u.cycle FROM lc_used u JOIN lc_rule_state s ON s.generation_id=? AND s.rule_id=u.rule_id AND s.cycle=u.cycle WHERE u.generation_id=? AND (u.rule_id,u.item_id)>(?,?) ORDER BY u.rule_id,u.item_id LIMIT ?`, g.ID, g.Base, afterRule, afterItem, CandidateBatch)
	if e != nil {
		return unavailable(e)
	}
	type use struct {
		rule  string
		item  int64
		cycle int64
	}
	uses := []use{}
	for rows.Next() {
		var u use
		if cause := rows.Scan(&u.rule, &u.item, &u.cycle); cause != nil {
			rows.Close()
			return unavailable(cause)
		}
		uses = append(uses, u)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return unavailable(e)
	}
	for _, u := range uses {
		if _, e = g.statements(tx).ExecContext(ctx, `INSERT INTO lc_used VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, g.ID, u.rule, u.item, u.cycle); e != nil {
			return unavailable(e)
		}
		g.After = u.rule + ":" + strconv.FormatInt(u.item, 10)
	}
	if len(uses) < CandidateBatch {
		g.Phase = "candidates"
		g.After = ""
	}
	return nil
}
func snapshotCandidates(ctx context.Context, tx *sql.Tx, g *generation) error {
	if g.Rule >= len(g.Config.Rules) {
		g.Phase = "scheduling"
		g.After = ""
		return nil
	}
	r := g.Config.Rules[g.Rule]
	// Ordinals are dense from 0, so the next one is also the rule's count so far:
	// one seek on UNIQUE(generation_id,rule_id,ordinal), not a recount per page.
	count, e := poolSize(ctx, tx, g.ID, r.ID)
	if e != nil {
		return e
	}
	limit := CandidateBatch
	if r.Query.Limit > 0 && int64(r.Query.Limit)-count < int64(limit) {
		limit = r.Query.Limit - int(count)
	}
	if limit <= 0 {
		g.Rule++
		g.After = ""
		return nil
	}
	page, after, scanned, unresolved, e := candidatePage(ctx, tx, r, time.UnixMilli(g.Created), g.After, limit, playHistorySnapshot{ChannelID: g.ChannelID, Fence: g.HistoryFence})
	if e != nil {
		return e
	}
	g.After = after
	g.Unresolved += unresolved
	for _, c := range page {
		if e = insertCandidate(ctx, g.statements(tx), g.ID, r.ID, count, c); e != nil {
			return unavailable(e)
		}
		count++
		g.Count++
	}
	if scanned < limit || r.Query.Limit > 0 && count >= int64(r.Query.Limit) {
		g.Rule++
		g.After = ""
	}
	return nil
}

// insertCandidate stores public IDs as integer entity references. Candidate
// pages already carry their resolved keys; preview pools resolve through the
// same public-ID lookup when they were assembled in Go.
func insertCandidate(ctx context.Context, q querier, generation, rule string, ordinal int64, c Candidate) error {
	itemID := c.itemEntityID
	if itemID == 0 {
		var err error
		itemID, err = entityid.Resolve(ctx, q, c.ItemID)
		if err != nil {
			return err
		}
	}
	showID := c.showEntityID
	if showID == 0 && c.ShowID != "" {
		var err error
		showID, err = entityid.Resolve(ctx, q, c.ShowID)
		if err != nil {
			return err
		}
	}
	_, err := q.ExecContext(ctx, `INSERT INTO lc_candidates VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, generation, rule, itemID, ordinal, c.LibraryID, c.AssetID, c.SourceFence, c.DurationMS, c.Title, showID, c.Season, c.Episode, c.Weight, c.SortKey)
	return err
}

type Entry struct {
	ID             string `json:"id"`
	ChannelID      string `json:"channelId"`
	StartMS        int64  `json:"startMs"`
	EndMS          int64  `json:"endMs"`
	ItemID         string `json:"itemId"`
	AssetID        string `json:"-"`
	LibraryID      string `json:"-"`
	SourceFence    string `json:"-"`
	Title          string `json:"title"`
	RuleID         string `json:"-"`
	BlockID        string `json:"-"`
	SourceOffsetMS int64  `json:"sourceOffsetMs"`
	SlateReason    string `json:"unavailableReason"`
}

func insertEntry(ctx context.Context, q querier, gen string, v Entry) error {
	var itemID int64
	var e error
	if v.ItemID != "" {
		itemID, e = entityid.Resolve(ctx, q, v.ItemID)
		if e != nil {
			return unavailable(e)
		}
	}
	_, e = q.ExecContext(ctx, `INSERT INTO lc_entries VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(generation_id,occurrence_id) DO NOTHING`, gen, v.ID, v.ChannelID, v.StartMS, v.EndMS, itemID, v.AssetID, v.LibraryID, v.SourceFence, v.Title, v.RuleID, v.BlockID, v.SourceOffsetMS, v.SlateReason)
	if e != nil {
		return unavailable(e)
	}
	return nil
}

type ruleCursor struct {
	Draw, Cycle                   int64
	LastSort, LastShow, LastBlock string
	Done                          bool
}

func readRuleCursor(ctx context.Context, q querier, gen, rule string) (ruleCursor, error) {
	var c ruleCursor
	e := q.QueryRowContext(ctx, `SELECT draw,cycle,last_sort,last_show,last_block,done FROM lc_rule_state WHERE generation_id=? AND rule_id=?`, gen, rule).Scan(&c.Draw, &c.Cycle, &c.LastSort, &c.LastShow, &c.LastBlock, &c.Done)
	if errors.Is(e, sql.ErrNoRows) {
		return c, nil
	}
	if e != nil {
		return c, unavailable(e)
	}
	return c, nil
}
func saveRuleCursor(ctx context.Context, q querier, gen, rule string, c ruleCursor) error {
	_, e := q.ExecContext(ctx, `INSERT INTO lc_rule_state VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(generation_id,rule_id) DO UPDATE SET draw=excluded.draw,cycle=excluded.cycle,last_sort=excluded.last_sort,last_show=excluded.last_show,last_block=excluded.last_block,done=excluded.done`, gen, rule, c.Draw, c.Cycle, c.LastSort, c.LastShow, c.LastBlock, c.Done)
	if e != nil {
		return unavailable(e)
	}
	return nil
}
func randomUnit(seed string, draw int64) float64 {
	b, _ := hex.DecodeString(digest(seed, fmt.Sprint(draw))[:16])
	v := binary.BigEndian.Uint64(b) >> 11
	return float64(v) / float64(uint64(1)<<53)
}

const candidateColumns = `COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=c.item_id),'') AS item_id,c.library_id,c.asset_id,c.source_fence,c.duration_ms,c.title,COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=c.show_id),'') AS show_id,c.season,c.episode,c.weight,c.sort_key,c.ordinal,c.item_id AS item_entity_id,c.show_id AS show_entity_id`

func scanCandidate(row *sql.Row) (Candidate, error) {
	var c Candidate
	e := row.Scan(&c.ItemID, &c.LibraryID, &c.AssetID, &c.SourceFence, &c.DurationMS, &c.Title, &c.ShowID, &c.Season, &c.Episode, &c.Weight, &c.SortKey, &c.Ordinal, &c.itemEntityID, &c.showEntityID)
	return c, e
}

// inOrderWalkSQL and inOrderHeadsSQL admit the same candidates: only each
// show's next unused episode in the cycle (argument order: cycle; then, for
// the heads form, generation and rule). The walk suits an ordered LIMIT 1; the
// heads form suits draws, which count every eligible candidate.
const inOrderWalkSQL = ` AND (c.show_id=0 OR NOT EXISTS(SELECT 1 FROM lc_candidates earlier WHERE earlier.generation_id=c.generation_id AND earlier.rule_id=c.rule_id AND earlier.show_id=c.show_id AND (earlier.season,earlier.episode,earlier.item_id)<(c.season,c.episode,c.item_id) AND NOT EXISTS(SELECT 1 FROM lc_used u WHERE u.generation_id=earlier.generation_id AND u.rule_id=earlier.rule_id AND u.item_id=earlier.item_id AND u.cycle=?)))`
const inOrderHeadsSQL = ` AND (c.show_id=0 OR c.item_id IN (SELECT (SELECT h.item_id FROM lc_candidates h WHERE h.generation_id=s.generation_id AND h.rule_id=s.rule_id AND h.show_id=s.show_id AND NOT EXISTS(SELECT 1 FROM lc_used u WHERE u.generation_id=h.generation_id AND u.rule_id=h.rule_id AND u.item_id=h.item_id AND u.cycle=?) ORDER BY h.season,h.episode,h.item_id LIMIT 1) FROM (SELECT DISTINCT generation_id,rule_id,show_id FROM lc_candidates WHERE generation_id=? AND rule_id=? AND show_id<>0) s))`

// historySQL reads the latest entries of a generation with each one's show. The
// show lookup is an index seek (lc_candidate_item), so each slot costs the
// history window, never the size of the candidate pool.
const historySQL = `SELECT e.item_id,COALESCE((SELECT c.show_id FROM lc_candidates c WHERE c.generation_id=e.generation_id AND c.item_id=e.item_id ORDER BY c.rule_id LIMIT 1),0) FROM lc_entries e WHERE e.generation_id=? ORDER BY e.start_ms DESC LIMIT ?`

// slotWrites is what one schedule slot changes. Planning a slot only reads, so
// the generator plans on a read snapshot and commits the slot in a short write
// that first checks the generation has not moved (scheduleSlot).
type slotWrites struct {
	cursors map[string]ruleCursor
	used    *usedMark
	entry   *Entry
	cursor  int64
	phase   string
}

type usedMark struct {
	rule        string
	item, cycle int64
}

// ruleCursor is a rule's cursor as the slot leaves it so far: a fallback that
// names the slot's own rule sees what the first selection decided.
func (w *slotWrites) ruleCursor(ctx context.Context, q querier, gen, rule string) (ruleCursor, error) {
	if c, ok := w.cursors[rule]; ok {
		return c, nil
	}
	return readRuleCursor(ctx, q, gen, rule)
}

// poolSize is how many candidates a rule has. Ordinals are dense from 0, so it
// is one seek on UNIQUE(generation_id,rule_id,ordinal).
func poolSize(ctx context.Context, q querier, gen, rule string) (int64, error) {
	var last int64
	e := q.QueryRowContext(ctx, `SELECT ordinal FROM lc_candidates WHERE generation_id=? AND rule_id=? ORDER BY ordinal DESC LIMIT 1`, gen, rule).Scan(&last)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, nil
	}
	if e != nil {
		return 0, unavailable(e)
	}
	return last + 1, nil
}

// maxWeight bounds a rule's candidate weights: 1 unless the owner set more.
func maxWeight(r Rule) float64 {
	top := 1.0
	for _, w := range r.Weights {
		top = max(top, w.Weight)
	}
	return top
}

// A random draw proposes candidates by ordinal. Ordinals are dense from 0, so a
// uniformly random one is a uniformly random candidate, found with one seek.
// Proposals are tested in order and the first eligible one is taken; a weighted
// rule accepts a proposal with probability weight/maxWeight. That is rejection
// sampling: the pick has exactly the distribution of a draw over the eligible
// set (uniform, or by weight) without counting that set, so a draw costs about
// the same in a pool of a hundred or a million. drawProposals go to SQLite per
// statement, for up to drawRounds statements; where eligible candidates are
// rarer than that (the end of a bag, a narrow fit) the draw counts them instead.
const (
	drawProposals = 64
	drawRounds    = 8
)

// proposalSQL tests a statement's proposals, [ordinal, threshold] pairs, in
// order. CROSS JOIN keeps the proposals outermost, so each is one seek.
const proposalSQL = `SELECT ` + candidateColumns + ` FROM json_each(?) j CROSS JOIN lc_candidates c ON c.ordinal=j.value->>0 AND c.weight>j.value->>1 WHERE `

func proposeCandidate(ctx context.Context, q querier, g *generation, r Rule, key string, draw int64, where string, args []any) (Candidate, bool, error) {
	n, e := poolSize(ctx, q, g.ID, r.ID)
	if e != nil || n == 0 {
		return Candidate{}, false, e
	}
	top := 0.0
	if r.Mode == "weighted-random" {
		top = maxWeight(r)
	}
	query := proposalSQL + where + ` ORDER BY j.key LIMIT 1`
	var proposals strings.Builder
	for round := 0; round < drawRounds; round++ {
		proposals.Reset()
		proposals.WriteByte('[')
		for i := 0; i < drawProposals; i++ {
			b, _ := hex.DecodeString(digest(key, strconv.FormatInt(draw, 10), strconv.Itoa(round*drawProposals+i))[:32])
			ordinal := int64(float64(binary.BigEndian.Uint64(b[:8])>>11) / float64(uint64(1)<<53) * float64(n))
			threshold := float64(binary.BigEndian.Uint64(b[8:])>>11) / float64(uint64(1)<<53) * top
			if i > 0 {
				proposals.WriteByte(',')
			}
			fmt.Fprintf(&proposals, "[%d,%s]", ordinal, strconv.FormatFloat(threshold, 'g', -1, 64))
		}
		proposals.WriteByte(']')
		c, e := scanCandidate(q.QueryRowContext(ctx, query, append([]any{proposals.String()}, args...)...))
		if e == nil {
			return c, true, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return c, false, unavailable(e)
		}
	}
	return Candidate{}, false, nil
}

func selectCandidate(ctx context.Context, q querier, g *generation, w *slotWrites, r Rule, block ResolvedBlock, remaining int64) (Candidate, bool, error) {
	cursor, e := w.ruleCursor(ctx, q, g.ID, r.ID)
	if e != nil {
		return Candidate{}, false, e
	}
	if block.Anchor == "block-start" && cursor.LastBlock != block.ID {
		cursor = ruleCursor{Cycle: cursor.Cycle + 1, LastBlock: block.ID}
	}
	if cursor.Done {
		return Candidate{}, false, nil
	}
	// History is explicitly limited by owner semantics, not by candidate count.
	historyLimit := max(r.DeduplicationWindow, r.MaxConsecutive)
	rows, e := q.QueryContext(ctx, historySQL, g.ID, historyLimit)
	if e != nil {
		return Candidate{}, false, unavailable(e)
	}
	recent := []int64{}
	var lastGroup int64
	consecutive := 0
	n := 0
	for rows.Next() {
		var item, show int64
		if cause := rows.Scan(&item, &show); cause != nil {
			rows.Close()
			return Candidate{}, false, unavailable(cause)
		}
		group := show
		if group == 0 {
			group = item
		}
		if n == 0 {
			lastGroup = group
		}
		if n == consecutive && group != 0 && group == lastGroup {
			consecutive++
		}
		if n < r.DeduplicationWindow && item != 0 {
			recent = append(recent, item)
		}
		n++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return Candidate{}, false, unavailable(e)
	}
	recentJSON := ""
	if len(recent) > 0 {
		encoded, cause := json.Marshal(recent)
		if cause != nil {
			return Candidate{}, false, unavailable(cause)
		}
		recentJSON = string(encoded)
	}
	inOrder := r.EpisodeMode == "in-order" || r.EpisodeMode == "marathon" || r.EpisodeMode == "rotate"
	for attempt := 0; attempt < 2; attempt++ {
		// Sequential selection: "sequential", the first cycle of "sequential-then-shuffle",
		// and the show rotation; everything else draws.
		sequential := (r.Mode == "sequential" || r.Mode == "sequential-then-shuffle" && cursor.Cycle == 0 || r.EpisodeMode == "rotate") && r.EpisodeMode != "randomized"
		args := []any{g.ID, r.ID}
		where := "c.generation_id=? AND c.rule_id=?"
		bag := r.Mode != "weighted-random" || inOrder || r.Exhaustion == "slate"
		if bag {
			where += " AND NOT EXISTS(SELECT 1 FROM lc_used u WHERE u.generation_id=c.generation_id AND u.rule_id=c.rule_id AND u.item_id=c.item_id AND u.cycle=?)"
			args = append(args, cursor.Cycle)
		}
		if len(recent) > 0 {
			// One JSON argument rather than a placeholder per title keeps the
			// statement text the same from slot to slot, so it is prepared once.
			where += " AND c.item_id NOT IN (SELECT value FROM json_each(?))"
			args = append(args, recentJSON)
		}
		if lastGroup != 0 && consecutive >= r.MaxConsecutive {
			where += " AND (CASE WHEN c.show_id=0 THEN c.item_id ELSE c.show_id END)<>?"
			args = append(args, lastGroup)
		}
		if block.Overrun == "fit" {
			where += " AND c.duration_ms<=?"
			args = append(args, remaining)
		}
		// One candidate at a time (an ordered walk, a show probe, a proposal), the
		// in-order rule checks it against its show's earlier episodes; a count of
		// the eligible set finds each show's next episode once instead
		// (countedDraw). Both forms admit the same candidates.
		walk, walkArgs := where, append([]any{}, args...)
		if inOrder {
			walk += inOrderWalkSQL
			walkArgs = append(walkArgs, cursor.Cycle)
		}
		if sequential {
			walk += " AND c.sort_key>?"
			walkArgs = append(walkArgs, cursor.LastSort)
		}
		key := g.Seed + ":" + r.ID + ":" + cursor.LastBlock
		var c Candidate
		e = sql.ErrNoRows
		lastShowID, _ := strconv.ParseInt(cursor.LastShow, 10, 64)
		if r.EpisodeMode == "marathon" && lastShowID != 0 && !(lastGroup == lastShowID && consecutive >= r.MaxConsecutive) {
			// A marathon stays with its show while the show has an eligible episode.
			// Episodes play in order, so that is only ever the show's next one, and a
			// draw among the show's candidates has that one outcome.
			c, e = scanCandidate(q.QueryRowContext(ctx, `SELECT `+candidateColumns+` FROM lc_candidates c WHERE `+walk+` AND c.show_id=? ORDER BY c.sort_key,c.item_id LIMIT 1`, append(append([]any{}, walkArgs...), lastShowID)...))
		}
		if errors.Is(e, sql.ErrNoRows) {
			if sequential {
				c, e = scanCandidate(q.QueryRowContext(ctx, `SELECT `+candidateColumns+` FROM lc_candidates c WHERE `+walk+` ORDER BY c.sort_key,c.item_id LIMIT 1`, walkArgs...))
			} else {
				var found bool
				if c, found, e = proposeCandidate(ctx, q, g, r, key, cursor.Draw, walk, walkArgs); e == nil && !found {
					c, e = countedDraw(ctx, q, g, r, inOrder, key, cursor, where, args)
				}
			}
		}
		if e == nil {
			cursor.Draw++
			cursor.LastSort = c.SortKey
			cursor.LastShow = ""
			if c.showEntityID != 0 {
				cursor.LastShow = strconv.FormatInt(c.showEntityID, 10)
			}
			cursor.LastBlock = block.ID
			w.cursors[r.ID] = cursor
			w.used = &usedMark{rule: r.ID, item: c.itemEntityID, cycle: cursor.Cycle}
			return c, true, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return c, false, unavailable(e)
		}
		// A loop resets at most once. Impossible constraints become an explicit slate
		// and can never spin the worker indefinitely.
		var unused bool
		if cause := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lc_candidates c WHERE c.generation_id=? AND c.rule_id=? AND NOT EXISTS(SELECT 1 FROM lc_used u WHERE u.generation_id=c.generation_id AND u.rule_id=c.rule_id AND u.item_id=c.item_id AND u.cycle=?))`, g.ID, r.ID, cursor.Cycle).Scan(&unused); cause != nil {
			return Candidate{}, false, unavailable(cause)
		}
		if unused {
			break
		} // Fit/dedup constraints are not exhaustion.
		if r.Exhaustion == "slate" {
			cursor.Done = true
			break
		}
		cursor.Cycle++
		cursor.LastSort = ""
		cursor.LastShow = ""
	}
	w.cursors[r.ID] = cursor
	return Candidate{}, false, nil
}

// countedDraw is the exact draw over the eligible set, for when proposals find
// nothing: it counts that set (or sums its weights) and walks to the pick.
func countedDraw(ctx context.Context, q querier, g *generation, r Rule, inOrder bool, key string, cursor ruleCursor, where string, args []any) (Candidate, error) {
	args = append([]any{}, args...)
	if inOrder {
		// Only each show's next unused episode qualifies, so find those heads once
		// per statement (one ordered seek per show) instead of checking every
		// candidate against all of its show's earlier episodes.
		where += inOrderHeadsSQL
		args = append(args, cursor.Cycle, g.ID, r.ID)
	}
	if r.Mode == "weighted-random" {
		var total sql.NullFloat64
		if cause := q.QueryRowContext(ctx, `SELECT SUM(c.weight) FROM lc_candidates c WHERE `+where, args...).Scan(&total); cause != nil {
			return Candidate{}, unavailable(cause)
		}
		if !total.Valid || total.Float64 <= 0 {
			return Candidate{}, sql.ErrNoRows
		}
		// SQLite's ordered window is disk-backed when needed. Only the selected row
		// crosses the DB boundary.
		ticket := randomUnit(key, cursor.Draw) * total.Float64
		query := `SELECT item_id,library_id,asset_id,source_fence,duration_ms,title,show_id,season,episode,weight,sort_key,ordinal,item_entity_id,show_entity_id FROM (SELECT ` + candidateColumns + `,SUM(c.weight) OVER (ORDER BY c.ordinal ROWS UNBOUNDED PRECEDING) AS running FROM lc_candidates c WHERE ` + where + `) WHERE running>? ORDER BY ordinal LIMIT 1`
		return scanCandidate(q.QueryRowContext(ctx, query, append(args, ticket)...))
	}
	var count int64
	if cause := q.QueryRowContext(ctx, `SELECT count(*) FROM lc_candidates c WHERE `+where, args...).Scan(&count); cause != nil {
		return Candidate{}, unavailable(cause)
	}
	if count == 0 {
		return Candidate{}, sql.ErrNoRows
	}
	offset := int64(randomUnit(key, cursor.Draw) * float64(count))
	return scanCandidate(q.QueryRowContext(ctx, `SELECT `+candidateColumns+` FROM lc_candidates c WHERE `+where+` ORDER BY c.ordinal LIMIT 1 OFFSET ?`, append(args, offset)...))
}
func slotAt(c Config, blocks []ResolvedBlock, at, end int64) ResolvedBlock {
	defaultSlot := func(instant int64) ResolvedBlock {
		return ResolvedBlock{ID: "default", RuleID: c.DefaultRuleID, Priority: -100001, Overrun: "finish", Anchor: "channel-cursor", Interval: BlockInterval{Start: Boundary{UTC: time.UnixMilli(instant)}, End: Boundary{UTC: time.UnixMilli(end)}}}
	}
	winner := func(instant int64) ResolvedBlock {
		selected := defaultSlot(instant)
		for _, b := range blocks {
			if b.Interval.Start.UTC.UnixMilli() <= instant && b.Interval.End.UTC.UnixMilli() > instant && (b.Priority > selected.Priority || b.Priority == selected.Priority && b.ID < selected.ID) {
				selected = b
			}
		}
		return selected
	}
	selected := winner(at)
	edges := []int64{end}
	for _, b := range blocks {
		for _, edge := range []int64{b.Interval.Start.UTC.UnixMilli(), b.Interval.End.UTC.UnixMilli()} {
			if edge > at && edge < end {
				edges = append(edges, edge)
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i] < edges[j] })
	next := end
	for _, edge := range edges {
		if edge == end || winner(edge).ID != selected.ID {
			next = edge
			break
		}
	}
	selected.Interval.End.UTC = time.UnixMilli(next)
	return selected
}

// planSlot works out the next slot of g's schedule without writing anything:
// what plays, and the rows that records (w).
func planSlot(ctx context.Context, q querier, g *generation, w *slotWrites) error {
	*w = slotWrites{cursors: map[string]ruleCursor{}, cursor: g.Cursor, phase: g.Phase}
	if g.Cursor >= g.End {
		w.phase = "validating"
		return nil
	}
	blocks, e := resolvedBlocks(g.Config, time.UnixMilli(g.Start), time.UnixMilli(g.End))
	if e != nil {
		return e
	}
	slot := slotAt(g.Config, blocks, g.Cursor, g.End)
	rules := map[string]Rule{}
	for _, r := range g.Config.Rules {
		rules[r.ID] = r
	}
	r, ok := rules[slot.RuleID]
	if !ok {
		return ErrInvalid
	}
	selected, found, e := selectCandidate(ctx, q, g, w, r, slot, slot.Interval.End.UTC.UnixMilli()-g.Cursor)
	if e != nil {
		return e
	}
	if !found && slot.Fallback != "" {
		r = rules[slot.Fallback]
		selected, found, e = selectCandidate(ctx, q, g, w, r, slot, slot.Interval.End.UTC.UnixMilli()-g.Cursor)
		if e != nil {
			return e
		}
	}
	v := Entry{ChannelID: g.ChannelID, StartMS: g.Cursor, RuleID: r.ID, BlockID: slot.ID}
	if found {
		v.EndMS = g.Cursor + selected.DurationMS
		if slot.Overrun == "cut" && v.EndMS > slot.Interval.End.UTC.UnixMilli() {
			v.EndMS = slot.Interval.End.UTC.UnixMilli()
		}
		v.ItemID = selected.ItemID
		v.AssetID = selected.AssetID
		v.LibraryID = selected.LibraryID
		v.SourceFence = selected.SourceFence
		v.Title = selected.Title
	} else {
		v.EndMS = min(g.Cursor+int64(30*time.Minute/time.Millisecond), slot.Interval.End.UTC.UnixMilli())
		v.Title = unavailableTitle
		v.SlateReason = "no-eligible-media"
	}
	if v.EndMS <= v.StartMS {
		return ErrInvalid
	}
	v.ID = digest(g.ChannelID, r.ID, fmt.Sprint(v.StartMS), fmt.Sprint(v.EndMS), v.ItemID, v.SourceFence)[:48]
	w.entry = &v
	w.cursor = v.EndMS
	if w.cursor >= g.End {
		w.phase = "validating"
	}
	return nil
}

// applySlot writes a planned slot and moves g past it.
func applySlot(ctx context.Context, q querier, g *generation, w *slotWrites) error {
	rules := make([]string, 0, len(w.cursors))
	for id := range w.cursors {
		rules = append(rules, id)
	}
	sort.Strings(rules)
	for _, id := range rules {
		if e := saveRuleCursor(ctx, q, g.ID, id, w.cursors[id]); e != nil {
			return e
		}
	}
	if w.used != nil {
		if _, e := q.ExecContext(ctx, `INSERT INTO lc_used VALUES(?,?,?,?) ON CONFLICT(generation_id,rule_id,item_id) DO UPDATE SET cycle=excluded.cycle`, g.ID, w.used.rule, w.used.item, w.used.cycle); e != nil {
			return unavailable(e)
		}
	}
	if w.entry != nil {
		if e := insertEntry(ctx, q, g.ID, *w.entry); e != nil {
			return e
		}
	}
	g.Cursor, g.Phase = w.cursor, w.phase
	return nil
}

// scheduleOne plans and writes one slot in tx (the builder preview's first day).
func scheduleOne(ctx context.Context, tx *sql.Tx, g *generation) error {
	var w slotWrites
	q := g.statements(tx)
	if e := planSlot(ctx, q, g, &w); e != nil {
		return e
	}
	return applySlot(ctx, q, g, &w)
}

// scheduleQuantum bounds one pass over a generation's slots. Each slot commits
// on its own, so a pass that runs out of time keeps every slot it made.
const scheduleQuantum = time.Second

// errMoved ends a pass whose generation changed under it: superseded, or taken
// over once its lease lapsed. That is not a failure of the pass.
var errMoved = errors.New("librarychannels: generation moved")

// scheduleSlots schedules up to EntryBatch slots of g within scheduleQuantum
// under the lease token, then hands the lease back.
func (s *Store) scheduleSlots(ctx context.Context, class dbwork.Class, g *generation, token string) error {
	started := time.Now()
	var e error
	for n := 0; n < EntryBatch && g.Phase == "scheduling"; n++ {
		if n > 0 && (time.Since(started) >= scheduleQuantum || !dbwork.Yield(ctx)) {
			break
		}
		if e = s.scheduleSlot(ctx, class, g, token); e != nil {
			break
		}
	}
	if errors.Is(e, errMoved) {
		e = nil
	}
	// Hand the lease back so the next pass need not wait for it to lapse, also
	// when this one ended at its deadline.
	release, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	if _, cause := dbwork.ExecWrite(release, s.db, class, `UPDATE lc_generations SET lease_until_ms=0,lease_token='' WHERE id=? AND lease_token=?`, g.ID, token); cause != nil {
		e = errors.Join(e, unavailable(cause))
	}
	return e
}

// scheduleSlot plans one slot on a read snapshot, outside the writer, then
// commits it. The plan reads only the generation's own rows, which change only
// under its lease, and every slot moves cursor_ms or the phase; so the write
// first checks the lease is still this pass's and the cursor and phase are
// where the plan found them.
func (s *Store) scheduleSlot(ctx context.Context, class dbwork.Class, g *generation, token string) error {
	var w slotWrites
	read, done, e := dbwork.BeginRead(ctx, s.db)
	if e != nil {
		return unavailable(e)
	}
	e = planSlot(ctx, g.statements(read), g, &w)
	done()
	if e != nil {
		return e
	}
	gated, e := dbwork.Begin(ctx, s.db, class)
	if e != nil {
		return unavailable(e)
	}
	defer gated.Rollback()
	tx := gated.Tx()
	result, e := tx.ExecContext(ctx, `UPDATE lc_generations SET cursor_ms=?,phase=? WHERE id=? AND status='pending' AND phase='scheduling' AND lease_token=? AND cursor_ms=?`, w.cursor, w.phase, g.ID, token, g.Cursor)
	if e != nil {
		return unavailable(e)
	}
	if n, cause := result.RowsAffected(); cause != nil || n != 1 {
		return errors.Join(errMoved, cause)
	}
	if e = applySlot(ctx, g.statements(tx), g, &w); e != nil {
		return e
	}
	if e = gated.Commit(); e != nil {
		return unavailable(e)
	}
	return nil
}
func publishGeneration(ctx context.Context, tx *sql.Tx, g *generation, now time.Time) error {
	var count, bad int
	var first, last sql.NullInt64
	if cause := tx.QueryRowContext(ctx, `SELECT count(*),min(start_ms),max(end_ms) FROM lc_entries WHERE generation_id=?`, g.ID).Scan(&count, &first, &last); cause != nil {
		return unavailable(cause)
	}
	if count == 0 || !first.Valid || !last.Valid || last.Int64 < g.End {
		return ErrUnavailable
	}
	if cause := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT start_ms,LAG(end_ms) OVER(ORDER BY start_ms) previous_end FROM lc_entries WHERE generation_id=?) WHERE previous_end IS NOT NULL AND previous_end<>start_ms`, g.ID).Scan(&bad); cause != nil {
		return unavailable(cause)
	}
	if bad != 0 {
		return ErrUnavailable
	}
	state, health := "healthy", ""
	if g.Count == 0 {
		state = "no-eligible-media"
		if g.Unresolved > 0 {
			state = "unresolved-duration"
		}
		health = state
	}
	result, e := tx.ExecContext(ctx, `UPDATE lc_channels SET active_generation=?,generated_through_ms=?,state=?,health_code=? WHERE id=? AND revision=? AND enabled=1 AND removed=0`, g.ID, last.Int64, state, health, g.ChannelID, g.Revision)
	if e != nil {
		return unavailable(e)
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `UPDATE lc_generations SET status='published',published_ms=?,start_ms=?,end_ms=? WHERE id=? AND status='pending'`, now.UnixMilli(), first.Int64, last.Int64, g.ID); e != nil {
		return unavailable(e)
	}
	g.Phase = "published"
	return nil
}

// Maintain queues stale/cancelled catalog work and tail extensions, without
// rewriting already committed current entries or running playback references.
func (s *Store) Maintain(ctx context.Context) error {
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if e != nil {
		return unavailable(e)
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	// Advance a durable keyset cursor even when the first eight channels have
	// healthy seven-day schedules. Healthy rows must not starve later channels.
	var lastPosition int
	var lastID string
	if e = tx.QueryRowContext(ctx, `SELECT position,channel_id FROM lc_maintenance_cursor WHERE singleton=1`).Scan(&lastPosition, &lastID); e != nil {
		return unavailable(e)
	}
	ids := []string{}
	position, channelID := lastPosition, lastID
	for _, wrap := range []bool{false, true} {
		if len(ids) == 8 {
			break
		}
		comparison := ">"
		if wrap {
			comparison = "<="
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,position FROM lc_channels WHERE enabled=1 AND removed=0 AND (position,id) `+comparison+` (?,?) AND NOT EXISTS(SELECT 1 FROM lc_generations g WHERE g.channel_id=lc_channels.id AND g.status='pending') ORDER BY position,id LIMIT ?`, lastPosition, lastID, 8-len(ids))
		if err != nil {
			return unavailable(err)
		}
		for rows.Next() {
			if cause := rows.Scan(&channelID, &position); cause != nil {
				rows.Close()
				return unavailable(cause)
			}
			ids = append(ids, channelID)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return unavailable(err)
		}
	}
	if len(ids) > 0 {
		if _, e = tx.ExecContext(ctx, `UPDATE lc_maintenance_cursor SET position=?,channel_id=? WHERE singleton=1`, position, channelID); e != nil {
			return unavailable(e)
		}
	}
	for _, id := range ids {
		c, e := readChannel(ctx, tx, id)
		if e != nil {
			return e
		}
		now := s.now()
		tail := false
		boundary, e := replacementBoundary(ctx, tx, id, c.Generation, now)
		if e != nil {
			return e
		}
		if c.Generation != "" {
			var oldFence string
			if cause := tx.QueryRowContext(ctx, `SELECT catalog_fence FROM lc_generations WHERE id=?`, c.Generation).Scan(&oldFence); cause != nil {
				return unavailable(cause)
			}
			fence, e := catalogFence(ctx, tx, c.Config)
			if e != nil {
				return e
			}
			if fence != oldFence {
				c.State = "stale"
			}
		}
		if c.Generation != "" && c.State != "stale" {
			through, e := time.Parse(time.RFC3339, c.GeneratedThrough)
			if e == nil && through.After(now.Add(6*24*time.Hour)) {
				continue
			}
			tail = true
		}
		if e = s.enqueueTx(ctx, tx, c.Config, c.Revision, c.Generation, boundary, tail); e != nil {
			return e
		}
	}
	return gated2.Commit()
}
func (s *Store) Run(ctx context.Context) {
	wake := workerloop.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "lc_*", "catalog_entities", "catalog_episodes", "catalog_asset_links", "catalog_assets", "episode_asset_boundaries", "playback_origin_roots", "playback_origin_assets", "playback_origin_items", "playback_origin_associations", "library_revisions", "personal_items")
	defer unregister()
	maintained := time.Time{}
	workerloop.Run(ctx, "librarychannels.generator", wake, func(ctx context.Context) time.Duration {
		check, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if time.Since(maintained) >= 10*time.Second {
			maintained = time.Now()
			_ = s.Maintain(check)
		}
		// RunBatch says whether it found a generation to work on. When it did,
		// there is probably another behind it; when it did not, there is nothing
		// to poll for and the next change will wake this loop.
		if worked, _ := s.RunBatch(check); worked {
			return 250 * time.Millisecond
		}
		if pruned, _ := s.PruneGenerations(check); pruned {
			return 250 * time.Millisecond
		}
		return 0
	})
}

// Stable block ordering is also used by preview consumers.
func SortBlocks(v []ResolvedBlock) {
	sort.Slice(v, func(i, j int) bool {
		if !v[i].Interval.Start.UTC.Equal(v[j].Interval.Start.UTC) {
			return v[i].Interval.Start.UTC.Before(v[j].Interval.Start.UTC)
		}
		if v[i].Priority != v[j].Priority {
			return v[i].Priority > v[j].Priority
		}
		return v[i].ID < v[j].ID
	})
}
