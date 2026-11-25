import {advancePendingChoice} from '@core/pending-track-choice.ts';
import type {PlaybackSnapshot} from '@core/index.ts';
import type {PlayerOffersData} from '@core/player-offers.ts';
import {searchItemSubtitles, downloadItemSubtitle, fetchSubtitleDocument, type SubtitleDocument, type SubtitlePlan, type SubtitleSnapshot} from '@core/subtitles.ts';
import type {SubtitlePreferences} from '@core/preference-effects.ts';
import {SUBTITLE_CJK_FONT_PATH, forcedChoiceOf, parseSubtitleLines, readSubtitleFile, readSubtitleFontList, selectedSubtitleOriginal, type SubtitleLines, type SubtitleOriginal} from '@core/subtitle-originals.ts';
import {sameFence, type PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import {wirelessPlayback} from './airplay.ts';
import {DrawnSubtitleRetry, revalidateTextSubtitles, subtitleFetch} from './subtitle-retry.ts';
import type {SubtitleSurface} from './subtitle-surface.ts';
import {createDrawnSubtitleSurface} from './subtitles/drawn-surface.ts';
import {decodeSubtitleText, subtitleFileLimits, type DrawnSubtitleSource, type ReadSubtitleFile} from './subtitles/types.ts';
import {assPlainCues} from './subtitles/ass-text.ts';
import type {VideoLike} from './web-media-player.ts';
import type {WebPlaybackEnvironment, WebPlaybackRuntimeSnapshot, WebPlaybackSource} from './types.ts';
import {emptySubtitle, problem, subtitleSearchFailure, type Stack, type WebSubtitleSearch} from './runtime-types.ts';

export type SubtitleRuntimeHost = Readonly<{
  stack(): Stack;
  video(): VideoLike | undefined;
  surface(): SubtitleSurface | undefined;
  subtitleTimeline(): number | null;
  source(): WebPlaybackSource;
  env(): WebPlaybackEnvironment;
  snapshot(): WebPlaybackRuntimeSnapshot;
  runtimeProblem(): string | undefined;
  setRuntimeProblem(code: string | undefined): void;
  current(stack?: Stack): boolean;
  matches(fence: PlaybackFence): boolean;
  require(fence: PlaybackFence): Stack;
  offers(stack: Stack, playback: PlaybackSnapshot): PlayerOffersData | null;
  playbackRun(): number;
  retainMediaStart(signal?: AbortSignal, isCurrent?: () => boolean): () => void;
  setTimer(fn: () => void, ms: number): unknown;
  clearTimer(timer: unknown): void;
  sync(): void;
  selectSubtitle(fence: PlaybackFence, trackId: string | null): Promise<void>;
  renderSubtitles(stack: Stack): void;
}>;

/** The chosen track's original, when the plan has this browser draw it (styled or image). */
export function drawnOriginal(plan: SubtitlePlan | null | undefined) {
  return plan?.mode === 'track' ? selectedSubtitleOriginal(plan) : null;
}

/** A media path as an address on this server; another origin is refused. */
function subtitleUrl(host: SubtitleRuntimeHost, path: string): string {
  const api = host.source().api;
  const url = api.mediaUrl(path);
  if (new URL(url).origin !== new URL(api.baseUrl).origin) problem('unexpected_subtitle_origin');
  return url;
}

/** What the overlay is given for an original. The script and pictures are read through the
 * load's own reader; fonts are addresses the styled renderer's worker fetches itself. */
async function drawnSource(host: SubtitleRuntimeHost, original: SubtitleOriginal, signal: AbortSignal): Promise<DrawnSubtitleSource> {
  if (original.format === 'vobsub') return {kind: 'vobsub', index: original.url, data: original.companionUrl ?? ''};
  if (original.format !== 'ass') return {kind: original.format, data: original.url};
  let fonts: string[] = [];
  if (original.fontsUrl) {
    try { fonts = (await readSubtitleFontList(subtitleFetch(host.env().fetch ?? fetch), subtitleUrl(host, original.fontsUrl), original.fontsUrl, signal)).map(font => subtitleUrl(host, font.url)); }
    // A track whose font list cannot be read is still drawn, in the fallback fonts.
    catch { signal.throwIfAborted(); }
  }
  return {kind: 'ass', script: original.url, fonts, cjkFont: subtitleUrl(host, SUBTITLE_CJK_FONT_PATH)};
}

export function subtitlePreferences(host: SubtitleRuntimeHost, stack: Stack): SubtitlePreferences | undefined {
  const saved = host.source().settings.subtitlePreferences?.();
  if (!saved) return undefined;
  const override = stack.subtitleStyle && stack.subtitleStyle.run === host.playbackRun() ? stack.subtitleStyle : undefined;
  if (!override) return saved;
  // An explicit size or background chosen in the player is drawn by this page, even where the
  // saved preference delegates to the system caption style; the saved value is not written.
  return {...saved, ...(override.size ? {size: override.size} : {}), ...(override.background ? {background: override.background} : {}), useSystemCaptionStyle: false};
}

/** The viewer's subtitle size for a drawn track, as a scale of the authored size. */
function drawnScale(host: SubtitleRuntimeHost, stack: Stack): number {
  const preferences = subtitlePreferences(host, stack);
  if (!preferences || preferences.useSystemCaptionStyle) return 1;
  return ({small: 0.9, medium: 1, large: 1.12, 'extra-large': 1.25} as Record<string, number>)[preferences.size] ?? 1;
}

/** Whether this run already failed to draw the track, so it is asked for in the picture. */
export function isUndrawable(host: SubtitleRuntimeHost, stack: Stack, trackId: string | null | undefined): boolean {
  return !!trackId && stack.undrawable?.run === host.playbackRun() && stack.undrawable.ids.has(trackId);
}

/** A drawn track that cannot be drawn here is asked for in the picture instead, once per track
 * in this playback run. A refusal (the owner turned conversions off) leaves the problem shown. */
function burnUndrawable(host: SubtitleRuntimeHost, stack: Stack, plan: SubtitlePlan): void {
  const id = plan.selected?.id, offsetMs = Number(plan.offsetUs) / 1000;
  if (!id || isUndrawable(host, stack, id)) return;
  const playback = stack.service.getSnapshot(), sessionId = playback.session?.id, intentId = playback.intentId;
  const run = host.playbackRun(), before = stack.undrawable?.run === run ? stack.undrawable.ids : [];
  stack.undrawable = Object.freeze({run, ids: new Set([...before, id])});
  const owns = () => host.current(stack) && host.playbackRun() === run && stack.service.getSnapshot().session?.id === sessionId && stack.service.getSnapshot().intentId === intentId;
  void stack.service.changePlayback({subtitleTrackId: id, subtitleDelivery: 'burn', ...(Number.isSafeInteger(offsetMs) ? {subtitleOffsetMs: offsetMs} : {subtitleOffsetUs: plan.offsetUs})})
    .then(() => { if (owns()) { if (host.runtimeProblem() === 'playback_subtitles_failed') host.setRuntimeProblem(undefined); host.sync(); } },
      () => { if (owns() && stack.service.getSnapshot().session?.generation === plan.generation) { host.setRuntimeProblem('playback_subtitles_failed'); host.sync(); } });
}

/** The selected addresses and the original load lifetime are checked before every read. */
function subtitleReader(host: SubtitleRuntimeHost, stack: Stack, life: AbortSignal, addresses: ReadonlySet<string>, current: () => boolean): ReadSubtitleFile {
  return async (address, maxBytes, signal) => {
    const joined = AbortSignal.any([signal, life, stack.life.signal]);
    joined.throwIfAborted();
    if (!current() || !addresses.has(address)) problem('playback_subtitle_unavailable');
    const file = await readSubtitleFile(subtitleFetch(host.env().fetch ?? fetch), subtitleUrl(host, address), maxBytes, joined);
    joined.throwIfAborted();
    if (!current()) throw new DOMException('Playback changed', 'AbortError');
    return file;
  };
}

/** Reads the chosen revision's own forced-line offer under the image load that can use it. A
 * stale, failed, cleared or replaced load never arms the switch. */
async function readForcedLines(host: SubtitleRuntimeHost, stack: Stack, linesUrl: string, key: string, epoch: number, life: AbortSignal, read: ReadSubtitleFile, current: () => boolean): Promise<void> {
  try {
    const file = await read(linesUrl, 8192, life);
    const lines = parseSubtitleLines(JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(file.bytes)));
    if (!lines || !current() || !forcedChoiceOf(lines)) return;
    stack.forcedLines = Object.freeze({lines, key, epoch});
    host.sync();
  } catch { /* no offer, no switch; the track still draws every line */ }
}

