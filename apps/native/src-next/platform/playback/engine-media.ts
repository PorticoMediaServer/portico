import type {PlaybackSession} from '../../../../../packages/client-core/src/index';
import type {Session} from '../../../../../packages/client-core/src/playback-v1/types';
import type {SubtitlePlan, SubtitleResource} from '../../../../../packages/client-core/src/subtitles';
import type {PlaybackChoices} from '../../../../../packages/client-core/src/playback-v1/choices';

/**
 * What crosses between the player screen and an engine that does more than the platform's own
 * player (ENGINE-CONTRACT.md): the engine the plan names, the subtitle track the engine is to
 * draw itself, and what the engine found in the file.
 *
 * The server decides how a chosen subtitle track reaches the screen and says so on the session's
 * presentation (`subtitle.delivery`): `sidecar` (plain cues the app draws), `device` (the engine
 * draws the track itself, from the original file listed beside it or from the file it is
 * playing) or `burn` (in the picture). The selection plan (`SubtitlePlan`) names the same track
 * with its cue document and its deliveries.
 */
export type SubtitleKind = 'text' | 'ass' | 'pgs' | 'vobsub' | 'dvb';
const kinds: readonly SubtitleKind[] = ['text', 'ass', 'pgs', 'vobsub', 'dvb'];
/** Richest first: when a track can be had several ways, the one that keeps most of it. */
const preference: readonly SubtitleKind[] = ['ass', 'pgs', 'vobsub', 'dvb', 'text'];
/** A subtitle file's address as the server gives it (the shape `subtitles.ts` in the shared package accepts): the cue document, an original, a styled track's font list, or an image track's line count. */
const originalPath = /^\/v1\/media\/subtitles\/[A-Za-z0-9_-]+\/[A-Za-z0-9_-]+\/[1-9]\d*(?:\.vtt|\/original\.(?:ass|ssa|sup|idx|sub|mks)|\/fonts|\/lines)?$/;

type SubtitleFileClock = Readonly<{timeDomain?: 'source-relative'; offsetMs?: number}>;
export type SubtitleDelivery = SubtitleFileClock & Readonly<{format: SubtitleKind; url?: string; companionUrl?: string; fontsUrl?: string; linesUrl?: string}>;
export type SubtitleFonts = Readonly<{key: string; fonts: readonly Readonly<{name: string; url: string}>[]}>;
export type SubtitleRender = SubtitleFileClock & Readonly<{
  kind: SubtitleKind;
  embedded?: Readonly<{language?: string; title?: string}>;
  url?: string;
  companionUrl?: string;
  fonts?: readonly Readonly<{name: string; url: string}>[];
}>;

/**
 * The engine the server's plan named for the session the service is playing, when the server's
 * account of that same presentation is in hand. `stale` is true when the server's account is of
 * another generation (a re-plan is on its way to the service): the engine in use then stays.
 */
export function planEngineOf(session: PlaybackSession | undefined, v1: Session | undefined): Readonly<{engine?: 'native' | 'mpv'; stale: boolean}> {
  if (!session || !v1 || v1.id !== session.id) return {stale: false};
  if (v1.presentation.generation !== session.generation) return {stale: true};
  return {engine: v1.presentation.engine, stale: false};
}

const path = (v: unknown) => (typeof v === 'string' && v.length <= 1024 && originalPath.test(v) ? v : undefined);

/** File timing is already the complete correction. Preserve it separately from
 * the selection plan's fallback offset; the engine chooses once, never adds. */
function fileClock(value: unknown): SubtitleFileClock {
  if (!value || typeof value !== 'object') return {};
  const clock = value as {timeDomain?: unknown; offsetMs?: unknown};
  return {
    ...(clock.timeDomain === 'source-relative' ? {timeDomain: 'source-relative' as const} : {}),
    ...(typeof clock.offsetMs === 'number' && Number.isFinite(clock.offsetMs) ? {offsetMs: clock.offsetMs} : {}),
  };
}

