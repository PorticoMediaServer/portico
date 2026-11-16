package api

import (
	"cmp"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func libraryOptionsOut(o db.LibraryOptions) LibraryOptions {
	watch := !o.NoWatch
	group, sound := o.GroupSeries == nil || *o.GroupSeries, !o.NoSoundAnalysis
	scrub := o.ScrubPreviews
	out := LibraryOptions{Depth: o.Depth, Trash: TrashRule{Mode: o.Trash.Mode, Days: o.Trash.Days}, AllowDelete: o.AllowDelete,
		Watch: &watch, RescanMinutes: o.RescanMinutes, GroupSeries: &group, Lyrics: o.Lyrics, SoundAnalysis: &sound, SegmentDetection: o.SegmentDetection,
		EpisodeThumbnails: cmp.Or(o.EpisodeThumbnails, db.EpisodeThumbnailsDefault), ScrubPreviews: &scrub}
	if out.SegmentDetection == "" {
		out.SegmentDetection = "server"
	}
	if out.Depth == "" {
		out.Depth = db.DepthBasic
	}
	if out.Trash.Mode == "" {
		out.Trash.Mode = db.TrashAfterDays
	}
	if out.Trash.Mode == db.TrashAfterDays && out.Trash.Days == 0 {
		out.Trash.Days = 30
	}
	return out
}

func optionsIn(o LibraryOptions) db.LibraryOptions {
	return db.LibraryOptions{Depth: o.Depth, Trash: db.TrashRule{Mode: o.Trash.Mode, Days: o.Trash.Days}, AllowDelete: o.AllowDelete,
		NoWatch: o.Watch != nil && !*o.Watch, RescanMinutes: o.RescanMinutes, GroupSeries: o.GroupSeries, Lyrics: o.Lyrics,
		ScrubPreviews: o.ScrubPreviews != nil && *o.ScrubPreviews, NoSoundAnalysis: o.SoundAnalysis != nil && !*o.SoundAnalysis, SegmentDetection: o.SegmentDetection, EpisodeThumbnails: o.EpisodeThumbnails}
}