function subtitleState(host: SubtitleRuntimeHost, stack: Stack): SubtitleSnapshot {
  const offered = host.offers(stack, stack.service.getSnapshot())?.subtitlePlan;
  return stack.subtitles?.getSnapshot() ?? (offered ? {...emptySubtitle, plan: offered, suppressed: false} : emptySubtitle);
}

/** The player's own observation of the track this runtime draws: never inferred from a choice. */
function recordSidecar(stack: Stack, path: 'text' | 'drawn', key: string, id: string | null): void {
  stack.subtitleRecorded = Object.freeze({id, path, key});
  stack.web.slot.selectSidecarSubtitle(id);
}

/** Retiring one path clears the record only when that path still owns it, so a late drawn
 * teardown can never erase the successor text track (or the reverse). */
export function clearRecordedSidecar(stack: Stack, path: 'text' | 'drawn', key?: string): void {
  const recorded = stack.subtitleRecorded;
  if (!recorded || recorded.path !== path || (key !== undefined && recorded.key !== key)) return;
  stack.subtitleRecorded = undefined;
  stack.web.slot.selectSidecarSubtitle(null);
}

/** The drawn path's own retirement: its reads, canvas or plain lines, offer, identity and epoch all end here. */
export function clearDrawnSubtitles(host: SubtitleRuntimeHost, stack: Stack): void {
  stack.drawnRetry?.dispose(); stack.drawnRetry = undefined;
  stack.drawnAbort?.abort(); stack.drawnAbort = undefined;
  stack.drawnSurface?.clear(); stack.drawnSurface?.dispose(); stack.drawnSurface = undefined;
  if (stack.drawnPlain) { host.surface()?.guard(false); host.surface()?.clear(); stack.drawnPlain = undefined; }
  stack.drawnKey = undefined; stack.drawnEpoch = (stack.drawnEpoch ?? 0) + 1;
  stack.forcedLines = undefined; stack.forcedOnly = undefined; stack.drawnFailed = undefined;
  clearRecordedSidecar(stack, 'drawn');
}

