import React, {useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {NativeEventEmitter, NativeModules, Platform, UIManager, requireNativeComponent, type HostComponent, type ViewProps} from 'react-native';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import {PlaybackService} from '../../../../../packages/client-core/src';
import type {LocalPlaybackFence} from '../../../../../packages/player-core/src';
import type {OfflineEntry} from '../../../platform/offline-downloads';
import type {RememberedDownloadsViewer} from '../../shell/remembered-downloads-viewer';
import {nativeDynamicRangeMatchingAvailable} from './dynamic-range-matching';
import {chooseEngine, localRuleFacts, parseEngineFeatures, type EngineFeatures, type EngineKind} from './engine-choice';
import {NativePlaybackCoordinator, type NativePlaybackTransport} from './NativePlaybackCoordinator';
import {NativeOfflinePlayback, type NativeOfflinePlaybackDevice, type NativeOfflineSource} from './offline-playback';
import type {NativePlaybackSettings} from './types';
import {NativeOfflineTracks, type NativeLocalTracksSnapshot} from './offline-tracks';
import type {PlaybackChoices} from '../../../../../packages/client-core/src/playback-v1/choices';
import {captionStyle} from './caption-style';
import {mediaRecovered} from './playback-recovery';

type EngineModule = Readonly<{
  acquire: NativePlaybackTransport['acquire']; command: NativePlaybackTransport['command']; release: NativePlaybackTransport['release'];
  getConstants?: () => Readonly<{available?: boolean; features?: unknown}>; available?: boolean; features?: unknown;
}>;
type SurfaceProps = ViewProps & {lease: string; surfaceActive?: boolean};
const names = {native: {module: 'PorticoPlaybackModule', event: 'PorticoPlaybackFact', view: 'PorticoPlayer'},
  mpv: {module: 'PorticoMpvPlaybackModule', event: 'PorticoMpvPlaybackFact', view: 'PorticoMpvPlayer'}} as const;
const views: Partial<Record<EngineKind, HostComponent<SurfaceProps>>> = {};
const cancelled = () => Object.assign(new Error('receipt_playback_closed'), {name: 'AbortError', code: 'cancelled'});

/** Receipt-owned local playback, with no server session, Home authority, queue,
 * events client, capability publisher or online promotion. The supplied device
 * remains shell-owned: this borrower never adopts, connects or disposes it. */
export function createNativeReceiptOnlyPlayback(options: Readonly<{
  identity: RememberedDownloadsViewer; device: NativeOfflinePlaybackDevice;
  isCurrent: () => boolean; settings: () => NativePlaybackSettings; subscribeSettings: (listener: () => void) => () => void;
}>) {
  let closed = false, epoch = 0, surface: object | undefined, pending: AbortController | undefined;
  let source: NativeOfflineSource | undefined, sourceEntry: OfflineEntry | undefined;
  let tracks: NativeOfflineTracks | undefined;
  const current = () => {try {return !closed && options.isCurrent() && options.identity.current();} catch {return false;}};
  if (!current()) throw cancelled();
  const id = NativeModules.PorticoRuntime?.playbackRequestId as (() => string) | undefined;
  const transports: Partial<Record<EngineKind, NativePlaybackTransport>> = {};
  const features: Partial<Record<EngineKind, EngineFeatures>> = {};
  for (const kind of ['native', 'mpv'] as const) {
    const name = names[kind], module = NativeModules[name.module] as EngineModule | undefined;
    if (!module || !UIManager.hasViewManagerConfig(name.view) || kind === 'mpv' && (module.getConstants?.() ?? module).available !== true) continue;
    features[kind] = parseEngineFeatures((module.getConstants?.() ?? module).features);
    const emitter = new NativeEventEmitter(module as never);
    transports[kind] = {acquire: owner => module.acquire(owner), command: value => module.command(value), release: lease => module.release(lease),
      subscribe: listener => {const subscription = emitter.addListener(name.event, listener); return () => subscription.remove();}};
  }
  if (!id || !transports.native) throw new Error('receipt_playback_unavailable');
  const admission = new NativeOfflinePlayback(options.identity, options.device);
  const playback = new PlaybackService(admission.localOnlyApi(), id, {deferRecovery: true});
  const listeners = new Set<() => void>();
  let snapshot = Object.freeze({...playback.getSnapshot(), localTracks: undefined as NativeLocalTracksSnapshot | undefined});
  const publish = () => {snapshot = Object.freeze({...playback.getSnapshot(), localTracks: tracks?.getSnapshot()}); listeners.forEach(listener => listener());};
  const coordinator = new NativePlaybackCoordinator(playback, transports.native, id,
    state => {try {return current() && state.session ? admission.mediaUrl(state.session) ?? '' : '';} catch {dispose(); return ''; }},
    fact => {
      check(); if (!current() || !source) return;
      const state = playback.getSnapshot();
      if (mediaRecovered(fact, state.positionSeconds) || fact.type === 'ended') playback.recovering(state.intentId, false);
      const trackResult = tracks?.accept(fact);
      if (trackResult === 'failed') playback.fail(state.intentId, serviceText('player.opt.unavailable'));
      else if (trackResult === 'changed') {configure(); publish();}
      if (fact.type === 'ready') playback.ready(state.intentId);
      else if (fact.type === 'systemPaused') playback.pause();
      else if (fact.type === 'stalled') playback.recovering(state.intentId, true);
      else if (fact.type === 'seekApplied') playback.seekApplied(state.intentId, fact.seekRevision, fact.position, fact.paused ? 'paused' : 'playing');
      else if (fact.type === 'seekFailed') playback.seekFailed(state.intentId, fact.seekRevision, serviceText('queuePlayer.startFailed'));
      else if (fact.type === 'progress' || fact.type === 'ended') playback.fact(state.intentId, fact.position, fact.type === 'ended' ? 'ended' : fact.paused ? 'paused' : 'playing');
      else if (fact.type === 'error') playback.fail(state.intentId, serviceText('downloads.offline.notPlayableHere'));
    }, {initial: 'native', transport: kind => transports[kind as EngineKind],
      choose: () => chooseEngine({setting: options.settings().engine, mpvAvailable: !!transports.mpv,
        nativeOnly: source?.metadata.kind === 'song' || source?.metadata.kind === 'audiobook_file',
        requiredSubtitleRendering: tracks?.requiredRendering(), nativeSubtitleRendering: features.native?.subtitleRendering,
        ...(source ? localRuleFacts({address: source.media.fileUrl, local: true, platform: Platform.OS === 'android' ? 'android' : 'apple', container: source.media.extras?.bundle?.media.container}) : {})})});
  let offIdentity = () => {}, offDevice = () => {}, offSettings = () => {}, offPlayback = () => {}, offCoordinator = () => {};
  const retirePlay = () => {
    const operation = ++epoch, previous = pending; pending = undefined; source = undefined; sourceEntry = undefined; tracks = undefined; previous?.abort();
    if (epoch !== operation) return; coordinator.stop();
    if (epoch !== operation) return; playback.leave(false);
    if (epoch === operation) publish();
  };
  const check = () => {let valid = false;
    try {valid = current() && (!source || source.current());} catch { /* Revoked device reads are unavailable. */ }
    if (!valid) dispose();
  };
  const configure = () => {
    if (!current()) {dispose(); return;}
    const settings = options.settings(), state = playback.getSnapshot();
    const rate = source?.metadata.kind === 'audiobook_file' ? settings.audiobookRate : source?.metadata.kind === 'song' ? 1 : settings.videoRate;
    if (!Number.isFinite(rate) || rate <= 0) {dispose(); return;}
    if (source && state.session && admission.mediaUrl(state.session)) {
      const previous = tracks?.getSnapshot().binding;
      if (!previous || previous.intentId !== state.intentId || previous.sessionId !== state.session.id || previous.generation !== state.session.generation)
        tracks = new NativeOfflineTracks(source.media, {intentId: state.intentId, sessionId: state.session.id, generation: state.session.generation});
      const engine = coordinator.getEngine() as EngineKind, rendering = features[engine]?.subtitleRendering ?? ['text'];
      const available = settings.engine !== 'native' && transports.mpv ? [...new Set([...rendering, ...(features.mpv?.subtitleRendering ?? [])])] : rendering;
      tracks!.configure(engine, coordinator.getInstanceId(), rendering, available, true);
    } else tracks = undefined;
    coordinator.setAudioTrack(tracks?.audioCommand() ?? null); coordinator.setLocalAudioTrack(tracks?.localAudioCommand() ?? null);
    coordinator.setOptions(rate, null, tracks?.subtitleCommand(captionStyle(settings.subtitles), true)
      ?? (state.session ? {binding: {intentId: state.intentId, sessionId: state.session.id, generation: state.session.generation}, mode: 'off'} : null));
    coordinator.setPassthroughPolicy({disabled: settings.overrides?.disablePassthrough === true || settings.overrides?.forceStereo === true, formats: settings.passthroughFormats ?? {}});
    coordinator.setDisplayMatching(Platform.OS === 'android' ? settings.matchFrameRate : undefined);
    coordinator.setDynamicRangeMatching(nativeDynamicRangeMatchingAvailable() ? settings.matchDynamicRange : undefined); coordinator.reconsiderEngine();
  };
  function dispose() {
    if (closed) return; closed = true; retirePlay(); admission.dispose();
    offIdentity(); offDevice(); offSettings(); offPlayback(); offCoordinator(); listeners.clear();
  }
  try {
    offIdentity = options.identity.subscribe(check); offDevice = options.device.subscribe(check);
    offSettings = options.subscribeSettings(() => {configure(); publish();}); offPlayback = playback.subscribe(() => {configure(); publish();}); offCoordinator = coordinator.subscribe(() => {configure(); publish();});
    check(); if (!current()) throw cancelled();
  } catch (error) {dispose(); throw error;}
  const fence = (): LocalPlaybackFence | undefined => {
    try {const state = playback.getSnapshot(); return current() && source?.current() && state.session ? admission.fence(state.session, state.playbackRun) : undefined;}
    catch {dispose(); return undefined;}
  };
  /** One committed surface owns the renderer. Replay gets a fresh mount ticket,
   * retires the old play, and never resumes it automatically. */
  function Surface() {
    const [ticket, setTicket] = useState<object>();
    const lease = useSyncExternalStore(coordinator.subscribe, coordinator.getSnapshot, coordinator.getSnapshot);
    const engine = coordinator.getEngine() as EngineKind;
    useLayoutEffect(() => {
      if (!current() || surface) return;
      const ticket = {}; surface = ticket; setTicket(ticket); coordinator.start();
      return () => {if (surface === ticket) {surface = undefined; retirePlay();}};
    }, []);
    const View = views[engine] ??= requireNativeComponent<SurfaceProps>(names[engine].view);
    const ownsSurface = !!ticket && surface === ticket && current();
    return <View lease={ownsSurface ? lease : ''} pointerEvents="none" accessible={false} {...(Platform.OS === 'android' ? {surfaceActive: ownsSurface && !!lease} : {})} style={{flex: 1}} />;
  }
  const open = async (entry: OfflineEntry, lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) => {
      if (!current() || !surface || lifetime.signal.aborted || !lifetime.isCurrent()) throw cancelled();
      // Reserve before any abort/publication can synchronously call open again.
      // Reentrant successors reserve immediately, but engine/service work begins
      // only in their own microtask after the retiring call stack has unwound.
      const opening = surface, operation = ++epoch, previous = pending, controller = new AbortController();
      pending = controller; source = undefined; sourceEntry = undefined; tracks = undefined; previous?.abort();
      const abort = () => controller.abort(); lifetime.signal.addEventListener('abort', abort, {once: true});
      const active = () => current() && surface === opening && epoch === operation && !controller.signal.aborted && !lifetime.signal.aborted && lifetime.isCurrent();
      try {
        await Promise.resolve(); if (!active()) throw cancelled();
        coordinator.stop(); if (!active()) throw cancelled();
        playback.leave(false); if (!active()) throw cancelled();
        publish(); if (!active()) throw cancelled();
        coordinator.start(); if (!active()) throw cancelled();
        const admitted = await admission.prepare(entry, active);
        if (!active() || !admitted.current()) throw cancelled();
        if (!['movie', 'episode', 'song', 'audiobook_file'].includes(admitted.metadata.kind ?? '')) throw new Error(serviceText('downloads.offline.notPlayableHere'));
        source = admitted; sourceEntry = entry; configure();
        await playback.playLocal(admitted.itemId, admitted.session, undefined, {signal: controller.signal, isCurrent: active});
        if (!active() || !admitted.current() || !fence()) throw cancelled();
      } catch (error) {if (epoch === operation) retirePlay(); throw error;}
      finally {lifetime.signal.removeEventListener('abort', abort); if (pending === controller) pending = undefined;}
  };
  const matches = (expected: LocalPlaybackFence) => {const actual = fence();
    return current() && !!surface && !!source?.current() && !!actual && JSON.stringify(actual) === JSON.stringify(expected);};
  const control = (expected: LocalPlaybackFence, action: () => void) => {if (matches(expected)) action();};
  return Object.freeze({Surface, getSnapshot: () => snapshot, open,
    change: (expected: LocalPlaybackFence, choice: PlaybackChoices) => control(expected, () => {
      if (!tracks) throw new Error(serviceText('player.opt.unavailable'));
      tracks.choose(choice); configure(); publish();
    }),
    pause: (expected: LocalPlaybackFence) => control(expected, () => playback.pause()),
    resume: (expected: LocalPlaybackFence) => control(expected, () => {const phase = playback.getSnapshot().phase; if (phase === 'starting' || phase === 'ready') playback.resume();}),
    replay: (expected: LocalPlaybackFence, lifetime: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>) => {
      const entry = sourceEntry;
      if (!matches(expected) || playback.getSnapshot().phase !== 'ended' || !entry) return Promise.reject(cancelled());
      return open(entry, lifetime);
    },
    seek: (expected: LocalPlaybackFence, seconds: number) => control(expected, () => playback.seek(seconds)),
    current, fence, subscribe: (listener: () => void) => {if (closed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};},
    close: (expected: LocalPlaybackFence) => control(expected, retirePlay), dispose,
  });
}
