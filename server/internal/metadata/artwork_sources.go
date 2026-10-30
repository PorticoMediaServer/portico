package metadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"image"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/imagework"
)

// artworkEncoderRevision names the encoder that made a source's blobs. A bump
// re-materialises sources as they are next acquired; it never changes a source
// identity or a selection.
const artworkEncoderRevision = 1

// artworkMaterial is one source and its three blobs, made from one decode of
// the accepted bytes, which are never kept. A reused material names a source
// that is already published at the current policy: it has no blobs to insert.
type artworkMaterial struct {
	source                     string
	acceptedMime               string
	acceptedWidth              int
	acceptedHeight             int
	small, large, ambient      artworkInstalled
	smallOutlier, largeOutlier bool
	largeEdge                  int
	reused                     bool
}

func (m artworkMaterial) installed() []artworkInstalled {
	if m.reused {
		return nil
	}
	return []artworkInstalled{m.small, m.large, m.ambient}
}

var acceptedArtworkMimes = map[string]string{"jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp"}

// encodeArtworkSource decodes the accepted bytes once and encodes both slots
// and the ambient effect. Nothing is installed.
func encodeArtworkSource(ctx context.Context, raw []byte, allowed map[string]bool) (encoded [3]artworkEncoded, mime string, w, h int, err error) {
	_, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return encoded, "", 0, 0, errors.New("unsupported_image")
	}
	decoded, release, err := decodeAcceptedArtwork(ctx, raw, allowed)
	if err != nil {
		return encoded, "", 0, 0, err
	}
	defer release()
	for i, slot := range []artworkSlot{artworkSlotSmall, artworkSlotLarge} {
		if encoded[i], err = encodeArtworkSlot(ctx, decoded, slot); err != nil {
			return encoded, "", 0, 0, err
		}
	}
	if encoded[2], err = encodeAmbientImage(ctx, decoded); err != nil {
		return encoded, "", 0, 0, err
	}
	return encoded, acceptedArtworkMimes[format], decoded.Bounds().Dx(), decoded.Bounds().Dy(), ctx.Err()
}

// materializeArtwork is the one place accepted artwork becomes a source. When
// the same bytes are already published at the current policy and their files
// are intact, nothing is decoded.
func (s *Service) materializeArtwork(ctx context.Context, raw []byte, allowed map[string]bool) (artworkMaterial, error) {
	sum := sha256.Sum256(raw)
	m := artworkMaterial{source: hex.EncodeToString(sum[:])}
	m.largeEdge, _ = artworkSlotLimits(ctx, artworkSlotLarge)
	if s.publishedArtworkSource(ctx, m.source, m.largeEdge) {
		m.reused = true
		return m, nil
	}
	encoded, mime, w, h, err := encodeArtworkSource(ctx, raw, allowed)
	if err != nil {
		return m, err
	}
	m.acceptedMime, m.acceptedWidth, m.acceptedHeight = mime, w, h
	m.smallOutlier, m.largeOutlier = encoded[0].outlier, encoded[1].outlier
	for i, slot := range []*artworkInstalled{&m.small, &m.large, &m.ambient} {
		if *slot, err = s.installArtworkFile(encoded[i].data, encoded[i].width, encoded[i].height); err != nil {
			// Every install failure is a storage refusal, retried without loss.
			if errors.Is(err, ErrArtworkSpace) {
				return m, ErrArtworkSpace
			}
			return m, errArtworkStorage
		}
	}
	return m, nil
}

// publishedArtworkSource reports whether source is published at this large
// edge and encoder, with all three files present.
func (s *Service) publishedArtworkSource(ctx context.Context, source string, largeEdge int) bool {
	var blobs [3]string
	err := dbwork.QueryRow(ctx, s.db, `SELECT small_blob,large_blob,ambient_blob FROM artwork_sources WHERE digest=? AND large_edge=? AND encoder_revision=?`, source, largeEdge, artworkEncoderRevision).Scan(&blobs[0], &blobs[1], &blobs[2])
	if err != nil {
		return false
	}
	for _, digest := range blobs {
		f, err := s.openArtworkObject(digest)
		if err != nil {
			return false
		}
		f.Close()
	}
	return true
}