/** The ways the server offers to hand a track to the device (`deliveries`), kept only as addresses of this kind of file. */
export function deliveriesOf(resource: SubtitleResource | null | undefined): readonly SubtitleDelivery[] {
  const out: SubtitleDelivery[] = [];
  for (const d of (resource?.deliveries ?? []).slice(0, 8)) {
    if (!kinds.includes(d.format as SubtitleKind)) continue;
    out.push({format: d.format as SubtitleKind, ...(path(d.url) ? {url: path(d.url)} : {}), ...(path(d.companionUrl) ? {companionUrl: path(d.companionUrl)} : {}), ...(path(d.fontsUrl) ? {fontsUrl: path(d.fontsUrl)} : {}), ...(path(d.linesUrl) ? {linesUrl: path(d.linesUrl)} : {}), ...fileClock(d)});
  }
  return out;
}

/** The kind a track is in its original form, from its format. */
export function kindOfFormat(format: string | undefined): SubtitleKind {
  switch (format) {
    case 'ass': case 'ssa': return 'ass';
    case 'pgs': case 'sup': return 'pgs';
    case 'vobsub': case 'idx': return 'vobsub';
    case 'dvb': case 'mks': return 'dvb';
    default: return 'text';
  }
}

/**
 * The kind an engine that draws `rendering` itself would draw a track in: the richest kind both
 * the track and the engine have. Undefined when the track has nothing but plain text, or the
 * engine draws only text. The player uses it to say, before the viewer chooses, whether a track
 * will be drawn on the device or has to be burned into the picture.
 */
export function renderingToRequest(resource: SubtitleResource, rendering: readonly string[]): SubtitleKind | undefined {
  const offered = deliveriesOf(resource).map(d => d.format);
  return preference.find(kind => kind !== 'text' && rendering.includes(kind) && offered.includes(kind));
}

/** The original file the device is to draw for the chosen track: the one the presentation lists
 * for it, else the delivery of the selection plan that came with an address. */
function chosenOriginal(plan: SubtitlePlan | null | undefined, v1: Session | undefined): SubtitleDelivery | undefined {
  const shown = v1?.presentation.subtitle;
  if (shown && shown.delivery === 'device') {
    const files = v1!.presentation.subtitles.filter(s => s.trackId === shown.trackId && kindOfFormat(s.format) !== 'text');
    const file = preference.map(kind => files.find(s => kindOfFormat(s.format) === kind)).find(Boolean);
    if (file && path(file.url)) return {format: kindOfFormat(file.format), url: file.url, ...(path(file.companionUrl) ? {companionUrl: file.companionUrl} : {}), ...(path(file.fontsUrl) ? {fontsUrl: file.fontsUrl} : {}), ...(path(file.linesUrl) ? {linesUrl: file.linesUrl} : {}), ...fileClock(file)};
  }
  if (!plan || plan.mode !== 'track' || !plan.selected) return undefined;
  const deliveries = deliveriesOf(plan.selected).filter(d => !!d.url);
  return preference.map(kind => deliveries.find(d => d.format === kind)).find(Boolean);
}

/** External AVPlayer composition cannot carry app-drawn captions. Only the
 * actual selected track and complete current file correction may request burn.
 * The existing change wire is integer milliseconds: never round a fractional
 * file correction or invent one when the current plan is unavailable. */
export function externalSubtitlePreparation(plan: SubtitlePlan | null | undefined, session: Session):
  Readonly<{kind: 'carried'} | {kind: 'unavailable'} | {kind: 'burn'; choices: PlaybackChoices; sourceId: string; trackId: string}> {
  const shown = session.presentation.subtitle;
  if (!shown) return {kind: plan?.mode === 'track' ? 'unavailable' : 'carried'};
  if (shown.delivery === 'burn') return {kind: 'carried'};
  if (!['device', 'sidecar'].includes(shown.delivery) || !session.versionId || !plan || plan.sessionId !== session.id || plan.generation !== session.presentation.generation) return {kind: 'unavailable'};
  const resource = plan.mode === 'track' ? plan.selected : plan.resources.find(value => value.id === shown.trackId);
  if (!resource || !resource.enabled || resource.retired || resource.id !== shown.trackId || resource.sourceId !== plan.sourceId) return {kind: 'unavailable'};
  const original = chosenOriginal(plan, session);
  const text = session.presentation.subtitles.find(file => file.trackId === shown.trackId && kindOfFormat(file.format) === 'text');
  const fileOffset = original?.offsetMs ?? text?.offsetMs;
  const planOffset = /^-?(?:0|[1-9]\d*)$/.test(plan.offsetUs) ? Number(plan.offsetUs) / 1000 : NaN;
  const offsetMs = fileOffset ?? planOffset;
  if (!Number.isSafeInteger(offsetMs)) return {kind: 'unavailable'};
  return {kind: 'burn', sourceId: plan.sourceId, trackId: shown.trackId,
    choices: {subtitleTrackId: shown.trackId, subtitleDelivery: 'burn', subtitleOffsetMs: offsetMs}};
}

