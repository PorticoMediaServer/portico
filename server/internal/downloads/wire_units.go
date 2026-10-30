package downloads

import (
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"reflect"
)

type progressEntryWire ProgressEntry

func (v ProgressEntry) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(progressEntryWire(v), map[string]string{"positionSeconds": "positionMs"})
}
func (ProgressEntry) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[progressEntryWire](), map[string]string{"positionSeconds": "positionMs"})
}
func (v *ProgressEntry) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*progressEntryWire)(v), map[string]string{"positionSeconds": "positionMs"})
}

type progressResultWire ProgressResult

func (v ProgressResult) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(progressResultWire(v), map[string]string{"positionSeconds": "positionMs"})
}
func (ProgressResult) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[progressResultWire](), map[string]string{"positionSeconds": "positionMs"})
}
func (v *ProgressResult) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*progressResultWire)(v), map[string]string{"positionSeconds": "positionMs"})
}

type sourceDescriptionWire SourceDescription

func (v SourceDescription) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(sourceDescriptionWire(v), map[string]string{"durationSeconds": "durationMs"})
}
func (SourceDescription) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[sourceDescriptionWire](), map[string]string{"durationSeconds": "durationMs"})
}

type titleWire Title

func (v Title) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(titleWire(v), map[string]string{"durationSeconds": "durationMs"})
}
func (Title) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[titleWire](), map[string]string{"durationSeconds": "durationMs"})
}
func (v *Title) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*titleWire)(v), map[string]string{"durationSeconds": "durationMs"})
}

type receiptViewingWire ReceiptViewing

func (v ReceiptViewing) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(receiptViewingWire(v), map[string]string{"positionSeconds": "positionMs"})
}
func (ReceiptViewing) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[receiptViewingWire](), map[string]string{"positionSeconds": "positionMs"})
}
func (v *ReceiptViewing) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*receiptViewingWire)(v), map[string]string{"positionSeconds": "positionMs"})
}

type bundleChapterWire BundleChapter

func (v BundleChapter) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(bundleChapterWire(v), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}
func (BundleChapter) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[bundleChapterWire](), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}
func (v *BundleChapter) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*bundleChapterWire)(v), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}

type bundleMarkerWire BundleMarker

func (v BundleMarker) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(bundleMarkerWire(v), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}
func (BundleMarker) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[bundleMarkerWire](), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}
func (v *BundleMarker) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*bundleMarkerWire)(v), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}

type bundleTrickplayWire BundleTrickplay

func (v BundleTrickplay) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(bundleTrickplayWire(v), map[string]string{"intervalSeconds": "intervalMs", "durationSeconds": "durationMs", "sourceOffsetSeconds": "sourceOffsetMs"})
}
func (BundleTrickplay) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[bundleTrickplayWire](), map[string]string{"intervalSeconds": "intervalMs", "durationSeconds": "durationMs", "sourceOffsetSeconds": "sourceOffsetMs"})
}
func (v *BundleTrickplay) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*bundleTrickplayWire)(v), map[string]string{"intervalSeconds": "intervalMs", "durationSeconds": "durationMs", "sourceOffsetSeconds": "sourceOffsetMs"})
}