// errArtworkStorage is a materialisation that could not install its files.
// The work stays queued; artwork is never dropped for lack of room.
var errArtworkStorage = errors.New("storage_unavailable")

// errArtworkSourceGone is a reused source collected between acquisition and
// publication. The work is retried and re-materialises it.
var errArtworkSourceGone = errors.New("artwork source was collected")

// publishArtworkSource records the material's blobs and source in the
// publishing transaction. A source made again at another large edge or by a
// newer encoder replaces its blobs, and every selection naming it moves to a
// new revision so cached pages drop the old descriptor.
func publishArtworkSource(ctx context.Context, tx *sql.Tx, m artworkMaterial, stamp string) error {
	if m.reused {
		var ok bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_sources WHERE digest=?)`, m.source).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return errArtworkSourceGone
		}
		return nil
	}
	for _, o := range m.installed() {
		if err := recordArtworkBlob(ctx, tx, o, stamp); err != nil {
			return err
		}
	}
	var existing bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_sources WHERE digest=?)`, m.source).Scan(&existing); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(digest) DO UPDATE SET small_blob=excluded.small_blob,large_blob=excluded.large_blob,ambient_blob=excluded.ambient_blob,large_edge=excluded.large_edge,small_outlier=excluded.small_outlier,large_outlier=excluded.large_outlier,encoder_revision=excluded.encoder_revision
 WHERE small_blob<>excluded.small_blob OR large_blob<>excluded.large_blob OR ambient_blob<>excluded.ambient_blob OR large_edge<>excluded.large_edge OR encoder_revision<>excluded.encoder_revision`,
		m.source, m.acceptedMime, m.acceptedWidth, m.acceptedHeight, m.small.digest, m.large.digest, m.ambient.digest, m.largeEdge, m.smallOutlier, m.largeOutlier, artworkEncoderRevision, stamp)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil
	}
	if existing {
		if _, err = tx.ExecContext(ctx, `UPDATE artwork_selections SET revision=revision+1 WHERE digest=?`, m.source); err != nil {
			return err
		}
	}
	if err = countArtworkOutcome(ctx, tx, artworkSlotSmall, m.small.mime, m.smallOutlier); err != nil {
		return err
	}
	return countArtworkOutcome(ctx, tx, artworkSlotLarge, m.large.mime, m.largeOutlier)
}

func recordArtworkBlob(ctx context.Context, tx *sql.Tx, o artworkInstalled, stamp string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(digest) DO UPDATE SET status='ready'`, o.digest, o.mime, o.width, o.height, o.size, stamp)
	return err
}

func artworkFormat(mime string) string {
	if mime == "image/png" {
		return "png"
	}
	return "jpeg"
}

func countArtworkOutcome(ctx context.Context, tx *sql.Tx, slot artworkSlot, mime string, outlier bool) error {
	outcome := "within"
	if outlier {
		outcome = "outlier"
	}
	return bumpArtworkCounter(ctx, tx, slot.String(), artworkFormat(mime), outcome)
}

