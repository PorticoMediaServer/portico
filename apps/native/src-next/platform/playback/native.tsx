import React, {useCallback, useEffect, useRef, useSyncExternalStore, type ComponentRef} from 'react';
import {AppState, Dimensions, NativeEventEmitter, NativeModules, Platform, UIManager, findNodeHandle, requireNativeComponent, type HostComponent, type ViewProps} from 'react-native';
import {CapabilityPublisher} from '../../../../../packages/client-core/src/playback-v1/options';
import {NativeCapabilityReadiness} from './capability-readiness';
import {localV1Http} from '../../../../../packages/client-core/src/playback-v1/local-http';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import {chooseEngine, localRuleFacts, parseEngineFeatures, type EngineKind} from './engine-choice';
import {planEngineOf, requiredSubtitleRendering} from './engine-media';
import {deviceCapabilityDocument} from './capability-document';
import type {NativeFacts} from './device-profile';
import type {MpvDeviceFacts} from './mpv-profile';
import {NativeListeningV1Adapter, type NativeListeningBridge} from './NativeListeningAdapter';
import type {NativePlaybackTransport} from './NativePlaybackCoordinator';
import {NativePlaybackRuntime} from './runtime';
import {PlaybackScopeHttp} from './scope-http';
import {createVideoSnapshotCapture} from '../video-snapshot';
import type {NativePlaybackAdapter, NativePlaybackRuntimeOptions, NativePlaybackSurfaceProps} from './types';
import {nativeDynamicRangeMatchingAvailable} from './dynamic-range-matching';
import {offlineDownloads} from '../../../platform/offline-downloads';

const names = {native: {module: 'PorticoPlaybackModule', fact: 'PorticoPlaybackFact', view: 'PorticoPlayer'}, mpv: {module: 'PorticoMpvPlaybackModule', fact: 'PorticoMpvPlaybackFact', view: 'PorticoMpvPlayer'}} as const;
type EngineModule = NativeListeningBridge & {
  acquire(owner: string): Promise<Readonly<{lease: string; instanceId: string}>>;
  command(value: Record<string, unknown>): Promise<unknown>;
  release(lease: string): Promise<unknown>;
  facts?: () => Promise<unknown>;
  getConstants?: () => Record<string, unknown>;
  available?: boolean;
  features?: unknown;
  purgeAudioCache?: () => Promise<unknown>;
};
type NativeSurfaceProps = ViewProps & {lease: string; surfaceActive?: boolean; pictureInPictureRequest?: number; pictureInPictureBinding?: unknown};
const views: Partial<Record<EngineKind, HostComponent<NativeSurfaceProps>>> = {};
function viewAvailable(name: string): boolean { try { return !!UIManager.hasViewManagerConfig(name); } catch { return false; } }
function moduleConstants(module: EngineModule) { try { return module.getConstants?.() ?? module; } catch { return module; } }
function transport(module: EngineModule, event: string): NativePlaybackTransport {
  const emitter = new NativeEventEmitter(module as never);
  return {acquire: owner => module.acquire(owner), command: value => module.command(value), release: lease => module.release(lease),
    subscribe: listener => { const subscription = emitter.addListener(event, listener); return () => subscription.remove(); }};
}
export type NativePlaybackFactoryOptions = Omit<NativePlaybackRuntimeOptions, 'nativeDynamicRangeMatching' | 'androidDisplayMatching' | 'transport' | 'engines' | 'engineFeatures' | 'nativeListening' | 'requestId' | 'capabilitiesReady' | 'disposeCapabilities' | 'timers'> & Readonly<{appVersion: string}>;

