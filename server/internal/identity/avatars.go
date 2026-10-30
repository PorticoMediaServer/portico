package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/imagework"
	"time"
)

var ErrAvatarUpload = errors.New("That image could not be read. Upload a JPEG or PNG picture.")
var ErrAvatarMissing = errors.New("no avatar")

// Profile avatars follow the artwork-upload approach in internal/metadata: the
// declared content type and the file name are ignored, the bytes are sniffed, the
// image is fully decoded and re-encoded (which strips EXIF, colour profiles,
// trailing data and anything else an original file carried), and only the
// re-encoded renditions are stored.
//
// Avatars are stored in the database rather than on disk because they are small,
// fixed-size and bound to a row that already cascades on profile deletion, so a
// deleted profile cannot leave a picture of a child behind on the filesystem.
// They are never served from a public path: every read proves the caller shares
// the account.

// AvatarUploadBytes caps the accepted original. A square avatar needs nothing
// larger, and a smaller cap is the cheapest defence against a decode bomb.
const AvatarUploadBytes = 4 << 20

// AvatarRenditions are the only sizes stored. Fixed sizes mean a client never
// negotiates one, and a new size is a server change, not a request parameter.
var AvatarRenditions = []int{64, 160, 512}

const avatarMaxDimension = 8000
const avatarMaxPixels = 24_000_000

func sniffAvatar(raw []byte) string {
	switch {
	case bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(raw, []byte{0xff, 0xd8, 0xff}):
		return "image/jpeg"
	}
	return ""
}

// AuthorizeProfileAvatarUpload validates account management and the target
// profile before the HTTP layer reads bytes or the service decodes an image.
// Upload publication revalidates inside its write transaction as well.
func (s *Service) AuthorizeProfileAvatarUpload(ctx context.Context, bearer, profile string) error {
	if !validFamilyID(profile) {
		return ErrDirectInput
	}
	snapshot, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return e
	}
	defer snapshot.Rollback()
	c, e := s.directCallerTx(ctx, snapshot.Tx(), bearer, true)
	if e != nil {
		return e
	}
	var deleted int
	if e = snapshot.Tx().QueryRowContext(ctx, `SELECT deleted FROM direct_profiles WHERE account_id=? AND id=?`, c.account.ID, profile).Scan(&deleted); e != nil || deleted != 0 {
		return ErrNotVisible
	}
	return nil
}

// squareCrop takes the largest centred square of the decoded image so a portrait
// or landscape original is not distorted into the square the client renders.
func squareCrop(src image.Image) image.Rectangle {
	b := src.Bounds()
	side := b.Dx()
	if b.Dy() < side {
		side = b.Dy()
	}
	x := b.Min.X + (b.Dx()-side)/2
	y := b.Min.Y + (b.Dy()-side)/2
	return image.Rect(x, y, x+side, y+side)
}

// renderAvatar produces one square JPEG (opaque) or PNG (transparent) rendition. The sampler is the same
// nearest-neighbour reduction the artwork worker uses: no new dependency, and a
// deterministic output for a given input, which keeps the digest stable.
func renderAvatar(src image.Image, crop image.Rectangle, size int) ([]byte, error) {
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	side := crop.Dx()
	if side == size {
		draw.Draw(out, out.Bounds(), src, crop.Min, draw.Src)
	} else {
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				out.Set(x, y, src.At(crop.Min.X+x*side/size, crop.Min.Y+y*side/size))
			}
		}
	}
	var buf bytes.Buffer
	var err error
	if out.Opaque() {
		err = jpeg.Encode(&buf, out, &jpeg.Options{Quality: 90})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, out)
	}
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ProfileAvatar describes the stored picture. Version is what a client puts in the
// artwork URL so a replaced avatar is fetched rather than read from a cache.
type ProfileAvatar struct {
	ProfileID  string `json:"profileId"`
	Version    int64  `json:"version"`
	UpdatedAt  string `json:"updatedAt"`
	SourceMIME string `json:"sourceMime"`
	URL        string `json:"url"`
}

