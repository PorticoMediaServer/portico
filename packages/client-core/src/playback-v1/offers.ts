import type {QualityRung} from '../delivery.ts';
import type {ContentScope} from '../library-content.ts';
import type {PlayerCurrentOffer, PlayerOfferControl, PlayerOfferedStream, PlayerOffersData, PlayerOffersQuery, PlayerSourceOffer} from '../player-offers.ts';
import {subtitleOriginals} from '../subtitle-originals.ts';
import type {SubtitleDelivery, SubtitleFormat, SubtitlePlan, SubtitleResource} from '../subtitles.ts';
import type {SegmentMarker, SegmentMarkerSet} from '../segment-markers.ts';
import {parseOptions, parseSession, type MediaVersion, type PlaybackOptions, type Session} from './types.ts';
import {playbackPath} from './routes.ts';

type Requester = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;

/** Formats the page draws as plain lines from the WebVTT the server makes of them. */
const textFormats: readonly string[] = ['srt', 'subrip', 'vtt', 'webvtt', 'ass', 'ssa', 'mov_text', 'text', 'smi', 'sami', 'ttml', 'dfxp'];
const subtitleFormat = (format: string): SubtitleFormat => (['srt', 'vtt', 'ass', 'ssa', 'pgs', 'sup', 'vobsub', 'idx', 'dvb', 'smi', 'sami', 'ttml', 'dfxp'].includes(format) ? format as SubtitleFormat : format === 'subrip' ? 'srt' : format === 'webvtt' ? 'vtt' : 'srt');

function qualityRungs(options: PlaybackOptions): readonly QualityRung[] {
  return Object.freeze(options.qualities.flatMap((q): QualityRung[] => {
    if (q.mode === 'original') return [{id: 'original', kind: 'original', label: 'Original', enabled: true}];
    if (q.mode === 'automatic') return [{id: 'automatic', kind: 'automatic', label: 'Automatic', enabled: true}];
    if (q.mode !== 'fixed' || !q.id) return [];
    return [{id: q.id, kind: 'fixed', label: q.id, enabled: true, ...(q.sameAsOriginal ? {sameAsOriginal: true} : {}),
      ...(q.dataGBPerHour ? {dataGBPerHour: q.dataGBPerHour} : {}), ...(q.maxVideoBitrateKbps ? {maxVideoBitrateBps: q.maxVideoBitrateKbps * 1000} : {}),
      ...(q.maxAudioBitrateKbps ? {maxAudioBitrateBps: q.maxAudioBitrateKbps * 1000} : {}), ...(q.maxHeight ? {targetDisplayHeight: q.maxHeight} : {})}];
  }).map(r => Object.freeze(r)));
}

function sourceOffer(version: MediaVersion, rungs: readonly QualityRung[]): PlayerSourceOffer {
  const video = version.video[0], audio = version.audio[0], known = version.video.length + version.audio.length > 0;
  // A track of the file is named by its stream index ("1"); a file beside it ("f3-0") gets a number past them.
  let beside = 100_000;
  const index = (id: string) => /^\d{1,6}$/.test(id) ? Number(id) : beside++;
  const streams: PlayerOfferedStream[] = [
    ...version.video.map((s): PlayerOfferedStream => ({index: index(s.id), type: 'video', codec: s.codec, default: true, forced: false, enabled: false, reason: 'track_selection_unavailable'})),
    ...version.audio.map((s): PlayerOfferedStream => ({index: index(s.id), type: 'audio', codec: s.codec, default: s.default, forced: false, enabled: true, reason: '',
      ...(s.language ? {language: s.language} : {}), ...(s.title ? {title: s.title} : {}), ...(s.channels ? {channels: s.channels} : {}), ...(s.layout ? {channelLayout: s.layout} : {})})),
    ...version.subtitles.map((s): PlayerOfferedStream => ({index: index(s.id), type: 'subtitle', codec: s.format, default: s.default, forced: s.forced, enabled: false, reason: 'subtitle_delivery_unavailable',
      ...(s.language ? {language: s.language} : {}), ...(s.title ? {title: s.title} : {})})),
  ];
  return Object.freeze({id: version.id, available: true, container: version.container ?? '', videoCodec: video?.codec ?? '', audioCodec: audio?.codec ?? '',
    width: video?.width ?? 0, height: video?.height ?? 0, duration: version.durationMs / 1000, factsRevision: known ? 1 : 0, factsStatus: known ? 'known' : 'unavailable',
    relation: 'unknown', boundary: 'whole_source', enabled: false, reason: 'source_selection_unavailable',
    streams: Object.freeze(known ? streams.map(s => Object.freeze(s)) : []), qualities: rungs});
}