/** The text path's own teardown, so exactly one subtitle owner draws at a time. */
export function clearTextSubtitles(host: SubtitleRuntimeHost, stack: Stack): void {
  stack.documentAbort?.abort(); stack.documentAbort = undefined;
  host.clearTimer(stack.documentTimer); stack.documentTimer = undefined;
  stack.documentKey = undefined;
  host.surface()?.guard(false); host.surface()?.clear();
  clearRecordedSidecar(stack, 'text');
}

/** The subtitle plan comes with the player's offers (the session's chosen track and its WebVTT);
 * the page draws a text track itself. */
export function bindSubtitles(host: SubtitleRuntimeHost, stack: Stack, offers: PlayerOffersData, key: string): void {
  if (!offers.current || !offers.subtitlePlan || !stack.itemId || stack.sessionKey !== key) return;
  // A reused stack or a new playback must never keep another playback's injected caption CSS.
  if (stack.subtitleStyle && stack.subtitleStyle.run !== host.playbackRun()) stack.subtitleStyle = undefined;
  host.surface()?.applyPreferences(subtitlePreferences(host, stack));
  host.renderSubtitles(stack);
}

function renderDrawnSubtitles(host: SubtitleRuntimeHost, stack: Stack, plan: SubtitlePlan, original: SubtitleOriginal): void {
  const video = host.video();
  // An installed engine playing the picture draws its own subtitles; the page never draws over it.
  if (!stack.expanded || !video || plan.renderer !== 'client' || stack.web.slot.nativeSurfaceActive) { clearDrawnSubtitles(host, stack); return; }
  // "Styled subtitles" off: the script's lines in the viewer's caption style, on the text surface.
  const plain = original.format === 'ass' && subtitlePreferences(host, stack)?.keepStyling === false;
  const key = JSON.stringify([stack.sessionKey, plan.sourceId, plan.revision, original.url, original.companionUrl ?? '', plan.offsetUs, plain]);
  if (stack.drawnKey === key) { stack.drawnSurface?.setScale(drawnScale(host, stack)); return; }
  // A new load owns its own epoch, so a late answer from the previous one cannot arm anything.
  clearDrawnSubtitles(host, stack);
  stack.drawnKey = key;
  const epoch = stack.drawnEpoch = (stack.drawnEpoch ?? 0) + 1;
  const abort = new AbortController(); stack.drawnAbort = abort;
  const sessionKey = stack.sessionKey, run = host.playbackRun(), subtitles = stack.subtitles;
  const owns = () => {
    const active = subtitleState(host, stack), selected = active.plan?.selected;
    return host.current(stack) && !abort.signal.aborted && !stack.life.signal.aborted && stack.expanded
      && stack.sessionKey === sessionKey && host.playbackRun() === run && stack.subtitles === subtitles
      && stack.drawnKey === key && stack.drawnEpoch === epoch && !active.suppressed
      && active.plan?.sessionId === plan.sessionId && active.plan.generation === plan.generation
      && active.plan.sourceId === plan.sourceId && active.plan.revision === plan.revision && active.plan.offsetUs === plan.offsetUs
      && selected?.id === plan.selected?.id && selected?.revision === plan.selected?.revision;
  };
  const current = () => owns() && !stack.drawnFailed;
  const addresses = new Set([original.url, ...(original.companionUrl ? [original.companionUrl] : []), ...(original.linesUrl ? [original.linesUrl] : [])]);
  const read = subtitleReader(host, stack, abort.signal, addresses, current);
  const failed = (error: unknown) => {
    if (!current()) return;
    stack.drawnFailed = true; stack.forcedLines = undefined;
    if (stack.drawnPlain) { host.surface()?.guard(false); host.surface()?.clear(); }
    clearRecordedSidecar(stack, 'drawn', key);
    // A passing outage is tried again; anything else is shown the ordinary way, in the picture.
    if (!stack.drawnRetry?.schedule(error) && !stack.drawnRetry?.pending) burnUndrawable(host, stack, plan);
    host.setRuntimeProblem('playback_subtitles_failed'); host.sync();
  };
  let attempt = 0;
  const shown = (loading: number) => {
    if (!current() || loading !== attempt) return false;
    recordSidecar(stack, 'drawn', key, plan.selected?.id ?? null);
    if (host.runtimeProblem() === 'playback_subtitles_failed') host.setRuntimeProblem(undefined);
    return true;
  };
  let present: () => void;
  const surface = plain ? undefined : stack.drawnSurface = createDrawnSubtitleSurface(video as HTMLVideoElement, read, failed);
  if (plain) {
    const text = host.surface();
    if (!text) { failed(new Error('This browser cannot show subtitles.')); return; }
    stack.drawnPlain = true;
    present = () => { const loading = ++attempt; void read(original.url, subtitleFileLimits.script, abort.signal).then(file => {
      if (!current() || loading !== attempt) return;
      text.show(assPlainCues(decodeSubtitleText(file.bytes, file.charset)), Number(plan.offsetUs) / 1e6);
      text.guard(true);
      if (shown(loading)) host.sync();
    }).catch(failed); };
  } else {
    if (!surface) { failed(new Error('This browser cannot draw subtitles.')); return; }
    surface.setDelay(Number(plan.offsetUs) / 1e6);
    surface.setForcedOnly(stack.forcedOnly === true);
    surface.setScale(drawnScale(host, stack));
    // The player's canonical offset may already be known (HLS presents a nonzero start).
    surface.timeline(host.subtitleTimeline());
    present = () => { const loading = ++attempt; void drawnSource(host, original, abort.signal).then(source => surface.present(source)).then(() => {
      if (!shown(loading)) return;
      if (original.linesUrl) void readForcedLines(host, stack, original.linesUrl, key, epoch, abort.signal, read, () => current() && loading === attempt);
      host.sync();
    }).catch(failed); };
  }
  stack.drawnRetry = new DrawnSubtitleRetry({current: owns, retry: () => {stack.drawnFailed = false;present();},
    setTimer: (fn, ms) => host.setTimer(fn, ms), clearTimer: timer => host.clearTimer(timer)});
  present();
}

