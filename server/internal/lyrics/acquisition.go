package lyrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/localonly"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type LocalReader interface {
	ReadLyrics(context.Context, string, string, int64, int64, string, string) (storage.LocalLyrics, error)
}
type ProviderSettings struct {
	LRCLIB     bool  `json:"lrclib"`
	Configured bool  `json:"configured"`
	Revision   int64 `json:"revision"`
}

func (s *Service) settings(ctx context.Context, tx *sql.Tx) (ProviderSettings, error) {
	var out ProviderSettings
	var enabled string
	out.Configured = s.Provider != nil
	e := tx.QueryRowContext(ctx, `SELECT (SELECT value FROM configuration WHERE key='lyrics_lrclib_enabled'),CAST((SELECT value FROM configuration WHERE key='lyrics_provider_revision') AS INTEGER)`).Scan(&enabled, &out.Revision)
	out.LRCLIB = enabled == "true" && out.Configured
	if e != nil {
		return out, e
	}
	_, epoch, err := localonly.Online(ctx, tx)
	if err != nil {
		return out, err
	}
	out.Revision += epoch - 1
	return out, nil
}

// librarySettings narrows the existing global provider authority for one song.
// Its revision fences staged candidates across per-library policy changes.
func (s *Service) librarySettings(ctx context.Context, tx *sql.Tx, library string) (ProviderSettings, error) {
	out, err := s.settings(ctx, tx)
	if err != nil {
		return out, err
	}
	allowed, err := libraryLyricsAllowed(ctx, tx, library)
	if err != nil {
		return out, err
	}
	local, err := localonly.Library(ctx, tx, library)
	if err != nil {
		return out, err
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM library_preferences WHERE library_id=?),1)`, library).Scan(&revision); err != nil {
		return out, err
	}
	out.LRCLIB = out.LRCLIB && allowed && !local
	out.Revision += revision - 1
	return out, nil
}

// onlineAdmission rechecks the exact library/provider revision as well as the
// item's current local-only policy immediately before every provider send.
func (s *Service) onlineAdmission(ctx context.Context, epoch int64, library, item string, expected ProviderSettings) context.Context {
	return localonly.WithOnlineAdmission(ctx, s.DB, epoch, localonly.ItemAdmission(item), func(ctx context.Context, tx *sql.Tx) error {
		var currentLibrary string
		if err := tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, item).Scan(&currentLibrary); err != nil {
			return err
		}
		if currentLibrary != library {
			return ErrConflict
		}
		current, err := s.librarySettings(ctx, tx, library)
		if err != nil {
			return err
		}
		if !current.LRCLIB || current.Revision != expected.Revision {
			return ErrConflict
		}
		return nil
	})
}

func (s *Service) Settings(ctx context.Context) (ProviderSettings, error) {
	gated, e := dbwork.BeginSnapshot(ctx, s.DB)
	if e != nil {
		return ProviderSettings{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	out, e := s.settings(ctx, tx)
	return out, e
}
func (s *Service) SetProvider(ctx context.Context, enabled bool, expected int64, authorize func(*sql.Tx) error) (ProviderSettings, error) {
	if authorize == nil {
		return ProviderSettings{}, identity.ErrUnauthorized
	}
	gated2, e := dbwork.Begin(ctx, s.DB, dbwork.ClassBackgroundMedia)
	if e != nil {
		return ProviderSettings{}, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if e = authorize(tx); e != nil {
		return ProviderSettings{}, e
	}
	before, e := s.settings(ctx, tx)
	if e != nil {
		return before, e
	}
	if before.Revision != expected {
		return before, ErrConflict
	}
	if enabled && s.Provider == nil {
		return before, ErrUnavailable
	}
	value := "false"
	if enabled {
		value = "true"
	}
	if _, e = tx.ExecContext(ctx, `UPDATE configuration SET value=? WHERE key='lyrics_lrclib_enabled'`, value); e != nil {
		return before, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE configuration SET value=CAST(value AS INTEGER)+1 WHERE key='lyrics_provider_revision'`); e != nil {
		return before, e
	}
	out, e := s.settings(ctx, tx)
	if e != nil {
		return out, e
	}
	return out, gated2.Commit()
}

