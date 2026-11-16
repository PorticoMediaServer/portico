package artwork

import (
	"bytes"
	"image/jpeg"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server-next/internal/atomicfile"
)

// PutSheet stores a decoder-made sprite sheet without resizing its cells. It is kept apart
// from public artwork variants: only the authenticated scrub-preview route serves it.
func (s *Store) PutSheet(id string, data []byte) error {
	if !ValidID(id) || len(data) > 4<<20 {
		return ErrNotAPicture
	}
	shape, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || shape.Width < 1 || shape.Height < 1 || shape.Width > 4096 || shape.Height > 4096 {
		return ErrNotAPicture
	}
	if err := os.MkdirAll(filepath.Dir(s.place(id, "sheet")), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(s.place(id, "sheet"), data, 0o644)
}

func (s *Store) ReadSheet(id string) ([]byte, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	data, err := os.ReadFile(s.place(id, "sheet"))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	return data, err
}
