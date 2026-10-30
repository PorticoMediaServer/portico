package subtitles

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

type FontAttachment struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}
type Delivery struct {
	Format       string `json:"format"`
	URL          string `json:"url,omitempty"`
	CompanionURL string `json:"companionUrl,omitempty"`
	FontsURL     string `json:"fontsUrl,omitempty"`
	// LinesURL (image subtitles) answers how many lines are forced (Lines),
	// for a device that offers "Forced only" only when it means something.
	LinesURL string `json:"linesUrl,omitempty"`
}
type FontFile struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type FontFiles struct {
	SkippedCount int        `json:"skippedCount,omitempty"`
	Fonts        []FontFile `json:"fonts"`
}

// MediaBaseURL keeps subtitle payloads in a namespace distinct from linear
// playback, regardless of the grant or file format.
func MediaBaseURL(grant, resource string, revision int64) string {
	return "/v1/media/subtitles/" + grant + "/" + resource + "/" + strconv.FormatInt(revision, 10)
}

func deliveryFormats(format string) []Delivery {
	switch format {
	case "ass", "ssa":
		return []Delivery{{Format: "text"}, {Format: "ass"}}
	case "pgs", "sup":
		return []Delivery{{Format: "pgs"}}
	case "vobsub", "idx":
		return []Delivery{{Format: "vobsub"}}
	case "dvb":
		return []Delivery{{Format: "dvb"}}
	default:
		return []Delivery{{Format: "text"}}
	}
}

// OriginalAsset reads a subtitle only under a live source-pinned session grant.
// Resources must be selected or part of this presentation's manifest; a resource
// from another viewer or another source cannot be addressed through the grant.
func (s *Service) OriginalAsset(ctx context.Context, p identity.Principal, item, session, id string, revision int64) (RenderAsset, error) {
	if e := s.CheckOriginal(ctx, p, item, session, id, revision); e != nil {
		return RenderAsset{}, e
	}
	if e := s.ensureSessionCached(ctx, p, item, session, id, revision); e != nil {
		return RenderAsset{}, e
	}
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return RenderAsset{}, e
	}
	defer snapshot.Rollback()
	if e = s.checkOriginalTx(ctx, snapshot.Tx(), p, item, session, id, revision); e != nil {
		return RenderAsset{}, e
	}
	var object mediaartifact.Object
	e = snapshot.Tx().QueryRowContext(ctx, `SELECT o.digest,o.size FROM subtitle_original_revisions o WHERE o.resource_id=? AND o.revision=?`, id, revision).Scan(&object.Digest, &object.Size)
	if e != nil {
		return RenderAsset{}, ErrUnsupported
	}
	reader, e := s.objects.Open(ctx, object)
	if e != nil {
		return RenderAsset{}, e
	}
	defer reader.Close()
	raw, e := io.ReadAll(io.LimitReader(reader, int64(AssetLimitBytes())+1))
	if e != nil {
		return RenderAsset{}, e
	}
	if len(raw) > AssetLimitBytes() {
		return RenderAsset{}, ErrCapacity
	}
	var a RenderAsset
	if json.Unmarshal(raw, &a) != nil || a.Version != 2 {
		return a, ErrInput
	}
	if len(a.Authored) > 0 {
		a.Data = a.Authored
	}
	return a, nil
}
func (s *Service) CheckOriginal(ctx context.Context, p identity.Principal, item, session, id string, revision int64) error {
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return e
	}
	defer snapshot.Rollback()
	return s.checkOriginalTx(ctx, snapshot.Tx(), p, item, session, id, revision)
}
func (s *Service) checkOriginalTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, session, id string, revision int64) error {
	scope, e := s.sessionTx(ctx, tx, p, item, session)
	if e != nil {
		return e
	}
	if err := s.checkDiscResourceSessionTx(ctx, tx, session, id); err != nil {
		return err
	}
	if !scope.available {
		return ErrUnavailable
	}
	var ok bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id LEFT JOIN subtitle_render_revisions rr ON rr.resource_id=v.resource_id AND rr.revision=v.revision JOIN playback_subtitle_pins pin ON pin.resource_id=v.resource_id AND pin.resource_revision=v.revision WHERE pin.session_id=? AND r.id=? AND v.revision=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND r.source_id=? AND v.source_size=? AND v.source_modified_ns=? AND (r.scope='shared' OR r.owner=?) AND (COALESCE(rr.source_evidence,'')='' OR rr.source_evidence=(SELECT evidence FROM subtitle_remote_sessions WHERE session_id=?)))`, session, id, revision, item, scope.source, scope.size, scope.modified, actor(p), session).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return identity.ErrUnauthorized
	}
	return nil
}
func originalURL(base, format string) string {
	suffix := format
	switch format {
	case "pgs":
		suffix = "sup"
	case "vobsub":
		suffix = "idx"
	case "dvb":
		suffix = "mks"
	}
	return base + "/original." + suffix
}
func deliveries(base, format string) []Delivery {
	out := deliveryFormats(format)
	for i := range out {
		if out[i].Format == "text" {
			out[i].URL = base + ".vtt"
		} else {
			out[i].URL = originalURL(base, format)
		}
		if out[i].Format == "ass" {
			out[i].FontsURL = base + "/fonts"
		}
		if out[i].Format == "vobsub" {
			out[i].CompanionURL = base + "/original.sub"
		}
		if out[i].Format == "pgs" || out[i].Format == "vobsub" {
			out[i].LinesURL = base + "/lines"
		}
	}
	return out
}

func RenderingFormat(format string) string {
	for _, d := range deliveryFormats(format) {
		if d.Format != "text" {
			return d.Format
		}
	}
	return "text"
}

func originalArchive(raw []byte) bool {
	var a struct {
		Version int `json:"version"`
	}
	return json.Unmarshal(raw, &a) == nil && a.Version == 2
}
