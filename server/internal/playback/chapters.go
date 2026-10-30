package playback

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"sort"
	"time"
)

var ErrStaleChapter = errors.New("The playing source or chapter facts changed. Refresh playback before seeking.")
var ErrChapterCursor = errors.New("Invalid chapter continuation.")

type chapterCursor struct {
	Revision string `json:"revision"`
	Index    int    `json:"index"`
	Limit    int    `json:"limit"`
}

// chapterThumbnails reports which chapter indexes have a current preview image.
// Only the head result of the asset's live inventory revision counts: a
// replaced file must drop its thumbnails before the sweep retires them, and a
// retired or superseded result never publishes a URL.
// ChapterThumbnails is which chapters of a library file have a current
// thumbnail, by chapter_index (the downloads bundle names the same images).
func ChapterThumbnails(ctx context.Context, tx *sql.Tx, asset string) (map[int]bool, error) {
	out := map[int]bool{}
	rows, e := tx.QueryContext(ctx, `SELECT a.ordinal FROM analysis_artifacts a
 JOIN analysis_results r ON r.id=a.result_id
 JOIN analysis_heads h ON h.result_id=r.id AND h.object_id=r.object_id AND h.source_revision=r.source_revision AND h.stage='chapter_images'
 JOIN inventory_objects o ON o.id=r.object_id AND o.revision=r.source_revision AND o.retired=0 AND o.state='available' AND o.root_incarnation=r.root_incarnation
 JOIN library_sources src ON src.id=o.source_id AND src.enabled=1 AND src.incarnation=o.root_incarnation AND src.generation=r.configuration_generation
 WHERE r.asset_id=? AND r.stage='chapter_images' AND r.retired_ms=0 AND a.kind='chapter_images' AND a.mime='image/jpeg'
 ORDER BY a.ordinal`, asset)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var ordinal int
		if e = rows.Scan(&ordinal); e != nil {
			return out, e
		}
		// Produced ordinals are zero based over chapter_index order from one.
		out[ordinal+1] = true
	}
	return out, rows.Err()
}
func (s *Service) Chapters(ctx context.Context, p identity.Principal, scope OffersScope, session, expected, cursor string, limit int) (ChapterProjection, error) {
	out := ChapterProjection{Scope: ChapterScope{OffersScope: scope, SessionID: session}, Status: "unavailable", Chapters: []PlayingChapter{}}
	if limit < 1 || limit > 100 || len(cursor) > 4096 {
		return out, ErrChapterCursor
	}
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var state, expires string
	var size, modified int64
	var pinSize, pinModified sql.NullInt64
	e = tx.QueryRowContext(ctx, `SELECT ps.generation,ps.asset_id,ps.state,ps.expires_at,ps.duration,a.size,a.modified_ns,pin.size,pin.modified_ns FROM playback_sessions ps JOIN catalog_assets a ON a.token=ps.asset_id LEFT JOIN playback_source_pins pin ON pin.session_id=ps.id AND pin.asset_id=ps.asset_id WHERE ps.id=? AND ps.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND (ps.session_hash=? OR EXISTS(SELECT 1 FROM authorization_family_tokens original JOIN authorization_family_tokens caller ON caller.family_id=original.family_id JOIN authorization_session_families family ON family.id=caller.family_id WHERE original.token_hash=ps.session_hash AND caller.token_hash=? AND caller.retired=0 AND family.revoked=0 AND caller.generation=family.current_generation)) AND ps.account_id=? AND ps.profile_id=? AND EXISTS(SELECT 1 FROM catalog_asset_links l JOIN catalog_assets ca ON ca.id=l.asset_id WHERE l.entity_id=ps.item_id AND ca.token=ps.asset_id)`, session, scope.ItemID, p.Hash, p.Hash, p.AccountID, p.ProfileID).Scan(&out.Scope.Generation, &out.Scope.SourceID, &state, &expires, &out.Duration, &size, &modified, &pinSize, &pinModified)
	if e != nil {
		return out, e
	}
	// A full-media derivative is not the original chapter clock. Even equal
	// durations do not establish AAC priming or source/presentation alignment.
	// Do not read original chapter facts or emit their seek actions for this pin.
	var preparedVersion string
	e = tx.QueryRowContext(ctx, `SELECT version_id FROM prepared_media_session_pins WHERE session_id=?`, session).Scan(&preparedVersion)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if e == nil {
		reason := "prepared_chapter_mapping_unavailable"
		if state == "stopped" || state == "ended" || state == "failed" || expires <= time.Now().UTC().Format(time.RFC3339) {
			reason = "session_inactive"
		}
		raw, _ := json.Marshal([]any{"prepared-chapters-v1", out.Scope, preparedVersion, reason})
		sum := sha256.Sum256(raw)
		out.Revision = hex.EncodeToString(sum[:])
		out.Reason = &reason
		if cursor != "" || expected != "" && expected != out.Revision {
			return out, ErrStaleChapter
		}
		return out, gated.Commit()
	}
	reason := ""
	if !pinSize.Valid {
		reason = "session_source_unverified"
	} else if pinSize.Int64 != size || pinModified.Int64 != modified {
		return out, ErrStaleChapter
	}
	if state == "stopped" || state == "ended" || state == "failed" || expires <= time.Now().UTC().Format(time.RFC3339) {
		reason = "session_inactive"
	}
	var revision int64
	var factsSize, factsModified int64
	var status string
	e = tx.QueryRowContext(ctx, `SELECT revision,size,modified_ns,status FROM asset_chapter_facts WHERE asset_id=?`, out.Scope.SourceID).Scan(&revision, &factsSize, &factsModified, &status)
	if errors.Is(e, sql.ErrNoRows) {
		if reason == "" {
			reason = "chapter_facts_unavailable"
		}
	} else if e != nil {
		return out, e
	} else if factsSize != size || factsModified != modified {
		return out, ErrStaleChapter
	} else if status == "invalid" && reason == "" {
		reason = "invalid_chapter_timeline"
	}
	// Chapter images are part of the projected facts. A set that gains or loses
	// previews must invalidate a held revision, not silently change its rows.
	thumbnails, e := ChapterThumbnails(ctx, tx, out.Scope.SourceID)
	if e != nil {
		return out, e
	}
	indexes := make([]int, 0, len(thumbnails))
	for index := range thumbnails {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	raw, _ := json.Marshal([]any{out.Scope, size, modified, revision, status, reason, indexes})
	sum := sha256.Sum256(raw)
	out.Revision = hex.EncodeToString(sum[:])
	if expected != "" && expected != out.Revision {
		return out, ErrStaleChapter
	}
	if reason != "" {
		out.Reason = &reason
		if cursor != "" {
			return out, ErrStaleChapter
		}
		return out, gated.Commit()
	}
	var key string
	if e = tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='chapter_cursor_key'`).Scan(&key); e != nil {
		return out, e
	}
	after := 0
	if cursor != "" {
		data, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || len(data) < 33 {
			return out, ErrChapterCursor
		}
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write(data[32:])
		if !hmac.Equal(data[:32], mac.Sum(nil)) {
			return out, ErrChapterCursor
		}
		var c chapterCursor
		if json.Unmarshal(data[32:], &c) != nil || c.Limit != limit || c.Index < 1 {
			return out, ErrChapterCursor
		}
		if c.Revision != out.Revision {
			return out, ErrStaleChapter
		}
		after = c.Index
	}
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM asset_chapters WHERE asset_id=?`, out.Scope.SourceID).Scan(&out.TotalCount); e != nil {
		return out, e
	}
	if out.TotalCount == 0 {
		out.Status = "none"
		if cursor != "" {
			return out, ErrStaleChapter
		}
		return out, gated.Commit()
	}
	rows, e := tx.QueryContext(ctx, `SELECT chapter_index,title,start_seconds,end_seconds FROM asset_chapters WHERE asset_id=? AND chapter_index>? ORDER BY chapter_index LIMIT ?`, out.Scope.SourceID, after, limit+1)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var c PlayingChapter
		if e = rows.Scan(&c.Index, &c.Title, &c.StartSeconds, &c.EndSeconds); e != nil {
			rows.Close()
			return out, e
		}
		c.ID = fmt.Sprintf("%s:%d", out.Scope.SourceID, c.Index)
		if c.Title == "" {
			c.Title = fmt.Sprintf("Chapter %d", c.Index)
		}
		c.Action = ChapterSeek{"seek", c.StartSeconds}
		if thumbnails[c.Index] {
			c.ThumbnailURL = "/v1/items/" + out.Scope.ItemID + "/chapters/" + c.ID + "/image"
		}
		out.Chapters = append(out.Chapters, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(out.Chapters) == 0 {
		return out, ErrStaleChapter
	}
	if len(out.Chapters) > limit {
		out.Chapters = out.Chapters[:limit]
		raw, _ := json.Marshal(chapterCursor{out.Revision, out.Chapters[len(out.Chapters)-1].Index, limit})
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write(raw)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(append(mac.Sum(nil), raw...))
	}
	out.Status = "available"
	return out, gated.Commit()
}