export function renderSubtitles(host: SubtitleRuntimeHost, stack: Stack): void {
  const state = subtitleState(host, stack), plan = state.plan, surface = host.surface();
  if (wirelessPlayback(host.video())) {
    clearDrawnSubtitles(host, stack);
    clearTextSubtitles(host, stack);
    return;
  }
  // A styled or image track the plan has this browser draw comes from its own original file.
  const drawn = drawnOriginal(plan);
  if (drawn) {
    // One owner: the text document, its poll and any engine track stop before a drawn track shows
    // (a styled track shown as plain lines already owns the text surface).
    if (!stack.drawnPlain) clearTextSubtitles(host, stack);
    if (state.suppressed) { clearDrawnSubtitles(host, stack); return; }
    renderDrawnSubtitles(host, stack, plan!, drawn);
    return;
  }
  clearDrawnSubtitles(host, stack);
  // The dock is quiet: Portico's subtitle surface is never visible in a thumbnail.
  if (!surface || !stack.expanded || !plan || state.suppressed || plan.mode !== 'track' || plan.renderer !== 'external_text' || !plan.documentUrl) { clearTextSubtitles(host, stack); return; }
  const documentUrl = plan.documentUrl;
  const key = JSON.stringify([stack.sessionKey, plan.sourceId, plan.revision, plan.documentUrl, plan.offsetUs]);
  if (stack.documentKey === key) return;
  stack.documentKey = key;
  stack.documentAbort?.abort(); host.clearTimer(stack.documentTimer);
  const abort = new AbortController(); stack.documentAbort = abort;
  void revalidateTextSubtitles<SubtitleDocument>({signal: abort.signal,
    current: () => host.current(stack) && stack.documentKey === key,
    read: async (etag, signal) => {
      const api = host.source().api;
      const url = api.mediaUrl(documentUrl);
      if (new URL(url).origin !== new URL(api.baseUrl).origin) problem('unexpected_subtitle_origin');
      return fetchSubtitleDocument(subtitleFetch(host.env().fetch ?? fetch), url, etag, signal);
    },
    show: document => {surface.show(document.cues, Number(plan.offsetUs) / 1e6);surface.guard(true);recordSidecar(stack, 'text', key, plan.selected?.id ?? null);},
    recovered: () => {
      if (host.runtimeProblem() === 'playback_subtitles_failed') {host.setRuntimeProblem(undefined);host.sync();}
    },
    clear: () => {surface.guard(false);surface.clear();clearRecordedSidecar(stack, 'text', key);},
    failed: () => {host.setRuntimeProblem('playback_subtitles_failed');host.sync();},
    schedule: (load, ms) => {stack.documentTimer = host.setTimer(load, ms);},
  });
}

