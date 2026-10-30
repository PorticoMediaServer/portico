package subtitles

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

type originalFile struct {
	part, name, format, encoding string
	object                       mediaartifact.Object
}

// sealOriginalFiles also counts an image subtitle's lines and forced lines
// (headers only), kept with the original for a device's "Forced only".
func (s *Service) sealOriginalFiles(ctx context.Context, raw []byte, durationUS int64) ([]originalFile, Lines, error) {
	var a RenderAsset
	if json.Unmarshal(raw, &a) != nil || a.Version != 2 {
		return nil, Lines{}, ErrInput
	}
	lines := imageLines(a.Format, a.Data, a.Companion)
	data := a.Data
	if len(a.Authored) > 0 {
		data = a.Authored
	}
	files := []originalFile{}
	add := func(part, name, format, encoding string, data []byte) error {
		if len(data) == 0 {
			return nil
		}
		if len(data) > BinaryLimitBytes() {
			return ErrCapacity
		}
		w, e := s.objects.Begin(int64(BinaryLimitBytes()))
		if e != nil {
			return e
		}
		defer w.Abort()
		if _, e = w.Write(data); e != nil {
			return e
		}
		object, e := w.Seal(ctx)
		if e != nil {
			return e
		}
		files = append(files, originalFile{part, name, format, encoding, object})
		return nil
	}
	if e := add("data", "", a.Format, a.Encoding, data); e != nil {
		return nil, Lines{}, e
	}
	if e := add("companion", "", "vobsub", "", a.Companion); e != nil {
		return nil, Lines{}, e
	}
	if a.Format == "ass" || a.Format == "ssa" {
		if cues, e := ParseASSAsText(a.Data, durationUS); e == nil {
			if document, e := textDocument(cues); e == nil {
				if e = add("text", "", "text", "utf-8", document); e != nil {
					return nil, Lines{}, e
				}
			}
		}
	}

	for i, f := range a.Fonts {
		if e := add("font:"+strconv.Itoa(i), f.Name, "font", "", f.Data); e != nil {
			return nil, Lines{}, e
		}
	}
	return files, lines, nil
}
func publishOriginalFiles(ctx context.Context, tx *sql.Tx, id string, revision int64, files []originalFile) error {
	for _, f := range files {
		if _, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_original_files(resource_id,revision,part,name,format,encoding,digest,size) VALUES(?,?,?,?,?,?,?,?)`, id, revision, f.part, f.name, f.format, f.encoding, f.object.Digest, f.object.Size); e != nil {
			return e
		}
	}
	return nil
}

// OpenOriginalFile lends one immutable raw file. Range delivery reads directly
// from the object store, without decoding a whole bitmap/font archive per viewer.
func (s *Service) OpenOriginalFile(ctx context.Context, p identity.Principal, item, session, id string, revision int64, part string) (*mediaartifact.Reader, int64, string, string, error) {
	if e := s.CheckOriginal(ctx, p, item, session, id, revision); e != nil {
		return nil, 0, "", "", e
	}
	if e := s.ensureSessionCached(ctx, p, item, session, id, revision); e != nil {
		return nil, 0, "", "", e
	}
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return nil, 0, "", "", e
	}
	defer snapshot.Rollback()
	if e = s.checkOriginalTx(ctx, snapshot.Tx(), p, item, session, id, revision); e != nil {
		return nil, 0, "", "", e
	}
	var object mediaartifact.Object
	var format, encoding string
	e = snapshot.Tx().QueryRowContext(ctx, `SELECT digest,size,format,encoding FROM subtitle_original_files WHERE resource_id=? AND revision=? AND part=?`, id, revision, part).Scan(&object.Digest, &object.Size, &format, &encoding)
	if e != nil {
		return nil, 0, "", "", e
	}
	reader, e := s.objects.Open(ctx, object)
	return reader, object.Size, format, encoding, e
}
func (s *Service) OriginalFonts(ctx context.Context, p identity.Principal, item, session, id string, revision int64, base string) (FontFiles, error) {
	out := FontFiles{Fonts: []FontFile{}}
	if e := s.CheckOriginal(ctx, p, item, session, id, revision); e != nil {
		return out, e
	}
	if e := s.ensureSessionCached(ctx, p, item, session, id, revision); e != nil {
		return out, e
	}
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return out, e
	}
	defer snapshot.Rollback()
	if e = s.checkOriginalTx(ctx, snapshot.Tx(), p, item, session, id, revision); e != nil {
		return out, e
	}
	_ = snapshot.Tx().QueryRowContext(ctx, `SELECT skipped_fonts FROM subtitle_original_revisions WHERE resource_id=? AND revision=?`, id, revision).Scan(&out.SkippedCount)
	rows, e := snapshot.Tx().QueryContext(ctx, `SELECT part,name FROM subtitle_original_files WHERE resource_id=? AND revision=? AND part LIKE 'font:%' ORDER BY length(part),part LIMIT 16`, id, revision)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var part, name string
		if e = rows.Scan(&part, &name); e != nil {
			return out, e
		}
		out.Fonts = append(out.Fonts, FontFile{Name: name, URL: base + "/fonts/" + part[len("font:"):]})
	}
	return out, rows.Err()
}

// OriginalLines is how many lines an image subtitle's original has, and how
// many of them are forced, under the same grant and pin as its file.
func (s *Service) OriginalLines(ctx context.Context, p identity.Principal, item, session, id string, revision int64) (Lines, error) {
	var out Lines
	if e := s.CheckOriginal(ctx, p, item, session, id, revision); e != nil {
		return out, e
	}
	if e := s.ensureSessionCached(ctx, p, item, session, id, revision); e != nil {
		return out, e
	}
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return out, e
	}
	defer snapshot.Rollback()
	if e = s.checkOriginalTx(ctx, snapshot.Tx(), p, item, session, id, revision); e != nil {
		return out, e
	}
	e = snapshot.Tx().QueryRowContext(ctx, `SELECT forced_lines,total_lines FROM subtitle_original_revisions WHERE resource_id=? AND revision=?`, id, revision).Scan(&out.Forced, &out.Total)
	if e != nil {
		return Lines{}, ErrUnsupported
	}
	return out, nil
}
