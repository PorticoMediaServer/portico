package dvr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// prepareRecordingLayout composes the real settings values supplied by their
// normalized reader. IO stays outside transactions; every reservation and
// receipt transition rechecks the existing occurrence claim and authority.
func (s *Store) prepareRecordingLayout(ctx context.Context, c *claim, result CaptureResult) (CaptureResult, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if err != nil {
		return result, err
	}
	tx := gated.Tx()
	if err = s.currentClaimTx(ctx, tx, c); err != nil {
		gated.Rollback()
		return result, err
	}
	layout, err := ReadRecordingLayoutTx(ctx, tx)
	if err != nil {
		gated.Rollback()
		return result, err
	}
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM dvr_recordings WHERE id=?`, c.request.Recording.ID).Scan(&state); err != nil {
		gated.Rollback()
		return result, err
	}
	// Withdrawn leaders may finish immutable source custody for followers, but
	// never publish an external filename for the withdrawn owner.
	if state == "cancelled" || state == "pending-delete" {
		gated.Rollback()
		return result, nil
	}
	receipt, exists, err := readRecordingLayoutReceiptTx(ctx, tx, c.request.Recording.ID)
	if err != nil {
		gated.Rollback()
		return result, err
	}
	var relative string
	if exists {
		if receipt.OwnerKey != c.request.Owner.Key() || receipt.ArtifactKey != c.request.ArtifactKey || receipt.Object != result.Object || receipt.Phase == "retired" {
			gated.Rollback()
			return result, ErrConflict
		}
	} else {
		template := layout.MovieTemplate
		programme := c.request.Recording.Programme
		if programme.Episode != nil || programme.SeriesID != "" {
			template = layout.ShowTemplate
		}
		if template == "" {
			gated.Rollback()
			return result, nil
		}
		var source, group string
		err = tx.QueryRowContext(ctx, `SELECT name FROM live_sources WHERE id=?`, c.request.Recording.Occurrence.SourceID).Scan(&source)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			gated.Rollback()
			return result, err
		}
		r := c.request.Recording
		if err = channelTx(ctx, tx, &r); err != nil {
			gated.Rollback()
			return result, err
		}
		if r.RuleID != "" {
			var raw string
			err = tx.QueryRowContext(ctx, `SELECT config_json FROM dvr_rules WHERE id=? AND owner_key=?`, r.RuleID, c.request.Owner.Key()).Scan(&raw)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				gated.Rollback()
				return result, err
			}
			if err == nil {
				var rule RuleConfig
				if err = json.Unmarshal([]byte(raw), &rule); err != nil {
					gated.Rollback()
					return result, err
				}
				group = rule.Name
			}
		}
		// The only actual capture container in this engine is MPEG-TS.
		if result.Container != "mpegts" {
			gated.Rollback()
			return result, ErrInvalid
		}
		var fallback bool
		relative, fallback, err = renderRecordingTemplate(template, recordingTemplateValues(r, source, group), ".ts")
		if err != nil || fallback {
			gated.Rollback()
			return result, err
		}
	}
	gated.Rollback()
	driver, ok := s.driver.(RecordingLayoutDriver)
	if !ok {
		return result, ErrUnavailable
	}
	if !exists {
		receipt, err = driver.PrepareRecordingLayout(ctx, c.request, result, relative, layout.Revision)
		if err != nil {
			return result, err
		}
	}
	recovering := exists
	base := relative
	if exists {
		base = receipt.BaseTarget
	}
	startCandidate := receipt.Collision
	if startCandidate < 1 {
		startCandidate = 1
	}
	for candidate := startCandidate; ; candidate++ {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if !exists {
			receipt.Collision = candidate
			receipt.Target, err = layoutCollisionCandidate(base, candidate)
			if err != nil {
				return result, err
			}
			g, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
			if e != nil {
				return result, e
			}
			if e = s.currentClaimTx(ctx, g.Tx(), c); e != nil {
				g.Rollback()
				return result, e
			}
			reserved, e := reserveRecordingLayoutTx(ctx, g.Tx(), receipt)
			if e != nil {
				g.Rollback()
				return result, e
			}
			if !reserved {
				g.Rollback()
				continue
			}
			if e = g.Commit(); e != nil {
				return result, e
			}
			exists = true
		}
		linked, e := driver.LinkRecordingLayout(ctx, receipt, recovering)
		if errors.Is(e, ErrRecordingLayoutCollision) && receipt.Phase == "prepared" && receipt.TargetIdentity == "" {
			next, e := layoutCollisionCandidate(base, candidate+1)
			if e != nil {
				return result, e
			}
			g, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
			if e != nil {
				return result, e
			}
			if e = s.currentClaimTx(ctx, g.Tx(), c); e != nil {
				g.Rollback()
				return result, e
			}
			// A collision proved this name was foreign before this attempt created a
			// target. The next prepared reservation cannot mutate a linked receipt.
			update, e := g.Tx().ExecContext(ctx, `UPDATE OR IGNORE dvr_recording_layouts SET target_relative_path=?,collision_ordinal=? WHERE recording_id=? AND owner_key=? AND artifact_id=? AND target_relative_path=? AND phase='prepared' AND target_identity=''`, next, candidate+1, receipt.RecordingID, receipt.OwnerKey, receipt.ArtifactKey, receipt.Target)
			if e != nil {
				g.Rollback()
				return result, e
			}
			n, _ := update.RowsAffected()
			if n != 1 {
				g.Rollback()
				continue
			}
			if e = g.Commit(); e != nil {
				return result, e
			}
			receipt.Target = next
			receipt.Collision = candidate + 1
			recovering = false
			continue
		}
		if e != nil {
			return result, e
		}
		if !sameRecordingLayoutReceipt(receipt, linked) || linked.Phase != "linked" || !linked.Valid() {
			return result, ErrInvalid
		}
		g, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
		if e != nil {
			return result, e
		}
		if e = s.currentClaimTx(ctx, g.Tx(), c); e != nil {
			g.Rollback()
			return result, e
		}
		saved, found, e := readRecordingLayoutReceiptTx(ctx, g.Tx(), receipt.RecordingID)
		if e != nil || !found || !sameRecordingLayoutReceipt(saved, receipt) {
			g.Rollback()
			if e != nil {
				return result, e
			}
			return result, ErrConflict
		}
		update, e := g.Tx().ExecContext(ctx, `UPDATE dvr_recording_layouts SET target_identity=?,phase='linked' WHERE recording_id=? AND phase IN('prepared','linked')`, linked.TargetIdentity, linked.RecordingID)
		if e != nil {
			g.Rollback()
			return result, e
		}
		n, _ := update.RowsAffected()
		if n != 1 {
			g.Rollback()
			return result, ErrConflict
		}
		if e = g.Commit(); e != nil {
			return result, e
		}
		result.LayoutReceipt = &linked
		return result, nil
	}
}
func verifyRecordingLayoutPublicationTx(ctx context.Context, tx *sql.Tx, c *claim, result CaptureResult) error {
	saved, exists, err := readRecordingLayoutReceiptTx(ctx, tx, c.request.Recording.ID)
	if err != nil {
		return err
	}
	if !exists {
		if result.LayoutReceipt != nil {
			return ErrInvalid
		}
		return nil
	}
	if result.LayoutReceipt == nil || saved != *result.LayoutReceipt || saved.Phase != "linked" || saved.OwnerKey != c.request.Owner.Key() || saved.ArtifactKey != c.request.ArtifactKey || saved.Object != result.Object || filepath.Join(saved.RootPath, saved.Source) != result.Path {
		return ErrInvalid
	}
	return nil
}