/** The version's offered deliveries and the current session's grant-confined subtitle files. */
export function sessionSubtitlePlan(session: Session, version: MediaVersion): SubtitlePlan {
  const presented = session.presentation, shown = presented.subtitle;
  const files = presented.subtitles.filter(file => file.trackId === shown?.trackId);
  const selectedFile = files.find(file => file.format === shown?.kind || shown?.kind === 'ass' && file.format === 'ssa')
    ?? files.find(file => file.format === 'vtt');
  const offsetMs = selectedFile?.offsetMs ?? 0;
  const renderer: SubtitlePlan['renderer'] = shown?.delivery === 'burn' ? 'burn_in'
    : shown?.delivery === 'device' && shown.kind !== 'text' && shown.kind !== 'other' ? 'client' : 'external_text';
  const resources = version.subtitles.map((s): SubtitleResource => {
    const deliveries = new Map((s.deliveries ?? []).map(delivery => [delivery.format, delivery]));
    for (const file of presented.subtitles.filter(file => file.trackId === s.id)) {
      const format = file.format === 'ssa' ? 'ass' : file.format === 'vtt' ? 'text' : file.format;
      if (!['text', 'ass', 'pgs', 'vobsub', 'dvb'].includes(format)) continue;
      deliveries.set(format as SubtitleDelivery['format'], Object.freeze({format: format as SubtitleDelivery['format'], url: file.url,
        ...(file.companionUrl ? {companionUrl: file.companionUrl} : {}), ...(file.fontsUrl ? {fontsUrl: file.fontsUrl} : {}), ...(file.linesUrl ? {linesUrl: file.linesUrl} : {})}));
    }
    const enabled = textFormats.includes(s.format) || deliveries.size > 0;
    const resourceRenderer = s.id === shown?.trackId ? renderer
      : textFormats.includes(s.format) ? 'external_text' : deliveries.size > 0 ? 'client' : 'burn_in';
    return Object.freeze({id: s.id, sourceId: version.id, revision: 1, scope: 'shared', language: s.language ?? '', title: s.title ?? '',
      format: subtitleFormat(s.format), origin: s.source === 'sidecar' ? 'sidecar' : 'embedded', rights: '', offsetUs: String(Math.round(offsetMs * 1000)), canManage: false,
      enabled, ...(enabled ? {} : {reason: 'subtitle_delivery_unavailable'}), deliveries: Object.freeze([...deliveries.values()]),
      renderer: resourceRenderer, default: s.default, forced: s.forced, hearingImpaired: s.sdh});
  });
  const selected = resources.find(r => r.id === shown?.trackId && r.enabled) ?? null;
  const document = selected ? presented.subtitles.find(s => s.trackId === selected.id && s.format === 'vtt')?.url : undefined;
  const ready = selected && (renderer === 'burn_in' || (renderer === 'client'
    ? subtitleOriginals(selected).some(original => original.format === shown?.kind) : !!document));
  return Object.freeze({version: 1, sessionId: session.id, generation: presented.generation, sourceId: version.id, revision: presented.generation, catalogRevision: 1,
    renderer, mode: ready ? 'track' : 'off', offAvailable: true, offsetUs: String(Math.round(offsetMs * 1000)), selected: ready ? selected : null,
    ...(ready && document ? {documentUrl: document} : {}), resources: Object.freeze(resources), discovered: Object.freeze([])});
}

/** The skippable spans of the version playing (intro, recap, credits) as the player's skip prompts
 * and its Up next card read them. A kind the player has no prompt for is left out. */
