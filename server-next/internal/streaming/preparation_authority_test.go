package streaming

import (
	"context"

	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

type preparationInspector struct {
	inspect func(context.Context, string, string) (subtitles.SourceMediaInfo, error)
}

func (i preparationInspector) InspectSubtitleSource(ctx context.Context, item, source string) (subtitles.SourceMediaInfo, error) {
	return i.inspect(ctx, item, source)
}
