//go:build !linux

package reading

import (
	"context"
	"image"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// PDFRaster is unavailable outside the pinned Linux artifact target.
type PDFRaster struct{}

func NewPDFRaster(bundle PDFRasterBundle, supervisor *storage.Supervisor) (*PDFRaster, error) {
	return nil, ErrUnavailable
}

func (r *PDFRaster) RenderCoverPNG(ctx context.Context, source archiveStagingSource, size int64) ([]byte, error) {
	return nil, ErrUnavailable
}

func (r *PDFRaster) RenderPage(ctx context.Context, source archiveStagingSource, size int64, page, width int) (*image.RGBA, error) {
	return nil, ErrUnavailable
}