/** Binds real platform modules. There is no alternate renderer/engine, demo session or URL. */
export function createNativeShellPlaybackAdapter(options: NativePlaybackFactoryOptions): NativePlaybackAdapter & {runtime: NativePlaybackRuntime} {
  const native = NativeModules[names.native.module] as EngineModule | undefined;
  const requestId = NativeModules.PorticoRuntime?.playbackRequestId as undefined | (() => string);
  // The real platform feature, not a form factor: a device without it never offers the tool.
  const pipSupported = (NativeModules.PorticoRuntime as {getConstants?: () => Record<string, unknown>} | undefined)?.getConstants?.().pictureInPictureSupported === true;
  if (!native || !requestId || !viewAvailable(names.native.view)) throw new Error(serviceText('player.nativeHandoverFailed'));
  const mpv = NativeModules[names.mpv.module] as EngineModule | undefined;
  const mpvAvailable = !!mpv && moduleConstants(mpv).available === true && viewAvailable(names.mpv.view);
  const nativeFeatures = parseEngineFeatures(moduleConstants(native).features);
  const mpvFeatures = mpvAvailable ? parseEngineFeatures(moduleConstants(mpv!).features) : undefined;
  const nativeTransport = transport(native, names.native.fact), mpvTransport = mpvAvailable ? transport(mpv!, names.mpv.fact) : undefined;
  const viewer = options.session()?.viewer;
  if (!viewer || !['local', 'hosted'].includes(viewer.authority)) throw new Error('Invalid playback authority.');
  const initialAuthority = options.authority();
  // H-11: the shell authority, not the library list; a library added or removed keeps publishing.
  const authorityKey = (value: typeof initialAuthority) => [value.serverId, value.accountId, value.profileId, value.epoch, value.privacyFence ?? value.viewerFence].join('\n');
  const authority = authorityKey(initialAuthority);
  const capabilityHttp = new PlaybackScopeHttp(localV1Http(options.api), () => authorityKey(options.authority()) === authority && options.session()?.viewer.profileId === viewer.profileId);
  const publisher = new CapabilityPublisher(capabilityHttp);
  let closed = false;
  const readiness = new NativeCapabilityReadiness(publisher, () => closed);
  const publishCapabilities = (): Promise<void> => readiness.publish(async () => {
    const settings = options.settings();
    const [facts, mpvFacts] = await Promise.all([
      (NativeModules.PorticoCapabilities?.facts?.() as Promise<NativeFacts | null> | undefined)?.catch(() => null) ?? Promise.resolve(null),
      mpvAvailable ? (mpv!.facts?.() as Promise<MpvDeviceFacts> | undefined)?.catch(() => null) ?? Promise.resolve(null) : Promise.resolve(null),
    ]);
    if (closed) return undefined;
    const android = Platform.OS === 'android', amazon = android && String((Platform.constants as {Manufacturer?: string}).Manufacturer ?? '').toLowerCase() === 'amazon';
    const screen = Dimensions.get('screen');
    const pad = !Platform.isTV && (android ? Math.min(screen.width, screen.height) >= 600 : (Platform as {isPad?: boolean}).isPad === true);
    const document = deviceCapabilityDocument({setting: settings.engine, passthroughFormats: settings.passthroughFormats, overrides: settings.overrides, native: facts, mpv: mpvFacts, mpvAvailable, appVersion: options.appVersion,
      device: {television: Platform.isTV, pad, family: android ? Platform.isTV ? amazon ? 'firetv' : 'androidtv' : 'android' : undefined, osVersion: String(Platform.Version)}});
    return document;
  });
  void publishCapabilities();
  const ready = readiness.ready;
  const offSettings = options.subscribeSettings(() => { void publishCapabilities(); });
  const capabilitiesModule = NativeModules.PorticoCapabilities;
  const capabilitiesEvent = capabilitiesModule ? new NativeEventEmitter(capabilitiesModule).addListener('portico.capabilities', () => { void publishCapabilities(); }) : undefined;
  const emitter = new NativeEventEmitter(native as never);
  const listening = typeof native.adoptListeningSession === 'function' ? new NativeListeningV1Adapter({
    bridge: native, mutationClock: async () => {const policy = await options.api.request<{serverTimeMs: number}>('/v1/user-mutations/policy'); if (!Number.isSafeInteger(policy.serverTimeMs)) throw new Error('mutation_clock'); return policy.serverTimeMs - Date.now();}, mutationScope: () => options.api.getUserMutationScope(), route: options.api.getRouteConnection(),
    principal: {origin: options.api.baseUrl, serverId: viewer.serverId, authority: viewer.authority as 'local' | 'hosted', accountId: viewer.accountId, profileId: viewer.profileId},
    bearer: () => options.session()?.accessToken ?? '', id: requestId,
    subscribe: sink => { const subscription = emitter.addListener('PorticoListeningSnapshot', sink); return () => subscription.remove(); },
  }) : undefined;
  // The engine views emit the PiP state they answered; a television offers none.
  const pictureEvents: {remove: () => void}[] = [];
  let runtime: NativePlaybackRuntime;
  runtime = new NativePlaybackRuntime({...options, ...(Platform.OS === 'android' ? {androidDisplayMatching: true as const, nativeLocalSubtitleTiming: true as const} : {}), ...(nativeDynamicRangeMatchingAvailable() ? {nativeDynamicRangeMatching: true as const} : {}), ...(pipSupported ? {pictureInPictureControls: true as const} : {}), offlineDevice: options.offlineDevice ?? offlineDownloads, engineFeatures: kind => kind === 'mpv' && mpvFeatures ? mpvFeatures : nativeFeatures, requestId, transport: nativeTransport, nativeListening: listening, capabilitiesReady: ready,
    disposeCapabilities: () => { closed = true; offSettings(); capabilitiesEvent?.remove(); for (const event of pictureEvents) event.remove(); capabilityHttp.dispose(); appState?.remove(); void native.purgeAudioCache?.().catch(() => {}); },
    engines: {initial: 'native', transport: kind => kind === 'mpv' ? mpvTransport : nativeTransport, choose: state => {
      const session = state.session, known = session && session.mode !== 'local' ? runtime.path.session(session.id) ?? runtime.queue.currentSession() : undefined;
      const plan = planEngineOf(session, known), snapshot = runtime.getSnapshot();
      const nativeOnly = !!state.nativeListening || !!session?.audio || known?.kind === 'audio';
      if (plan.stale && !nativeOnly) return runtime.coordinator.getEngine();
      return chooseEngine({setting: options.settings().engine, mpvAvailable, planEngine: plan.engine, nativeOnly,
        ...(session?.mode === 'local' ? localRuleFacts({address: session.streamUrl, local: true, platform: Platform.OS === 'android' ? 'android' : 'apple', container: runtime.localContainer(session)}) : {}),
        requiredSubtitleRendering: session?.mode === 'local' ? runtime.localSubtitleRendering() : requiredSubtitleRendering(snapshot.subtitles?.plan, known), nativeSubtitleRendering: nativeFeatures.subtitleRendering});
    }}});
  runtime.installSnapshotCapture(createVideoSnapshotCapture({isCurrent: runtime.isSnapshotSourceCurrent}));
  if (pipSupported) {
    // Each engine answers for itself: a producer that is not the active engine cannot
    // speak for the current picture, even under the same lease.
    const admitFrom = (engine: 'native' | 'mpv') => (fact: unknown) => {if (runtime.getSnapshot().engine === engine) runtime.admitPictureInPicture(fact);};
    pictureEvents.push(new NativeEventEmitter(native as never).addListener('PorticoPictureInPicture', admitFrom('native')));
    if (mpvAvailable) pictureEvents.push(new NativeEventEmitter(mpv as never).addListener('PorticoMpvPictureInPicture', admitFrom('mpv')));
  }
  const appState = AppState.addEventListener('change', state => { if (state === 'active') void runtime.refreshAuthority().catch(() => {}); else void runtime.flush().catch(() => {}); });

  /** The only new component is a native-module view bridge; all player chrome belongs later. */
  function Surface({dockBounds, expandedBounds, docked, subtitlesVisible, surfaceActive}: NativePlaybackSurfaceProps) {
    const owner = useRef<object>({});
    const surfaceRef = useCallback((node: ComponentRef<HostComponent<NativeSurfaceProps>> | null) => { runtime.setSurfaceTag(owner.current, node ? findNodeHandle(node) : null); }, []);
    const state = useSyncExternalStore(runtime.subscribe, runtime.getSnapshot);
    useEffect(() => runtime.attachSurface(owner.current), []);
    useEffect(() => { runtime.setSubtitleDisplay(subtitlesVisible && !docked); }, [subtitlesVisible, docked]);
    const kind = state.engine;
    const SurfaceView = views[kind] ??= requireNativeComponent<NativeSurfaceProps>(names[kind].view);
    const target = docked ? dockBounds : expandedBounds;
    const bounds = target && [target.x, target.y, target.width, target.height].every(Number.isFinite) && target.width > 0 && target.height > 0 ? target : undefined;
    const visible = !!bounds && !!state.fence && !!state.lease;
    const producerActive = visible && surfaceActive === true;
    // Fabric can retain a native descendant's previous layout below a hidden
    // ancestor. Revoke Android producer composition through its own native prop
    // as well, including close before the asynchronous lease release completes.
    // Keep this host/ref/owner stable; expanded -> docked does not revoke it.
    return <SurfaceView ref={surfaceRef} lease={state.lease} pointerEvents="none" accessible={false} importantForAccessibility="no-hide-descendants"
      {...(Platform.OS === 'android' ? {surfaceActive: producerActive, pictureInPictureRequest: state.pictureInPicture?.request ?? 0, pictureInPictureBinding: state.pictureInPicture?.binding ?? null} : {})}
      style={{position: 'absolute', left: bounds?.x ?? 0, top: bounds?.y ?? 0, width: bounds?.width ?? 0, height: bounds?.height ?? 0, display: visible ? 'flex' : 'none', opacity: visible ? 1 : 0}} />;
  }
  return {
    runtime, dock: runtime.dock, getSnapshot: runtime.getSnapshot, subscribe: runtime.subscribe, start: runtime.start, startVersion: runtime.startVersion, startOffline: runtime.startOffline, selectionPlayback: runtime.selectionPlayback, Surface,
    cataloguePlayback: runtime.cataloguePlayback,
    change: runtime.change, chooseQuality: runtime.chooseQuality, setRate: runtime.setRate, setForcedOnly: runtime.setForcedOnly, setSubtitleStyle: runtime.setSubtitleStyle, subtitleControls: runtime.subtitleControls, readSession: runtime.readSession, listeningRead: runtime.listeningRead,
    prepareExternalPresentation: runtime.prepareExternalPresentation, requestPictureInPicture: runtime.requestPictureInPicture,
    onTransition: runtime.onTransition, toggle: runtime.toggle, replay: runtime.replay, retry: runtime.retry, setQueuePolicy: runtime.setQueuePolicy, editQueue: runtime.editQueue, selectQueuedEntry: runtime.selectQueuedEntry, seek: runtime.seek, skip: runtime.skip,
    previous: runtime.previous, next: runtime.next, queuePlay: runtime.queue.playEntry.bind(runtime.queue), stop: runtime.stop, goLive: runtime.goLive, setSleepTimer: runtime.setSleepTimer, beginRemote: runtime.beginRemote, updateRemote: runtime.updateRemote, dismissRemote: runtime.dismissRemote, continueHere: runtime.continueHere, restoreBookmark: runtime.restoreBookmark, open: runtime.open, dispose: runtime.dispose,
    policyDismiss: runtime.policyDismiss, policySkip: runtime.policySkip, policyUndo: runtime.policyUndo, policyAdvance: runtime.policyAdvance, policyCancelAutoplay: runtime.policyCancelAutoplay,
    captureVideoSnapshot: runtime.captureVideoSnapshot,
    ...(options.openDestinations ? {cast: runtime.cast} : {}),
  };
}
