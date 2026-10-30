package subtitles

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

// DownloadTrack is one subtitle track a download carries with it, so the title
// has its subtitles with no server in reach.
//
// Text says the track can be fetched as WebVTT, the rendering every device can
// show. Original names the form the track was authored in when that is a
// styled script or a picture stream a device can draw for itself ("ass",
// "pgs", "vobsub", "dvb"); Companion says that form comes as a pair of files
// (VobSub's index and its pictures); Fonts counts the fonts attached to a
// styled script, which travel with it.
type DownloadTrack struct {
	ID        string `json:"id"`
	Revision  int64  `json:"revision"`
	Language  string `json:"language"`
	Title     string `json:"title"`
	Format    string `json:"format"`
	Default   bool   `json:"default"`
	Forced    bool   `json:"forced"`
	Text      bool   `json:"text"`
	Original  string `json:"original,omitempty"`
	Companion bool   `json:"companion,omitempty"`
	Fonts     int    `json:"fonts,omitempty"`
	// Lines is an image track's line count and how many of its lines are
	// forced, as /lines answers it (PGS and VobSub).
	Lines *Lines `json:"lines,omitempty"`
}

// DownloadTracks lists the subtitle tracks of one file of a title that this
// viewer may take offline: the shared ones and the viewer's own, for the file
// as it is now. A track read from an earlier version of the file is left out;
// its timings are not the file's.
//
// A styled track's fonts are only known once the track has been read out of
// its file, which the server does the first time anybody needs it. A styled
// track that has not been read yet is read now; if that is still going on when
// ctx ends, the answer is ErrUnavailable and the caller asks again later (the
// reading carries on meanwhile). A track that cannot be read at all is left
// out, and the rest are listed.
func (s *Service) DownloadTracks(ctx context.Context, p identity.Principal, item, source string) ([]DownloadTrack, error) {
	out := []DownloadTrack{}
	if !validID(item) || !validID(source) {
		return out, ErrInput
	}
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return out, e
	}
	catalog, e := s.ListTx(ctx, snapshot.Tx(), p, item, source)
	snapshot.Rollback()
	if e != nil {
		return out, e
	}
	for _, r := range catalog.Resources {
		if r.discScoped || !r.Enabled || r.Retired || !visible(r, p) {
			continue
		}
		track := DownloadTrack{ID: r.ID, Revision: r.Revision, Language: r.Language, Title: r.Title, Format: r.Format, Default: r.Default, Forced: r.Forced}
		for _, d := range r.Deliveries {
			switch d.Format {
			case "text":
				track.Text = true
			case "ass", "pgs", "vobsub", "dvb":
				track.Original = d.Format
			}
		}
		track.Companion = track.Original == "vobsub"
		if track.Original == "ass" {
			if e = s.ensureCached(ctx, p, item, r.ID, r.Revision); e != nil {
				if ctx.Err() != nil {
					return out, ErrUnavailable
				}
				continue
			}
			if e = s.db.QueryRowContext(ctx, `SELECT count(*) FROM subtitle_original_files WHERE resource_id=? AND revision=? AND part LIKE 'font:%'`, r.ID, r.Revision).Scan(&track.Fonts); e != nil {
				return out, e
			}
			if track.Fonts > 16 {
				track.Fonts = 16
			}
		}
		if track.Original == "pgs" || track.Original == "vobsub" {
			// A download plays offline, where it cannot ask: it carries the
			// count that tells the player whether "Forced only" means anything.
			if e = s.ensureCached(ctx, p, item, r.ID, r.Revision); e != nil {
				if ctx.Err() != nil {
					return out, ErrUnavailable
				}
			} else {
				var lines Lines
				if e = s.db.QueryRowContext(ctx, `SELECT forced_lines,total_lines FROM subtitle_original_revisions WHERE resource_id=? AND revision=?`, r.ID, r.Revision).Scan(&lines.Forced, &lines.Total); e == nil {
					track.Lines = &lines
				} else if !errors.Is(e, sql.ErrNoRows) {
					return out, e
				}
			}
		}
		if track.Text || track.Original != "" {
			out = append(out, track)
		}
	}
	return out, nil
}