func bumpArtworkCounter(ctx context.Context, tx *sql.Tx, slot, format, outcome string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO artwork_encoding_counters(slot,format,outcome,count) VALUES(?,?,?,1) ON CONFLICT(slot,format,outcome) DO UPDATE SET count=count+1`, slot, format, outcome)
	return err
}

// recordArtworkRefusal counts a cap_unreachable refusal. Other errors are not
// encoder outcomes and are ignored.
func recordArtworkRefusal(ctx context.Context, db *sql.DB, err error) error {
	var refusal *artworkCapError
	if !errors.As(err, &refusal) {
		return nil
	}
	format := "jpeg"
	if refusal.png {
		format = "png"
	}
	_, e := dbwork.ExecWrite(ctx, db, dbwork.ClassBackgroundMedia, `INSERT INTO artwork_encoding_counters(slot,format,outcome,count) VALUES(?,?,'cap_unreachable',1) ON CONFLICT(slot,format,outcome) DO UPDATE SET count=count+1`, refusal.slot.String(), format)
	return e
}

// ArtworkEncodingCounter is one durable encoder outcome tally.
type ArtworkEncodingCounter struct {
	Slot    string `json:"slot"`
	Format  string `json:"format"`
	Outcome string `json:"outcome"`
	Count   int64  `json:"count"`
}

// ArtworkEncodingCounts reports the durable encoder outcome tallies.
func (s *Service) ArtworkEncodingCounts(ctx context.Context) ([]ArtworkEncodingCounter, error) {
	rows, err := dbwork.ReadHandle(ctx, s.db).QueryContext(ctx, `SELECT slot,format,outcome,count FROM artwork_encoding_counters ORDER BY slot,format,outcome`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArtworkEncodingCounter{}
	for rows.Next() {
		var c ArtworkEncodingCounter
		if err = rows.Scan(&c.Slot, &c.Format, &c.Outcome, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// artworkSlotBlobs is a published source's three files.
type artworkSlotBlobs struct {
	small, large, ambient artworkBlob
}

type artworkBlob struct {
	digest, mime         string
	width, height, bytes int
}

// EncodedArtwork is one encoded rendition for stores outside the card-source
// tables (programme images and channel logos).
type EncodedArtwork struct {
	Data          []byte
	Mime          string
	Width, Height int
	Outlier       bool
}

func exportEncoded(e artworkEncoded) EncodedArtwork {
	return EncodedArtwork{e.data, e.mime, e.width, e.height, e.outlier}
}

// ErrArtworkCapUnreachableEncoding is the encoder's refusal of a picture that
// exceeds its cap's allowance at the quality floor.
var ErrArtworkCapUnreachableEncoding = errArtworkCapUnreachable

// EncodeArtworkSlots decodes raw once and encodes the small slot and a large
// slot at the default edge, for programme images.
func EncodeArtworkSlots(ctx context.Context, raw []byte, allowed map[string]bool) (small, large EncodedArtwork, err error) {
	decoded, release, err := decodeAcceptedArtwork(ctx, raw, allowed)
	if err != nil {
		return small, large, err
	}
	defer release()
	s, err := encodeArtworkSlot(ctx, decoded, artworkSlotSmall)
	if err != nil {
		return small, large, err
	}
	l, err := encodeArtworkSlot(ctx, decoded, artworkSlotLarge)
	if err != nil {
		return small, large, err
	}
	return exportEncoded(s), exportEncoded(l), nil
}

// EncodeChannelLogo encodes the one logo rendition: 256 px on the long edge,
// lossless PNG when the logo has transparency.
func EncodeChannelLogo(ctx context.Context, raw []byte, allowed map[string]bool) (EncodedArtwork, error) {
	decoded, release, err := decodeAcceptedArtwork(ctx, raw, allowed)
	if err != nil {
		return EncodedArtwork{}, err
	}
	defer release()
	e, err := encodeArtworkSlot(ctx, decoded, artworkSlotLogo)
	return exportEncoded(e), err
}

// decodeAcceptedArtwork bounds and decodes accepted bytes under the shared
// image gate. The caller encodes from the picture and then releases the gate,
// so one decoded arrival is in flight at a time.
func decodeAcceptedArtwork(ctx context.Context, raw []byte, allowed map[string]bool) (image.Image, func(), error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || !allowed[format] {
		return nil, nil, errors.New("unsupported_image")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 10000 || config.Height > 10000 || int64(config.Width)*int64(config.Height) > 24000000 {
		return nil, nil, errors.New("dimension_limit")
	}
	release, err := imagework.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err == nil && (decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height) {
		release()
		return nil, nil, errors.New("dimension_limit")
	}
	if err != nil {
		release()
		return nil, nil, errors.New("malformed_image")
	}
	if err = ctx.Err(); err != nil {
		release()
		return nil, nil, err
	}
	return decoded, release, nil
}

// RecordArtworkOutcome counts one published programme or logo rendition.
func RecordArtworkOutcome(ctx context.Context, tx *sql.Tx, slot string, e EncodedArtwork) error {
	outcome := "within"
	if e.Outlier {
		outcome = "outlier"
	}
	return bumpArtworkCounter(ctx, tx, slot, artworkFormat(e.Mime), outcome)
}

// RecordArtworkRefusal counts a cap_unreachable refusal; other errors are not
// encoder outcomes and are ignored.
func RecordArtworkRefusal(ctx context.Context, db *sql.DB, err error) error {
	return recordArtworkRefusal(ctx, db, err)
}
