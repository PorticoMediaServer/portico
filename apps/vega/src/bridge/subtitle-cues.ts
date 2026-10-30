import {defaultI18n} from '../../../../packages/i18n/src/index.ts';
/**
 * The subtitle command and the server's cue document, read strictly (ENGINE-CONTRACT.md §7).
 * Nothing here touches the device, so it runs under test as it is.
 */
export type CaptionBackground = 'none' | 'outline' | 'shadow' | 'translucent' | 'opaque';
export type CaptionColor = 'white' | 'yellow' | 'cyan' | 'green' | 'pink';
export type CaptionPosition = 'bottom' | 'raised' | 'top';
export type CaptionFont = 'default' | 'serif' | 'mono' | 'rounded';
/** The look the viewer chose, as the command carries it. `color`, `position` and `font`
 * are read here and drawn by the app's own line; the command's `size` and its
 * `keepStyling`/`useSystemCaptionStyle` are not carried, because this device has no
 * styled-file path and cannot read the viewer's system caption style. */
export type CaptionStyle = Readonly<{scale: number; background: CaptionBackground; color: CaptionColor; position: CaptionPosition; font: CaptionFont}>;
export const defaultCaptionStyle: CaptionStyle = Object.freeze({scale: 1, background: 'translucent', color: 'white', position: 'bottom', font: 'default'});

export type SubtitleCommand = Readonly<{
  binding?: unknown;
  mode?: string;
  revision?: number;
  resourceId?: string;
  resourceRevision?: number;
  refreshRevision?: number;
  /** A decimal string of microseconds, -600 s to +600 s; positive shows every line later. */
  offsetUs?: string;
  documentUrl?: string;
  style?: Readonly<{scale?: unknown; background?: unknown; color?: unknown; position?: unknown; font?: unknown}>;
}>;

export type Cue = Readonly<{start: number; end: number; text: string; italic: boolean; top: boolean}>;
export type CueList = Readonly<{cues: readonly Cue[]; /** The latest end among the cues up to and including each one. */ ends: readonly number[]}>;
export type SubtitleDisplay = Readonly<{text: string; italic: boolean; top: boolean}>;
export const NOTHING: SubtitleDisplay = Object.freeze({text: '', italic: false, top: false});

export const BYTE_LIMIT = 8 * 1024 * 1024;
const CUE_LIMIT = 20000, TEXT_LIMIT = 4194304, DAY_US = 86400000000, OFFSET_LIMIT_US = 600000000;
const route = /^\/v1\/media\/subtitles\/[A-Za-z0-9_-]{1,128}\/[A-Za-z0-9_-]{1,128}\/[1-9][0-9]*$/;

const positiveInteger = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value) && value >= 1 && value <= 9007199254740991 && Math.floor(value) === value;

/** A decimal string of microseconds in its one canonical spelling, within `maximum`; else null. */
export function decimalUs(value: unknown, maximum: number, signed: boolean): number | null {
  if (typeof value !== 'string' || !/^-?(0|[1-9][0-9]{0,17})$/.test(value) || value === '-0') return null;
  if (value.startsWith('-') && !signed) return null;
  const n = Number(value);
  return Math.abs(n) <= maximum ? n : null;
}

const backgrounds = ['none', 'outline', 'shadow', 'translucent', 'opaque'] as const;
const colors = ['white', 'yellow', 'cyan', 'green', 'pink'] as const;
const positions = ['bottom', 'raised', 'top'] as const;
const fonts = ['default', 'serif', 'mono', 'rounded'] as const;
/** The value when it is one of `allowed`, else `fallback`: one unrecognised field never changes another. */
const one = <T extends string>(allowed: readonly T[], value: unknown, fallback: T): T => allowed.includes(value as T) ? value as T : fallback;

/**
 * Portico's caption style from the command: a size multiplier (0.5 to 3), the background
 * the line is drawn on, its colour, where it sits and the face it asks for. Each field
 * falls back on its own, so an unknown value can neither be guessed at nor move another
 * field; an unrecognised background stays the soft box rather than becoming a bare line.
 */
export function captionStyleOf(style: SubtitleCommand['style']): CaptionStyle {
  const scale = typeof style?.scale === 'number' && Number.isFinite(style.scale) ? Math.max(0.5, Math.min(3, style.scale)) : 1;
  const background = one(backgrounds, style?.background, 'translucent');
  const color = one(colors, style?.color, 'white');
  const position = one(positions, style?.position, 'bottom');
  const font = one(fonts, style?.font, 'default');
  return scale === 1 && background === 'translucent' && color === 'white' && position === 'bottom' && font === 'default'
    ? defaultCaptionStyle
    : {scale, background, color, position, font};
}

