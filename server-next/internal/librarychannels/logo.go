package librarychannels

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif" // a GIF's first frame
	_ "image/jpeg"
	"image/png"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
)

// Logo limits: an uploaded picture of up to 10 MB and 16 to 4,096 pixels a side, kept as a
// small PNG so no screen ever makes a size of it.
const (
	MaxLogoBytes = 10 << 20
	logoWidth    = 400
)

// ErrLogo: the upload is not a picture this server reads, or is too small or too large.
var ErrLogo = errors.New("not a JPEG, PNG or GIF from 16 to 4,096 pixels a side")

// ReadLogo reads an uploaded logo, an untrusted picture, and answers it as the small PNG that is
// kept, with the hash that names it. Its size is read from the header before it is decoded,
// so a picture claiming to be enormous is refused unread.
func ReadLogo(data []byte) ([]byte, string, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 16 || config.Height < 16 || config.Width > 4096 || config.Height > 4096 {
		return nil, "", ErrLogo
	}
	picture, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", ErrLogo
	}
	var out bytes.Buffer
	if err := png.Encode(&out, artwork.Shrink(picture, logoWidth)); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(out.Bytes())
	return out.Bytes(), hex.EncodeToString(sum[:]), nil
}
