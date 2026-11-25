import {readPlaybackSubtitleFonts, readPlaybackSubtitleLines} from '../../../../../packages/client-core/src/query/playback';
import type {HttpLocalApi, PlaybackService} from '../../../../../packages/client-core/src/index';
import {presentedSidecarSubtitle} from '../../../../../packages/client-core/src/playback-v1/sidecar-subtitle';
import type {PlaybackOptions, Session} from '../../../../../packages/client-core/src/playback-v1/types';
import {forcedChoiceOf, parseSubtitleLines} from '../../../../../packages/client-core/src/subtitle-originals';
import type {SubtitleSnapshot} from '../../../../../packages/client-core/src/subtitles';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import {sameFence, type DockPlayback, type PlaybackFence, type PlayerSessionChoices} from '../../../../../packages/player-core/src';
import type {NativePlaybackCoordinator} from './NativePlaybackCoordinator';
import {directAudioStreamIndex, type NativeAudioTrackSelection} from './audio-track-selection';
import {captionStyle} from './caption-style';
import type {EngineFeatures, EngineKind} from './engine-choice';
import {deviceDrawnTrack, engineSubtitleRender, fontsUrlOf, linesUrlOf, parseSubtitleFonts, type SubtitleFonts} from './engine-media';
import {forcedImageLinesPath} from './forced-image';
import {automaticSteps, type NativePlayback} from './native-playback';
import type {NativeOfflineTracks} from './offline-tracks';
import type {NativeSubtitleStyleChoices} from './subtitle-style-choices';
import type {NativePlaybackSettings} from './types';

export type ForcedImageRead = {
  fence: PlaybackFence;
  path: string;
  epoch: number;
  controller: AbortController;
  mixed?: boolean;
};

export type NativeOptionsHost = Readonly<{
  retired: boolean;
  playback: PlaybackService;
  coordinator: NativePlaybackCoordinator;
  path: NativePlayback;
  choices: PlayerSessionChoices;
  subtitleStyles: NativeSubtitleStyleChoices;
  audioTrackSelection: NativeAudioTrackSelection;
  localTracks?: NativeOfflineTracks;
  options?: PlaybackOptions;
  subtitles?: SubtitleSnapshot;
  api: HttpLocalApi;
  settings: () => NativePlaybackSettings;
  engineFeatures: (engine: EngineKind) => EngineFeatures;
  hasMpvTransport: () => boolean;
  androidDisplayMatching?: true;
  nativeDynamicRangeMatching?: true;
  nativeLocalSubtitleTiming?: true;
  expanded: boolean;
  displaySubtitles: boolean;
  getSourceEpoch: () => number;
  sourceSignal?: AbortSignal;
  audioTrackInstance?: string;
  setAudioTrackInstance: (instance: string | undefined) => void;
  setChosenRate: (rate: number) => void;
  getFonts: () => SubtitleFonts | 'pending' | undefined;
  getFontsKey: () => string | undefined;
  setFonts: (key: string | undefined, fonts: SubtitleFonts | 'pending' | undefined) => void;
  getForcedImage: () => ForcedImageRead | undefined;
  setForcedImage: (read: ForcedImageRead | undefined) => void;
  fence: () => PlaybackFence | undefined;
  local: (fence: PlaybackFence) => DockPlayback;
  currentSession: () => Session | undefined;
  active: () => boolean;
  matches: (fence: PlaybackFence) => boolean;
  request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>;
  reapply: () => void;
  publish: () => void;
}>;

export function clearForcedImage(host: Pick<NativeOptionsHost, 'getForcedImage' | 'setForcedImage'>): void {
  host.getForcedImage()?.controller.abort();
  host.setForcedImage(undefined);
}

export function forcedImageAvailable(
  host: Pick<NativeOptionsHost, 'getForcedImage' | 'playback' | 'getSourceEpoch' | 'expanded' | 'displaySubtitles' | 'coordinator'>,
  fence: PlaybackFence | undefined,
): boolean {
  const read = host.getForcedImage(), phase = host.playback.getSnapshot().phase;
  return !!fence && fence.kind === 'queue' && !!read?.mixed && read.epoch === host.getSourceEpoch()
    && !read.controller.signal.aborted && sameFence(read.fence, fence) && host.expanded && host.displaySubtitles
    && host.coordinator.getEngine() === 'mpv' && (phase === 'ready' || phase === 'starting');
}