func avatarURL(profile string, version int64) string {
	if version <= 0 {
		return ""
	}
	return "/v1/profiles/" + profile + "/avatar?v=" + itoa(version)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for v > 0 {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	return string(digits[i:])
}

// UploadProfileAvatar decodes, crops, re-encodes and stores the renditions. The
// decode happens before any transaction begins so a hostile image never holds a
// write lock on the database.
func (s *Service) UploadProfileAvatar(ctx context.Context, bearer, profile string, raw []byte) (ProfileAvatar, error) {
	if !validFamilyID(profile) {
		return ProfileAvatar{}, ErrDirectInput
	}
	if e := s.AuthorizeProfileAvatarUpload(ctx, bearer, profile); e != nil {
		return ProfileAvatar{}, e
	}
	prepared, e := PrepareProfileAvatar(ctx, raw)
	if e != nil {
		return ProfileAvatar{}, e
	}
	ctx = profilePictureWriteContext(ctx)
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if e != nil {
		return ProfileAvatar{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return ProfileAvatar{}, e
	}
	var deleted int
	if e = tx.QueryRowContext(ctx, `SELECT deleted FROM direct_profiles WHERE account_id=? AND id=?`, c.account.ID, profile).Scan(&deleted); e != nil || deleted != 0 {
		return ProfileAvatar{}, ErrNotVisible
	}
	if e = StorePreparedProfileAvatarTx(ctx, tx, profile, prepared); e != nil {
		return ProfileAvatar{}, e
	}
	out, e := s.avatarRecordTx(ctx, tx, profile)
	if e != nil {
		return out, e
	}
	if e = s.collectAvatarObjectsTx(ctx, tx); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET revision=revision+1 WHERE id=?`, profile); e != nil {
		return out, e
	}
	return out, gated.Commit()
}

// collectAvatarObjectsTx drops rendition rows no profile points at. An avatar is
// personal data: nothing keeps it once the last profile stops using it.
func (s *Service) collectAvatarObjectsTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM profile_avatar_objects WHERE digest NOT IN(SELECT digest FROM profile_avatars)`)
	return err
}

func (s *Service) avatarRecordTx(ctx context.Context, tx *sql.Tx, profile string) (ProfileAvatar, error) {
	out := ProfileAvatar{ProfileID: profile}
	e := tx.QueryRowContext(ctx, `SELECT version,updated_at,source_mime FROM profile_avatars WHERE profile_id=?`, profile).Scan(&out.Version, &out.UpdatedAt, &out.SourceMIME)
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrAvatarMissing
	}
	out.URL = avatarURL(profile, out.Version)
	return out, e
}