type SearchInput struct {
	Target
	Provider         string `json:"provider"`
	Query            string `json:"query"`
	Language         string `json:"language"`
	ResourceID       string `json:"resourceId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}
type Candidate struct {
	ID         string     `json:"id"`
	Language   string     `json:"language"`
	Format     string     `json:"format"`
	Provenance Provenance `json:"provenance"`
	Preview    string     `json:"preview"`
}
type SearchResult struct {
	Candidates []Candidate `json:"candidates"`
	Warnings   []string    `json:"warnings"`
}
type acquired struct {
	doc           Document
	language      string
	provenance    Provenance
	title, artist string
	duration      float64
}

func (s *Service) Search(ctx context.Context, a Access, in SearchInput, probe string) (SearchResult, error) {
	out := SearchResult{Candidates: []Candidate{}, Warnings: []string{}}
	language, e := Language(in.Language)
	if e != nil || in.SourceVersion == "" || in.ExpectedRevision < 0 || in.ExpectedRevision > 128 || in.ResourceID == "" && in.ExpectedRevision != 0 || in.ResourceID != "" && in.ExpectedRevision == 0 {
		return out, ErrInput
	}
	if in.Provider != "local" && in.Provider != "lrclib" || len(in.Query) > 256 || !boundedText(in.Query, 256) {
		return out, ErrInput
	}
	gated, entity, e := s.begin(ctx, a)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	_, src, _, e := resolve(ctx, tx, a, entity, in.Target)
	if e != nil || src == nil {
		gated.Rollback()
		if e != nil {
			return out, e
		}
		return out, ErrSource
	}
	settings, e := s.librarySettings(ctx, tx, a.LibraryID)
	if e != nil {
		gated.Rollback()
		return out, e
	}
	var lookupEpoch int64
	if in.Provider == "lrclib" {
		lookupEpoch, e = localonly.CaptureOnline(ctx, tx)
		if e != nil {
			gated.Rollback()
			return out, e
		}
		if !settings.LRCLIB {
			gated.Rollback()
			return out, ErrUnavailable
		}
		if local, err := localonly.Item(ctx, tx, a.ItemID); err != nil || local {
			gated.Rollback()
			if err != nil {
				return out, err
			}
			return out, localonly.Err
		}
	}
	// Search uses catalog identity as its default; an explicit owner/viewer query
	// remains advisory and never changes the canonical music identity.
	query := strings.TrimSpace(in.Query)
	if query == "" {
		e = tx.QueryRowContext(ctx, `SELECT i.title||' '||COALESCE((SELECT ar.title FROM catalog_song_artists sa JOIN catalog_entities ar ON ar.id=sa.artist_id WHERE sa.song_id=i.id ORDER BY ar.id LIMIT 1),'') FROM catalog_entities i WHERE i.public_id=pid_blob(?)`, a.ItemID).Scan(&query)
	}
	gated.Rollback()
	if e != nil {
		return out, e
	}
	var found []acquired
	switch in.Provider {
	case "local":
		if s.Local == nil || !src.Local {
			return out, ErrUnavailable
		}
		local, e := s.Local.ReadLyrics(ctx, src.Root, src.Path, src.Size, src.ModifiedNS, language, probe)
		if e != nil {
			return out, ErrUnavailable
		}
		out.Warnings = local.Warnings
		for _, v := range local.Candidates {
			d, e := Parse(v.Data, v.Format)
			lang, le := Language(v.Language)
			if e != nil || le != nil || validateTiming(d, d.EmbeddedOffsetMS, src.SourceDuration) != nil {
				out.Warnings = append(out.Warnings, "A local lyric was rejected because its text or timing was invalid.")
				continue
			}
			if d.SkippedLines > 0 {
				out.Warnings = append(out.Warnings, "Some lyric lines could not be read and were skipped.")
			}
			label := v.Label
			if !boundedText(label, 256) {
				label = "Local lyrics"
			}
			found = append(found, acquired{doc: d, language: lang, provenance: Provenance{Origin: v.Origin, Label: label, Rights: "Rights supplied by the media owner; not independently verified."}})
		}
	case "lrclib":
		if !settings.LRCLIB || s.Provider == nil {
			return out, ErrUnavailable
		}
		if e = localonly.CheckOnline(ctx, dbwork.ReadHandle(ctx, s.DB), lookupEpoch); e != nil {
			return out, e
		}
		ctx = s.onlineAdmission(ctx, lookupEpoch, a.LibraryID, a.ItemID, settings)
		found, e = s.Provider.Search(ctx, query)
		if e != nil {
			return out, e
		}
	}
	// External work has finished before entering a short publication transaction.
	// Re-check both source association and current viewer/provider authorization.
	var gated2 *dbwork.Write
	gated2, entity, e = s.begin(ctx, a)
	if e != nil {
		return out, e
	}
	tx = gated2.Tx()
	defer gated2.Rollback()
	_, current, _, e := resolve(ctx, tx, a, entity, in.Target)
	if e != nil {
		return out, e
	}
	if current == nil || current.Version != src.Version {
		return out, ErrSource
	}
	nowSettings, e := s.librarySettings(ctx, tx, a.LibraryID)
	if e != nil {
		return out, e
	}
	if in.Provider == "lrclib" && (!nowSettings.LRCLIB || nowSettings.Revision != settings.Revision) {
		return out, ErrConflict
	}
	if in.ResourceID != "" {
		r, e := readRevision(ctx, tx, a, entity, src, in.ResourceID, in.ExpectedRevision, false)
		if e != nil {
			return out, e
		}
		if !r.CanManage {
			return out, identity.ErrUnauthorized
		}
		var head int64
		var deleted bool
		if e = tx.QueryRowContext(ctx, `SELECT revision,deleted FROM lyric_resources WHERE id=?`, in.ResourceID).Scan(&head, &deleted); e != nil {
			return out, e
		}
		if head != in.ExpectedRevision || deleted {
			return out, ErrConflict
		}
	}
	now := time.Now().UTC()
	if _, e = tx.ExecContext(ctx, `DELETE FROM lyric_candidates WHERE expires_at<=? OR actor=?`, now.Format(time.RFC3339), a.Actor.key()); e != nil {
		return out, e
	}
	// At most 24 short-lived candidates per actor and 4096 server-wide.
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM lyric_candidates`).Scan(&count); e != nil {
		return out, e
	}
	if count+len(found) > 4096 {
		return out, ErrCapacity
	}
	for i, v := range found {
		if i >= 24 {
			break
		}
		if validateTiming(v.doc, v.doc.EmbeddedOffsetMS, src.SourceDuration) != nil {
			out.Warnings = append(out.Warnings, "A provider lyric did not fit this source timeline.")
			continue
		}
		id := identity.Token()
		doc, _ := json.Marshal(v.doc)
		prov, _ := json.Marshal(v.provenance)
		if _, e = tx.ExecContext(ctx, `INSERT INTO lyric_candidates(id,item_id,asset_id,source_version,actor,target_id,expected_revision,document,provenance,language,expires_at,provider_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, entity, src.ID, src.Version, a.Actor.key(), in.ResourceID, in.ExpectedRevision, string(doc), string(prov), v.language, now.Add(10*time.Minute).Format(time.RFC3339), settings.Revision); e != nil {
			return out, e
		}
		preview := []rune(v.doc.Lines[0].Text)
		if len(preview) > 160 {
			preview = preview[:160]
		}
		out.Candidates = append(out.Candidates, Candidate{ID: id, Language: v.language, Format: v.doc.Format, Provenance: v.provenance, Preview: string(preview)})
	}
	if len(out.Warnings) > 12 {
		out.Warnings = out.Warnings[:12]
	}
	return out, gated2.Commit()
}
func (s *Service) Cleanup(ctx context.Context) error {
	_, e := dbwork.ExecWrite(ctx, s.DB, dbwork.ClassBackgroundMedia, `DELETE FROM lyric_candidates WHERE expires_at<=?`, time.Now().UTC().Format(time.RFC3339))
	return e
}
func validOrigin(origin string) (*url.URL, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("lyric provider origin must be an HTTPS origin without credentials, a path or query")
	}
	return u, nil
}
