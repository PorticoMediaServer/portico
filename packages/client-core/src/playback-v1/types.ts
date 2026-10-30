import type {ListAdminSessionsResponse, StartPlaybackSessionRequest} from '../../../contracts/src/types.ts';
import {parseDashboardSessionFacts} from '../admin/activity-summary/parser.ts';
import type {BrowseNode, BrowseSortSelection} from '../browse.ts';
import {validateSubtitleDelivery} from '../subtitles.ts';
import {apiRoot} from '../../../contracts/src/routes.ts';
import {parseObservedPlayback, type ObservedPlayback} from './observed-playback.ts';
import {parsePlaybackInfo, type PlaybackInfo} from './playback-info.ts';
import {parseDiscMediaFacts, type DiscMediaFacts} from '../disc-media-facts.ts';
import {capacityChangedEventType, parseCapacityChangedEvent} from '../account-capacity.ts';
/**
 * Playback v1 resource shapes and tolerant readers (style guide amendment 3): unknown fields are
 * ignored, unknown open-enum values map to a fallback, absent arrays read as empty. Strict only
 * where the contract-drift policy requires it: ids, revisions, generations and URLs.
 * These are hand-written until lane C's generated `packages/contracts` lands (plan §2); the
 * readers then become thin adapters over the generated decoders.
 */
export class ContractError extends Error {
  constructor(what: string) { super(`Unreadable playback response: ${what}`); this.name = 'ContractError'; }
}

import {parseAudioRenderV2, playableAudio, type AudioRenderV2} from './audio-render.ts';
import type {QualityInForce, QualityOption} from './quality.ts';

