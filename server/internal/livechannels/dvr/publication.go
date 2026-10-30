package dvr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strconv"
	"strings"
	"time"
)

// publish is the sole catalog publication point. Media work has already closed,
// probed and sealed bytes. Every owner/cancellation/worker fence is checked in
// the same transaction that makes the normal catalog item visible.
func (s *Store) publish(ctx context.Context, c *claim, result CaptureResult) error {
	if result.Object.Size <= 0 || !canonicalID.MatchString(result.Object.Digest) || result.Duration <= 0 || result.Root == "" || result.Path == "" || !filepath.IsAbs(result.Root) || filepath.Clean(result.Root) != result.Root || filepath.Clean(result.Path) != result.Path || result.Path != filepath.Join(result.Root, "captures", c.request.Recording.ID, "objects", result.Object.Digest) || filepath.Base(result.Root) != c.request.Owner.Key() {
		return ErrInvalid
	}
	if strings.ContainsAny(result.VideoCodec+result.AudioCodec+result.Container, "\x00\r\n") || len(result.VideoCodec) > 64 || len(result.AudioCodec) > 64 {
		return ErrInvalid
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if e = s.currentClaimTx(ctx, tx, c); e != nil {
		return e
	}
	var ownerState string
	if e = tx.QueryRowContext(ctx, `SELECT state FROM dvr_recordings WHERE id=?`, c.request.Recording.ID).Scan(&ownerState); e != nil {
		return e
	}
	if ownerState == "cancelled" || ownerState == "pending-delete" {
		// The bytes now have immutable custody. Keep the withdrawn owner's
		// state and expose no catalogue item; the followers link these bytes.
		cp := result.CaptureCheckpoint
		cp.SharedReady = true
		cp.SourceFence = c.checkpoint.SourceFence
		if c.request.Input.Locator != "" {
			cp.SourceFence = c.request.Input.Fence()
		}
		body, err := json.Marshal(cp)
		if err != nil {
			return err
		}
		_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET artifact_digest=?,bytes=?,checkpoint_json=?,lease_until_ms=0,finished_ms=?,updated_ms=? WHERE id=? AND claim_token=?`, result.Object.Digest, result.Object.Size, string(body), s.now().UnixMilli(), s.now().UnixMilli(), c.request.Recording.ID, c.token)
		if e != nil {
			return e
		}
		return gated.Commit()
	}
	if e = verifyRecordingLayoutPublicationTx(ctx, tx, c, result); e != nil {
		return e
	}
	// A successful producer does not bypass a source change between the last
	// renewal and publication. Preserve validated bytes as incomplete, including
	// sealed-object recovery whose original source revision was checkpointed.
	if result.Complete {
		expected := c.request.Input.Fence()
		if c.recovery || c.sharedFrom != "" {
			expected = c.checkpoint.SourceFence
		}
		var active, state string
		e = tx.QueryRowContext(ctx, `SELECT active_generation,state FROM live_sources WHERE id=?`, c.request.Recording.Occurrence.SourceID).Scan(&active, &state)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		input, lookup := s.live.InputTx(ctx, tx, c.request.Recording.Occurrence.SourceID, c.request.Recording.Occurrence.ChannelID, active)
		if expected == "" || e != nil || state != "active" || lookup != nil || input.Fence() != expected {
			result.Complete = false
			result.Reason = "source-changed"
		}
	}
	library := digest([]string{"private-recorded-library-v1", c.request.Owner.Key()})
	var existingLibrary, root string
	e = tx.QueryRowContext(ctx, `SELECT d.library_id,l.root FROM dvr_private_libraries d JOIN libraries l ON l.id=d.library_id WHERE d.authority=? AND d.account_id=? AND d.profile_id=?`, c.request.Owner.Authority, c.request.Owner.AccountID, c.request.Owner.ProfileID).Scan(&existingLibrary, &root)
	if errors.Is(e, sql.ErrNoRows) {
		// One private library holds both films and guide-identified episodes.
		// Its container kind is presentation, not evidence about each recording.
		if _, e = tx.ExecContext(ctx, `INSERT INTO libraries(id,name,kind,root) VALUES(?,'Recorded TV','movie',?)`, library, result.Root); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO dvr_private_libraries VALUES(?,?,?,?)`, library, c.request.Owner.Authority, c.request.Owner.AccountID, c.request.Owner.ProfileID); e != nil {
			return e
		}
	} else if e != nil {
		return e
	} else if existingLibrary != library || root != result.Root {
		return ErrConflict
	}
	handle, e := compactcatalog.LibraryTx(ctx, tx, library)
	if e != nil {
		return e
	}
	overview := c.request.Recording.Programme.Description
	if !result.Complete {
		overview += "\n\nIncomplete recording. Captured " + result.First.UTC().Format(time.RFC3339) + " to " + result.Last.UTC().Format(time.RFC3339) + "; the full programme was not captured and validated."
	}
	state := "incomplete-playable"
	if result.Complete {
		state = "completed"
	}
	programme := c.request.Recording.Programme
	kind, parent, title := compactcatalog.Movie, int64(0), programme.Title
	if programme.Episode != nil && programme.Episode.Season > 0 && programme.Episode.Number > 0 {
		identity := programme.SeriesID
		if identity == "" {
			identity = digest([]string{c.request.Recording.Occurrence.SourceID, programme.Title})
		}
		showKey := "recorded:" + identity
		show, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Show, Key: compactcatalog.ShowKey(showKey), Title: programme.Title, Year: programme.Year})
		if err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, show, map[string]any{"local_key": showKey, "provider_match_status": "unmatched"}); err != nil {
			return err
		}
		season, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Season, Parent: show, Key: compactcatalog.SeasonKey(showKey, programme.Episode.Season), Title: fmt.Sprintf("Season %d", programme.Episode.Season)})
		if err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, season, map[string]any{"show_id": show, "number": programme.Episode.Season}); err != nil {
			return err
		}
		kind, parent = compactcatalog.Episode, season
		title = programme.Subtitle
		if title == "" {
			title = fmt.Sprintf("%s · S%d E%d", programme.Title, programme.Episode.Season, programme.Episode.Number)
		}
	} else if programme.Subtitle != "" {
		title += " · " + programme.Subtitle
	}
	entity, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: kind, Parent: parent, Key: compactcatalog.RecordingKey(c.request.Recording.ID), Title: title, Year: programme.Year, Added: s.now().UTC().Format(time.RFC3339Nano)})
	if e == nil && kind == compactcatalog.Episode {
		var show int64
		if e = tx.QueryRowContext(ctx, `SELECT show_id FROM catalog_seasons WHERE entity_id=?`, parent).Scan(&show); e != nil {
			return e
		}
		date, _ := strconv.Atoi(strings.ReplaceAll(strings.SplitN(programme.Start, "T", 2)[0], "-", ""))
		e = compactcatalog.SetFactsTx(ctx, tx, entity, map[string]any{"show_id": show, "season_id": parent, "numbering": "recorded:" + c.request.Recording.ID, "number": programme.Episode.Number, "local_identity_status": "parsed", "ordering_basis": "guide", "air_date": date})
	}
	if e != nil {
		return e
	}
	var priorEntity int64
	var priorDigest string
	e = tx.QueryRowContext(ctx, `SELECT item_id,artifact_digest FROM dvr_catalog_provenance WHERE recording_id=?`, c.request.Recording.ID).Scan(&priorEntity, &priorDigest)
	if e == nil {
		if priorEntity != entity || priorDigest != result.Object.Digest {
			return ErrConflict
		}
	} else if errors.Is(e, sql.ErrNoRows) {
		asset, token, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{
			Path: result.Path, Size: result.Object.Size, ModifiedNS: result.ModifiedNS,
			Container: result.Container, VideoCodec: result.VideoCodec, AudioCodec: result.AudioCodec,
			Width: result.Width, Height: result.Height, Duration: result.Duration,
		})
		if e != nil {
			return e
		}
		if e = compactcatalog.SetFactsTx(ctx, tx, entity, map[string]any{"overview": overview}); e != nil {
			return e
		}
		end := result.Duration
		if e = compactcatalog.LinkAssetTx(ctx, tx, entity, asset, compactcatalog.Link{Part: 0, Start: 0, End: &end}); e != nil {
			return e
		}
		// The guide's rating becomes the recorded item's content rating, so a
		// published recording is filtered by profile restrictions exactly as a
		// library title is (Channels spec §8.1).
		if rating := c.request.Recording.Programme.Rating; rating != nil && strings.TrimSpace(rating.Value) != "" {
			value := strings.TrimSpace(rating.Value)
			if e = compactcatalog.SetAttributesTx(ctx, tx, entity, "contentRating", []string{value}); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO dvr_catalog_provenance VALUES(?,?,?,?,?,?,?,?,?,?,?)`, entity, c.request.Recording.ID, token, c.request.Recording.Occurrence.SourceID, c.request.Recording.Occurrence.ChannelID, c.request.Recording.Occurrence.ProgrammeID, c.request.Recording.Occurrence.Generation, result.Object.Digest, result.First.UnixMilli(), result.Last.UnixMilli(), state)
		if e != nil {
			return e
		}
	} else {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE dvr_recordings SET state=?,reason=?,item_id=?,artifact_digest=?,bytes=?,captured_start_ms=?,captured_end_ms=?,lease_until_ms=0,revision=revision+1,finished_ms=?,updated_ms=? WHERE id=? AND claim_token=?`, state, result.Reason, entity, result.Object.Digest, result.Object.Size, result.First.UnixMilli(), result.Last.UnixMilli(), s.now().UnixMilli(), s.now().UnixMilli(), c.request.Recording.ID, c.token)
	if e != nil {
		return e
	}
	if e = unreserveTx(ctx, tx, c.request.Recording.ID); e != nil {
		return e
	}
	if e = touchTx(ctx, tx, c.request.Owner); e != nil {
		return e
	}
	return gated.Commit()
}
