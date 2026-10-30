import type {AudioStream, PlaybackOptions, VideoStream} from '../../../../../packages/client-core/src/playback-v1/types';
import {formatVideoBytes, videoAudioFormatName, videoPictureName, videoTrackWords} from '../../../../../packages/client-core/src/video-track-labels';
import type {I18n} from '../../../../../packages/i18n/src';
import {durationText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import type {DiscTitles} from './disc-titles';

export type NativeVersionRow = Readonly<{id: string; heading: string; detail: string;
  /** Present when the row is one of a physical version's own other titles. The
   * revision is the document's, which the preparation must match; the title's own
   * revision exists only once the server has accepted its measured facts. */
  disc?: Readonly<{versionId: string; revision: string; titleId: string; titleRevision?: string}>}>;

export type NativeVersionNames = Readonly<{
  picture: (video: VideoStream) => string;
  audio: (audio: AudioStream) => string;
  size: (bytes: number | undefined) => string | undefined;
}>;
export function nativeVersionNames(t: I18n['t'], locale: string): NativeVersionNames {
  const words = videoTrackWords(t, locale);
  return {picture: video => videoPictureName(video, words), audio: audio => videoAudioFormatName(audio, words), size: bytes => formatVideoBytes(bytes, locale)};
}

/** Native-only §21/§23 row composition. Options order is also File info's
 * authoritative group order. No ordinal, container, bitrate or selection tags.
 * Menu eligibility (>=2) is separate from failure exclusion (possibly one). */
export function nativeVersionChoices(options: PlaybackOptions, itemId: string, names: NativeVersionNames, failedVersionId?: string): readonly NativeVersionRow[] {
  if (options.itemId !== itemId || new Set(options.versions.map(version => version.id)).size !== options.versions.length) return [];
  return options.versions.filter(version => version.id !== failedVersionId).map(version => {
    const picture = version.video[0] ? names.picture(version.video[0]) : '';
    const edition = version.edition?.trim() ? version.edition : undefined;
    // Options preserves the file's stream-index order. A viewer's preferred
    // language/track and globalPreferred never describe another file's default.
    const audio = version.audio.find(track => track.default) ?? version.audio[0];
    return Object.freeze({id: version.id, heading: edition ?? picture,
      detail: [edition ? picture : undefined, audio ? names.audio(audio) : undefined, names.size(version.sizeBytes)].filter(Boolean).join(', ')});
  });
}

/** A disc's own other titles, listed in Versions under their physical version as
 * frames 16 and 18 draw them: the title by its number, its chapters and its length,
 * and the opaque title id. Every stored title is listed, analyzed or not: choosing
 * one is how the server is asked to analyze it, so hiding the unanalyzed ones would
 * make that preparation unreachable. The main title is the version row itself. */
export function nativeDiscTitleChoices(drawn: DiscTitles, versionId: string, t: I18n['t']): readonly NativeVersionRow[] {
  if (drawn.versionId !== versionId) return [];
  return drawn.titles.filter(title => !title.main).map(title => Object.freeze({
    id: `${versionId}:${title.id}`, heading: t('next.overlays.discTitle', {number: title.number}),
    // A title the server has not analyzed has no accepted chapter facts yet: the main
    // sheet's own rule, so the row says Preparing there instead of claiming zero.
    detail: [title.ready ? t('next.overlays.discChapters', {count: title.chapterCount}) : t('next.browse.native.markAllPreparing'),
      durationText(Math.round(title.durationMs / 1000), t)].join(' · '),
    disc: Object.freeze({versionId, revision: drawn.revision, titleId: title.id,
      ...(title.revision === undefined ? {} : {titleRevision: title.revision})}),
  }));
}
