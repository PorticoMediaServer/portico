package writer

import (
	"strings"
	"testing"
)

func TestMPHLSDomainDraftTimelineAndPrivateInit(t *testing.T) {
	p := mpHLSPlanInput{digest: strings.Repeat("a", 64), timelineDigest: strings.Repeat("b", 64), backend: "software", strategy: "copy_remux", timeline: mpSourceKeyframes, renditions: 2, segmentCount: 100, durationUS: 100000000}
	if !p.valid() {
		t.Fatal(p)
	}
	p.timeline = mpConvertedGrid
	if p.valid() {
		t.Fatal("copy picture mislabeled as a converted grid")
	}
	p.strategy = "video_conversion"
	if !p.valid() {
		t.Fatal(p)
	}
	p.renditions = 33
	if p.valid() {
		t.Fatal("unbounded rendition set")
	}
	for _, s := range []string{"../init.mp4", "/init.mp4", "init\\part.mp4", "a..mp4", "init.mp4?token=secret", ""} {
		if mpHLSName(s) {
			t.Fatal(s)
		}
	}
	f := mpHLSWindowFence{execution: "window-2", generation: 2, planDigest: strings.Repeat("a", 64), start: 10, end: 20}
	q := mpHLSPublishedArtifact{execution: f.execution, windowGeneration: 2, planDigest: f.planDigest, rendition: "main", name: "segment-10.m4s", digest: strings.Repeat("c", 64), size: 100, index: 10, startUS: 40000000, endUS: 44000000, decodeFromUS: 39000000, kind: mpMediaSegment}
	if !q.valid(f) {
		t.Fatal(q)
	}
	for _, change := range []func(*mpHLSPublishedArtifact){func(q *mpHLSPublishedArtifact) { q.execution = "window-1" }, func(q *mpHLSPublishedArtifact) { q.windowGeneration = 1 }, func(q *mpHLSPublishedArtifact) { q.index = 21 }, func(q *mpHLSPublishedArtifact) { q.decodeFromUS = q.startUS + 1 }, func(q *mpHLSPublishedArtifact) { q.name = "../segment.ts" }} {
		bad := q
		change(&bad)
		if bad.valid(f) {
			t.Fatal("bad publication admitted", bad)
		}
	}
	q.kind = mpPrivateInit
	q.index = -1
	q.startUS = 0
	q.endUS = 0
	q.decodeFromUS = 0
	q.name = "init.mp4"
	if !q.valid(f) {
		t.Fatal(q)
	}
	q.startUS = 1
	if q.valid(f) {
		t.Fatal("init with media timing")
	}
}
