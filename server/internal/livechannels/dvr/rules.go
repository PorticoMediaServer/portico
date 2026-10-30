package dvr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func scanRule(row scanner) (Rule, error) {
	var r Rule
	var config string
	var at int64
	e := row.Scan(&r.ID, &r.Revision, &config, &r.ReconcileState, &r.Diagnostic, &at)
	if e != nil {
		return r, e
	}
	if json.Unmarshal([]byte(config), &r.Config) != nil {
		return r, ErrUnavailable
	}
	if r.Config.BlockedKeywords == nil {
		r.Config.BlockedKeywords = []string{}
	}
	r.UpdatedAt = time.UnixMilli(at).UTC().Format(time.RFC3339Nano)
	return r, nil
}

const ruleColumns = `r.id,r.revision,r.config_json,COALESCE(w.phase,'idle'),COALESCE(w.diagnostic,''),r.updated_ms`

func ruleTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner, id string) (Rule, error) {
	r, e := scanRule(tx.QueryRowContext(ctx, `SELECT `+ruleColumns+` FROM dvr_rules r LEFT JOIN dvr_rule_work w ON w.rule_id=r.id WHERE r.owner_key=? AND r.id=? AND r.deleted=0`, o.Key(), id))
	if errors.Is(e, sql.ErrNoRows) {
		return r, ErrDenied
	}
	return r, e
}
func (s *Store) SaveRule(ctx context.Context, a livechannels.Authority, o livechannels.Owner, in RuleInput) (Rule, error) {
	var out Rule
	if !hexID.MatchString(in.RequestID) || !canonicalID.MatchString(in.ID) || in.ExpectedRevision < 0 || !in.Config.Valid() {
		return out, ErrInvalid
	}
	e := s.transaction(ctx, a, o, func(tx *sql.Tx, allowed func(string, string) bool) error {
		if err := s.durable(ctx, tx, o, in.Config.SourceID, ""); err != nil {
			return ErrDenied
		}
		if !allowed(in.Config.SourceID, "") {
			return ErrDenied
		}
		for _, id := range append(append([]string{}, in.Config.AllowedChannels...), in.Config.BlockedChannels...) {
			if !allowed(in.Config.SourceID, id) {
				return ErrDenied
			}
		}
		originalInput := in
		replay, e := receiptTx(ctx, tx, o, in.RequestID, in, &out)
		if e != nil || replay {
			return e
		}
		if in.UseDefaults {
			var err error
			in.Config.Options, err = DefaultOptionsTx(ctx, tx)
			if err != nil {
				return err
			}
		}
		old, e := ruleTx(ctx, tx, o, in.ID)
		create := errors.Is(e, ErrDenied)
		if e != nil && !create {
			return e
		}
		if create && in.ExpectedRevision != 0 || !create && old.Revision != in.ExpectedRevision {
			return ErrConflict
		}
		if create {
			if !in.Anchor.Valid() || in.Anchor.SourceID != in.Config.SourceID || !allowed(in.Anchor.SourceID, in.Anchor.ChannelID) {
				return ErrInvalid
			}
			p, e := s.live.ProgrammeTx(ctx, tx, in.Anchor.SourceID, in.Anchor.ChannelID, in.Anchor.Generation, in.Anchor.ProgrammeID)
			if e != nil {
				return e
			}
			if e = programmeVisibleTx(ctx, tx, o, p); e != nil {
				return e
			}
			if !in.Config.identityMatches(p) {
				return ErrInvalid
			}
			if in.Config.Episodes == "new" && p.NewEvidence != "new" && p.NewEvidence != "repeat" {
				return ErrUnsupportedPredicate
			}
		} else {
			// An edit may refine predicates/padding, never change the series identity.
			if old.Config.SourceID != in.Config.SourceID || old.Config.SeriesID != in.Config.SeriesID || old.Config.MatchKind != in.Config.MatchKind || old.Config.Title != in.Config.Title || old.Config.ChannelID != in.Config.ChannelID {
				return ErrConflict
			}
			if old.Config.Episodes != "new" && in.Config.Episodes == "new" {
				var known bool
				predicate, args := in.Config.identitySQL()
				args = append([]any{in.Config.SourceID}, args...)
				e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM live_sources s JOIN live_programmes p ON p.generation_id=s.active_generation LEFT JOIN live_programme_metadata m ON m.generation_id=p.generation_id AND m.id=p.id WHERE s.id=? AND `+predicate+` AND m.new_evidence IN('new','repeat'))`, args...).Scan(&known)
				if e != nil {
					return e
				}
				if !known {
					return ErrUnsupportedPredicate
				}
			}
		}
		// Draft/disabled rules are useful even where the capture adapter is absent.
		// Enabling a rule must never promise captures that this runtime cannot make.
		if in.Config.Enabled && !s.captureAvailable {
			return ErrCaptureUnavailable
		}
		var generation, state string
		e = tx.QueryRowContext(ctx, `SELECT active_generation,state FROM live_sources WHERE id=?`, in.Config.SourceID).Scan(&generation, &state)
		if e != nil {
			return ErrConflict
		}
		if in.Config.Enabled && state != "active" {
			return livechannels.ErrConflict
		}
		b, _ := json.Marshal(in.Config)
		now := s.now().UnixMilli()
		targetID, targetRevision := in.ID, in.ExpectedRevision+1
		if create {
			var priorID string
			var priorRevision int64
			var deleted bool
			e = tx.QueryRowContext(ctx, `SELECT id,revision,deleted FROM dvr_rules WHERE owner_key=? AND source_id=? AND series_id=? AND match_title=? AND match_channel=?`, o.Key(), in.Config.SourceID, in.Config.SeriesID, strings.ToLower(strings.TrimSpace(in.Config.Title)), in.Config.ChannelID).Scan(&priorID, &priorRevision, &deleted)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if e == nil && !deleted {
				return ErrConflict
			}
			var collision bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_rules WHERE id=? AND id<>?)`, in.ID, priorID).Scan(&collision); e != nil {
				return e
			}
			if collision {
				return ErrConflict
			}
			if priorID != "" {
				targetID, targetRevision = priorID, priorRevision+1
				_, e = tx.ExecContext(ctx, `UPDATE dvr_rules SET deleted=0,enabled=?,revision=?,config_json=?,updated_ms=? WHERE id=? AND owner_key=?`, in.Config.Enabled, targetRevision, string(b), now, targetID, o.Key())
			} else {
				_, e = tx.ExecContext(ctx, `INSERT INTO dvr_rules(id,owner_key,authority,account_id,profile_id,source_id,series_id,match_title,match_channel,revision,config_json,enabled,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,1,?,?,?)`, in.ID, o.Key(), o.Authority, o.AccountID, o.ProfileID, in.Config.SourceID, in.Config.SeriesID, strings.ToLower(strings.TrimSpace(in.Config.Title)), in.Config.ChannelID, string(b), in.Config.Enabled, now)
			}
		} else {
			_, e = tx.ExecContext(ctx, `UPDATE dvr_rules SET revision=revision+1,config_json=?,enabled=?,updated_ms=? WHERE id=? AND owner_key=?`, string(b), in.Config.Enabled, now, in.ID, o.Key())
		}
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO dvr_rule_work VALUES(?,?,?,'existing','',0,?,'') ON CONFLICT(rule_id) DO UPDATE SET rule_revision=excluded.rule_revision,generation=excluded.generation,phase='existing',cursor='',not_before_ms=0,updated_ms=excluded.updated_ms,diagnostic=''`, targetID, targetRevision, generation, now)
		if e != nil {
			return e
		}
		if in.Config.Enabled {
			_, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO live_source_dependencies VALUES(?,'rule',?)`, in.Config.SourceID, targetID)
		} else {
			_, e = tx.ExecContext(ctx, `DELETE FROM live_source_dependencies WHERE kind='rule' AND id=?`, targetID)
		}
		if e != nil {
			return e
		}
		out, e = ruleTx(ctx, tx, o, targetID)
		if e != nil {
			return e
		}
		if e = touchTx(ctx, tx, o); e != nil {
			return e
		}
		return saveReceiptTx(ctx, tx, o, in.RequestID, originalInput, out, s.now())
	})
	return out, e
}

