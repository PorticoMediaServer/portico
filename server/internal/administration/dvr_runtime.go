package administration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func runtimeDefaults(ctx context.Context, tx *sql.Tx) (DVRDocument, error) {
	v := DefaultDVRDefaults()
	var rev int64
	var days, count int
	err := tx.QueryRowContext(ctx, `SELECT revision,before_seconds*1000,after_seconds*1000,retention_days,episode_limit,live_tuner_conflict FROM dvr_defaults WHERE singleton=1`).Scan(&rev, &v.PrePaddingMs, &v.PostPaddingMs, &days, &count, &v.LiveTunerConflict)
	if count > 0 {
		v.Keep = KeepPolicy{Mode: "keep-count", KeepCount: count}
	} else if days > 0 {
		v.Keep = KeepPolicy{Mode: "keep-days", KeepDays: days}
	}
	return DVRDocument{rev, digestOf(v), v, []FolderToken{}, dvrEnumerations()}, err
}
func (s *Service) runtimeDVRSettings(ctx context.Context, auth Authorize) (DVRDocument, error) {
	var out DVRDocument
	err := s.snapshot(ctx, auth, func(tx *sql.Tx) error { var e error; out, e = runtimeDefaults(ctx, tx); return e })
	return out, err
}
func (s *Service) saveRuntimeDVRSettings(ctx context.Context, auth Authorize, c Change[DVRDefaults]) (DVRDocument, error) {
	var out DVRDocument
	if !validOperationID(c.OperationID) {
		return out, ErrInput
	}
	if err := validateDVRDefaults(&c.Settings); err != nil {
		return out, err
	}
	err := s.transaction(ctx, auth, func(tx *sql.Tx) error {
		r, err := usermutation.CheckContext(ctx, tx, s.milliseconds())
		if err != nil {
			return err
		}
		if r != nil {
			out, err = runtimeDefaults(ctx, tx)
			return err
		}

		saved, replay, err := receipt[DVRDocument](ctx, tx, dvrScope, c.OperationID, digestOf(c))
		if err != nil {
			return err
		}
		if replay {
			out = saved
			return nil
		}
		days, count := 0, 0
		if c.Settings.Keep.Mode == "keep-days" {
			days = c.Settings.Keep.KeepDays
		}
		if c.Settings.Keep.Mode == "keep-count" {
			count = c.Settings.Keep.KeepCount
		}
		result, err := tx.ExecContext(ctx, `UPDATE dvr_defaults SET revision=revision+1,before_seconds=?,after_seconds=?,retention_days=?,episode_limit=?,live_tuner_conflict=? WHERE singleton=1 AND revision=?`, c.Settings.PrePaddingMs/1000, c.Settings.PostPaddingMs/1000, days, count, c.Settings.LiveTunerConflict, c.ExpectedRevision)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		out, err = runtimeDefaults(ctx, tx)
		if err != nil {
			return err
		}
		return saveSettingsReceipt(ctx, tx, dvrScope, c.OperationID, digestOf(c), out, dvrScope, out.Revision, s.milliseconds())
	})
	return out, err
}
func groupFromRule(r dvr.Rule, o livechannels.Owner) RecordingGroup {
	before, after := r.Config.Options.BeforeMs, r.Config.Options.AfterMs
	keep := KeepPolicy{Mode: "keep-all"}
	if r.Config.Options.EpisodeLimit > 0 {
		keep = KeepPolicy{Mode: "keep-count", KeepCount: r.Config.Options.EpisodeLimit}
	} else if r.Config.Options.RetentionDays > 0 {
		keep = KeepPolicy{Mode: "keep-days", KeepDays: r.Config.Options.RetentionDays}
	}
	kind, match := "series", r.Config.SeriesID
	if r.Config.MatchKind == "title" {
		kind, match = "title", r.Config.Title
	}
	return RecordingGroup{Owner: o, Status: r.ReconcileState, ID: r.ID, Kind: kind, Name: r.Config.Name, Match: match, SourceID: r.Config.SourceID, Enabled: r.Config.Enabled, Revision: r.Revision, Options: GroupOptions{PrePaddingMs: &before, PostPaddingMs: &after, Keep: &keep, NewEpisodesOnly: r.Config.Episodes == "new", Priority: r.Config.Options.Priority}, UpdatedAt: r.UpdatedAt}
}
func (s *Service) runtimeRecordingGroups(ctx context.Context, auth Authorize, token string, limit int) (RecordingGroupPage, error) {
	out := RecordingGroupPage{Items: []RecordingGroup{}}
	size, err := pageSize(limit)
	if err != nil {
		return out, err
	}
	c, err := decodeCursor("recording-groups", token)
	if err != nil {
		return out, err
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, `SELECT id,authority,account_id,profile_id,revision,config_json,updated_ms,COALESCE((SELECT phase FROM dvr_rule_work w WHERE w.rule_id=dvr_rules.id),'idle') FROM dvr_rules WHERE deleted=0 AND (?=0 OR updated_ms<? OR (updated_ms=? AND id<?)) UNION ALL SELECT id,'','','',revision,json_object('name',name,'sourceId',source_id,'seriesId',match_text),created_ms,'legacy' FROM admin_dvr_groups WHERE (?=0 OR created_ms<? OR (created_ms=? AND id<?)) ORDER BY 7 DESC,1 DESC LIMIT ?`, c.Order, c.Order, c.Order, c.ID, c.Order, c.Order, c.Order, c.ID, size+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		orders := []int64{}
		for rows.Next() {
			var r dvr.Rule
			var o livechannels.Owner
			var raw, kind string
			var at int64
			if e = rows.Scan(&r.ID, &o.Authority, &o.AccountID, &o.ProfileID, &r.Revision, &raw, &at, &kind); e != nil {
				return e
			}
			if e = json.Unmarshal([]byte(raw), &r.Config); e != nil {
				return e
			}
			r.UpdatedAt = timeFromMilliseconds(at)
			r.ReconcileState = kind
			if !r.Config.Enabled {
				r.ReconcileState = "disabled"
			}
			if kind == "legacy" {
				r.Config.Enabled = false
				r.ReconcileState = "needs-owner-and-guide-selection"
			}
			g := groupFromRule(r, o)
			g.CreatedAt = g.UpdatedAt
			out.Items = append(out.Items, g)
			orders = append(orders, at)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out.Items) > size {
			out.Items = out.Items[:size]
			out.NextCursor = encodeCursor("recording-groups", orders[size-1], out.Items[size-1].ID)
		}
		return nil
	})
	return out, err
}

