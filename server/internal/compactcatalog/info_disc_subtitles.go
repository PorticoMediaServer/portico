package compactcatalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/subtitleoffer"
)

// The per-holder informational subtitle projection has two sequential phases.
// copy_family names the phase that owns the header's copy cursors:
//
//	0  idle: start the discovered-subtitle phase when its families changed,
//	   otherwise start the disc-title phase when its families changed.
//	1  projectInfoSubtitles' existing discovered-subtitle copy.
//	2  this file's bounded accepted-disc-title copy.
//
// The header is the only durable cursor. Each call emits at most one raw
// subtitle stream (and its one resource) for the holder at the head of the
// pending-link lane, so a holder is never released before its resource set is
// complete. Completing either phase resets every copy field in the same
// transaction that releases the holder, and the driver immediately starts the
// next phase instead of returning through idle, so a completed discovered pass
// is never restarted merely because the disc pass still has work to do.
const (
	infoCopyFamilyDiscoveredSubtitles = 1
	infoCopyFamilyDiscSubtitles       = 2

	infoDiscoveredSubtitleFamilyMask = 1<<5 | 1<<7 | 1<<8           // families 6, 8, 9
	infoDiscSubtitleFamilyMask       = 1<<9 | 1<<15 | 1<<16 | 1<<17 // families 10, 16, 17, 18
	infoSubtitleProjectionMask       = infoDiscoveredSubtitleFamilyMask | infoDiscSubtitleFamilyMask

	infoDiscSubtitlePhaseInit    = 0 // resolved; copy_aux is 0 and no stream is read yet
	infoDiscSubtitlePhaseEmit    = 1 // copy_aux is the last emitted stream_index
	infoDiscSubtitlePhaseChanged = 2 // as phase 1, and a new resource row was inserted
)

// projectInfoDiscSubtitles is the per-holder subtitle apply driver. It runs the
// unchanged discovered-subtitle projection first and then the accepted disc
// title projection, and reports done only when every phase this change's family
// mask requires has finished for the holder.
func projectInfoDiscSubtitles(ctx context.Context, tx *sql.Tx, c infoChange, item int64) (bool, error) {
	discovered := c.familyMask&infoDiscoveredSubtitleFamilyMask != 0
	disc := c.familyMask&infoDiscSubtitleFamilyMask != 0
	if !discovered && !disc {
		return true, nil
	}
	var family int64
	if err := tx.QueryRowContext(ctx, `SELECT copy_family FROM catalog_info_changes WHERE id=?`, c.id).Scan(&family); err != nil {
		return false, err
	}
	switch family {
	case 0:
		if discovered {
			done, err := projectInfoSubtitles(ctx, tx, c, item)
			if err != nil || !done {
				return done, err
			}
		}
		if disc {
			return projectInfoDiscSubtitleQuantum(ctx, tx, c, item)
		}
		return true, nil
	case infoCopyFamilyDiscoveredSubtitles:
		if !discovered {
			return false, ErrInfoInvariant
		}
		done, err := projectInfoSubtitles(ctx, tx, c, item)
		if err != nil || !done {
			return done, err
		}
		if disc {
			return projectInfoDiscSubtitleQuantum(ctx, tx, c, item)
		}
		return true, nil
	case infoCopyFamilyDiscSubtitles:
		if !disc {
			return false, ErrInfoInvariant
		}
		return projectInfoDiscSubtitleQuantum(ctx, tx, c, item)
	default:
		return false, ErrInfoInvariant
	}
}

// infoDiscSubtitleTarget is the exact accepted owner observation and title that
// one holder's disc resources are built from. Every field is re-read from a
// point row in the emitting quantum; no title or stream set is collected in Go.
type infoDiscSubtitleTarget struct {
	object, title, revision     string
	sourceRevision, fingerprint string
	sourceSize, sourceModified  int64
	sourceFacts                 int64
	originUS, timingKnown       int64
	policyRevision              int64
}