export function segmentMarkers(options: PlaybackOptions, versionId: string, generation: number): SegmentMarkerSet {
  const kinds: Readonly<Record<string, SegmentMarker['kind']>> = {intro: 'intro', recap: 'recap', credits: 'credits', commercial: 'commercial', outro: 'outro'};
  const markers = options.markers.flatMap((m): SegmentMarker[] => kinds[m.type] && m.endMs > m.startMs && m.id
    ? [Object.freeze({id: m.id, kind: kinds[m.type]!, startSeconds: m.startMs / 1000, endSeconds: m.endMs / 1000, automaticSafe: m.automaticSafe})] : []);
  return Object.freeze({sourceId: versionId, revision: String(generation), markers: Object.freeze(markers.sort((a, b) => a.startSeconds - b.startSeconds))});
}

const qualityIdOf = (session: Session): string => {
  const q = session.presentation.quality;
  return q.mode === 'fixed' && q.id ? q.id : q.mode === 'automatic' ? 'automatic' : 'original';
};

/**
 * What the player screen offers for a title, and for the session playing it: its versions with
 * their tracks and quality menu, the session's way of playing, and its subtitles. Read from
 * `GET /v1/items/{id}/playback-options` and, while one plays, `GET /v1/playback/sessions/{id}`.
 */
export async function readPlayerOffers(api: Requester, q: PlayerOffersQuery, scope: ContentScope, signal?: AbortSignal): Promise<PlayerOffersData> {
  const session = q.sessionId ? parseSession(await api.request(playbackPath('GET /v1/playback/sessions/{id}', {id: q.sessionId}), 'GET', undefined, signal)) : undefined;
  const query = session?.versionId ? '?' + new URLSearchParams({versionId: session.versionId}) : '';
  const options = parseOptions(await api.request(playbackPath('GET /v1/items/{id}/playback-options', {id: q.itemId}) + query, 'GET', undefined, signal));
  const libraryId = q.libraryId ?? (await api.request<{libraryId?: unknown}>(playbackPath('GET /v1/items/{id}', {id: q.itemId}), 'GET', undefined, signal)).libraryId;
  if (typeof libraryId !== 'string' || !libraryId) throw Object.assign(new Error('This title is not in a library.'), {code: 'invalid_playback_offers'});
  const rungs = qualityRungs(options);
  const sources = Object.freeze(options.versions.map(version => sourceOffer(version, rungs)));
  const version = options.versions.find(v => v.id === session?.versionId);
  const playing = session && session.state !== 'ended' && version ? session : undefined;
  const plan = playing && version ? sessionSubtitlePlan(playing, version) : null;
  const current: PlayerCurrentOffer | null = playing ? Object.freeze({sessionId: playing.id, generation: playing.presentation.generation, state: playing.state,
    sourceId: version!.id, delivery: playing.presentation.mode === 'stream' ? 'hls' : 'direct', qualityId: qualityIdOf(playing),
    audioSelection: playing.presentation.mode === 'stream' ? 'server_first_audio' : 'platform_default', subtitleSelection: plan ? plan.mode : 'off'}) : null;
  const controls: readonly PlayerOfferControl[] = Object.freeze([
    Object.freeze({id: 'source', enabled: false, reason: 'source_selection_unavailable'}),
    Object.freeze({id: 'audio', enabled: true, reason: ''}),
    Object.freeze(plan ? {id: 'subtitles', enabled: true, reason: ''} : {id: 'subtitles', enabled: false, reason: 'subtitle_delivery_unavailable'}),
    Object.freeze(rungs.some(r => r.kind === 'fixed') ? {id: 'quality', enabled: true, reason: ''} : {id: 'quality', enabled: false, reason: 'quality_selection_unavailable'}),
  ] as PlayerOfferControl[]);
  const revision = `${q.itemId}-${playing?.presentation.generation ?? 0}`;
  return Object.freeze({preparedVersions: Object.freeze([]), preparedOffersRevision: '', subtitlePlan: plan, subtitlePlanUnavailableReason: plan ? null : 'subtitle_delivery_unavailable',
    scope: Object.freeze({serverId: scope.serverId, libraryId, itemId: q.itemId, viewerFence: 'profile'}), revision, sources, current, controls,
    markers: playing && version ? segmentMarkers(options, version.id, playing.presentation.generation) : null,
    deliveryPolicy: null, offersRevision: revision, transcodingEnabled: true});
}