/**
 * The kind the device itself must draw for the chosen track: set when the server delivers the
 * track for the device to draw in a kind richer than plain text. The engine in use must then be
 * one that draws that kind.
 */
export function requiredSubtitleRendering(plan: SubtitlePlan | null | undefined, v1?: Session): SubtitleKind | undefined {
  const shown = v1?.presentation.subtitle;
  if (shown && shown.delivery === 'device' && shown.kind !== 'text' && shown.kind !== 'other') return shown.kind;
  if (!plan || plan.mode !== 'track' || !plan.selected || plan.renderer !== 'client') return undefined;
  const kind = chosenOriginal(plan, undefined)?.format;
  return kind && kind !== 'text' ? kind : undefined;
}

/** The address of the list of fonts attached to the chosen styled track, when it has one. */
export function fontsUrlOf(plan: SubtitlePlan | null | undefined, v1?: Session): string | undefined {
  const original = chosenOriginal(plan, v1);
  return original?.format === 'ass' ? original.fontsUrl : undefined;
}

/** Where the chosen image track's forced-line count is read (PGS, VobSub), when the server offers it. */
export function linesUrlOf(plan: SubtitlePlan | null | undefined, v1?: Session): string | undefined {
  const original = chosenOriginal(plan, v1);
  return original?.format === 'pgs' || original?.format === 'vobsub' ? original.linesUrl : undefined;
}

/** A font list answer (`{fonts: [{name, url}]}`), kept only as addresses under the list's own. */
export function parseSubtitleFonts(raw: unknown, listUrl: string): SubtitleFonts {
  const list = raw && typeof raw === 'object' && Array.isArray((raw as {fonts?: unknown}).fonts) ? ((raw as {fonts: unknown[]}).fonts) : [];
  const fonts: {name: string; url: string}[] = [];
  for (const entry of list.slice(0, 16)) {
    const f = entry as {name?: unknown; url?: unknown} | null;
    if (!f || typeof f.name !== 'string' || f.name.length > 160 || typeof f.url !== 'string' || !f.url.startsWith(listUrl + '/') || !/^\d+$/.test(f.url.slice(listUrl.length + 1))) continue;
    fonts.push({name: f.name, url: f.url});
  }
  return Object.freeze({key: listUrl, fonts: Object.freeze(fonts)});
}

/** The track the device draws for this presentation although the selection plan is off: the
 * server turned its own copy off because the engine reads the track from the file it is playing. */
export function deviceDrawnTrack(plan: SubtitlePlan | null | undefined, v1: Session | undefined): Readonly<{id: string; kind: SubtitleKind; resource?: SubtitleResource}> | undefined {
  const shown = v1?.presentation.subtitle;
  if (!shown || shown.delivery !== 'device' || shown.kind === 'other') return undefined;
  if (plan && plan.mode === 'track' && plan.selected) return undefined;
  return {id: shown.trackId, kind: shown.kind, resource: plan?.resources.find(r => r.id === shown.trackId)};
}

/**
 * Where the chosen track is, for an engine that draws `rendering` itself (the command's
 * `subtitles.render`). Undefined when the engine should draw the server's plain cue document
 * instead, as the platform's own player always does.
 *
 * - The original subtitle file, when the server delivered one in a kind the engine draws.
 * - The track inside the media itself, when the file is played as it is and the track came from
 *   it: the engine then needs no second download, and a styled track keeps its attached fonts.
 * `fonts` is the fetched font list for a styled file that has one; while it is still being
 * fetched (`'pending'`) nothing is returned, so the track is added once, with its fonts.
 */