function readForcedImage(host: NativeOptionsHost, fence: PlaybackFence, path: string | undefined): void {
  const source = host.sourceSignal, previous = host.getForcedImage(), epoch = host.getSourceEpoch();
  if (!path || fence.kind !== 'queue' || !source || source.aborted) {clearForcedImage(host); return;}
  if (previous?.path === path && previous.epoch === epoch && sameFence(previous.fence, fence)) return;
  clearForcedImage(host);
  const read: ForcedImageRead = {fence, path, epoch, controller: new AbortController(), mixed: undefined};
  host.setForcedImage(read);
  const abort = () => read.controller.abort(); source.addEventListener('abort', abort, {once: true});
  const current = () => host.active() && host.getForcedImage() === read && read.epoch === host.getSourceEpoch()
    && !read.controller.signal.aborted && host.matches(fence);
  void readPlaybackSubtitleLines({request: host.request}, path, read.controller.signal).then(raw => {
    if (!current()) return;
    read.mixed = forcedChoiceOf(parseSubtitleLines(raw)); host.reapply(); host.publish();
  }).catch(() => {if (current()) {read.mixed = false; host.publish();}})
    .finally(() => source.removeEventListener('abort', abort));
}

export function applyPlaybackOptions(host: NativeOptionsHost): void {
  if (host.retired) return;
  const state = host.playback.getSnapshot(), fence = host.fence(), settings = host.settings();
  host.coordinator.setPassthroughPolicy({disabled: settings.overrides?.disablePassthrough === true || settings.overrides?.forceStereo === true, formats: settings.passthroughFormats ?? {}});
  host.coordinator.setDisplayMatching(host.androidDisplayMatching ? settings.matchFrameRate ?? 'auto' : undefined);
  host.coordinator.setDynamicRangeMatching(host.nativeDynamicRangeMatching ? settings.matchDynamicRange ?? 'auto' : undefined);
  const instance = host.coordinator.getInstanceId();
  if (instance !== host.audioTrackInstance) {host.setAudioTrackInstance(instance); host.audioTrackSelection.clear();}
  if (!state.session || !fence) {
    clearForcedImage(host);
    host.audioTrackSelection.clear();
    host.coordinator.setAudioTrack(null);
    host.coordinator.setLocalAudioTrack(null);
    host.coordinator.setOptions(1, null, null);
    return;
  }
  if (fence.kind !== 'local') host.coordinator.setLocalAudioTrack(null);
  if (fence.kind === 'channel') {
    clearForcedImage(host);
    host.audioTrackSelection.clear(); host.coordinator.setAudioTrack(null);
    host.coordinator.setOptions(1, null, {binding: {intentId: state.intentId, sessionId: state.session.id, generation: state.session.generation}, mode: 'off'});
    return;
  }
  const baseRate = host.local(fence).kind === 'audiobook' ? settings.audiobookRate : host.local(fence).kind === 'music' ? 1 : settings.videoRate;
  const rate = host.choices.get('speed', fence.playback, baseRate);
  host.setChosenRate(rate);
  const style = captionStyle(host.subtitleStyles.read(fence, {
    ...settings.subtitles,
    size: host.choices.get('subtitleSize', fence.playback, settings.subtitles.size),
    // The existing player offers three box overrides. Outline/shadow remain
    // genuine profile choices; do not narrow them to that older choice set.
    background: host.choices.has('subtitleBackground', fence.playback)
      ? host.choices.get('subtitleBackground', fence.playback, 'none') : settings.subtitles.background,
  }));
  if (fence.kind === 'local') {
    clearForcedImage(host);
    host.audioTrackSelection.clear();
    const engine: EngineKind = host.coordinator.getEngine() === 'mpv' ? 'mpv' : 'native', tracks = host.localTracks;
    // The local renderer validates every original, companion and font against the download.
    const rendering = host.engineFeatures(engine).subtitleRendering;
    const offeredRendering = settings.engine !== 'native' && host.hasMpvTransport()
      ? [...new Set([...rendering, ...host.engineFeatures('mpv').subtitleRendering])] : rendering;
    tracks?.configure(engine, host.coordinator.getInstanceId(), rendering, offeredRendering, engine === 'mpv' || host.nativeLocalSubtitleTiming === true);
    host.coordinator.setAudioTrack(tracks?.audioCommand() ?? null);
    host.coordinator.setLocalAudioTrack(tracks?.localAudioCommand() ?? null);
    host.coordinator.setOptions(rate, null, tracks?.subtitleCommand(style, host.displaySubtitles && host.expanded)
      ?? {binding: {intentId: state.intentId, sessionId: state.session.id, generation: state.session.generation}, mode: 'off'});
    return;
  }
  host.path.setRate(rate);
  const session = host.currentSession(), plan = host.subtitles?.plan;
  const binding = {intentId: state.intentId, sessionId: state.session.id, generation: state.session.generation};
  const audioIndex = directAudioStreamIndex(host.coordinator.getEngine(), session, host.options);
  host.coordinator.setAudioTrack(host.audioTrackSelection.select(binding, audioIndex));
  if (host.coordinator.getEngine() === 'mpv' && session?.kind === 'vod' && session.role === 'local' && session.presentation.mode === 'direct'
    && session.presentation.audioTrackId && audioIndex === undefined && (state.phase === 'starting' || state.phase === 'ready')) {
    host.playback.fail(state.intentId, serviceText('player.opt.unavailable')); return;
  }
  const audioTrack = host.audioTrackSelection.getSnapshot();
  host.path.setTracks(host.coordinator.getEngine() === 'mpv' && session?.presentation.mode === 'direct'
    ? audioTrack?.state === 'applied' ? session.presentation.audioTrackId : undefined : session?.presentation.audioTrackId,
  session?.presentation.subtitle?.trackId ?? null);
  let subtitles: Record<string, unknown> | null = null;
  let forcedPath: string | undefined;
  const visible = host.displaySubtitles && host.expanded;
  const burned = session?.presentation.subtitle?.delivery === 'burn' || plan?.mode === 'track' && plan.renderer === 'burn_in';
  const drawn = deviceDrawnTrack(plan, session), selected = plan?.mode === 'track' && !host.subtitles?.suppressed ? plan.selected : null;
  const listUrl = fontsUrlOf(plan, session);
  if (visible && listUrl && listUrl !== host.getFontsKey()) {
    host.setFonts(listUrl, 'pending'); const epoch = host.getSourceEpoch();
    void readPlaybackSubtitleFonts({request: host.request}, listUrl, host.sourceSignal).then(raw => {
      if (host.active() && epoch === host.getSourceEpoch() && host.getFontsKey() === listUrl) {host.setFonts(listUrl, parseSubtitleFonts(raw, listUrl)); host.reapply();}
    }).catch(() => {if (host.active() && epoch === host.getSourceEpoch() && host.getFontsKey() === listUrl) {host.setFonts(listUrl, parseSubtitleFonts(null, listUrl)); host.reapply();}});
  }
  if (plan) subtitles = {binding, mode: 'off', revision: plan.revision};
  if (visible && plan && !burned && (selected || drawn)) {
    // mediaUrl enforces the current selected origin/media grant. No raw URL concatenation.
    const engine: EngineKind = host.coordinator.getEngine() === 'mpv' ? 'mpv' : 'native';
    // A native engine may genuinely install the rich subtitle producer too.
    // Plain cue documents retain their existing platform selection path.
    const rendering = host.engineFeatures(engine).subtitleRendering.filter(kind => engine === 'mpv' || kind !== 'text');
    const render = engineSubtitleRender(plan, state.session, rendering, path => host.api.mediaUrl(path), host.getFonts(), session);
    if (selected && plan.documentUrl || render) {
      const resource = selected ?? drawn?.resource;
      if (engine === 'mpv') {
        // The service keeps a route-relative media path. Resolve it through
        // the same authority-local route used by the actual native producer.
        try {forcedPath = forcedImageLinesPath(host.api.mediaUrl(state.session.streamUrl), render, resource, linesUrlOf(plan, session));}
        catch { /* an unavailable route cannot grant an optional filter */ }
      }
      subtitles = {binding, mode: 'track', revision: plan.revision, resourceId: selected?.id ?? drawn?.id, resourceRevision: resource?.revision ?? 1,
        manifestName: resource?.manifestName, origin: resource?.origin ?? 'embedded', language: resource?.language ?? '', title: resource?.title ?? '', offsetUs: selected ? plan.offsetUs : '0',
        ...(selected && plan.documentUrl ? {documentUrl: host.api.mediaUrl(plan.documentUrl)} : {}), ...(render ? {render} : {}), refreshRevision: 0, style};
    }
  }
  // The rebuilt server keeps no subtitle plan: the session names the track in force and lists its
  // WebVTT file, which the engine reads like a planned document. A burned track is in the picture.
  const shown = !plan && session?.kind === 'vod' ? session.presentation.subtitle : undefined;
  if (session && shown && shown.delivery !== 'burn') {
    const file = presentedSidecarSubtitle(host.api, session.presentation);
    const offered = host.options?.versions.find(value => value.id === session.versionId)?.subtitles.find(value => value.id === shown.trackId);
    const revision = Math.max(1, session.presentation.generation);
    subtitles = visible && file ? {binding, mode: 'track', revision, resourceId: file.trackId, resourceRevision: file.fileRevision,
      origin: offered?.source === 'sidecar' ? 'sidecar' : 'embedded', language: offered?.language ?? '', title: offered?.title ?? '',
      offsetUs: String(Math.round(file.offsetMs * 1000)), documentUrl: file.documentUrl, refreshRevision: 0, style}
      : {binding, mode: 'off', revision};
  }
  readForcedImage(host, fence, forcedPath);
  if (subtitles && forcedImageAvailable(host, fence) && host.choices.get('forcedOnly', fence.playback, false)) subtitles = {...subtitles, forcedOnly: true};
  host.coordinator.setOptions(rate, null, subtitles);
  const version = host.options?.versions.find(value => value.id === session?.versionId);
  host.path.setAutomatic(session?.presentation.quality.mode === 'automatic' ? {steps: automaticSteps(host.options?.qualities ?? []), originalKbps: version?.bitrateKbps} : null);
}
