package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
)

// ffmpeg is the shared, already resolved and verified startup tool.
func initializeAnalysis(db *sql.DB, state string, inputs *subtitlevideo.Runtime, ffmpeg string, supervisor *storage.Supervisor) (*mediaanalysis.Service, error) {
	o := mediaanalysis.Options{DB: db, Directory: storagepaths.Path(state, "analysis-artifacts"), FFmpeg: ffmpeg, Open: func(ctx context.Context, item, asset string) (mediaanalysis.Input, error) {
		return inputs.OpenSubtitleInput(ctx, item, asset, "")
	}, Decoder: func(input mediaanalysis.Input) mediaanalysis.Decode { return inputs.AnalysisDecoder(input) }}
	helper := os.Getenv("PORTICO_SONIC_HELPER")
	if helper == "" {
		helper = filepath.Join(filepath.Dir(ffmpeg), "portico-sonic")
	}
	model := os.Getenv("PORTICO_SONIC_MODEL_DIRECTORY")
	if model == "" {
		model = filepath.Join(filepath.Dir(helper), "musicnn-mtt")
	}
	var sonicError error
	runtimeOptions := sonic.RuntimeOptions{Helper: helper, ModelDirectory: model, Supervisor: supervisor, Libraries: filepath.SplitList(os.Getenv("PORTICO_SONIC_LIBRARIES"))}
	o.OpenSonic = func() (*sonic.Runtime, error) { return sonic.OpenRuntime(runtimeOptions) }
	o.Sonic, sonicError = o.OpenSonic()
	if sonicError != nil {
		log.Printf("Sonic analysis unavailable: %v", sonicError)
	}
	// These are real operator budgets. No capability is suppressed because a
	// developer lacked a decoder, browser, simulator, or provider credentials.
	for name, dest := range map[string]*int64{"PORTICO_ANALYSIS_MAX_INPUT_BYTES": &o.MaxInputBytes, "PORTICO_ANALYSIS_MAX_GENERATED_BYTES": &o.MaxGeneratedBytes, "PORTICO_ANALYSIS_MAX_CACHE_BYTES": &o.MaxCacheBytes} {
		if value := os.Getenv(name); value != "" {
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil || n <= 0 {
				return nil, fmt.Errorf("invalid %s", name)
			}
			*dest = n
		}
	}
	for name, dest := range map[string]*int{"PORTICO_ANALYSIS_MAX_PREVIEW_FRAMES": &o.MaxPreviewFrames, "PORTICO_ANALYSIS_RETAIN_DAYS": &o.RetainDays} {
		if value := os.Getenv(name); value != "" {
			n, e := strconv.Atoi(value)
			if e != nil || n <= 0 {
				return nil, fmt.Errorf("invalid %s", name)
			}
			*dest = n
		}
	}
	s, err := mediaanalysis.New(o)
	if err != nil {
		return nil, err
	}
	if err = s.QueueSonicRefresh(context.Background()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
