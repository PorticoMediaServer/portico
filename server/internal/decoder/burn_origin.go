package decoder

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

// BurnInputOrigin measures the demuxer origin of a sealed bitmap subtitle file.
// Restoring that origin preserves authored cue times without global copyts.
func BurnInputOrigin(ctx context.Context, executable, file, format string) (int64, error) {
	if !filepath.IsAbs(file) || filepath.Clean(file) != file || !filepath.IsAbs(executable) {
		return 0, ErrInvalidConfiguration
	}
	formats := map[string]string{"pgs": "sup", "vobsub": "vobsub,mpeg", "mks": "matroska", "dvb": "matroska"}[format]
	if formats == "" {
		return 0, ErrInvalidConfiguration
	}
	read := []string{file}
	if format == "vobsub" {
		read = append(read, file[:len(file)-len(filepath.Ext(file))]+".sub")
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd, err := mediaexec.CommandContext(bounded, mediaexec.Job{Executable: executable, Args: []string{"-v", "error", "-max_alloc", "8388608", "-protocol_whitelist", "file", "-format_whitelist", formats, "-probesize", "8388608", "-analyzeduration", "10000000", "-show_entries", "format=start_time", "-of", "json", file}, ReadPaths: read})
	if err != nil {
		return 0, err
	}
	cmd.WaitDelay = time.Second
	output := &toolOutput{}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	var result struct {
		Format struct {
			Start string `json:"start_time"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		return 0, err
	}
	if result.Format.Start == "" || result.Format.Start == "N/A" {
		return 0, nil
	}
	start, err := strconv.ParseFloat(result.Format.Start, 64)
	if err != nil || math.IsNaN(start) || math.IsInf(start, 0) || math.Abs(start) > 86400 {
		return 0, ErrInvalidConfiguration
	}
	return int64(math.Round(start * 1e6)), nil
}