export type Selection = Readonly<{url: string; revision: number; offsetUs: number; refreshRevision: unknown; binding: string; style: CaptionStyle}>;
export const INVALID_COMMAND = defaultI18n.t('playback.engine.invalidSubtitleCommand');
export const FOREIGN_ADDRESS = defaultI18n.t('playback.engine.foreignSubtitleAddress');

/**
 * The track a command names, or why it is refused. The cue document must be this playback
 * session's own: on the media's scheme, host and port, under the grant the media address carries,
 * at exactly the resource and revision the command names, with no credentials, query or fragment.
 * (This device has no downloads, so a `file:` address is never accepted.)
 */
export function selectionOf(command: SubtitleCommand, source: string): Selection | {error: string} {
  const offset = decimalUs(command.offsetUs, OFFSET_LIMIT_US, true);
  if (!positiveInteger(command.revision) || !positiveInteger(command.resourceRevision) || offset === null || typeof command.documentUrl !== 'string' || typeof command.resourceId !== 'string') return {error: INVALID_COMMAND};
  let target: URL, media: URL;
  try { target = new URL(command.documentUrl); media = new URL(source); } catch { return {error: FOREIGN_ADDRESS}; }
  const grant = media.pathname.split('/')[3] ?? '';
  const expected = `/v1/media/subtitles/${grant}/${command.resourceId}/${command.resourceRevision}`;
  const own = ['http:', 'https:'].includes(target.protocol) && target.protocol === media.protocol && target.host.toLowerCase() === media.host.toLowerCase() &&
    !target.username && !target.password && !target.search && !target.hash && !command.documentUrl.includes('#') && !command.documentUrl.includes('?') &&
    media.pathname.startsWith('/v1/media/') && target.pathname === expected && route.test(expected);
  if (!own) return {error: FOREIGN_ADDRESS};
  return {url: command.documentUrl, revision: command.revision, offsetUs: offset, refreshRevision: command.refreshRevision, binding: JSON.stringify(command.binding ?? null), style: captionStyleOf(command.style)};
}

const plain = (text: string): boolean => {
  if (!text || text.length > 8192 || text.includes('{\\')) return false;
  for (let i = 0; i < text.length; i++) { const c = text.charCodeAt(i); if ((c < 32 && c !== 9 && c !== 10) || c === 127) return false; }
  return true;
};
/** A cue time: a canonical decimal string of microseconds, or the same as a whole number. */
const time = (value: unknown): number | null => (typeof value === 'number' ? (Number.isInteger(value) && value >= 0 && value <= DAY_US ? value : null) : decimalUs(value, DAY_US, false));

/** The cue document. Strict about the envelope, forgiving about cues: one bad cue costs only itself. */
export function parseCueDocument(text: string): CueList | null {
  let document: {version?: unknown; timeDomain?: unknown; cues?: unknown};
  try { document = JSON.parse(text); } catch { return null; }
  if (!document || typeof document !== 'object' || document.version !== 1 || document.timeDomain !== 'source-relative' || !Array.isArray(document.cues) || document.cues.length > CUE_LIMIT) return null;
  const cues: Cue[] = [], ends: number[] = [];
  let previous = -1, held = 0, latest = 0;
  for (const cue of document.cues as {startUs?: unknown; endUs?: unknown; text?: unknown; italic?: unknown; top?: unknown}[]) {
    if (!cue || typeof cue !== 'object') continue;
    const start = time(cue.startUs), end = time(cue.endUs);
    if (start === null || end === null || end <= start || start < previous || typeof cue.text !== 'string' || !plain(cue.text)) continue;
    held += cue.text.length;
    if (held > TEXT_LIMIT) return null;
    previous = start;
    latest = Math.max(latest, end);
    cues.push({start, end, text: cue.text, italic: cue.italic === true, top: cue.top === true});
    ends.push(latest);
  }
  if (!cues.length && document.cues.length) return null;
  return {cues, ends};
}

/** What is on screen at `positionUs` of the media's own timeline, with the track's offset applied. */
export function displayAt(list: CueList | null, positionUs: number, offsetUs: number): SubtitleDisplay {
  if (!list) return NOTHING;
  const now = positionUs - offsetUs, {cues, ends} = list;
  // The last cue starting at or before now, then back over the ones still running.
  let low = 0, high = cues.length;
  while (low < high) { const middle = (low + high) >> 1; if (cues[middle].start <= now) low = middle + 1; else high = middle; }
  const active: string[] = [];
  let italic = false, top = false;
  for (let index = low - 1; index >= 0 && ends[index] > now; index--) {
    const cue = cues[index];
    if (cue.end > now) { active.unshift(cue.text); italic ||= cue.italic; top ||= cue.top; }
  }
  return active.length ? {text: active.join('\n'), italic, top} : NOTHING;
}