// Authority is the already rechecked server owner; Store still independently
// checks the selected recording owner's durable membership and recording grant.
func groupAuthority(auth Authorize) livechannels.Authority {
	return func(ctx context.Context, tx *sql.Tx, _ bool) (string, func(string, string) bool, error) {
		if err := auth(ctx, tx); err != nil {
			return "", nil, err
		}
		return "server-owner", func(string, string) bool { return true }, nil
	}
}
func mapDVRError(err error) error {
	switch {
	case errors.Is(err, dvr.ErrDenied), errors.Is(err, livechannels.ErrDenied):
		return ErrDenied
	case errors.Is(err, dvr.ErrConflict):
		return ErrConflict
	case errors.Is(err, dvr.ErrInvalid), errors.Is(err, dvr.ErrUnsupportedPredicate):
		return invalid("anchor", "match", "options")
	case errors.Is(err, dvr.ErrCaptureUnavailable):
		return ErrUnavailable
	}
	return err
}
func (s *Service) saveRuntimeRecordingGroup(ctx context.Context, auth Authorize, id string, c RecordingGroupChange) (RecordingGroup, error) {
	var out RecordingGroup
	if s.DVR == nil {
		return out, ErrUnavailable
	}
	if !validOperationID(c.OperationID) || !c.Owner.Valid() {
		return out, invalid("owner", "operationId")
	}
	if err := validateGroup(&c); err != nil {
		return out, err
	}
	if (c.Kind != "series" && c.Kind != "title") || c.Options.FolderTemplate != "" {
		return out, invalid("kind", "options.folderTemplate")
	}
	if c.Options.Keep != nil && c.Options.Keep.KeepUntilWatched {
		return out, invalid("options.keepPolicy")
	}
	err := s.transaction(ctx, auth, func(tx *sql.Tx) error {
		saved, replay, err := receipt[RecordingGroup](ctx, tx, "recording-group", c.OperationID, digestOf([]any{id, c}))
		if err != nil {
			return err
		}
		if replay {
			out = saved
			return nil
		}
		defaults, err := dvr.DefaultOptionsTx(ctx, tx)
		if err != nil {
			return err
		}
		target := id
		if target == "" {
			target = digestOf([]string{"admin-rule", c.OperationID, c.Owner.Key()})
		}
		config := dvr.RuleConfig{AllowedChannels: []string{}, BlockedChannels: []string{}, BlockedKeywords: []string{}, Keywords: []string{}, Options: defaults}
		var previous *RecordingGroup
		if c.ExpectedRevision > 0 {
			var raw string
			err = tx.QueryRowContext(ctx, `SELECT config_json FROM dvr_rules WHERE owner_key=? AND id=? AND revision=? AND deleted=0`, c.Owner.Key(), target, c.ExpectedRevision).Scan(&raw)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
			if json.Unmarshal([]byte(raw), &config) != nil {
				return ErrUnavailable
			}
			projection := groupFromRule(dvr.Rule{Config: config}, c.Owner)
			previous = &projection
		}
		changed := func(old, next any) bool { return digestOf(old) != digestOf(next) }
		if previous == nil || changed(previous.Options.PrePaddingMs, c.Options.PrePaddingMs) {
			config.Options.BeforeMs = defaults.BeforeMs
			if c.Options.PrePaddingMs != nil {
				config.Options.BeforeMs = *c.Options.PrePaddingMs
			}
		}
		if previous == nil || changed(previous.Options.PostPaddingMs, c.Options.PostPaddingMs) {
			config.Options.AfterMs = defaults.AfterMs
			if c.Options.PostPaddingMs != nil {
				config.Options.AfterMs = *c.Options.PostPaddingMs
			}
		}
		if previous == nil || changed(previous.Options.Keep, c.Options.Keep) {
			config.Options.RetentionDays, config.Options.EpisodeLimit = defaults.RetentionDays, defaults.EpisodeLimit
			if c.Options.Keep != nil {
				config.Options.RetentionDays, config.Options.EpisodeLimit = 0, 0
				if c.Options.Keep.Mode == "keep-days" {
					config.Options.RetentionDays = c.Options.Keep.KeepDays
				}
				if c.Options.Keep.Mode == "keep-count" {
					config.Options.EpisodeLimit = c.Options.Keep.KeepCount
				}
			}
		}
		config.Options.Priority = c.Options.Priority
		if !config.Options.Valid() {
			return invalid("options")
		}
		config.Name, config.SourceID, config.Enabled = c.Name, c.SourceID, c.Enabled
		config.Episodes = "all"
		if c.Options.NewEpisodesOnly {
			config.Episodes = "new"
		}
		if previous == nil || previous.Kind != c.Kind || previous.Match != c.Match || previous.SourceID != c.SourceID {
			config.MatchKind, config.Title, config.ChannelID, config.SeriesID = "", "", "", c.Match
			if c.Kind == "title" {
				config.MatchKind, config.Title, config.ChannelID, config.SeriesID = "title", c.Match, c.Anchor.ChannelID, ""
			}
		}
		in := dvr.RuleInput{ID: target, Mutation: dvr.Mutation{ExpectedRevision: c.ExpectedRevision, RequestID: digestOf([]string{"admin-rule-request", c.OperationID})[:48]}, Anchor: c.Anchor, Config: config}

		r, err := s.DVR.SaveRule(dbwork.WithWrite(ctx, tx, dbwork.ClassInteractive), groupAuthority(auth), c.Owner, in)
		if err != nil {
			return mapDVRError(err)
		}
		out = groupFromRule(r, c.Owner)
		return saveReceipt(ctx, tx, "recording-group", c.OperationID, digestOf([]any{id, c}), out, s.milliseconds())
	})
	return out, err
}
func (s *Service) deleteRuntimeRecordingGroup(ctx context.Context, auth Authorize, id string, revision int64, operation string) error {
	if !validOperationID(operation) || id == "" {
		return ErrInput
	}
	return s.transaction(ctx, auth, func(tx *sql.Tx) error {
		var o livechannels.Owner
		err := tx.QueryRowContext(ctx, `SELECT authority,account_id,profile_id FROM dvr_rules WHERE id=?`, id).Scan(&o.Authority, &o.AccountID, &o.ProfileID)
		if errors.Is(err, sql.ErrNoRows) {
			r, e := tx.ExecContext(ctx, `DELETE FROM admin_dvr_groups WHERE id=? AND revision=?`, id, revision)
			if e != nil {
				return e
			}
			n, _ := r.RowsAffected()
			if n != 1 {
				return ErrConflict
			}
			return nil
		}
		if err != nil {
			return err
		}
		if s.DVR == nil {
			return ErrUnavailable
		}
		return mapDVRError(s.DVR.DeleteRule(dbwork.WithWrite(ctx, tx, dbwork.ClassInteractive), groupAuthority(auth), o, id, dvr.DeleteRuleInput{Mutation: dvr.Mutation{ExpectedRevision: revision, RequestID: digestOf([]string{"admin-delete-rule", operation})[:48]}, Future: "keep"}))
	})
}
