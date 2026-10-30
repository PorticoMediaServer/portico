import {throwIfAborted} from './abort-lifetime.ts';
import {routePattern} from "../../contracts/src/routes.ts";
import {routePath} from "../../contracts/src/route-paths.ts";

/** The server's Chinese, Japanese and Korean font for a subtitle drawn on the device (the one burned-in subtitles use). Public; cached by its ETag. */
export const SUBTITLE_CJK_FONT_PATH = routePath("get_subtitle_fonts_cjk");
/**
 * Subtitles a device draws itself: reading what the server offers and fetching it.
 *
 * Beside the text every track can be delivered as, a styled track (ASS, SSA) can be delivered as
 * its author wrote it, with the fonts attached to the file, and an image track (Blu-ray PGS, DVD
 * VobSub, DVB) as its original pictures. A device that has a renderer for one of these asks for
 * it when it selects the track; the server then leaves the video alone instead of burning the
 * subtitle in, and the plan comes back with `renderer: "client"`.
 *
 * This file is the client side of that exchange for every app: which original a track offers
 * (from the `deliveries` of a subtitle resource or of a playback-options subtitle stream), which
 * one to ask for given what the device draws, and bounded reads of the files. Asking is done with
 * the selection itself: `subtitles: {trackId, delivery: "embeddedClient"}` on the session
 * (`PlaybackChoices.subtitleDelivery`).
 */
export type DrawnSubtitleFormat = 'ass' | 'pgs' | 'vobsub' | 'dvb';
export const drawnSubtitleFormats: readonly DrawnSubtitleFormat[] = Object.freeze(['ass', 'pgs', 'vobsub', 'dvb']);

/** One original as the server offers it. Addresses are server paths under the playback's media grant. */
/** `linesUrl` (PGS, VobSub): how many of the track's lines are forced, for "Forced only" (`parseSubtitleLines`). */
export type SubtitleOriginal = Readonly<{format: DrawnSubtitleFormat; url: string; companionUrl?: string; fontsUrl?: string; linesUrl?: string}>;

/** `GET …/lines`: an image subtitle's lines and how many are forced. */
export type SubtitleLines = Readonly<{forced: number; total: number}>;

/** Reads `GET …/lines`; undefined when malformed. */
export function parseSubtitleLines(v: unknown): SubtitleLines | undefined {
  if (!record(v)) return undefined;
  const forced = v.forced, total = v.total;
  if (typeof forced !== 'number' || typeof total !== 'number' || !Number.isSafeInteger(forced) || !Number.isSafeInteger(total) || forced < 0 || total < forced) return undefined;
  return Object.freeze({forced, total});
}

/** "Forced only" changes what is shown: some lines are forced and others are not (as the web's overlay decides). */
export function forcedChoiceOf(lines: SubtitleLines | undefined): boolean {
  return !!lines && lines.forced > 0 && lines.forced < lines.total;
}

const record = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const base = routePattern("get_v1_media_subtitles_grant_resource_revision")
  .replace("{grant}", "[A-Za-z0-9_-]{1,256}")
  .replace("{resource}", "[A-Za-z0-9_-]{1,128}")
  .replace("{revision}", "[1-9]\\d{0,17}");
const originalPath = new RegExp('^' + base + '/original\\.(?:ass|ssa|sup|idx|sub|mks)$');
const fontListPath = new RegExp('^' + base + '/fonts$');
const suffixes: Record<DrawnSubtitleFormat, readonly string[]> = {ass: ['ass', 'ssa'], pgs: ['sup'], vobsub: ['idx'], dvb: ['mks']};

/**
 * The originals a subtitle resource can be delivered as. Entries that are not understood (a
 * format from a newer server, an address that is not a media path) are left out, never trusted.
 */
export function subtitleOriginals(resource: unknown): readonly SubtitleOriginal[] {
  if (!record(resource) || !Array.isArray(resource.deliveries)) return [];
  const out: SubtitleOriginal[] = [];
  for (const entry of resource.deliveries.slice(0, 16)) {
    if (!record(entry) || typeof entry.format !== 'string' || typeof entry.url !== 'string') continue;
    const format = entry.format as DrawnSubtitleFormat;
    if (!drawnSubtitleFormats.includes(format) || !originalPath.test(entry.url) || !suffixes[format].some(suffix => (entry.url as string).endsWith('.' + suffix))) continue;
    const root = entry.url.slice(0, entry.url.lastIndexOf('/'));
    const companionUrl = format === 'vobsub' && entry.companionUrl === root + '/original.sub' ? entry.companionUrl : undefined;
    const fontsUrl = format === 'ass' && typeof entry.fontsUrl === 'string' && fontListPath.test(entry.fontsUrl) && entry.fontsUrl === root + '/fonts' ? entry.fontsUrl : undefined;
    const linesUrl = (format === 'pgs' || format === 'vobsub') && entry.linesUrl === root + '/lines' ? entry.linesUrl : undefined;
    // DVD subtitles are two files; without the second there is nothing to draw.
    if (format === 'vobsub' && !companionUrl) continue;
    out.push(Object.freeze({format, url: entry.url, ...(companionUrl ? {companionUrl} : {}), ...(fontsUrl ? {fontsUrl} : {}), ...(linesUrl ? {linesUrl} : {})}));
  }
  return Object.freeze(out);
}

