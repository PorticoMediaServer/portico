package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// This is one current show's raw walk, never an episode/value graph. Large
// source keys are lossless O(B) scalar exceptions; labels never enter the set.
type restrictedShowCursor struct {
	Reclaimed    int64                     `json:"reclaimed"`
	Started      bool                      `json:"started"`
	Show         int64                     `json:"show"`
	Episode      int64                     `json:"episode"`
	Season       *int64                    `json:"season"`
	Number       int64                     `json:"number"`
	Group        bool                      `json:"group"`
	Eligible     bool                      `json:"eligible"`
	Phase        string                    `json:"phase"`
	Policy       FacetBaseItemPolicyCursor `json:"policy"`
	TermStarted  bool                      `json:"termStarted"`
	Provider     string                    `json:"provider"`
	Source       string                    `json:"source"`
	FieldStarted bool                      `json:"fieldStarted"`
	Field        string                    `json:"field"`
	Value        string                    `json:"value"`
}

// nextRestrictedShow consumes one raw source row or one raw8 policy/reclaim
// quantum. Rejected source rows still advance the complete source key. The
// caller commits contributions, counts and this checkpoint in one transaction.
func (p RestrictedFacetBaseProducer) nextRestrictedShow(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot, r identity.ContentRestrictions, cursor string) (FacetBaseBatch, error) {
	var c restrictedShowCursor
	var out FacetBaseBatch
	if cursor != "" && json.Unmarshal([]byte(cursor), &c) != nil {
		return out, ErrFacetBaseInput
	}
	switch c.Phase {
	case "", "episodes":
		var show, episode, number int64
		var season sql.NullInt64
		read := func(where string, args ...any) error {
			return tx.QueryRowContext(ctx, `SELECT show_id,season_id,number,entity_id FROM catalog_episodes INDEXED BY catalog_episodes_show `+where+` ORDER BY show_id,season_id,number,entity_id LIMIT 1`, args...).Scan(&show, &season, &number, &episode)
		}
		var err error
		if !c.Started {
			err = read("")
		} else if c.Season != nil {
			err = read(`WHERE (show_id,season_id,number,entity_id)>(?,?,?,?)`, c.Show, *c.Season, c.Number, c.Episode)
		} else {
			err = read(`WHERE show_id=? AND season_id IS NULL AND (number,entity_id)>(?,?)`, c.Show, c.Number, c.Episode)
			if errors.Is(err, sql.ErrNoRows) {
				err = read(`WHERE show_id=? AND season_id IS NOT NULL`, c.Show)
			}
			if errors.Is(err, sql.ErrNoRows) {
				err = read(`WHERE show_id>?`, c.Show)
			}
		}
		end := errors.Is(err, sql.ErrNoRows)
		if err != nil && !end {
			return out, err
		}
		if c.Group && (end || show != c.Show) {
			c.Phase = "clear"
			break // Do not consume the next show's row until this group's set is empty.
		}
		if end {
			out.Done = true
			break
		}
		c.Started, c.Group = true, true
		c.Show, c.Episode, c.Number = show, episode, number
		c.Season = nil
		if season.Valid {
			v := season.Int64
			c.Season = &v
		}
		c.Phase = "episodes"
		var item sql.NullInt64
		var library int64
		err = tx.QueryRowContext(ctx, `SELECT ep.item_id,ep.library_id FROM catalog_browse_rows ep JOIN catalog_browse_rows sh ON sh.entity_id=? WHERE ep.entity_id=?`, show, episode).Scan(&item, &library)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return out, err
		}
		if library != s.Scope.LibraryID || !item.Valid {
			break
		}
		c.Policy = FacetBaseItemPolicyCursor{Item: item.Int64}
		c.Phase = "policy"
	case "policy":
		done, visible, err := StepFacetBaseItemPolicy(ctx, tx, r, &c.Policy)
		if err != nil {
			return out, err
		}
		if !done {
			break
		}
		c.Phase = "episodes"
		if !visible {
			break
		}
		first := !c.Eligible
		c.Eligible = true
		if s.Scope.Field == "year" || s.Scope.Field == "decade" {
			if first {
				var year sql.NullInt64
				if err = tx.QueryRowContext(ctx, `SELECT year FROM catalog_browse_rows WHERE entity_id=?`, c.Show).Scan(&year); err != nil {
					return out, err
				}
				if year.Valid && year.Int64 >= 1800 && year.Int64 <= 2199 {
					v := year.Int64
					if s.Scope.Field == "decade" {
						v = v / 10 * 10
					}
					if err = restrictedShowContribute(ctx, tx, s, strconv.FormatInt(v, 10), &out); err != nil {
						return out, err
					}
				}
			}
		} else if s.Scope.Field == "genre" {
			c.TermStarted = false
			c.Provider = ""
			c.Source = ""
			c.Phase = "episodeGenres"
			if first {
				c.Phase = "showGenres"
			}
		} else {
			c.FieldStarted = false
			c.Field = ""
			c.Value = ""
			c.Phase = "fields"
		}
	case "showGenres", "episodeGenres":
		subject := c.Episode
		if c.Phase == "showGenres" {
			subject = c.Show
		}
		q := `SELECT src.provider,src.source_id,t.vocab,COALESCE(src.label_override,t.label) FROM catalog_term_sources src LEFT JOIN catalog_terms t ON t.id=src.term_id WHERE src.entity_id=?`
		args := []any{subject}
		if c.TermStarted {
			q += ` AND (src.provider,src.source_id)>(?,?)`
			args = append(args, c.Provider, c.Source)
		}
		q += ` ORDER BY src.provider,src.source_id LIMIT 1`
		var provider, source string
		var vocab sql.NullInt64
		var value sql.NullString
		err := tx.QueryRowContext(ctx, q, args...).Scan(&provider, &source, &vocab, &value)
		if errors.Is(err, sql.ErrNoRows) {
			if c.Phase == "showGenres" {
				c.Phase = "episodeGenres"
			} else {
				c.Phase = "episodes"
			}
			c.TermStarted = false
			c.Provider = ""
			c.Source = ""
			break
		}
		if err != nil {
			return out, err
		}
		c.TermStarted = true
		c.Provider = provider
		c.Source = source
		if vocab.Valid && vocab.Int64 == 1 && value.Valid && value.String != "" {
			if err = restrictedShowContribute(ctx, tx, s, value.String, &out); err != nil {
				return out, err
			}
		}
	case "fields":
		q := `SELECT field,value FROM catalog_show_facet_episodes WHERE episode_id=?`
		args := []any{c.Episode}
		if c.FieldStarted {
			q += ` AND (field,value)>(?,?)`
			args = append(args, c.Field, c.Value)
		}
		q += ` ORDER BY field,value LIMIT 1`
		var field, value string
		err := tx.QueryRowContext(ctx, q, args...).Scan(&field, &value)
		if errors.Is(err, sql.ErrNoRows) {
			c.Phase = "episodes"
			c.Field = ""
			c.Value = ""
			c.FieldStarted = false
			break
		}
		if err != nil {
			return out, err
		}
		c.FieldStarted = true
		c.Field = field
		c.Value = value
		if field == s.Scope.Field && value != "" {
			if err = restrictedShowContribute(ctx, tx, s, value, &out); err != nil {
				return out, err
			}
		}
	case "clear":
		done, err := retireRestrictedShowSeenStep(ctx, tx, s.ID)
		if err != nil {
			return out, err
		}
		c.Reclaimed++ // Deletion progress must also advance the durable checkpoint.
		if done {
			c.Group = false
			c.Eligible = false
			c.Phase = "episodes"
		}
	default:
		return out, ErrFacetBaseInput
	}
	raw, err := json.Marshal(c)
	out.Cursor = string(raw)
	return out, err
}

func restrictedShowContribute(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot, value string, out *FacetBaseBatch) error {
	f, err := unrestrictedFacetPublicFact(ctx, tx, s.Scope.Field, value, 1)
	if err != nil {
		return err
	}
	id, err := ensureFacetBaseValueID(ctx, tx, s.Scope, f.Value)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO catalog_facet_base_show_seen(base_id,value_id) VALUES(?,?) ON CONFLICT(base_id,value_id) DO NOTHING`, s.ID, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 0 {
		f, err = incrementFacetBaseFact(ctx, tx, s, value, 1)
		if err == nil {
			out.Facts = []FacetBaseFact{f}
		}
	}
	return err
}

func retireRestrictedShowSeenStep(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM catalog_facet_base_show_seen WHERE base_id=? AND value_id IN(SELECT value_id FROM catalog_facet_base_show_seen WHERE base_id=? ORDER BY value_id LIMIT 8)`, id, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 0, err
}
