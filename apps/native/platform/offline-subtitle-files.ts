import type {DownloadBundleSubtitle} from '../../../packages/client-core/src/downloads';

/**
 * The subtitle files that travel with a download, as they are kept beside it and handed to the
 * player. The server lists each track's files by form (`files` in the bundle):
 *
 * - `vtt`: the track as plain text (every text track, and a styled one's plain reading);
 * - `ass`, `pgs`, `dvb`: the track as it was authored, for a player that draws it itself;
 * - `vobsub` and `sub`: a VobSub track's two files, the index and the pictures beside it;
 * - `font0` … `font15`: the fonts attached to a styled script.
 *
 * Every file of one track shares a stem (`sub-<id>-<revision>`), so the files that belong
 * together sit together and are found again from the track alone. A VobSub index is kept as
 * `.idx` with its pictures as `.sub`: the player opens the index and finds the pictures itself,
 * by putting `sub` in place of the index's three-letter extension (ENGINE-CONTRACT.md, section 7:
 * "the `.idx` address, the `.sub` beside it"). Under any longer extension the pictures are never
 * found and the track draws nothing.
 *
 * No React and no native code: tested under Node.
 */
const FONT = /^font(\d{1,2})$/;
/** The forms a track is offered in, closest to how it was authored first; plain text last. */
const PREFERENCE = ['ass', 'pgs', 'vobsub', 'dvb', 'vtt'] as const;
export const MAX_SUBTITLE_FONTS = 16;

const stem = (id: string, revision: number) => `sub-${id}-${revision}`;

/** The name one file of a track is kept under, in the download's own folder. */
export function subtitleExtraName(id: string, revision: number, form: string): string {
  return `${stem(id, revision)}.${form === 'vobsub' ? 'idx' : form}`;
}

/** Every file of a track to fetch: its kept name and the server's path for it. */
export function subtitleExtraFiles(track: DownloadBundleSubtitle): readonly (readonly [name: string, path: string])[] {
  return Object.entries(track.files).map(([form, file]) => [subtitleExtraName(track.id, track.revision, form), file.path] as const);
}

export type StoredSubtitle = Readonly<{
  id: string; language: string; title: string; default: boolean; forced: boolean;
  revision?: number;
  /** The form on this device that the track is played from: `ass`, `pgs`, `vobsub`, `dvb` or `vtt`. */
  format: string;
  /** The file the player opens: for a VobSub track its index (the pictures are beside it, under the same name ending `.sub`). */
  url: string;
  /** Plain reading saved beside a rich original, for an engine without that renderer. */
  textUrl?: string;
  /** A styled script's attached fonts that are on this device, by the names they are kept under. */
  fonts?: readonly Readonly<{name: string; url: string}>[];
  /** An image track's line count and how many are forced, as the server counted them. */
  lines?: Readonly<{forced: number; total: number}>;
}>;

/**
 * The track as it can be played from this device: the richest form whose files are all here.
 * A VobSub track counts only with both of its files; a styled script is offered as soon as the
 * script is here, with whichever of its fonts have arrived (the rest are fetched on a later pass).
 * Undefined when nothing of the track is on the device yet.
 */
export function storedSubtitle(track: DownloadBundleSubtitle, saved: ReadonlySet<string>, extrasDir: string): StoredSubtitle | undefined {
  if (!extrasDir) return undefined;
  const has = (form: string) => saved.has(subtitleExtraName(track.id, track.revision, form));
  const at = (form: string) => extrasDir + subtitleExtraName(track.id, track.revision, form);
  const form = PREFERENCE.find(candidate => (candidate === 'vobsub' ? has('vobsub') && has('sub') : has(candidate)));
  if (!form) return undefined;
  const fonts = form !== 'ass' ? [] : Object.keys(track.files)
    .map(name => ({name, index: Number(FONT.exec(name)?.[1] ?? NaN)}))
    .filter(font => Number.isInteger(font.index) && font.index < MAX_SUBTITLE_FONTS && has(font.name))
    .sort((a, b) => a.index - b.index)
    .map(font => Object.freeze({name: subtitleExtraName(track.id, track.revision, font.name), url: at(font.name)}));
  return Object.freeze({
    id: track.id, revision: track.revision, language: track.language, title: track.title, default: track.default, forced: track.forced, format: form, url: at(form),
    ...(form !== 'vtt' && has('vtt') ? {textUrl: at('vtt')} : {}),
    ...(fonts.length ? {fonts: Object.freeze(fonts)} : {}),
    ...(track.lines ? {lines: track.lines} : {}),
  });
}