/** The original to ask for when selecting this track on a device that draws `draws`; undefined means text (or burn-in). */
export function drawnRenderingFor(resource: unknown, draws: readonly string[]): DrawnSubtitleFormat | undefined {
  const original = subtitleOriginals(resource).find(original => draws.includes(original.format));
  if (original) return original.format;
  // Unselected tracks publish delivery kinds without media-grant addresses. Selection
  // pins the track and supplies its confined URLs; capability does not need a URL yet.
  if (!record(resource) || !Array.isArray(resource.deliveries)) return undefined;
  for (const delivery of resource.deliveries.slice(0, 16)) {
    if (!record(delivery) || delivery.url !== undefined || delivery.companionUrl !== undefined || delivery.fontsUrl !== undefined || delivery.linesUrl !== undefined) continue;
    const format = delivery.format as DrawnSubtitleFormat;
    if (drawnSubtitleFormats.includes(format) && draws.includes(format)) return format;
  }
  return undefined;
}

/** The original the device is to draw now: set when the plan says the device renders the selected track. */
export function selectedSubtitleOriginal(plan: unknown): SubtitleOriginal | null {
  if (!record(plan) || plan.renderer !== 'client' || plan.mode !== 'track' || !record(plan.selected)) return null;
  const selected = plan.selected;
  if (typeof selected.id !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(selected.id) || !Number.isSafeInteger(selected.revision) || Number(selected.revision) < 1) return null;
  // A syntactically valid media address for another resource/revision is not this selection.
  const suffix = '/' + selected.id + '/' + selected.revision + '/';
  return subtitleOriginals(selected).find(original => original.url.slice(0, original.url.lastIndexOf('/') + 1).endsWith(suffix)) ?? null;
}

export type SubtitleFile = Readonly<{bytes: Uint8Array; charset: string}>;
const refused = (status: number) => Object.assign(new Error('Subtitle access is no longer available.'), {status});
const unreadable = () => Object.assign(new Error('The subtitle could not be read.'), {code: 'invalid_subtitles'});

/**
 * One file of an original, read whole and never beyond `maxBytes`. No cookies are sent and the
 * browser cache is not used: the address carries the playback's grant and the server checks it on
 * every read. A refusal rejects with the HTTP `status`.
 */
export async function readSubtitleFile(fetcher: typeof fetch, url: string, maxBytes: number, signal: AbortSignal): Promise<SubtitleFile> {
  throwIfAborted(signal);
  if (!Number.isSafeInteger(maxBytes) || maxBytes < 1) throw unreadable();
  const response = await fetcher(url, {signal, credentials: 'omit', cache: 'no-store', redirect: 'error'});
  if (signal.aborted) { void response.body?.cancel().catch(() => {}); throwIfAborted(signal); }
  if (!response.ok) { void response.body?.cancel().catch(() => {}); throw refused(response.status); }
  const declared = response.headers.get('Content-Length');
  if (declared !== null && (!/^\d+$/.test(declared) || Number(declared) > maxBytes)) { void response.body?.cancel().catch(() => {}); throw unreadable(); }
  const charset = /;\s*charset=("?)([A-Za-z0-9._:-]{1,40})\1/i.exec(response.headers.get('Content-Type') ?? '')?.[2]?.toLowerCase() ?? '';
  if (!response.body) throw unreadable();
  const reader = response.body.getReader(), chunks: Uint8Array[] = [];
  let total = 0;
  const abort = () => { void reader.cancel().catch(() => {}); };
  signal.addEventListener('abort', abort, {once: true});
  try {
    throwIfAborted(signal);
    for (;;) {
      const {done, value} = await reader.read();
      throwIfAborted(signal);
      if (done) break;
      total += value.length;
      if (total > maxBytes) throw unreadable();
      chunks.push(value);
    }
    if (signal.aborted) throw new Error('Subtitle request cancelled.');
  } finally {
    signal.removeEventListener('abort', abort);
    void reader.cancel().catch(() => {});
    reader.releaseLock();
  }
  if (chunks.length === 1) return Object.freeze({bytes: chunks[0]!, charset});
  const bytes = new Uint8Array(total);
  let at = 0;
  for (const chunk of chunks) { bytes.set(chunk, at); at += chunk.length; }
  return Object.freeze({bytes, charset});
}

export type SubtitleFontFile = Readonly<{name: string; url: string}>;
/**
 * The fonts attached to a styled subtitle's file: names and addresses (server paths), at most 32.
 * `listUrl` is the address that was fetched and `listPath` the server path it stands for; every
 * font must be a numbered entry under that path.
 */
export async function readSubtitleFontList(fetcher: typeof fetch, listUrl: string, listPath: string, signal: AbortSignal): Promise<readonly SubtitleFontFile[]> {
  if (!fontListPath.test(listPath)) throw unreadable();
  const file = await readSubtitleFile(fetcher, listUrl, 65536, signal);
  let parsed: unknown;
  try { parsed = JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(file.bytes)); } catch { throw unreadable(); }
  if (!record(parsed) || !Array.isArray(parsed.fonts)) throw unreadable();
  const out: SubtitleFontFile[] = [];
  for (const font of parsed.fonts.slice(0, 32)) {
    if (!record(font) || typeof font.url !== 'string' || !font.url.startsWith(listPath + '/') || !/^\d{1,3}$/.test(font.url.slice(listPath.length + 1))) continue;
    out.push(Object.freeze({name: typeof font.name === 'string' ? font.name.slice(0, 160) : '', url: font.url}));
  }
  return Object.freeze(out);
}