/** Audio was part of the initial start. Advance only this stack's exact
 * subtitle choice once its standing session has published a real plan. */
export function advancePreplay(host: SubtitleRuntimeHost, stack: Stack, playback: PlaybackSnapshot, fence: PlaybackFence | undefined): void {
  const pending = stack.preplay;
  if (!pending || !host.current(stack) || !fence || fence.kind !== 'queue' || !playback.session) return;
  const snapshot = host.snapshot();
  if (pending.progress.choice?.itemId !== playback.itemId || snapshot.session?.versionId !== pending.versionId) {stack.preplay = undefined;return;}
  const subtitles = stack.subtitles?.getSnapshot(), plan = subtitles?.plan ?? snapshot.offers?.subtitlePlan;
  if (!plan || plan.sessionId !== fence.sessionId || plan.generation !== fence.generation || plan.sourceId !== fence.sourceId) return;
  const progress = advancePendingChoice(pending.progress, {
    sessionId: fence.sessionId, audio: {availability: 'unavailable'},
    subtitles: {mode: plan.mode, selectedId: plan.selected?.id, busy: subtitles?.busy ?? false, resources: plan.resources},
  }, {
    // Audio is already the verified start track, never a second asynchronous restart.
    selectRendition: () => {problem('playback_audio_unavailable');},
    selectTrack: () => {problem('playback_audio_unavailable');},
    chooseSubtitle: trackId => {
      const original = fence;
      queueMicrotask(() => {
        if (!host.current(stack) || !host.matches(original)) return;
        void host.selectSubtitle(original, trackId).catch(() => {
          if (host.current(stack) && host.matches(original)) {host.setRuntimeProblem('playback_subtitles_failed');host.sync();}
        });
      });
    },
  });
  stack.preplay = progress.choice ? {versionId: pending.versionId, progress} : undefined;
}