type DeleteRuleInput struct {
	Mutation
	Future string `json:"future"`
}

func (s *Store) DeleteRule(ctx context.Context, a livechannels.Authority, o livechannels.Owner, id string, in DeleteRuleInput) error {
	if in.Future != "keep" && in.Future != "cancel" {
		return ErrInvalid
	}
	if !canonicalID.MatchString(id) || !hexID.MatchString(in.RequestID) || in.ExpectedRevision < 1 {
		return ErrInvalid
	}
	input := struct {
		Kind, ID string
		Mutation DeleteRuleInput
	}{"delete-rule", id, in}
	return s.transaction(ctx, a, o, func(tx *sql.Tx, _ func(string, string) bool) error {
		var deleted bool
		replay, e := receiptTx(ctx, tx, o, in.RequestID, input, &deleted)
		if e != nil || replay {
			return e
		}
		old, e := ruleTx(ctx, tx, o, id)
		if e != nil {
			return e
		}
		if old.Revision != in.ExpectedRevision {
			return ErrConflict
		}
		if in.Future == "keep" {
			// Explicitly detached future intents no longer follow this rule.
			if _, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET manual=1,rule_id='',rule_revision=0,revision=revision+1,updated_ms=? WHERE rule_id=? AND state IN('scheduled','conflicted','waiting-source','waiting-guide')`, s.now().UnixMilli(), id); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, `UPDATE dvr_rules SET enabled=0,deleted=1,revision=revision+1,updated_ms=? WHERE id=?`, s.now().UnixMilli(), id)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE dvr_rule_work SET rule_revision=rule_revision+1,phase='existing',cursor='',not_before_ms=0,updated_ms=? WHERE rule_id=?`, s.now().UnixMilli(), id)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM live_source_dependencies WHERE kind='rule' AND id=?`, id); e != nil {
			return e
		}
		if e = touchTx(ctx, tx, o); e != nil {
			return e
		}
		return saveReceiptTx(ctx, tx, o, in.RequestID, input, true, s.now())
	})
}

// PreviewRule evaluates an exact guide generation using the same matcher as the
// durable worker. It returns bounded samples and complete counts, not a hidden
// first-N decision. No reservation/capture is created by previewing a rule.
type RulePreview struct {
	Matches            int                      `json:"matches"`
	UnknownNewEvidence int                      `json:"unknownNewEvidence"`
	Sample             []livechannels.Programme `json:"sample"`
	Generation         string                   `json:"generation"`
	Capacity           livechannels.Capacity    `json:"capacity"`
}

func (s *Store) PreviewRule(ctx context.Context, a livechannels.Authority, o livechannels.Owner, c RuleConfig) (RulePreview, error) {
	out := RulePreview{Sample: []livechannels.Programme{}}
	if !c.Valid() {
		return out, ErrInvalid
	}
	e := s.snapshot(ctx, a, o, func(tx *sql.Tx, allowed func(string, string) bool) error {
		if !allowed(c.SourceID, "") {
			return ErrDenied
		}
		if e := tx.QueryRowContext(ctx, `SELECT active_generation FROM live_sources WHERE id=? AND state='active'`, c.SourceID).Scan(&out.Generation); e != nil {
			return ErrConflict
		}
		var e error
		out.Capacity, e = livechannels.CapacityTx(ctx, tx, c.SourceID)
		if e != nil {
			return e
		}
		programmeAllowed, err := programmePredicateTx(ctx, tx, o)
		if err != nil {
			return err
		}
		predicate, args := c.identitySQL()
		args = append([]any{out.Generation}, args...)
		args = append(args, s.now().UTC().Format(time.RFC3339))
		rows, e := tx.QueryContext(ctx, programmeQuery+` WHERE p.generation_id=? AND `+predicate+` AND p.end_utc>? ORDER BY p.start_utc,p.id`, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		c.Enabled = true
		for rows.Next() {
			p, e := scanProgramme(rows)
			if e != nil {
				return e
			}
			if !allowed(c.SourceID, p.ChannelID) || !programmeAllowed(programmeRating(p)) {
				continue
			}
			if c.Episodes == "new" && p.NewEvidence == "unknown" {
				out.UnknownNewEvidence++
			}
			if c.Matches(p) {
				out.Matches++
				if len(out.Sample) < 12 {
					out.Sample = append(out.Sample, p)
				}
			}
		}
		return rows.Err()
	})
	return out, e
}

const programmeQuery = `SELECT p.id,p.channel_id,p.title,p.start_utc,p.end_utc,p.lineage,COALESCE(m.series_id,''),COALESCE(m.episode_id,''),COALESCE(m.new_evidence,'unknown'),COALESCE(m.description,''),COALESCE(m.facts,'{}') FROM live_programmes p LEFT JOIN live_programme_metadata m ON m.generation_id=p.generation_id AND m.id=p.id`

func scanProgramme(row scanner) (livechannels.Programme, error) {
	var p livechannels.Programme
	var facts string
	e := row.Scan(&p.ID, &p.ChannelID, &p.Title, &p.Start, &p.End, &p.Lineage, &p.SeriesID, &p.EpisodeID, &p.NewEvidence, &p.Description, &facts)
	p.ApplyFacts(facts)
	return p, e
}