// downloadResource reads one track for a download: the current revision of a
// resource of this title that the viewer may see. The caller has already
// established that the viewer may transfer the title; this checks the track
// itself the way a listing does, then makes sure its bytes have been read out
// of the file.
func (s *Service) downloadResource(ctx context.Context, p identity.Principal, item, id string, revision int64) (Resource, identity.Principal, error) {
	if !validID(item) || !validID(id) || revision < 1 {
		return Resource{}, p, ErrInput
	}
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return Resource{}, p, e
	}
	r, e := scanResource(snapshot.Tx().QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=r.current_revision WHERE r.id=? AND v.revision=? AND r.deleted=0 AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, id, revision, item))
	snapshot.Rollback()
	if errors.Is(e, sql.ErrNoRows) {
		return r, p, identity.ErrNotVisible
	}
	if e != nil {
		return r, p, e
	}
	if r.discScoped {
		return r, p, ErrUnsupported
	}
	if !visible(r, p) {
		return r, p, identity.ErrNotVisible
	}
	if e = s.ensureCached(ctx, p, item, id, revision); e != nil {
		if ctx.Err() != nil {
			return r, p, ErrUnavailable
		}
		return r, p, e
	}
	return r, p, nil
}

// DownloadTrackVTT renders one track as a whole WebVTT document on the title's
// own clock, for a device to keep beside the downloaded file. A styled track
// is rendered from its plain-text reading.
func (s *Service) DownloadTrackVTT(ctx context.Context, p identity.Principal, item, id string, revision int64) ([]byte, error) {
	if _, _, e := s.downloadResource(ctx, p, item, id, revision); e != nil {
		return nil, e
	}
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return nil, e
	}
	defer snapshot.Rollback()
	// Read again after the track was filled in: its bytes are known now.
	r, e := scanResource(snapshot.Tx().QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=r.current_revision WHERE r.id=? AND v.revision=?`, id, revision))
	if e != nil || !visible(r, p) {
		return nil, identity.ErrNotVisible
	}
	object := mediaartifact.Object{Digest: r.digest, Size: r.size}
	if r.Renderer != "external_text" {
		if e = snapshot.Tx().QueryRowContext(ctx, `SELECT digest,size FROM subtitle_original_files WHERE resource_id=? AND revision=? AND part='text'`, id, revision).Scan(&object.Digest, &object.Size); e != nil {
			return nil, identity.ErrNotVisible
		}
	}
	var offset int64
	if e = snapshot.Tx().QueryRowContext(ctx, `SELECT offset_us FROM subtitle_revisions WHERE resource_id=? AND revision=?`, id, revision).Scan(&offset); e != nil {
		return nil, e
	}
	// Opened under the snapshot, so collection cannot retire the bytes between
	// the check and the read.
	reader, e := s.objects.Open(ctx, object)
	if e != nil {
		return nil, e
	}
	defer reader.Close()
	if e = snapshot.Commit(); e != nil {
		return nil, e
	}
	document, e := ManifestWebVTT(reader, offset, -1)
	if e != nil {
		return nil, e
	}
	// The timestamp map is for a subtitle cut into streaming segments. A file
	// kept whole beside its video has no use for it.
	return bytes.Replace(document, []byte("X-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:0\n"), nil, 1), nil
}

// OpenDownloadOriginal lends one file of a track as it was authored: "data"
// is the styled script or the picture stream itself, "companion" the second
// file of a pair, "font:N" one attached font. It answers the file's size, its
// format and, for a script, the character encoding it was written in.
func (s *Service) OpenDownloadOriginal(ctx context.Context, p identity.Principal, item, id string, revision int64, part string) (*mediaartifact.Reader, int64, string, string, error) {
	if _, _, e := s.downloadResource(ctx, p, item, id, revision); e != nil {
		return nil, 0, "", "", e
	}
	snapshot, _, e := s.readTx(ctx, p, item)
	if e != nil {
		return nil, 0, "", "", e
	}
	defer snapshot.Rollback()
	var object mediaartifact.Object
	var format, encoding string
	e = snapshot.Tx().QueryRowContext(ctx, `SELECT digest,size,format,encoding FROM subtitle_original_files WHERE resource_id=? AND revision=? AND part=?`, id, revision, part).Scan(&object.Digest, &object.Size, &format, &encoding)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, 0, "", "", identity.ErrNotVisible
	}
	if e != nil {
		return nil, 0, "", "", e
	}
	reader, e := s.objects.Open(ctx, object)
	return reader, object.Size, format, encoding, e
}