export async function selectSubtitle(host: SubtitleRuntimeHost, fence: PlaybackFence, trackId: string | null): Promise<void> {
  if (fence.kind !== 'queue') problem('playback_subtitle_unavailable');
  const stack = host.require(fence), plan = stack.subtitles?.getSnapshot().plan ?? host.snapshot().offers?.subtitlePlan;
  const track = trackId === null ? null : plan?.resources.find(resource => resource.id === trackId && resource.sourceId === fence.sourceId && resource.enabled);
  if (!plan || trackId !== null && !track) problem('playback_subtitle_unavailable');
  const offsetMs = track ? Number(track.offsetUs) / 1000 : undefined;
  // The replan wire is integer milliseconds. Preserve the actual signed correction
  // exactly, including zero; a fractional correction cannot be rounded into another clock.
  if (track && !Number.isSafeInteger(offsetMs)) problem('playback_subtitle_unavailable');
  const finishRetention = host.retainMediaStart(stack.life.signal, () => host.current(stack));
  try {
    // A track this playback could not draw is asked for in the picture again, not handed over.
    await stack.service.changePlayback({subtitleTrackId: trackId, ...(track ? {subtitleOffsetMs: offsetMs} : {}), ...(isUndrawable(host, stack, trackId) ? {subtitleDelivery: 'burn' as const} : {})});
    if (!host.current(stack)) return;
    // The session now names the track on and its WebVTT: read the plan again and draw it.
    await stack.offers.refresh({quiet: true});
    if (host.current(stack)) {host.renderSubtitles(stack);host.sync();}
  } finally {finishRetention();}
}

/** Shifts the drawn track's own timing without changing which track is on. The plan must be
 * the one this full fence names and the track must still be that source's selected,
 * deliverable resource; whole milliseconds only, inside the plan's own signed bound. */
export async function setSubtitleOffset(host: SubtitleRuntimeHost, fence: PlaybackFence, offsetMs: number): Promise<void> {
  if (fence.kind !== 'queue') problem('playback_subtitle_unavailable');
  const stack = host.require(fence), plan = stack.subtitles?.getSnapshot().plan ?? host.snapshot().offers?.subtitlePlan;
  if (!plan || plan.sessionId !== fence.sessionId || plan.generation !== fence.generation || plan.sourceId !== fence.sourceId
    || plan.mode !== 'track' || !plan.selected) problem('playback_subtitle_unavailable');
  const track = plan.selected;
  if (track.sourceId !== fence.sourceId || track.retired === true || !track.enabled) problem('playback_subtitle_unavailable');
  // A correction the replan wire cannot carry is refused, never rounded into another clock.
  if (!Number.isSafeInteger(offsetMs) || Math.abs(offsetMs) > 600000) problem('playback_subtitle_unavailable');
  const finishRetention = host.retainMediaStart(stack.life.signal, () => host.current(stack));
  try {
    await stack.service.changePlayback({subtitleTrackId: track.id, subtitleOffsetMs: offsetMs, ...(isUndrawable(host, stack, track.id) ? {subtitleDelivery: 'burn' as const} : {})});
    if (!host.current(stack)) return;
    await stack.offers.refresh({quiet: true});
    if (host.current(stack)) {host.renderSubtitles(stack);host.sync();}
  } finally {finishRetention();}
}

