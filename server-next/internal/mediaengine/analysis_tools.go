package mediaengine

import (
	"context"

	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

// AnalysisTools is what heavy background analysis needs to decode a file as the converter
// does: the supervisor its processes run under, and the ffmpeg in force once it has said what
// it can do ("" on a server without a working one).
func (e *Engine) AnalysisTools(ctx context.Context) (*processpool.Supervisor, string, error) {
	t := e.tools()
	converter, _, err := t.wait(ctx)
	if err != nil || !converter {
		return e.processes, "", err
	}
	return e.processes, t.ffmpeg, nil
}