// projectInfoDiscSubtitleQuantum advances the accepted disc title projection by
// one raw stream. The durable cursors are copy_key=object, copy_key2=title,
// copy_key3=accepted title facts revision and copy_aux=last emitted
// stream_index (initialised to 0). copy_phase is 0 before any stream is read,
// 1 while emitting and 2 once a new resource row has been inserted (for the one
// collection revision bump the retired fanout performed per holder). Every disc
// value is nonnegative: phase-0 recovery exclusively owns copy_aux -1/-2, so the
// apply phase must never write a negative sentinel into the header.
func projectInfoDiscSubtitleQuantum(ctx context.Context, tx *sql.Tx, c infoChange, item int64) (bool, error) {
	var family, current, phase, cursor int64
	var object, title, revision string
	err := tx.QueryRowContext(ctx, `SELECT copy_family,copy_id,copy_phase,copy_aux,copy_key,copy_key2,copy_key3 FROM catalog_info_changes WHERE id=?`, c.id).
		Scan(&family, &current, &phase, &cursor, &object, &title, &revision)
	if err != nil {
		return false, err
	}
	if family == 0 {
		object, title, revision, ok, err := resolveInfoDiscSubtitleTarget(ctx, tx, c)
		if err != nil {
			return false, err
		}
		if !ok {
			// No accepted owner disc title is associated with this input. There is
			// nothing to emit; the holder can proceed.
			return true, nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_family=?,copy_id=?,copy_phase=?,copy_aux=0,copy_key=?,copy_key2=?,copy_key3=? WHERE id=?`,
			infoCopyFamilyDiscSubtitles, item, infoDiscSubtitlePhaseInit, object, title, revision, c.id)
		return false, err
	}
	if family != infoCopyFamilyDiscSubtitles || current != item ||
		(phase != infoDiscSubtitlePhaseInit && phase != infoDiscSubtitlePhaseEmit && phase != infoDiscSubtitlePhaseChanged) {
		return false, ErrInfoInvariant
	}
	return emitInfoDiscSubtitleStream(ctx, tx, c, item, object, title, revision, phase, cursor)
}

// resolveInfoDiscSubtitleTarget selects the title this accepted input speaks
// for and proves it against the asset's accepted owner object/revision pin.
//
// A family16 manifest names exactly one staged title (the alternates producer
// and a changed main timed title both stage one). Without it, only a changed
// family10 can move the accepted main association, and its accepted
// media_title_key is that title. An empty family16 manifest is the
// structure-wide timed-title clear and therefore has no title to project.
func resolveInfoDiscSubtitleTarget(ctx context.Context, tx *sql.Tx, c infoChange) (string, string, string, bool, error) {
	var ownerObject, ownerRevision sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT info_owner_object,info_owner_revision FROM catalog_assets WHERE id=? AND token=?`, c.asset, c.token).
		Scan(&ownerObject, &ownerRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	if !ownerObject.Valid || !ownerRevision.Valid {
		// The accepted asset surface has no owner observation; no disc title is
		// current for display.
		return "", "", "", false, nil
	}
	object := ownerObject.String

	var expected int64
	err = tx.QueryRowContext(ctx, `SELECT rows_expected FROM catalog_info_input_families WHERE change_id=? AND family=16 AND sealed=1`, c.id).Scan(&expected)
	switch {
	case err == nil:
		if expected == 0 {
			return "", "", "", false, nil
		}
		if expected != 1 {
			return "", "", "", false, ErrInfoInvariant
		}
		var stagedObject, title, revision string
		err = tx.QueryRowContext(ctx, `SELECT object_id,title_key,revision FROM catalog_info_new_disc_title_media WHERE change_id=? AND object_id=?`, c.id, object).
			Scan(&stagedObject, &title, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			// The accepted timed title belongs to another observation than the
			// asset's accepted owner file. Alternate-title resources for that
			// object stay untouched; this surface does not display them.
			return "", "", "", false, nil
		}
		if err != nil {
			return "", "", "", false, err
		}
		target, ok, err := loadInfoDiscSubtitleTarget(ctx, tx, c, object, title, revision)
		if err != nil || !ok {
			return "", "", "", false, err
		}
		return target.object, target.title, target.revision, true, nil
	case errors.Is(err, sql.ErrNoRows):
		if c.familyMask&(1<<9) == 0 {
			return "", "", "", false, nil
		}
		var title string
		err = tx.QueryRowContext(ctx, `SELECT media_title_key FROM inventory_disc_facts WHERE object_id=? AND asset_id=?`, object, c.token).Scan(&title)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", "", false, nil
		}
		if err != nil {
			return "", "", "", false, err
		}
		if title == "" {
			// The accepted main association was cleared. The retired producer
			// removed no resource for a clear; currentness already hides the old
			// title's rows and alternate-title resources are preserved.
			return "", "", "", false, nil
		}
		target, ok, err := loadInfoDiscSubtitleTarget(ctx, tx, c, object, title, "")
		if err != nil || !ok {
			return "", "", "", false, err
		}
		return target.object, target.title, target.revision, true, nil
	default:
		return "", "", "", false, err
	}
}

// loadInfoDiscSubtitleTarget re-proves the exact accepted owner object, revision
// and title facts in one point row. The owner pin is the asset's accepted
// observation, never raw source health or availability. The optional revision
// binds a staged family16 row to the canonical title row it installed.
func loadInfoDiscSubtitleTarget(ctx context.Context, tx *sql.Tx, c infoChange, object, title, revision string) (infoDiscSubtitleTarget, bool, error) {
	var t infoDiscSubtitleTarget
	t.object, t.title, t.revision = object, title, revision
	frame, e := ReadInfoAsset(ctx, tx, c.asset, c.token, false)
	if e != nil {
		return t, false, e
	}
	var size, modified, facts int64
	err := tx.QueryRowContext(ctx, `SELECT ?,?,COALESCE(st.revision,0),
 d.source_revision,d.fingerprint,tm.revision,tm.origin_us,tm.timing_known,
 COALESCE(
  (SELECT w.policy_revision FROM catalog_info_input_families f
   JOIN catalog_info_input_witnesses w ON w.change_id=f.change_id AND w.ordinal=f.witness_ordinal
   WHERE f.change_id=? AND f.family=16 AND f.sealed=1),
  (SELECT w.policy_revision FROM catalog_info_input_families f
   JOIN catalog_info_input_witnesses w ON w.change_id=f.change_id AND w.ordinal=f.witness_ordinal
   WHERE f.change_id=? AND f.family=10 AND f.sealed=1),
  0)
 FROM catalog_assets a
 JOIN inventory_disc_facts d ON d.object_id=a.info_owner_object AND d.asset_id=a.token
 JOIN inventory_disc_title_media tm ON tm.object_id=d.object_id AND tm.title_key=?
 LEFT JOIN asset_stream_facts st ON st.asset_id=a.token AND st.size=? AND st.modified_ns=?
 WHERE a.id=? AND a.token=? AND a.info_owner_object=? AND a.info_owner_revision=d.source_revision AND d.encryption='none'
 AND tm.source_revision=d.source_revision AND tm.fingerprint=d.fingerprint AND tm.timing_known=1
 AND (?='' OR tm.revision=?)`,
		frame.Size, frame.ModifiedNS, c.id, c.id, title, frame.Size, frame.ModifiedNS, c.asset, c.token, object, revision, revision).
		Scan(&size, &modified, &facts, &t.sourceRevision, &t.fingerprint, &t.revision,
			&t.originUS, &t.timingKnown, &t.policyRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	if err != nil {
		return t, false, err
	}
	t.sourceSize, t.sourceModified, t.sourceFacts = size, modified, facts
	return t, true, nil
}

// emitInfoDiscSubtitleStream emits at most one subtitle stream's resource. The
// resource identity and every revision/render/disc field reproduce
// subtitles.PersistDiscResourcesTx exactly; that helper is retired because its
// all-holder materialization was unbounded.
func emitInfoDiscSubtitleStream(ctx context.Context, tx *sql.Tx, c infoChange, item int64, object, title, revision string, phase, cursor int64) (bool, error) {
	target, ok, err := loadInfoDiscSubtitleTarget(ctx, tx, c, object, title, revision)
	if err != nil {
		return false, err
	}
	if !ok {
		// The accepted title facts moved under this single-writer apply. Fail
		// closed instead of emitting a resource against an unaccepted revision.
		return false, ErrInfoInvariant
	}
	var index, isDefault, isForced int64
	var codec, language, titleText, detail string
	query := `SELECT stream_index,codec,language,title,is_default,is_forced,detail_json
 FROM inventory_disc_title_media_streams
 WHERE object_id=? AND title_key=? AND type='subtitle' AND stream_index>=0`
	args := []any{object, title}
	if phase != infoDiscSubtitlePhaseInit {
		query += ` AND stream_index>?`
		args = append(args, cursor)
	}
	query += ` ORDER BY stream_index LIMIT 1`
	err = tx.QueryRowContext(ctx, query, args...).
		Scan(&index, &codec, &language, &titleText, &isDefault, &isForced, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		if phase == infoDiscSubtitlePhaseChanged {
			// One bump per holder, as the retired fanout did after it saw any
			// newly inserted resource for that item.
			if _, err = tx.ExecContext(ctx, `INSERT INTO subtitle_collections(item_id,revision) VALUES(?,2) ON CONFLICT(item_id) DO UPDATE SET revision=revision+1`, item); err != nil {
				return false, err
			}
		}
		return true, clearInfoDiscSubtitleCursor(ctx, tx, c.id)
	}
	if err != nil {
		return false, err
	}

	nextPhase := phase
	if nextPhase == infoDiscSubtitlePhaseInit {
		nextPhase = infoDiscSubtitlePhaseEmit
	}
	format := infoDiscSubtitleFormat(codec)
	if format != "" && subtitleoffer.ValidFormat(format) {
		hearing := infoDiscSubtitleHearingImpaired(detail)
		presented := subtitleoffer.Present(format, language, titleText, hearing)
		resource := infoDiscSubtitleResourceID(item, c.token, object, title, target.revision, target.policyRevision, index)
		result, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision,discovery_id,discovery_revision,discovered) VALUES(?,?,?,'shared','',1,'',1,0)`, resource, item, c.token)
		if e != nil {
			return false, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return false, e
		}
		if n > 0 {
			nextPhase = infoDiscSubtitlePhaseChanged
		}
		if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,rights,original_digest,source_size,source_modified_ns,source_facts_revision,created_at) VALUES(?,1,'',0,?,?,?,'embedded','','',?,?,?,strftime('%Y-%m-%dT%H:%M:%SZ','now'))`,
			resource, format, presented.Language, presented.Title, target.sourceSize, target.sourceModified, target.sourceFacts); e != nil {
			return false, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_render_revisions(resource_id,revision,renderer,is_default,is_forced,source_evidence,hearing_impaired) VALUES(?,1,?,?,?,'',?)`,
			resource, presented.Renderer, isDefault, isForced, hearing); e != nil {
			return false, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_disc_resources(resource_id,object_id,title_key,title_facts_revision,stream_index,source_revision,fingerprint,policy_revision,origin_us,timing_known) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			resource, object, title, target.revision, index, target.sourceRevision, target.fingerprint, target.policyRevision, target.originUS, target.timingKnown); e != nil {
			return false, e
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_phase=?,copy_aux=? WHERE id=?`, nextPhase, index, c.id)
	return false, err
}

func clearInfoDiscSubtitleCursor(ctx context.Context, tx *sql.Tx, changeID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_family=0,copy_id=0,copy_phase=0,copy_aux=0,copy_key='',copy_key2='',copy_key3='' WHERE id=? AND phase=4`, changeID)
	return err
}

// infoDiscSubtitleResourceID is the retired producer's stable identity:
// disc-subtitle-v1:<item>:<asset>:<object>:<title>:<factsRevision>:<policy>:<stream>.
func infoDiscSubtitleResourceID(item int64, token, object, title, revision string, policy, stream int64) string {
	sum := sha256.Sum256([]byte("disc-subtitle-v1:" + strconv.FormatInt(item, 10) + ":" + token + ":" + object + ":" + title + ":" + revision + ":" + strconv.FormatInt(policy, 10) + ":" + strconv.FormatInt(stream, 10)))
	return hex.EncodeToString(sum[:])
}

// infoDiscSubtitleFormat is the fixed codec-to-format table of
// subtitles.CodecFormat. The catalogue package owns this producer's input and
// cannot import the subtitles package without a cycle; a codec absent here is
// the retired producer's unsupported_format row and emits no resource.
func infoDiscSubtitleFormat(codec string) string {
	switch codec {
	case "subrip", "text", "mov_text":
		return "srt"
	case "webvtt":
		return "vtt"
	case "ass":
		return "ass"
	case "ssa":
		return "ssa"
	case "hdmv_pgs_subtitle":
		return "pgs"
	case "dvd_subtitle":
		return "vobsub"
	case "dvb_subtitle":
		return "dvb"
	}
	return ""
}

// infoDiscSubtitleHearingImpaired reads the stored disposition bit. It is part
// of the same bounded detail_json the observation already published; malformed
// or oversized text is treated as not observed, exactly as assets.DecodeDetail.
func infoDiscSubtitleHearingImpaired(raw string) bool {
	if raw == "" || len(raw) > 4096 {
		return false
	}
	var detail struct {
		HearingImpaired bool `json:"hearingImpaired"`
	}
	if json.Unmarshal([]byte(raw), &detail) != nil {
		return false
	}
	return detail.HearingImpaired
}
