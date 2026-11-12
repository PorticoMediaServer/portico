package dvr

import (
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// RecordingSettings are the owner's recording defaults. A recording captures its folder
// when it starts; changing the default never moves or cuts off an existing recording.
type RecordingSettings struct {
	Folder      string `json:"folder"`
	BeforeMs    int64  `json:"beforeMs"`
	AfterMs     int64  `json:"afterMs"`
	FloorBytes  int64  `json:"floorBytes"`
	CapBytes    int64  `json:"capBytes"`
	KeepDays    int    `json:"keepDays"`
	KeepNewest  int    `json:"keepNewest"`
	StopViewers bool   `json:"stopViewers"`
}

var Document = settings.Define("dvr", RecordingSettings{BeforeMs: 60_000, AfterMs: 180_000, FloorBytes: 10 << 30}, func(v *RecordingSettings) error {
	v.Folder = strings.TrimSpace(v.Folder)
	if v.Folder != "" && (!filepath.IsAbs(v.Folder) || len(v.Folder) > 4096) {
		return &settings.Invalid{Field: "folder", Message: "Give the full path, or leave it empty for the server's own."}
	}
	for _, f := range []struct {
		name  string
		value int64
	}{{"beforeMs", v.BeforeMs}, {"afterMs", v.AfterMs}} {
		if f.value < 0 || f.value > math.MaxInt64/int64(time.Millisecond) {
			return &settings.Invalid{Field: f.name, Message: "Give a length of 0 or more milliseconds."}
		}
	}
	if v.FloorBytes < 0 || v.CapBytes < 0 {
		return &settings.Invalid{Field: "floorBytes", Message: "Give sizes of 0 or more bytes."}
	}
	if v.KeepDays < 0 || v.KeepNewest < 0 || v.KeepDays > 0 && v.KeepNewest > 0 {
		return &settings.Invalid{Field: "keepDays", Message: "Choose days, a number of episodes, or keep until deleted."}
	}
	return nil
})

func (v RecordingSettings) Options(home string) Options {
	o := Defaults(home)
	if v.Folder != "" {
		o.Folder = filepath.Clean(v.Folder)
	}
	o.PadBefore, o.PadAfter = time.Duration(v.BeforeMs)*time.Millisecond, time.Duration(v.AfterMs)*time.Millisecond
	o.Floor, o.Cap, o.KeepDays, o.KeepNewest = v.FloorBytes, v.CapBytes, v.KeepDays, v.KeepNewest
	o.StopViewers = v.StopViewers
	return o
}
