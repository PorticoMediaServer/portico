package librarychannels

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
)

// Logo limits: an uploaded picture of up to 10 MB and 16 to 4,096 pixels a side. It is kept
// by the shared picture store in two small sizes, so no screen ever makes a size of it.
const MaxLogoBytes = 10 << 20

// LogoSizes are the sizes a channel logo is kept in: small for the guide's rows, large for
// the channel's own page and the on-screen logo.
var LogoSizes = artwork.Sizes{Small: 160, Large: 400, SmallCap: 30_000, LargeCap: 120_000}

// ErrLogo: the upload is not a picture this server reads, or is too small or too large.
var ErrLogo = errors.New("not a JPEG, PNG, GIF or WebP from 16 to 4,096 pixels a side")

// LogoID checks an uploaded logo, an untrusted picture, by its header alone, and answers the
// id it is stored under: the hash of its bytes. The picture store decodes and sizes it.
func LogoID(data []byte) (string, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 16 || config.Height < 16 || config.Width > 4096 || config.Height > 4096 {
		return "", ErrLogo
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