export function engineSubtitleRender(plan: SubtitlePlan | null, session: PlaybackSession, rendering: readonly string[], absolute: (path: string) => string, fonts?: SubtitleFonts | 'pending', v1?: Session): SubtitleRender | undefined {
  if (v1?.presentation.subtitle?.delivery === 'burn' || plan?.mode === 'track' && plan.renderer === 'burn_in') return undefined;
  const drawn = deviceDrawnTrack(plan, v1);
  const selected = plan && plan.mode === 'track' ? plan.selected : null;
  const track = selected ?? drawn?.resource;
  if (!selected && !drawn) return undefined;
  const original = chosenOriginal(plan, v1);
  const inFile = session.mode === 'direct' && (drawn ? !original : selected!.origin === 'embedded');
  const kind = original && rendering.includes(original.format) ? original.format : inFile ? (drawn?.kind ?? kindOfFormat(selected!.format)) : undefined;
  if (!kind || !rendering.includes(kind)) return undefined;
  const url = original && original.format === kind && original.url ? absolute(original.url) : undefined;
  if (!inFile && !url) return undefined;
  const wantsFonts = !!url && kind === 'ass' && !!original?.fontsUrl && !inFile;
  if (wantsFonts && fonts === 'pending') return undefined;
  const list = wantsFonts && fonts && fonts !== 'pending' && fonts.key === original!.fontsUrl ? fonts.fonts.map(f => ({name: f.name, url: absolute(f.url)})) : [];
  return Object.freeze({
    kind,
    ...(inFile ? {embedded: Object.freeze({...(track?.language ? {language: track.language} : {}), ...(track?.title ? {title: track.title} : {})})} : {}),
    ...(url ? {url} : {}),
    ...(url && original?.companionUrl ? {companionUrl: absolute(original.companionUrl)} : {}),
    ...fileClock(original),
    ...(list.length ? {fonts: Object.freeze(list)} : {}),
  });
}

/** What an engine reported finding in the file (its `mediaInfo` fact). */
export type EngineMedia = Readonly<{
  intentId: number;
  chapters: readonly PlayerChapter[];
  video?: Readonly<{width?: number; height?: number; framesPerSecond?: number; codec?: string; dynamicRange?: string; outputDynamicRange?: string; hardwareDecoding?: boolean}>;
}>;

/** A chapter of the playing title: where it starts, what it is called, and its picture when the server has one. */
export type PlayerChapter = Readonly<{startSeconds: number; title: string; thumbnail?: string}>;

/** A chapter list from an engine fact, the server's playback options or a download, bounded and in order. */
export function chaptersOf(raw: unknown): readonly PlayerChapter[] {
  if (!Array.isArray(raw)) return [];
  const out: {startSeconds: number; title: string; thumbnail?: string}[] = [];
  for (const entry of raw.slice(0, 500)) {
    const c = entry as {startSeconds?: unknown; startMs?: unknown; title?: unknown; thumbnail?: unknown} | null;
    const start = typeof c?.startSeconds === 'number' ? c.startSeconds : typeof c?.startMs === 'number' ? c.startMs / 1000 : NaN;
    if (!Number.isFinite(start) || start < 0) continue;
    out.push({startSeconds: start, title: typeof c?.title === 'string' ? c.title.slice(0, 256) : '', ...(typeof c?.thumbnail === 'string' && /^\/v1\//.test(c.thumbnail) && c.thumbnail.length <= 1024 ? {thumbnail: c.thumbnail} : {})});
  }
  return out.sort((a, b) => a.startSeconds - b.startSeconds);
}

/** The chapter a position falls in, when the title has chapters. */
export function chapterAt(chapters: readonly PlayerChapter[], seconds: number): PlayerChapter | undefined {
  let found: PlayerChapter | undefined;
  for (const chapter of chapters) { if (chapter.startSeconds <= seconds + 0.001) found = chapter; else break; }
  return found;
}