/** Only the page-drawn text renderer on this runtime's own surface can honour a style: a text
 * track, or a styled track shown as plain lines. */
function canStyleSubtitles(host: SubtitleRuntimeHost, stack: Stack): boolean {
  if (!host.surface() || stack.retired || host.snapshot()?.nativeVideoSurfaceActive === true) return false;
  const plan = stack.subtitles?.getSnapshot().plan ?? host.snapshot()?.offers?.subtitlePlan;
  return !!plan && (plan.renderer === 'external_text' || plan.renderer === 'client' && stack.drawnPlain === true);
}

/** The style this playback actually draws with: the saved preferences plus any change made
 * here. Undefined where the host supplies no preferences at all. */
export function subtitleStyle(host: SubtitleRuntimeHost, fence: PlaybackFence): Readonly<Pick<SubtitlePreferences, 'size' | 'background'>> | undefined {
  const stack = host.stack();
  if (fence.kind !== 'queue' || !host.matches(fence) || !canStyleSubtitles(host, stack)) return undefined;
  const preferences = subtitlePreferences(host, stack);
  return preferences ? Object.freeze({size: preferences.size, background: preferences.background}) : undefined;
}

/** desktop-player f3: size and background last until this playback stops. The change reaches
 * only this stack's own surface; the saved preferences are never written. */
export function setSubtitleStyle(host: SubtitleRuntimeHost, fence: PlaybackFence, patch: Readonly<{size?: SubtitlePreferences['size']; background?: SubtitlePreferences['background']}>): void {
  if (fence.kind !== 'queue') problem('playback_subtitle_unavailable');
  const stack = host.require(fence);
  if (!canStyleSubtitles(host, stack)) problem('playback_subtitle_unavailable');
  if (patch.size !== undefined && !['small', 'medium', 'large', 'extra-large'].includes(patch.size)) problem('playback_subtitle_unavailable');
  if (patch.background !== undefined && !['none', 'translucent', 'opaque'].includes(patch.background)) problem('playback_subtitle_unavailable');
  const run = host.playbackRun(), held = stack.subtitleStyle?.run === run ? stack.subtitleStyle : undefined;
  stack.subtitleStyle = Object.freeze({run, ...(held?.size ? {size: held.size} : {}), ...(held?.background ? {background: held.background} : {}),
    ...(patch.size ? {size: patch.size} : {}), ...(patch.background ? {background: patch.background} : {})});
  host.surface()?.applyPreferences(subtitlePreferences(host, stack));
  host.sync();
}

/** The chosen image track's own forced-line offer. Only a mixed track (some forced, some not)
 * from the exact source and revision still on screen has a switch. */
export function subtitleForced(host: SubtitleRuntimeHost, fence: PlaybackFence): Readonly<{only: boolean; lines: SubtitleLines}> | undefined {
  if (fence.kind !== 'queue' || !host.matches(fence)) return undefined;
  const stack = host.stack(), offer = stack.forcedLines;
  // The offer belongs to the one image load that produced it, on screen, in this stack.
  if (!stack.drawnSurface || !stack.expanded || stack.drawnFailed || !offer || !stack.drawnKey) return undefined;
  if (offer.key !== stack.drawnKey || offer.epoch !== stack.drawnEpoch) return undefined;
  return Object.freeze({only: stack.forcedOnly === true, lines: offer.lines});
}

