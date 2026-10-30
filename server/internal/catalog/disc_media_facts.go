package catalog

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/disc"
)

// DiscMediaFacts is the measured title behind one physical version's timed
// media. It supplies display facts, never an alternate-title playback grant.
type DiscMediaFacts struct {
	Kind         FileInfoDiscKind `json:"kind"`
	Packaging    DiscPackaging    `json:"packaging"`
	TitleNumber  int              `json:"titleNumber"`
	ChapterCount int              `json:"chapterCount"`
}

type DiscPackaging string

func (DiscPackaging) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []any{"iso", "folder"}}
}

// ReadDiscMediaFacts uses the caller's admitted asset and original snapshot.
// Old scans without the exact timed-media link, stale roots/revisions, and
// ambiguous asset associations deliberately produce no optional facts.
func ReadDiscMediaFacts(ctx context.Context, q discQueryer, asset string) (*DiscMediaFacts, error) {
	rows, err := q.QueryContext(ctx, `SELECT d.kind,d.packaging,t.title_number,
 (SELECT COUNT(*) FROM inventory_disc_chapters c WHERE c.object_id=t.object_id AND c.title_key=t.title_key)
 FROM catalog_assets a
 JOIN inventory_disc_facts d ON d.asset_id=a.token
 JOIN inventory_objects o ON o.id=d.object_id AND o.asset_id=a.token
 JOIN library_sources s ON s.id=o.source_id
 JOIN inventory_disc_titles t ON t.object_id=d.object_id AND t.title_key=d.media_title_key
 JOIN asset_stream_facts st ON st.asset_id=a.token AND st.revision=d.media_stream_revision AND st.size=a.size AND st.modified_ns=a.modified_ns
 JOIN asset_chapter_facts ch ON ch.asset_id=a.token AND ch.revision=d.media_chapter_revision AND ch.size=a.size AND ch.modified_ns=a.modified_ns AND ch.status='known'
 WHERE a.token=? AND a.available=1 AND d.source_revision=o.revision AND d.source_generation=s.generation
 AND d.root_incarnation=s.incarnation AND o.root_incarnation=s.incarnation
 AND s.enabled=1 AND s.kind='local' AND o.retired=0 AND o.state='available' AND d.encryption='none'
 LIMIT 2`, asset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out *DiscMediaFacts
	for rows.Next() {
		var value DiscMediaFacts
		if err = rows.Scan(&value.Kind, &value.Packaging, &value.TitleNumber, &value.ChapterCount); err != nil {
			return nil, err
		}
		if out != nil || (value.Kind != "bluray" && value.Kind != "dvd") || (value.Packaging != "iso" && value.Packaging != "folder") || value.TitleNumber < 1 || value.TitleNumber > disc.MaxTitles || value.ChapterCount < 0 || value.ChapterCount > 65535 {
			return nil, nil
		}
		out = &value
	}
	return out, rows.Err()
}

// readDiscMediaFactsBatch applies the same exact observation/fact fences as the
// single-asset reader. Multiple matches still make that asset ambiguous.
func (s *Service) readDiscMediaFactsBatch(tokens []string) (map[string]*DiscMediaFacts, error) {
	out := map[string]*DiscMediaFacts{}
	if len(tokens) == 0 {
		return out, nil
	}
	rows, err := s.read().Query(`SELECT a.token,d.kind,d.packaging,t.title_number,
 (SELECT COUNT(*) FROM inventory_disc_chapters c WHERE c.object_id=t.object_id AND c.title_key=t.title_key)
 FROM catalog_assets a
 JOIN inventory_disc_facts d ON d.asset_id=a.token
 JOIN inventory_objects o ON o.id=d.object_id AND o.asset_id=a.token
 JOIN library_sources s ON s.id=o.source_id
 JOIN inventory_disc_titles t ON t.object_id=d.object_id AND t.title_key=d.media_title_key
 JOIN asset_stream_facts st ON st.asset_id=a.token AND st.revision=d.media_stream_revision AND st.size=a.size AND st.modified_ns=a.modified_ns
 JOIN asset_chapter_facts ch ON ch.asset_id=a.token AND ch.revision=d.media_chapter_revision AND ch.size=a.size AND ch.modified_ns=a.modified_ns AND ch.status='known'
 WHERE a.token IN(SELECT value FROM json_each(?)) AND a.available=1 AND d.source_revision=o.revision AND d.source_generation=s.generation
 AND d.root_incarnation=s.incarnation AND o.root_incarnation=s.incarnation
 AND s.enabled=1 AND s.kind='local' AND o.retired=0 AND o.state='available' AND d.encryption='none'`, idsJSON(tokens))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var token string
		var value DiscMediaFacts
		if err = rows.Scan(&token, &value.Kind, &value.Packaging, &value.TitleNumber, &value.ChapterCount); err != nil {
			return nil, err
		}
		counts[token]++
		if counts[token] > 1 || (value.Kind != "bluray" && value.Kind != "dvd") || (value.Packaging != "iso" && value.Packaging != "folder") || value.TitleNumber < 1 || value.TitleNumber > disc.MaxTitles || value.ChapterCount < 0 || value.ChapterCount > 65535 {
			out[token] = nil
			continue
		}
		out[token] = &value
	}
	return out, rows.Err()
}
