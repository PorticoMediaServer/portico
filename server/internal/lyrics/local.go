package lyrics

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
	"time"
)

// discoverLocal reads owner-supplied files outside the writer transaction, then
// repeats viewer authorization and source validation before publishing them.
func (s *Service) discoverLocal(ctx context.Context, a Access, t Target) error {
	if s.Local == nil {
		return nil
	}
	gate, entity, e := s.begin(ctx, a)
	if e != nil {
		return e
	}
	tx := gate.Tx()
	_, src, _, e := resolve(ctx, tx, a, entity, t)
	if e != nil || src == nil || !src.Local {
		gate.Rollback()
		return e
	}
	var present bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lyric_resources WHERE item_id=? AND asset_id=? AND source_version=? AND scope='library' AND deleted=0)`, entity, src.ID, src.Version).Scan(&present)
	gate.Rollback()
	if e != nil || present {
		return e
	}
	local, e := s.Local.ReadLyrics(ctx, src.Root, src.Path, src.Size, src.ModifiedNS, "und", "")
	if e != nil {
		return nil
	}
	var chosen *acquired
	best := -1
	for _, v := range local.Candidates {
		lower := strings.ToLower(string(v.Data))
		if v.Format == "text" && (strings.Contains(lower, "exact audio copy") || strings.Contains(lower, "track quality") || strings.Contains(lower, "extraction logfile") || strings.Contains(lower, "accuraterip")) {
			continue
		}
		d, err := Parse(v.Data, v.Format)
		lang, le := Language(v.Language)
		if err != nil || le != nil || validateTiming(d, d.EmbeddedOffsetMS, src.SourceDuration) != nil {
			continue
		}
		rank := 0
		if v.Origin == "sidecar" {
			rank += 2
		}
		if v.Format == "lrc" {
			rank++
		}
		if rank <= best {
			continue
		}
		best = rank
		rights := "Supplied with the media by its owner."
		if d.SkippedLines > 0 {
			rights += " Some lyric lines could not be read and were skipped."
		}
		label := v.Label
		if !boundedText(label, 256) {
			label = "Local lyrics"
		}
		chosen = &acquired{doc: d, language: lang, provenance: Provenance{Origin: v.Origin, Label: label, Rights: rights}}
	}
	if chosen == nil {
		return nil
	}
	gate, entity, e = s.begin(ctx, a)
	if e != nil {
		return e
	}
	defer gate.Rollback()
	tx = gate.Tx()
	_, fresh, _, e := resolve(ctx, tx, a, entity, t)
	if e != nil {
		return e
	}
	if fresh == nil || fresh.ID != src.ID || fresh.Version != src.Version || fresh.Size != src.Size || fresh.ModifiedNS != src.ModifiedNS {
		return ErrSource
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM lyric_resources WHERE item_id=? AND scope='library'`, entity).Scan(&count); e != nil {
		return e
	}
	if count >= 16 {
		return nil
	}
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lyric_resources WHERE item_id=? AND asset_id=? AND source_version=? AND scope='library' AND deleted=0)`, entity, src.ID, src.Version).Scan(&present); e != nil || present {
		return e
	}
	id := identity.Token()
	if _, e = tx.ExecContext(ctx, `INSERT INTO lyric_resources(id,item_id,asset_id,source_version,scope,authority,account_id,profile_id,revision,source_duration,source_start,source_end)VALUES(?,?,?,?,'library','local','','',1,?,?,?)`, id, entity, src.ID, src.Version, src.SourceDuration, src.StartSeconds, src.StartSeconds+src.Duration); e != nil {
		return e
	}

	raw, _ := json.Marshal(chosen.doc)
	prov, _ := json.Marshal(chosen.provenance)
	if _, e = tx.ExecContext(ctx, `INSERT INTO lyric_revisions(resource_id,revision,document,digest,language,offset_ms,provenance,actor,created_at,deleted)VALUES(?,1,?,?,?,?,?,?,?,0)`, id, string(raw), digest(chosen.doc), chosen.language, chosen.doc.EmbeddedOffsetMS, string(prov), "local", time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
		return e
	}
	return gate.Commit()
}
