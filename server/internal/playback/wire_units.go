package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"reflect"
)

type chapterSeekWire ChapterSeek

func (v ChapterSeek) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(chapterSeekWire(v), map[string]string{"positionSeconds": "positionMs"})
}
func (ChapterSeek) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[chapterSeekWire](), map[string]string{"positionSeconds": "positionMs"})
}
func (v *ChapterSeek) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*chapterSeekWire)(v), map[string]string{"positionSeconds": "positionMs"})
}

type playingChapterWire PlayingChapter

func (v PlayingChapter) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(playingChapterWire(v), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}
func (PlayingChapter) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[playingChapterWire](), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}
func (v *PlayingChapter) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*playingChapterWire)(v), map[string]string{"startSeconds": "startMs", "endSeconds": "endMs"})
}

type chapterProjectionWire ChapterProjection

func (v ChapterProjection) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(chapterProjectionWire(v), map[string]string{"duration": "durationMs"})
}
func (ChapterProjection) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[chapterProjectionWire](), map[string]string{"duration": "durationMs"})
}
func (v *ChapterProjection) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*chapterProjectionWire)(v), map[string]string{"duration": "durationMs"})
}

type deliveryPlanWire DeliveryPlan

func (v DeliveryPlan) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(deliveryPlanWire(v), map[string]string{"duration": "durationMs"})
}
func (DeliveryPlan) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[deliveryPlanWire](), map[string]string{"duration": "durationMs"})
}
func (v *DeliveryPlan) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*deliveryPlanWire)(v), map[string]string{"duration": "durationMs"})
}

type sourceOfferWire SourceOffer

func (v SourceOffer) MarshalJSON() ([]byte, error) {
	return apiwire.MarshalDurations(sourceOfferWire(v), map[string]string{"duration": "durationMs"})
}
func (SourceOffer) JSONSchema() map[string]any {
	return apiwire.DurationSchema(reflect.TypeFor[sourceOfferWire](), map[string]string{"duration": "durationMs"})
}
func (v *SourceOffer) UnmarshalJSON(raw []byte) error {
	return apiwire.UnmarshalDurations(raw, (*sourceOfferWire)(v), map[string]string{"duration": "durationMs"})
}
