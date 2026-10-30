import {sameLanguage} from './language.ts';
/**
 * MU4 COMPAT-01: bitmap (image) subtitle helpers. Pure and platform-free.
 *
 * A bitmap track (Blu-ray PGS, DVD VobSub, DVB) cannot be drawn as text: the
 * server burns it into the picture, which converts the whole video. The
 * Subtitles menu marks such tracks and confirms them on 4K/HDR sources; when
 * a language has both a text and a bitmap track, the text track wins.
 */
export type BitmapTrackLike = Readonly<{format?: unknown; renderer?: unknown; language?: unknown}>;

/** Bitmap subtitle formats, as the track lists spell them (lowercase). */
const BITMAP_FORMATS = Object.freeze(['pgs', 'sup', 'vobsub', 'idx', 'dvb', 'dvbsub'] as const);

/** Whether a subtitle format is image-based (case-insensitive, fail-closed). */
export function isBitmapSubtitleFormat(format: unknown): boolean {
  return typeof format === 'string' && (BITMAP_FORMATS as readonly string[]).includes(format.trim().toLowerCase());
}

/** Whether a subtitle track burns in: a bitmap format, or a burn_in renderer. */
export function isBitmapResource(track: BitmapTrackLike | null | undefined): boolean {
  if (!track || typeof track !== 'object') return false;
  if (track.renderer === 'burn_in') return true;
  return isBitmapSubtitleFormat(track.format);
}

/** Whether a track is a text track (rendered as text, never burned in). */
export function isTextResource(track: BitmapTrackLike | null | undefined): boolean {
  if (!track || typeof track !== 'object') return false;
  if (track.renderer === 'burn_in' || isBitmapSubtitleFormat(track.format)) return false;
  return typeof track.format === 'string' && track.format.trim() !== '';
}


/**
 * The text track for `language` when one exists (the automatic preference when
 * a language carries both a text and a bitmap track). Undefined when there is
 * none, so the caller's explicit choice stands.
 */
export function textTrackForLanguage<T extends BitmapTrackLike>(resources: readonly T[] | null | undefined, language: unknown): T | undefined {
  if (!Array.isArray(resources) || typeof language !== 'string' || !language.trim()) return undefined;
  return resources.find(r => r && sameLanguage(r.language, language) && isTextResource(r));
}

export type SourceFacts = Readonly<{is4k: boolean; isHdr: boolean}>;
const none: SourceFacts = Object.freeze({is4k: false, isHdr: false});

/** 4K or HDR from already-loaded source facts (width/height/hdr), fail-open to false. */
export function sourceIs4kOrHdr(facts: Readonly<{width?: unknown; height?: unknown; hdr?: unknown}> | null | undefined): boolean {
  if (!facts || typeof facts !== 'object') return false;
  const width = typeof facts.width === 'number' ? facts.width : 0;
  const height = typeof facts.height === 'number' ? facts.height : 0;
  if (height >= 2100 || width >= 3800) return true;
  const hdr = typeof facts.hdr === 'string' ? facts.hdr.trim().toLowerCase() : '';
  return hdr !== '' && hdr !== 'sdr' && hdr !== 'none';
}

/**
 * 4K/HDR source facts from a raw `playback-options` answer (M27 already reads
 * one per play; this parses more out of the same answer, never a new request).
 * Any video stream at 4K size or carrying an HDR marker counts. Fail-open.
 */
export function sourceFactsOf(raw: unknown): SourceFacts {
  try {
    if (!raw || typeof raw !== 'object') return none;
    const versions = (raw as {versions?: unknown}).versions;
    if (!Array.isArray(versions)) return none;
    for (const v of versions) {
      if (!v || typeof v !== 'object') continue;
      const video = (v as {video?: unknown}).video;
      if (!Array.isArray(video)) continue;
      for (const s of video) {
        if (!s || typeof s !== 'object') continue;
        const stream = s as {width?: unknown; height?: unknown; hdr?: unknown};
        if (sourceIs4kOrHdr(stream)) return Object.freeze({is4k: true, isHdr: typeof stream.hdr === 'string' && stream.hdr.trim() !== '' && stream.hdr.trim().toLowerCase() !== 'sdr'});
      }
    }
  } catch { /* fail open */ }
  return none;
}

/** Whether choosing `track` on this source asks once per play (bitmap on 4K/HDR, not yet acknowledged). */
export function bitmapWarningNeeded(track: BitmapTrackLike | null | undefined, facts: SourceFacts | null | undefined, acknowledged: boolean): boolean {
  return !acknowledged && !!facts && (facts.is4k || facts.isHdr) && isBitmapResource(track);
}

/**
 * ARCH-API-08: what the server owner turned off for playback. `GET /v1/capabilities` reports
 * `transcoding` and `subtitle_burn_in` as `disabled_by_owner` from the one owner switch
 * (transcoding); a play's offers carry the same switch as `transcodingEnabled`, so the player
 * reads it there (no extra request). Burning in subtitles converts the video, so it follows
 * transcoding. Never used for a platform gap: only the owner's switch hides or disables a control.
 */
export type OwnerPlaybackSwitches = Readonly<{transcoding: boolean; subtitleBurnIn: boolean}>;
export function ownerPlaybackSwitches(source: Readonly<{transcodingEnabled?: unknown; features?: unknown}> | null | undefined): OwnerPlaybackSwitches {
  const features = source && typeof source.features === 'object' && source.features ? source.features as Record<string, unknown> : undefined;
  const transcodingOff = source?.transcodingEnabled === false || features?.transcoding === 'disabled_by_owner';
  const burnInOff = transcodingOff || features?.subtitle_burn_in === 'disabled_by_owner';
  return Object.freeze({transcoding: !transcodingOff, subtitleBurnIn: !burnInOff});
}