// ProfileAvatars reports the avatar version of every live profile on the account,
// so a profile list can build its picture URLs without one request per profile.
func (s *Service) ProfileAvatars(ctx context.Context, bearer string) (map[string]ProfileAvatar, error) {
	gated2, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return nil, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, false)
	if e != nil {
		return nil, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT a.profile_id,a.version,a.updated_at,a.source_mime FROM profile_avatars a JOIN direct_profiles p ON p.id=a.profile_id WHERE p.account_id=? AND p.deleted=0`, c.account.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]ProfileAvatar{}
	for rows.Next() {
		var record ProfileAvatar
		if e = rows.Scan(&record.ProfileID, &record.Version, &record.UpdatedAt, &record.SourceMIME); e != nil {
			return nil, e
		}
		record.URL = avatarURL(record.ProfileID, record.Version)
		out[record.ProfileID] = record
	}
	return out, rows.Err()
}

// ReadProfileAvatar returns one rendition for a caller that already holds a
// viewing session on the same account. The version the client asked for is
// reported back so the caller can refuse a stale cache read.
func (s *Service) ReadProfileAvatar(ctx context.Context, account, profile string, size int) ([]byte, int64, string, error) {
	if !validFamilyID(profile) {
		return nil, 0, "", ErrDirectInput
	}
	chosen := AvatarRenditions[len(AvatarRenditions)-1]
	for _, candidate := range AvatarRenditions {
		if size <= candidate {
			chosen = candidate
			break
		}
	}
	var rendition []byte
	var version int64
	var updated string
	e := s.db.QueryRowContext(ctx, `SELECT o.image,a.version,a.updated_at FROM profile_avatars a JOIN profile_avatar_objects o ON o.digest=a.digest AND o.rendition=? JOIN direct_profiles p ON p.id=a.profile_id WHERE a.profile_id=? AND p.account_id=? AND p.deleted=0`, chosen, profile, account).Scan(&rendition, &version, &updated)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, 0, "", ErrAvatarMissing
	}
	return rendition, version, updated, e
}

// DeleteProfileAvatar removes the picture and falls the profile back to its colour
// token. The version still advances so a cached URL cannot resurrect the image.
func (s *Service) DeleteProfileAvatar(ctx context.Context, bearer, profile string) error {
	if !validFamilyID(profile) {
		return ErrDirectInput
	}
	ctx = profilePictureWriteContext(ctx)
	gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if e != nil {
		return e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return e
	}
	var deleted int
	if e = tx.QueryRowContext(ctx, `SELECT deleted FROM direct_profiles WHERE account_id=? AND id=?`, c.account.ID, profile).Scan(&deleted); e != nil || deleted != 0 {
		return ErrNotVisible
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM profile_avatars WHERE profile_id=?`, profile); e != nil {
		return e
	}
	if e = s.collectAvatarObjectsTx(ctx, tx); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE direct_profiles SET revision=revision+1 WHERE id=?`, profile); e != nil {
		return e
	}
	return gated3.Commit()
}

// PreparedProfileAvatar holds only bounded, decoded and re-encoded renditions.
// Preparation runs outside the writer; publishing rechecks the caller and profile.
type PreparedProfileAvatar struct {
	renditions map[int][]byte
	digest     string
	mime       string
}

// PrepareProfileAvatar accepts JPEG or PNG and makes every stored rendition now.
func PrepareProfileAvatar(ctx context.Context, raw []byte) (PreparedProfileAvatar, error) {
	if len(raw) == 0 || len(raw) > AvatarUploadBytes {
		return PreparedProfileAvatar{}, ErrAvatarUpload
	}
	mime := sniffAvatar(raw)
	if mime == "" {
		return PreparedProfileAvatar{}, ErrAvatarUpload
	}
	config, _, e := image.DecodeConfig(bytes.NewReader(raw))
	if e != nil || config.Width < 1 || config.Height < 1 || config.Width > avatarMaxDimension || config.Height > avatarMaxDimension || config.Width*config.Height > avatarMaxPixels {
		return PreparedProfileAvatar{}, ErrAvatarUpload
	}
	release, e := imagework.Acquire(ctx)
	if e != nil {
		return PreparedProfileAvatar{}, e
	}
	defer release()
	decoded, _, e := image.Decode(bytes.NewReader(raw))
	if e != nil {
		return PreparedProfileAvatar{}, ErrAvatarUpload
	}
	if e = ctx.Err(); e != nil {
		return PreparedProfileAvatar{}, e
	}
	if decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return PreparedProfileAvatar{}, ErrAvatarUpload
	}
	crop := squareCrop(decoded)
	if crop.Dx() < 1 {
		return PreparedProfileAvatar{}, ErrAvatarUpload
	}
	renditions := map[int][]byte{}
	digest := sha256.New()
	for _, size := range AvatarRenditions {
		if e = ctx.Err(); e != nil {
			return PreparedProfileAvatar{}, e
		}
		encoded, err := renderAvatar(decoded, crop, size)
		if err != nil {
			return PreparedProfileAvatar{}, err
		}
		renditions[size] = encoded
		digest.Write(encoded)
	}
	release()
	if e = ctx.Err(); e != nil {
		return PreparedProfileAvatar{}, e
	}
	key := base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
	return PreparedProfileAvatar{renditions: renditions, digest: key, mime: mime}, nil
}

// StorePreparedProfileAvatarTx publishes pre-made images under the caller's transaction.
func StorePreparedProfileAvatarTx(ctx context.Context, tx *sql.Tx, profile string, prepared PreparedProfileAvatar) error {
	if len(prepared.renditions) != len(AvatarRenditions) || prepared.digest == "" {
		return ErrAvatarUpload
	}
	for size, encoded := range prepared.renditions {
		if _, e := tx.ExecContext(ctx, `INSERT INTO profile_avatar_objects(digest,rendition,bytes,image) VALUES(?,?,?,?) ON CONFLICT(digest,rendition) DO NOTHING`, prepared.digest, size, len(encoded), encoded); e != nil {
			return e
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, e := tx.ExecContext(ctx, `INSERT INTO profile_avatars(profile_id,digest,source_mime,version,updated_at) SELECT ?,?,?,revision+1,? FROM direct_profiles WHERE id=? ON CONFLICT(profile_id) DO UPDATE SET digest=excluded.digest,source_mime=excluded.source_mime,updated_at=excluded.updated_at,version=excluded.version`, profile, prepared.digest, prepared.mime, now, profile); e != nil {
		return e
	}
	return nil
}

func profilePictureWriteContext(ctx context.Context) context.Context {
	return dbwork.WithPlan(ctx, dbwork.WritePlan{Name: "profile-picture", Rows: 64, Pages: 1024, VMOps: 100000, Why: "The fixed renditions, profile revision and removal of superseded personal images publish atomically."})
}