type Obj = Record<string, unknown>;
export const obj = (v: unknown): v is Obj => !!v && typeof v === 'object' && !Array.isArray(v);
const idRe = /^[A-Za-z0-9_.-]{1,128}$/;
export function id(v: unknown, what: string): string { if (typeof v !== 'string' || !idRe.test(v)) throw new ContractError(what); return v; }
const optId = (v: unknown, what: string) => (v === undefined ? undefined : id(v, what));
export function revision(v: unknown, what = 'revision'): string { if (typeof v !== 'string' || !v || v.length > 256) throw new ContractError(what); return v; }
/** Public identity of one measured title inside its separately named physical version. */
export type DiscTitleRef = Readonly<NonNullable<StartPlaybackSessionRequest['discTitle']>>;
export function parseDiscTitleRef(value: unknown): DiscTitleRef | undefined {
  if (value === undefined) return undefined;
  if (!obj(value) || typeof value.id !== 'string' || !/^[a-f0-9]{64}$/.test(value.id)
    || typeof value.revision !== 'string' || !/^[a-f0-9]{64}$/.test(value.revision)) throw new ContractError('disc title');
  return Object.freeze({id: value.id, revision: value.revision});
}
const str = (v: unknown, fallback = '') => (typeof v === 'string' ? v : fallback);
const num = (v: unknown, fallback = 0) => (typeof v === 'number' && Number.isFinite(v) ? v : fallback);
const optNum = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);
const bool = (v: unknown) => v === true;
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
function oneOf<T extends string>(v: unknown, values: readonly T[], fallback: T): T { return (values as readonly unknown[]).includes(v) ? v as T : fallback; }
/** A relative `/v1/…` path or an absolute http(s) URL. */
export function url(v: unknown, what: string): string {
  if (typeof v !== 'string' || v.length > 4096 || !(/^\/v1\//.test(v) || /^https?:\/\//.test(v))) throw new ContractError(what);
  return v;
}

// ── Options (spec §4.1) ─────────────────────────────────────────────
export type VideoStream = Readonly<{
  id: string; codec: string; profile?: string; level?: number;
  bitDepth?: number; width?: number; height?: number; fps?: number; hdr?: string; bitrateKbps?: number;
  interlaced?: boolean; hdr10Plus?: boolean; chromaSubsampling?: string;
  frameRateNumerator?: number; frameRateDenominator?: number; frameRateMode?: string;
  dolbyVisionProfile?: number; dolbyVisionCompatibility?: number;
  dolbyVisionRpu?: boolean; dolbyVisionBaseLayer?: boolean;
  dolbyVisionEnhancementType?: string; dolbyVisionCmVersion?: string;
  masterDisplay?: string; maxCll?: string;
}>;
export type AudioStream = Readonly<{profile?: string; variant?: string; objectAudio?: string; dtsx?: boolean; sampleRate?: number; bitDepth?: number; id: string; codec: string; channels?: number; layout?: string; atmos: boolean; language?: string; title?: string; default: boolean; commentary: boolean; visualImpaired: boolean}>;
export type SubtitleStream = Readonly<{id: string; format: string; source: 'embedded' | 'sidecar' | 'downloaded' | 'other'; language?: string; title?: string; forced: boolean; sdh: boolean; default: boolean; deliveries?: readonly import('../subtitles.ts').SubtitleDelivery[]}>;
export type VersionPart = Readonly<{id: string; index: number; durationMs: number; sizeBytes?: number}>;
export type MediaVersion = Readonly<{id: string; label: string; edition?: string; container?: string; disc?: DiscMediaFacts; discTitle?: DiscTitleRef; sizeBytes?: number; durationMs: number; bitrateKbps?: number; parts: readonly VersionPart[]; video: readonly VideoStream[]; audio: readonly AudioStream[]; subtitles: readonly SubtitleStream[]}>;
/** A skippable segment (spec §4.2). `id` is what a skip report names; the viewer's `auto`
 * preference applies only when `automaticSafe`. */
export type Marker = Readonly<{id: string; type: 'intro' | 'recap' | 'credits' | 'preview' | 'commercial' | 'other'; startMs: number; endMs: number; confidence: 'detected' | 'manual' | 'embedded' | 'other'; automaticSafe: boolean}>;
export type Chapter = Readonly<{startMs: number; title: string; thumbnail?: string}>;
export type Trickplay = Readonly<{url: string; intervalMs: number; tileWidth: number; tileHeight: number; columns: number; rows: number}>;
/** What happens to one stream: played as it is, copied into a stream, converted, burned into the
 * picture; for a subtitle also `sidecar` (a text file beside the stream, drawn by the app) and
 * `device` (a styled or image track the engine draws itself). */
export type StreamAction = 'direct' | 'copy' | 'transcode' | 'burn' | 'sidecar' | 'device' | 'drop' | 'other';
export type PlanStream = Readonly<{id: string; action: StreamAction; to?: string; reasons: readonly string[]}>;
/** The player engine a plan names: the platform's own player, or mpv. */
export type PlaybackEngine = 'native' | 'mpv';
export type PlaybackPlan = Readonly<{versionId: string; mode: 'direct' | 'stream'; engine: PlaybackEngine; streams: readonly PlanStream[]; estimatedBitrateKbps?: number; quality: QualityInForce}>;
export type PlaybackOptions = Readonly<{
  itemId: string; kind: string;
  resume?: Readonly<{positionMs: number; partIndex: number}>;
  alternatives?: readonly Readonly<{versionId: string; label: string}>[];
  collection?: Readonly<{id: string; title: string; next: Readonly<{id: string; title: string; year?: number; genres: readonly string[]; backdropUrl?: string}>}>;
  versions: readonly MediaVersion[]; chapters: readonly Chapter[]; markers: readonly Marker[]; trickplay?: Trickplay;
  /** The quality menu for the preferred (or previewed) version: the original, automatic, then the ladder steps no taller than its picture. */
  qualities: readonly QualityOption[];
  /** What a start that names nothing would play: the version this device plays best, the audio
   * track (`reason`: `profile_language` or `default`; the viewer's Settings decide, never an earlier
   * playback) and the saved quality for this network. */
  preferred?: Readonly<{versionId: string; audioId?: string; reason: string; quality: QualityInForce}>;
  plan?: PlaybackPlan;
}>;

const qualityModes = ['original', 'cd-quality', 'fixed', 'automatic', 'limit'] as const;
/** A quality in force, tolerant: anything unreadable is the original. */
export function parseQuality(v: unknown): QualityInForce {
  if (!obj(v)) return Object.freeze({mode: 'original'});
  return Object.freeze({mode: oneOf(v.mode, qualityModes, 'original'), ...(typeof v.id === 'string' && v.id ? {id: v.id} : {}), ...(optNum(v.maxVideoBitrateKbps) ? {maxVideoBitrateKbps: num(v.maxVideoBitrateKbps)} : {}), ...(optNum(v.maxHeight) ? {maxHeight: num(v.maxHeight)} : {}), ...(optNum(v.maxAudioBitrateKbps) ? {maxAudioBitrateKbps: num(v.maxAudioBitrateKbps)} : {})});
}
const engineOf = (v: unknown): PlaybackEngine => (v === 'mpv' ? 'mpv' : 'native');
const streamActions = ['direct', 'copy', 'transcode', 'burn', 'sidecar', 'device', 'drop'] as const;

// These are source facts already present in the canonical Go Options DTO.
// Keeping them never grants a decoder, Dolby profile, HDR display or route.
const sourceInteger = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
function videoSourceFacts(s: Obj): Partial<VideoStream> {
  return {
    ...(sourceInteger(s.level) ? {level: s.level} : {}),
    ...(typeof s.chromaSubsampling === 'string' ? {chromaSubsampling: s.chromaSubsampling} : {}),
    ...(sourceInteger(s.frameRateNumerator) && s.frameRateNumerator > 0 ? {frameRateNumerator: s.frameRateNumerator} : {}),
    ...(sourceInteger(s.frameRateDenominator) && s.frameRateDenominator > 0 ? {frameRateDenominator: s.frameRateDenominator} : {}),
    ...(typeof s.frameRateMode === 'string' ? {frameRateMode: s.frameRateMode} : {}),
    ...(sourceInteger(s.dolbyVisionProfile) && s.dolbyVisionProfile <= 255 ? {dolbyVisionProfile: s.dolbyVisionProfile} : {}),
    ...(sourceInteger(s.dolbyVisionCompatibility) && s.dolbyVisionCompatibility <= 15 ? {dolbyVisionCompatibility: s.dolbyVisionCompatibility} : {}),
    ...(typeof s.dolbyVisionRpu === 'boolean' ? {dolbyVisionRpu: s.dolbyVisionRpu} : {}),
    ...(typeof s.dolbyVisionBaseLayer === 'boolean' ? {dolbyVisionBaseLayer: s.dolbyVisionBaseLayer} : {}),
    ...(typeof s.dolbyVisionEnhancementType === 'string' ? {dolbyVisionEnhancementType: s.dolbyVisionEnhancementType} : {}),
    ...(typeof s.dolbyVisionCmVersion === 'string' ? {dolbyVisionCmVersion: s.dolbyVisionCmVersion} : {}),
    ...(typeof s.masterDisplay === 'string' ? {masterDisplay: s.masterDisplay} : {}),
    ...(typeof s.maxCll === 'string' ? {maxCll: s.maxCll} : {}),
  };
}

export function parseOptions(v: unknown): PlaybackOptions {
  if (!obj(v)) throw new ContractError('playback options');
  const versions = arr(v.versions).map((x, i): MediaVersion => {
    if (!obj(x)) throw new ContractError(`version ${i}`);
    const disc = parseDiscMediaFacts(x.disc);
    const discTitle = parseDiscTitleRef(x.discTitle);
    return Object.freeze({
      id: id(x.id, 'version id'), label: str(x.label), edition: typeof x.edition === 'string' ? x.edition : undefined, container: typeof x.container === 'string' ? x.container : undefined,
      ...(disc ? {disc} : {}),
      ...(discTitle ? {discTitle} : {}),
      sizeBytes: optNum(x.sizeBytes), durationMs: num(x.durationMs), bitrateKbps: optNum(x.bitrateKbps),
      parts: Object.freeze(arr(x.parts).filter(obj).map(p => Object.freeze({id: id(p.id, 'part id'), index: num(p.index), durationMs: num(p.durationMs), sizeBytes: optNum(p.sizeBytes)}))),
      video: Object.freeze(arr(x.video).filter(obj).map(s => Object.freeze({...videoSourceFacts(s), id: id(s.id, 'video id'), codec: str(s.codec), profile: typeof s.profile === 'string' ? s.profile : undefined, ...(typeof s.interlaced === 'boolean' ? {interlaced: s.interlaced} : {}), ...(typeof s.hdr10Plus === 'boolean' ? {hdr10Plus: s.hdr10Plus} : {}), bitDepth: optNum(s.bitDepth), width: optNum(s.width), height: optNum(s.height), fps: optNum(s.fps), hdr: typeof s.hdr === 'string' ? s.hdr : undefined, bitrateKbps: optNum(s.bitrateKbps)}))),
      audio: Object.freeze(arr(x.audio).filter(obj).map(s => Object.freeze({id: id(s.id, 'audio id'), codec: str(s.codec), ...(typeof s.profile === 'string' ? {profile: s.profile} : {}), channels: optNum(s.channels), layout: typeof s.layout === 'string' ? s.layout : undefined,
        sampleRate: Number.isSafeInteger(s.sampleRate) && Number(s.sampleRate) > 0 ? s.sampleRate as number : undefined, bitDepth: Number.isSafeInteger(s.bitDepth) && Number(s.bitDepth) > 0 ? s.bitDepth as number : undefined,
        variant: typeof s.variant === 'string' ? s.variant : undefined, objectAudio: typeof s.objectAudio === 'string' ? s.objectAudio : undefined, dtsx: typeof s.dtsx === 'boolean' ? s.dtsx : undefined,
        atmos: bool(s.atmos), language: typeof s.language === 'string' ? s.language : undefined, title: typeof s.title === 'string' ? s.title : undefined, default: bool(s.default), commentary: bool(s.commentary), visualImpaired: bool(s.visualImpaired)}))),
      subtitles: Object.freeze(arr(x.subtitles).filter(obj).map(s => Object.freeze({id: id(s.id, 'subtitle id'), format: str(s.format), source: oneOf(s.source, ['embedded', 'sidecar', 'downloaded'] as const, 'other' as never), language: typeof s.language === 'string' ? s.language : undefined, title: typeof s.title === 'string' ? s.title : undefined, forced: bool(s.forced), sdh: bool(s.sdh), default: bool(s.default), ...(s.deliveries===undefined?{}:{deliveries:Object.freeze(arr(s.deliveries).map(validateSubtitleDelivery))})}))),
    });
  });
  const plan = obj(v.plan) ? Object.freeze({
    versionId: id(v.plan.versionId, 'plan version'), mode: oneOf(v.plan.mode, ['direct', 'stream'] as const, 'stream'), engine: engineOf(v.plan.engine),
    streams: Object.freeze(arr(v.plan.streams).filter(obj).map(s => Object.freeze({id: str(s.id), action: oneOf(s.action, streamActions, 'other' as never), to: typeof s.to === 'string' && s.to ? s.to : undefined, reasons: Object.freeze(arr(s.reasons).filter((r): r is string => typeof r === 'string'))}))),
    estimatedBitrateKbps: optNum(v.plan.estimatedBitrateKbps), quality: parseQuality(v.plan.quality),
  }) : undefined;
  const t = obj(v.trickplay) ? v.trickplay : undefined;
  return Object.freeze({
    itemId: id(v.itemId, 'item id'), kind: str(v.kind, 'other'),
    resume: obj(v.resume) ? Object.freeze({positionMs: num(v.resume.positionMs), partIndex: num(v.resume.partIndex)}) : undefined,
    versions: Object.freeze(versions),
    collection: obj(v.collection) && obj(v.collection.next) ? Object.freeze({id: id(v.collection.id, 'collection id'), title: str(v.collection.title), next: Object.freeze({id: id(v.collection.next.id, 'collection successor'), title: str(v.collection.next.title), year: optNum(v.collection.next.year), genres: Object.freeze(arr(v.collection.next.genres).filter((g): g is string => typeof g==='string')), ...(artworkPath(v.collection.next.backdropUrl)?{backdropUrl:v.collection.next.backdropUrl as string}:{})})}) : undefined,
    alternatives: Object.freeze(arr(v.alternatives).filter(obj).map(a => Object.freeze({versionId: id(a.versionId, 'alternative version'), label: str(a.label)}))),
    chapters: Object.freeze(arr(v.chapters).filter(obj).map(c => Object.freeze({startMs: num(c.startMs), title: str(c.title), thumbnail: typeof c.thumbnail === 'string' ? c.thumbnail : undefined}))),
    markers: Object.freeze(arr(v.markers).filter(obj).map(m => Object.freeze({id: str(m.id), type: oneOf(m.type, ['intro', 'recap', 'credits', 'preview', 'commercial'] as const, 'other' as never), startMs: num(m.startMs), endMs: num(m.endMs), confidence: oneOf(m.confidence, ['detected', 'manual', 'embedded'] as const, 'other' as never), automaticSafe: bool(m.automaticSafe)}))),
    trickplay: t ? Object.freeze({url: url(t.url, 'trickplay url'), intervalMs: num(t.intervalMs), tileWidth: num(t.tileWidth), tileHeight: num(t.tileHeight), columns: num(t.columns), rows: num(t.rows)}) : undefined,
    qualities: Object.freeze(arr(v.qualities).filter(obj).filter(q => q.mode === 'original' || q.mode === 'cd-quality' || q.mode === 'automatic' || q.mode === 'limit' && typeof q.maxAudioBitrateKbps === 'number' && Number.isFinite(q.maxAudioBitrateKbps) && q.maxAudioBitrateKbps > 0 || q.mode === 'fixed' && typeof q.id === 'string').map(q => Object.freeze({mode: q.mode as 'original' | 'cd-quality' | 'automatic' | 'fixed' | 'limit', ...(typeof q.id === 'string' && q.id ? {id: q.id as QualityOption['id']} : {}), sameAsOriginal: q.sameAsOriginal === true, applicable: q.applicable !== false, dataGBPerHour: optNum(q.dataGBPerHour), maxHeight: optNum(q.maxHeight), maxVideoBitrateKbps: optNum(q.maxVideoBitrateKbps), maxAudioBitrateKbps: optNum(q.maxAudioBitrateKbps)}))),
    preferred: obj(v.preferred) ? Object.freeze({versionId: id(v.preferred.versionId, 'preferred version'), audioId: optId(v.preferred.audioId, 'preferred audio'), reason: str(v.preferred.reason), quality: parseQuality(v.preferred.quality)}) : undefined,
    plan,
  });
}

// ── Sessions (spec §5) ──────────────────────────────────────────────
export type Decision = Readonly<{action: StreamAction; reasons: readonly string[]; to?: string; channels?: number}>;
/** The subtitle track a presentation shows and how it reaches the screen: `sidecar` (a WebVTT file
 * in `subtitles`, drawn by the app), `device` (the engine draws the track itself, from the original file listed in `subtitles` or
 * from the file it is playing) or `burn` (in the picture). `kind` is the capability name of its format. */
export type PresentationSubtitle = Readonly<{trackId: string; kind: 'text' | 'ass' | 'pgs' | 'vobsub' | 'dvb' | 'other'; delivery: 'sidecar' | 'device' | 'burn' | 'other'}>;
export type Presentation = Readonly<{
  choiceRevision?: number;
  generation: number; mode: 'direct' | 'stream'; url: string; startPositionMs: number;
  /** The latest accepted seek of a VOD session: a new id is a new seek, even to an equal target. */
  seekId?: string;
  /** The player engine the device must play this presentation with. */
  engine: PlaybackEngine;
  subtitles: readonly Readonly<{trackId: string; format: string; url: string; companionUrl?: string; fontsUrl?: string; linesUrl?: string; timeDomain?: 'source-relative'; offsetMs?: number}>[];
  decision: Readonly<{video?: Decision; audio?: Decision; subtitles?: Decision}>;
  bitrateKbps?: number;
  /** The quality in force (what was asked for, or the saved default for this network). */
  quality: QualityInForce;
  /** The audio track being played. */
  audioTrackId?: string;
  /** The subtitle track in force; absent when subtitles are off. */
  subtitle?: PresentationSubtitle;
  /** What the URL delivers: the file's own container for direct play, `fmp4_hls` or `mpegts_hls` for a stream. */
  container?: string;
  hdr?: Readonly<{source?: string; delivered?: string; toneMapped: boolean}>;
  /** The version 2 plan (spec §18.1): the client decodes the original file (or a FLAC/Opus
   * conversion) with the server's exact trim and gains. */
  audio?: AudioRenderV2;
}>;
export type SessionKind = 'vod' | 'audio' | 'live' | 'channel' | 'other';
/** What the session's controllers asked for (RN-09). The revision moves only on an accepted pause,
 * play or seek, never on the player's own reports, so a newer one is a command to apply once. */
export type SessionControl = Readonly<{revision: string; state: 'playing' | 'paused'}>;

export type Session = Readonly<{
  /** Absent for channels (their desired state is in the channel view) and ended sessions. */
  control?: SessionControl;
  id: string; revision: string; kind: SessionKind; role: 'local' | 'receiver' | 'group_member' | 'other';
  state: 'playing' | 'paused' | 'ended' | 'other'; itemId?: string; versionId?: string;
  discTitle?: DiscTitleRef;
  queue?: Readonly<{queueId: string; entryId: string}>;
  lease: Readonly<{expiresAt?: string; reportEveryMs: number}>;
  presentation: Presentation;
  /** Only the current renderer snapshot returned by the authorized server read. */
  observedPlayback?: ObservedPlayback;
  playbackInfo?: PlaybackInfo;
  /** Why an ended session ended (spec §14): "terminated" by an administrator with their message,
   * "stopped", "transferred", "lease_expired", "replaced"… Absent until it ends. */
  end?: SessionEnd;
}>;

/** Why the server ended a session, and what the viewer is told. */
export type SessionEnd = Readonly<{reason: string; message?: string}>;

/** A server end from a `session.updated` payload or a session's `end`; undefined if it has none. */
export function parseSessionEnd(v: unknown): SessionEnd | undefined {
  if (!obj(v) || typeof v.reason !== 'string' || v.reason === '') return undefined;
  return Object.freeze({reason: v.reason, message: typeof v.message === 'string' && v.message !== '' ? v.message : undefined});
}

function decision(v: unknown): Decision | undefined {
  if (!obj(v)) return undefined;
  return Object.freeze({action: oneOf(v.action, streamActions, 'other' as never), reasons: Object.freeze(arr(v.reasons).filter((r): r is string => typeof r === 'string')), to: typeof v.to === 'string' ? v.to : undefined, channels: optNum(v.channels)});
}

export function parseSession(v: unknown): Session {
  if (!obj(v) || !obj(v.presentation)) throw new ContractError('session');
  const discTitle = parseDiscTitleRef(v.discTitle);
  if (discTitle && (v.kind !== 'vod' || v.itemId === undefined || v.versionId === undefined)) throw new ContractError('disc title session');
  const presentation = parsePresentation(v.presentation);
  let observed: ObservedPlayback | undefined;
  let playbackInfo: PlaybackInfo | undefined;
  if (v.state !== 'ended') {
    try { observed = parseObservedPlayback(v.observedPlayback); } catch { /* Invalid optional facts never become output, or end valid playback. */ }
    if (v.kind === 'vod' || v.kind === 'audio' && playableAudio(presentation.audio)) { try { playbackInfo = parsePlaybackInfo(v.playbackInfo); } catch { /* Invalid comparison never replaces a valid session or renderer observation. */ } }
  }
  const control = obj(v.control) && typeof v.control.revision === 'string' && /^[1-9]\d{0,15}$/.test(v.control.revision) && (v.control.state === 'playing' || v.control.state === 'paused')
    ? Object.freeze({revision: v.control.revision, state: v.control.state}) : undefined;
  return Object.freeze({
    ...(control ? {control} : {}),
    ...(observed !== undefined ? {observedPlayback: observed} : {}),
    ...(playbackInfo !== undefined ? {playbackInfo} : {}),
    id: id(v.id, 'session id'), revision: revision(v.revision), kind: oneOf(v.kind, ['vod', 'audio', 'live', 'channel'] as const, 'other' as never),
    role: oneOf(v.role, ['local', 'receiver', 'group_member'] as const, 'other' as never), state: oneOf(v.state, ['playing', 'paused', 'ended'] as const, 'other' as never),
    itemId: optId(v.itemId, 'item id'), versionId: optId(v.versionId, 'version id'),
    ...(discTitle ? {discTitle} : {}),
    queue: obj(v.queue) ? Object.freeze({queueId: id(v.queue.queueId, 'queue id'), entryId: id(v.queue.entryId, 'entry id')}) : undefined,
    lease: Object.freeze({expiresAt: obj(v.lease) && typeof v.lease.expiresAt === 'string' ? v.lease.expiresAt : undefined, reportEveryMs: obj(v.lease) ? num(v.lease.reportEveryMs, 10_000) : 10_000}),
    presentation,
    end: parseSessionEnd(v.end),
  });
}

/** A presentation (§5.1). A prepared next entry's (§18.2) has no media URL until committed. */
export function parsePresentation(p: unknown, prepared = false): Presentation {
  if (!obj(p)) throw new ContractError('presentation');
  const generation = p.generation;
  if (typeof generation !== 'number' || !Number.isSafeInteger(generation) || generation < 0) throw new ContractError('generation');
  if (p.choiceRevision !== undefined && (typeof p.choiceRevision !== 'number' || !Number.isSafeInteger(p.choiceRevision) || p.choiceRevision < 1)) throw new ContractError('choice revision');
  const d = obj(p.decision) ? p.decision : {};
  return Object.freeze({
    ...(typeof p.seekId === 'string' && /^[A-Za-z0-9_-]{1,64}$/.test(p.seekId) ? {seekId: p.seekId} : {}),
    choiceRevision: typeof p.choiceRevision === 'number' && Number.isSafeInteger(p.choiceRevision) && p.choiceRevision > 0 ? p.choiceRevision : 1,
    generation, mode: oneOf(p.mode, ['direct', 'stream'] as const, 'stream'), url: prepared && (p.url === undefined || p.url === '') ? '' : url(p.url, 'presentation url'), startPositionMs: num(p.startPositionMs),
    engine: engineOf(p.engine), quality: parseQuality(p.quality),
    audioTrackId: typeof p.audioTrackId === 'string' && p.audioTrackId ? p.audioTrackId : undefined,
    subtitle: obj(p.subtitle) && typeof p.subtitle.trackId === 'string' && p.subtitle.trackId ? Object.freeze({trackId: p.subtitle.trackId, kind: oneOf(p.subtitle.kind, ['text', 'ass', 'pgs', 'vobsub', 'dvb'] as const, 'other' as never), delivery: oneOf(p.subtitle.delivery, ['sidecar', 'device', 'burn'] as const, 'other' as never)}) : undefined,
    container: typeof p.container === 'string' && p.container ? p.container : undefined,
    subtitles: Object.freeze(arr(p.subtitles).filter(obj).map(s => Object.freeze({trackId: id(s.trackId, 'subtitle track'), format: str(s.format), url: url(s.url, 'subtitle url'), ...(s.timeDomain === 'source-relative' ? {timeDomain: 'source-relative' as const} : {}), ...(typeof s.offsetMs === 'number' && Number.isFinite(s.offsetMs) ? {offsetMs: s.offsetMs} : {}), ...(s.companionUrl===undefined?{}:{companionUrl:url(s.companionUrl,'subtitle companion url')}), ...(s.fontsUrl===undefined?{}:{fontsUrl:url(s.fontsUrl,'subtitle fonts url')}), ...(s.linesUrl===undefined?{}:{linesUrl:url(s.linesUrl,'subtitle lines url')})}))),
    decision: Object.freeze({video: decision(d.video), audio: decision(d.audio), subtitles: decision(d.subtitles)}),
    bitrateKbps: optNum(p.bitrateKbps),
    hdr: obj(p.hdr) ? Object.freeze({source: typeof p.hdr.source === 'string' ? p.hdr.source : undefined, delivered: typeof p.hdr.delivered === 'string' ? p.hdr.delivered : undefined, toneMapped: bool(p.hdr.toneMapped)}) : undefined,
    audio: parseAudioRenderV2(p.audioRender),
  });
}

/** `:prepare-next`'s answer (spec §18.2). The plan is required: without one there is no edge. */
export function parsePreparedNext(v: unknown): PreparedNext {
  if (!obj(v) || typeof v.expiresAt !== 'string' || !Number.isFinite(Date.parse(v.expiresAt))) throw new ContractError('prepared next');
  const presentation = parsePresentation(v.presentation, true);
  if (!playableAudio(presentation.audio)) throw new ContractError('prepared next render plan');
  return Object.freeze({token: id(v.token, 'prepared token'), expiresAt: v.expiresAt, entryId: id(v.entryId, 'entry id'), itemId: id(v.itemId, 'item id'), presentation});
}

// ── Queues (spec §8, ARCH-MEDIA-03) ─────────────────────────────────
/** selectionBasis is an opaque server witness, independent of the visible page selectionRevision. */
export type QueueBrowseSelector = Readonly<{selectionRevision?: string; selectionBasis?: string; libraryId: string; pivot?: string; query?: BrowseNode; sort?: readonly BrowseSortSelection[]}>;
export type Selector =
  | Readonly<{container: Readonly<{kind: string; id: string}>}>
  | Readonly<{query: QueueBrowseSelector}>
  | Readonly<{saved: Readonly<{id: string}>}>
  | Readonly<{items: Readonly<{ids: readonly string[]}>}>
  | Readonly<{journey: Readonly<{from: string; to: string; length: number}>}>;
export type QueueSegment = Readonly<{id: string; kind: 'items' | 'selector' | 'radio' | 'other'; sourceRevision?: string; label: string; count: number; state: 'building' | 'ready' | 'failed' | 'other'}>;
export type QueueHeader = Readonly<{
  id: string; revision: string; total: number; remainingCount?: number; totalDurationMs?: number; durationKnown?: boolean; current?: Readonly<{entryId: string; position: number}>;
  repeat: 'off' | 'one' | 'all'; shuffle?: Readonly<{seed: string; lap: number}>; segments: readonly QueueSegment[];
  /** What plays after the current entry, through the viewer's fence (spec §18.5). */
  next?: Readonly<{entryId: string | null; available: boolean; reason: 'ready' | 'end' | 'unavailable'}>;
  /** The viewer's post-play policy for this queue (spec §18.5). */
  postPlay?: Readonly<{autoplay: boolean; countdownSeconds: number; passoutCheckDue: boolean; automaticAdvances: number; startDuringCredits: boolean}>;
}>;

/** A private preparation of the next entry's audio (spec §18.2): frame 0 only until committed. */
export type PreparedNext = Readonly<{token: string; expiresAt: string; entryId: string; itemId: string; presentation: Presentation}>;
export type QueueEntry = Readonly<{entryId: string; position: number; itemId: string; libraryId?: string; kind: string; title: string; durationMs?: number; available: boolean; segmentId?: string; album?: string; artistId?: string; artist?: string; showId?: string; seasonId?: string; episode?: Readonly<{seasonNumber: number; number: number}>; backdropUrl?: string;
  /** A song's album (a book file's book), with its disc and track (spec §18.5); absent when unknown. */
  albumId?: string; disc?: number; track?: number;
  /** The title's poster (inherited from its season, show, album or book) and an episode's own still, when they exist. */
  posterUrl?: string; stillUrl?: string}>;

/**
 * Whether `next` follows `current` on the same album, so the engine joins them gaplessly rather
 * than crossfading (Plexamp's behavior, Plan §9.6): the next track on the disc, or the first track
 * of the next disc. With track numbers unknown, being on the same album is enough.
 */
export function consecutiveOnAlbum(current: QueueEntry | undefined, next: QueueEntry | undefined): boolean {
  if (!current?.albumId || current.albumId !== next?.albumId) return false;
  if (!current.track || !next.track) return true;
  const disc = current.disc ?? 1, nextDisc = next.disc ?? 1;
  return nextDisc === disc && next.track === current.track + 1 || nextDisc === disc + 1 && next.track === 1;
}

export function parseQueue(v: unknown): QueueHeader {
  if (!obj(v)) throw new ContractError('queue');
  const total = v.total;
  if (typeof total !== 'number' || !Number.isSafeInteger(total) || total < 0) throw new ContractError('queue total');
  return Object.freeze({
    id: id(v.id, 'queue id'), revision: revision(v.revision), total, remainingCount: optNum(v.remainingCount), totalDurationMs: optNum(v.totalDurationMs), durationKnown: bool(v.durationKnown),
    current: obj(v.current) ? Object.freeze({entryId: id(v.current.entryId, 'current entry'), position: num(v.current.position)}) : undefined,
    repeat: oneOf(v.repeat, ['off', 'one', 'all'] as const, 'off'),
    shuffle: obj(v.shuffle) ? Object.freeze({seed: str(v.shuffle.seed), lap: num(v.shuffle.lap)}) : undefined,
    segments: Object.freeze(arr(v.segments).filter(obj).map(s => Object.freeze({id: id(s.id, 'segment id'), kind: oneOf(s.kind, ['items', 'selector', 'radio'] as const, 'other' as never), ...(typeof s.sourceRevision === 'string' ? {sourceRevision: s.sourceRevision} : {}), label: str(s.label), count: num(s.count), state: oneOf(s.state, ['building', 'ready', 'failed'] as const, 'other' as never)}))),
    next: obj(v.next) ? Object.freeze({entryId: typeof v.next.entryId === 'string' ? id(v.next.entryId, 'next entry') : null, available: v.next.available === true, reason: oneOf(v.next.reason, ['ready', 'end', 'unavailable'] as const, 'end')}) : undefined,
    postPlay: obj(v.postPlay) ? Object.freeze({autoplay: v.postPlay.autoplay !== false, countdownSeconds: num(v.postPlay.countdownMs, 10000) / 1000, passoutCheckDue: v.postPlay.passoutCheckDue === true, automaticAdvances: num(v.postPlay.automaticAdvances), startDuringCredits: v.postPlay.startDuringCredits === true}) : undefined,
  });
}

/** An artwork address on this server's API, as the queue window gives it. */
const artworkPath = (v: unknown): boolean => typeof v === 'string' && v.length <= 512 && v.startsWith(apiRoot) && !/[\s\\]/.test(v);

/** Non-wire observations retain whether the server actually supplied a fact.
 * Legacy QueueEntry defaults remain compatible but confer no editing authority. */
export type QueueEntryFacts = Readonly<{position?: number; available?: boolean}>;
const entryFacts = new WeakMap<QueueEntry, QueueEntryFacts>();
const unknownEntryFacts: QueueEntryFacts = Object.freeze({});
export function queueEntryFacts(entry: QueueEntry): QueueEntryFacts {return entryFacts.get(entry) ?? unknownEntryFacts;}

export function parseEntry(v: unknown): QueueEntry {
  if (!obj(v)) throw new ContractError('queue entry');
  const available = v.available !== false;
  // A placeholder carries no item: one that became unavailable, or (kind "pending") one still
  // being snapshotted in a large queue that fills in shortly (P16).
  const entry: QueueEntry = Object.freeze({entryId: id(v.entryId, 'entry id'), position: num(v.position), itemId: available ? id(v.itemId, 'item id') : typeof v.itemId === 'string' ? v.itemId : '', kind: str(v.kind, 'other'), title: str(v.title), durationMs: optNum(v.durationMs), available,
    ...(v.available === true && typeof v.libraryId === 'string' && v.libraryId ? {libraryId: id(v.libraryId, 'library id')} : {}),
    ...(typeof v.segmentId === 'string' ? {segmentId: id(v.segmentId, 'segment id')} : {}),
    ...(typeof v.album === 'string' ? {album: v.album} : {}), ...(typeof v.artist === 'string' ? {artist: v.artist} : {}),
    ...(typeof v.artistId === 'string' && v.artistId ? {artistId: id(v.artistId, 'artist id')} : {}),
    ...(typeof v.showId === 'string' && v.showId ? {showId: id(v.showId, 'show id')} : {}),
    ...(typeof v.seasonId === 'string' && v.seasonId ? {seasonId: id(v.seasonId, 'season id')} : {}),
    ...(obj(v.episode) ? {episode: Object.freeze({seasonNumber: num(v.episode.seasonNumber), number: num(v.episode.number)})} : {}),
    ...(artworkPath(v.backdropUrl) ? {backdropUrl: v.backdropUrl as string} : {}),
    ...(artworkPath(v.posterUrl) ? {posterUrl: v.posterUrl as string} : {}), ...(artworkPath(v.stillUrl) ? {stillUrl: v.stillUrl as string} : {}),
    ...(typeof v.albumId === 'string' && v.albumId ? {albumId: v.albumId} : {}), ...(optNum(v.disc) ? {disc: v.disc as number} : {}), ...(optNum(v.track) ? {track: v.track as number} : {})});
  entryFacts.set(entry, Object.freeze({
    ...(typeof v.position === 'number' && Number.isSafeInteger(v.position) && v.position >= 0 ? {position: v.position} : {}),
    ...(typeof v.available === 'boolean' ? {available: v.available} : {}),
  }));
  return entry;
}

/** The style guide's collection envelope (§4.5). */
export function parsePage<T>(v: unknown, item: (x: unknown) => T): Readonly<{items: readonly T[]; nextCursor?: string; total?: number; start?: number; revision?: string}> {
  if (!obj(v) || !Array.isArray(v.items)) throw new ContractError('collection');
  const page = obj(v.page) ? v.page : {};
  return Object.freeze({items: Object.freeze(v.items.map(item)), nextCursor: typeof page.nextCursor === 'string' ? page.nextCursor : undefined, total: optNum(page.total), start: optNum(page.start), revision: typeof page.revision === 'string' ? page.revision : undefined});
}

// ── Devices, commands, transfers (spec §9) ──────────────────────────
export type ControllableDevice = Readonly<{id: string; name: string; form: string; nowPlaying?: Readonly<{itemId?: string; title?: string; state?: string; positionMs?: number}>}>;
export function parseDevice(v: unknown): ControllableDevice {
  if (!obj(v)) throw new ContractError('device');
  const np = obj(v.nowPlaying) ? v.nowPlaying : undefined;
  return Object.freeze({id: id(v.id, 'device id'), name: str(v.name), form: str(v.form, 'other'), nowPlaying: np ? Object.freeze({itemId: typeof np.itemId === 'string' ? np.itemId : undefined, title: typeof np.title === 'string' ? np.title : undefined, state: typeof np.state === 'string' ? np.state : undefined, positionMs: optNum(np.positionMs)}) : undefined});
}

// ── Admin Now Playing (spec §14) ────────────────────────────────────
export type AdminSession = Readonly<{
 app?:string;platform?:string;address?:string;currentBitrateKbps?:number|null;rateWindowMs?:number;live?:boolean;
 media?:ListAdminSessionsResponse['items'][number]['media'];delivery?:ListAdminSessionsResponse['items'][number]['delivery'];item?:ListAdminSessionsResponse['items'][number]['item'];

  id: string; user: string; profile: string; device: string; itemId?: string; title: string; kind: SessionKind; state: string; positionMs: number;
  /** The length of what is playing, when the server knows it (never for a channel). */
  durationMs?: number;
  decision: Readonly<{video?: Decision; audio?: Decision; subtitles?: Decision}>; bitrateKbps?: number; bandwidthKbps?: number;
  /** The player engine the device is playing with and the quality in force (absent for a channel). */
  engine?: PlaybackEngine; quality?: QualityInForce;
  problem?:'buffering'|'behind';bufferingSince?:string;
  transcode?: Readonly<{speed?: number;observedAt?:string;cadenceMs?:number;hardware?: boolean; throttled?: boolean}>; location: 'local' | 'remote' | 'other'; startedAt?: string;
}>;
export function parseAdminSession(v: unknown): AdminSession {
  if (!obj(v)) throw new ContractError('admin session');
  const d = obj(v.decision) ? v.decision : {};
  const named = (x: unknown) => (obj(x) ? str(x.name) : str(x));
  return Object.freeze({
    ...parseDashboardSessionFacts(v),
 id: id(v.id, 'session id'), user: named(v.user), profile: named(v.profile), device: named(v.device),
    itemId: obj(v.item) && typeof v.item.id === 'string' ? v.item.id : typeof v.itemId === 'string' ? v.itemId : undefined,
    title: obj(v.item) ? str(v.item.title) : str(v.title),
    kind: oneOf(v.kind, ['vod', 'audio', 'live', 'channel'] as const, 'other' as never), state: str(v.state), positionMs: num(v.positionMs),
    ...(typeof v.durationMs === 'number' && Number.isSafeInteger(v.durationMs) && v.durationMs > 0 ? {durationMs: v.durationMs} : {}),
    decision: Object.freeze({video: decision(d.video), audio: decision(d.audio), subtitles: decision(d.subtitles)}),
    bitrateKbps: optNum(v.bitrateKbps), bandwidthKbps: optNum(v.bandwidthKbps),
    engine: typeof v.engine === 'string' && v.engine ? engineOf(v.engine) : undefined, quality: obj(v.quality) ? parseQuality(v.quality) : undefined,
    transcode: obj(v.transcode) ? Object.freeze({speed: optNum(v.transcode.speed),observedAt:typeof v.transcode.observedAt==='string'?v.transcode.observedAt:undefined,cadenceMs:optNum(v.transcode.cadenceMs), ...(typeof v.transcode.hardware==='boolean'?{hardware:v.transcode.hardware}:{}),...(typeof v.transcode.throttled==='boolean'?{throttled:v.transcode.throttled}:{})}) : undefined,
    problem: v.problem==='buffering' || v.problem==='behind'?v.problem:undefined,bufferingSince:typeof v.bufferingSince==='string'?v.bufferingSince:undefined,
    location: oneOf(v.location, ['local', 'remote'] as const, 'other' as never), startedAt: typeof v.startedAt === 'string' ? v.startedAt : undefined,
  });
}

// ── Events (style guide amendment 8; Public API ARCH-API-07) ────────
export type ServerEvent = Readonly<{id: string; type: string; at?: string; resource?: Readonly<{kind: string; id: string}>; revision?: string; data?: unknown}>;
export function parseEvent(v: unknown): ServerEvent | undefined {
  // Two variants of event exist. One this app does not know comes from a newer server and is skipped.
  if (obj(v) && v.variant === 'capacity') return parseCapacityChangedEvent(v);
  if (!obj(v) || v.variant !== 'change' || v.type === capacityChangedEventType || typeof v.id !== 'string' || !v.id || v.id.length > 128 || typeof v.type !== 'string' || !v.type) return undefined;
  return Object.freeze({id: v.id, type: v.type, at: typeof v.at === 'string' ? v.at : undefined, resource: obj(v.resource) && typeof v.resource.kind === 'string' && typeof v.resource.id === 'string' ? Object.freeze({kind: v.resource.kind, id: v.resource.id}) : undefined, revision: typeof v.revision === 'string' ? v.revision : undefined, data: v.data});
}