export function setSubtitleForcedOnly(host: SubtitleRuntimeHost, fence: PlaybackFence, on: boolean): void {
  if (fence.kind !== 'queue') problem('playback_subtitle_unavailable');
  const stack = host.require(fence), offer = stack.forcedLines;
  if (!stack.drawnSurface || !stack.expanded || stack.drawnFailed || !offer || offer.key !== stack.drawnKey || offer.epoch !== stack.drawnEpoch) problem('playback_subtitle_unavailable');
  stack.forcedOnly = on === true;
  stack.drawnSurface.setForcedOnly(stack.forcedOnly);
  host.sync();
}

/** Whether subtitles can be searched for this playback, and the server's reason when they cannot (rule 67). */
export function subtitleProvider(host: SubtitleRuntimeHost, fence: PlaybackFence): Readonly<{enabled: boolean; reason?: string}> | undefined {
  if (fence.kind !== 'queue' || !host.matches(fence) || !host.snapshot().offers?.subtitlePlan) return undefined;
  const search = subtitleSearch(host, fence);
  return search?.off ? Object.freeze({enabled: false, reason: search.setupReason ?? 'provider_not_configured'}) : Object.freeze({enabled: true});
}

/** Search state for this playback only. The panel decides how to present it. */
export function subtitleSearch(host: SubtitleRuntimeHost, fence: PlaybackFence): WebSubtitleSearch | undefined {
  if (fence.kind !== 'queue' || !host.matches(fence) || !host.snapshot().offers?.subtitlePlan) return undefined;
  const stack = host.stack();
  return sameFence(stack.subtitleSearchFence, fence) && stack.subtitleSearch ? stack.subtitleSearch
    : Object.freeze({itemId: fence.itemId, busy: false, searched: false, candidates: Object.freeze([])});
}

export async function searchSubtitles(host: SubtitleRuntimeHost, fence: PlaybackFence, language: string): Promise<void> {
  if (fence.kind !== 'queue') problem('playback_subtitle_unavailable');
  const stack = host.require(fence);
  const pending = Object.freeze({itemId: fence.itemId, busy: true, searched: true, candidates: Object.freeze([])});
  stack.subtitleSearchFence = fence; stack.subtitleSearch = pending; host.sync();
  let result: WebSubtitleSearch;
  try {
    const candidates = await searchItemSubtitles(host.source().api, fence.itemId, [language], stack.life.signal);
    result = Object.freeze({...pending, busy: false, candidates});
  } catch (error) {result = subtitleSearchFailure(fence.itemId, error);}
  if (!host.current(stack) || !host.matches(fence) || stack.subtitleSearch !== pending) return;
  stack.subtitleSearch = result; host.sync();
}

/** Only a result offered for this playback can be downloaded and selected. */
export async function downloadSubtitle(host: SubtitleRuntimeHost, fence: PlaybackFence, candidateId: string): Promise<void> {
  if (fence.kind !== 'queue') problem('playback_subtitle_unavailable');
  const stack = host.require(fence), search = subtitleSearch(host, fence);
  const candidate = search?.candidates.find(value => value.id === candidateId);
  if (!search || !candidate || search.busy) problem('playback_subtitle_unavailable');
  const pending = Object.freeze({...search, busy: true, error: undefined});
  stack.subtitleSearch = pending; host.sync();
  const current = () => host.current(stack) && host.matches(fence) && stack.subtitleSearch === pending;
  // Every exit on this stack ends the busy state: a playback that moved on (another version or quality) keeps the
  // results to choose again, never a search stuck busy.
  const settle = (next: WebSubtitleSearch) => {if (host.current(stack) && stack.subtitleSearch === pending) {stack.subtitleSearch = next; host.sync();}};
  try {
    const track = await downloadItemSubtitle(host.source().api, fence.itemId, fence.sourceId, candidate.id, stack.life.signal);
    if (!current()) {settle(Object.freeze({...search, busy: false})); return;}
    await stack.offers.refresh({quiet: true});
    if (!current()) {settle(Object.freeze({...search, busy: false})); return;}
    await host.selectSubtitle(fence, track.id);
    if (host.current(stack) && stack.subtitleSearch === pending) {stack.subtitleSearch = undefined; host.sync();}
  } catch (error) {
    settle(Object.freeze({...subtitleSearchFailure(fence.itemId, error), candidates: search.candidates}));
  }
}
