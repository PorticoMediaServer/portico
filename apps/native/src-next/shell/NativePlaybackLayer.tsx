import {DockVideoExitProvider, DockVideoExitCanvas} from '../kit/dock/DockHandover';
import {DockVideoExitImages} from '../kit/dock/video-exit-paint';
import {useListeningCoverPublication} from '../kit/dock/ListeningCoverBridge';
import {usePlayerMorphPublication} from '../kit/dock/PlayerMorphBridge';
import {InitialPicturePresentation} from '../kit/dock/InitialPicturePresentation';
import {initialPictureKey} from '../kit/dock/initial-picture-model';
import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {BackHandler, Image, Platform, View, useWindowDimensions} from 'react-native';
import {GestureDetector} from 'react-native-gesture-handler';
import Animated, {useAnimatedStyle} from 'react-native-reanimated';
import {sameFence, type PlaybackFence} from '../../../../packages/player-core/src';
import type {V1QueueEditIntent} from '../../../../packages/client-core/src/playback-v1/v1-queue-player';
import {useTheme} from '../kit/theme';
import {focusTestID} from '../kit/focus';
import {tvFocusIds} from './tv-focus';
import {DockHost, PlayerMorph, bindNativeDock, usePlayerMorph, type DockPresentation,type CastDockBinding} from '../kit/dock';
import {commandIsCurrent, dockBarFrame, liveWaitingIdentity, playbackPosition} from '../kit/dock/model';
import {StartingDock} from '../kit/dock/StartingDock';
import {StartTimedOut} from './StartTimedOut';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {ListeningPageBackground, ListeningPagePreview, NowPlayingScreen, TVListeningRoute, type NowPlayingScreenProps} from '../screens/destinations/music/NowPlayingScreen';
import {NowPlayingLyrics} from '../screens/destinations/music/NowPlayingLyrics';
import type {LyricsTarget} from '../../../../packages/client-core/src/lyrics';
import {musicSignalView} from '../screens/destinations/music/MusicSignal';
import {NativeListeningNotice} from '../screens/destinations/music/ListeningNotice';
import {NativePlayOnSheets} from '../screens/watch/PlayOnSheets';
import {useListeningSong} from './use-listening-song';
import {nativeMusicSignal} from '../screens/destinations/music/signal-facts';
import {captureListeningOpening, expandedListeningOccupant, localListeningOccupant, type ListeningPaintOpening} from './listening-presentation';
import {nativeEqualizer} from '../platform/playback/equalizer';
import {listeningNoticeKey} from '../../../../packages/client-core/src/audio-effects';
import {playbackIdentity} from '../../../../packages/client-core/src/presentation/kit-model';
import type {NativeShellRuntime} from './runtime';
import {authorityKey, sameAuthority, type ShellAuthority} from './model';
import {nativeShellBackTransition, type NativeShellBounds, type NativeShellPlaybackAdapter, type NativeShellPlaybackSnapshot, type NativeShellVideoSnapshot} from './playback';
import {NativeVideoSnapshotCapture} from './video-snapshot';
import {watchSlotInHost, watchSlotIsCurrent} from './watch-slot-layout';
import type {NativeExpandedVideoSlot, NativeWatchReader} from './watch-outlet';
import {TVStartFailure} from './TVStartFailure';
import {TouchPlaybackPreparation} from './TouchPlaybackPreparation';
import {TouchStartFailure} from './TouchStartFailure';
import {TVPlaybackPreparation} from './TVPlaybackPreparation';

const noListeningSubscribe = () => () => {};
const noListeningNavigation = () => undefined;
const noPlayOnState = () => undefined;
/** The pushed route as identity, for a lease that only belongs to the page that opened it. */
const listeningRouteKey = (route: Readonly<{kind: string}> | undefined) => route ? JSON.stringify(route) : '';

/** Lives above route/tab contents. A stable Surface type keeps the actual native view mounted. */
export function NativePlaybackLayer({adapter, runtime, authority, contentInset, onTVListeningPresenceChange}: {adapter: NativeShellPlaybackAdapter; runtime: NativeShellRuntime; authority: ShellAuthority; contentInset?: number; onTVListeningPresenceChange?: (present: boolean) => void}) {
  const snapshot = useSyncExternalStore(adapter.subscribe, adapter.getSnapshot);
  const {width, height} = useWindowDimensions(), theme = useTheme(), insets = useSafeAreaInsets();
  // A real server preparation can precede any output fence or session. Its
  // original start owns this status; capacity refusals keep their own surface.
  const preparation = theme.isTV && snapshot.retired === false && sameAuthority(snapshot.authority, authority)
    && !snapshot.startFailure && !snapshot.capacityWait ? snapshot.playback.preparation : undefined;
  const watch = runtime.watch;
  const subscribeWatch = useCallback((listener: () => void) => watch ? watch.subscribe(listener) : () => {}, [watch]);
  const readWatchSlot = useCallback(() => watch?.getSnapshot(), [watch]);
  const watchSlot = useSyncExternalStore(subscribeWatch, readWatchSlot);
  const window = useMemo(() => ({width, height}), [width, height]);
  const windowFrame = useMemo(() => ({x: 0, y: 0, width, height}), [width, height]);
  const currentWindow = useRef(window), currentWatch = useRef(watch); currentWindow.current = window; currentWatch.current = watch;
  const local = snapshot.dock.local, docked = !!local && !local.expanded;
  const fence = snapshot.fence;
  const [dockMeasurement, setDockMeasurement] = useState<Readonly<{fence: PlaybackFence; bounds: NativeShellBounds}>>();
  // A docked picture is only real at a slot measured for THIS fence. An old
  // slot or the full-window fallback must never stand in for current geometry.
  const dockSlot = dockMeasurement && fence && sameFence(dockMeasurement.fence, fence) ? dockMeasurement.bounds : undefined;
  // Only the current occupant's own video/live item has a picture. Audio and an
  // empty dock keep the SAME mounted Surface, ref and owner (unmounting disposes
  // the runtime), but they have no picture to paint, measure, produce or drag.
  const pictureFence = fence && local && sameFence(local.fence, fence) && (local.kind === 'video' || local.kind === 'live')
    && (!docked || dockSlot !== undefined) ? fence : undefined;
  const [hostLayout, setHostLayout] = useState(0), hostLayoutNow = useRef(0);
  const host = useRef<View>(null), slot = useRef<NativeShellBounds | undefined>(undefined), slotRevision = useRef(0);
  const miniExitProof = useRef<{fence: PlaybackFence; bounds: NativeShellBounds; canvasBounds: NativeShellBounds; hostLayout: number; viewport: {width: number; height: number};
    host: View; scope: object | undefined; sourceCurrent: () => boolean; prepared: boolean; claimed: boolean; claimSettled: boolean} | undefined>(undefined);
  const exitImages = useMemo(() => new DockVideoExitImages(() => currentWindow.current, runtime.reportError), [runtime]);
  useEffect(() => {
    const signal = runtime.retirementSignal;
    const retire = () => {miniExitProof.current = undefined; exitImages.dispose();};
    if (signal?.aborted) {retire(); return;}
    signal?.addEventListener('abort', retire, {once: true});
    return () => {
      signal?.removeEventListener('abort', retire);
      miniExitProof.current = undefined;
      // Effect replay retires this UI cycle; only the original bootstrap abort
      // permanently disposes its owner. A new cycle needs a fresh capture.
      exitImages.clear();
    };
  }, [exitImages, runtime.retirementSignal]);
  const watchRevision = useRef(0), lastExpandedFrame = useRef<NativeShellBounds | undefined>(undefined);
  const [expandedMeasurement, setExpandedMeasurement] = useState<Readonly<{reader: NativeWatchReader; slot: NativeExpandedVideoSlot; host: View; bounds: NativeShellBounds}>>();
  const expandedBounds = watch && expandedMeasurement?.reader === watch && expandedMeasurement.slot === watchSlot
    && expandedMeasurement.host === host.current && watchSlotIsCurrent(watchSlot, authority, snapshot.fence, window) ? expandedMeasurement.bounds : undefined;
  if (expandedBounds) lastExpandedFrame.current = expandedBounds;
  // A real outlet with no current measurement must never promote a watch
  // picture to the full window. Retain internal morph geometry while hidden.
  const fullFrame = watch ? expandedBounds ?? lastExpandedFrame.current ?? windowFrame : windowFrame;
  const expandedReady = !watch || expandedBounds !== undefined;
  const initialGeometry = useRef<{key: object; frame: NativeShellBounds} | undefined>(undefined);
  const offeredInitial = snapshot.initialPresentation;
  if (!offeredInitial || initialGeometry.current?.key !== offeredInitial.key) initialGeometry.current = undefined;
  if (offeredInitial && expandedBounds && !docked && fence && sameFence(offeredInitial.fence, fence)) initialGeometry.current = {key: offeredInitial.key, frame: expandedBounds};
  const [picture, setPicture] = useState<Readonly<{owner: NativeVideoSnapshotCapture; authority: ShellAuthority; image: NativeShellVideoSnapshot}> | undefined>(undefined);
  const retainedImage = useRef<Readonly<{owner: NativeVideoSnapshotCapture; image: NativeShellVideoSnapshot}> | undefined>(undefined);
  const [imageState, setImageState] = useState<Readonly<{image: NativeShellVideoSnapshot; state: 'ready' | 'failed'}>>();
  const [exitDecoded, setExitDecoded] = useState<NativeShellVideoSnapshot>();
  const touching = useRef(false), moving = useRef(false), alive = useRef(true);
  // The touch listening surface's guarded controls own a lease over the player opening and the
  // real pushed page it belongs to. The lease is published only while that surface is open,
  // aborted when the page or the opening leaves, and renewed on return with fresh handlers.
  // It never keys on the output fence: a retry's own intentional new output must survive it,
  // and the core still fences the failed output it replaces.
  const navigation = runtime.navigation;
  const navigationState = useSyncExternalStore(navigation?.subscribe ?? noListeningSubscribe, navigation?.getSnapshot ?? noListeningNavigation);
  useEffect(() => {
    const keep = !snapshot.retired && !snapshot.pictureInPicture?.active && local && !local.expanded && ['video', 'live'].includes(local.kind) ? local.fence : undefined;
    // Child handovers consume in layout; after that commit, unused old files
    // have no paint owner when the mini closes, expands or changes occupant.
    exitImages.retireUnconsumed(keep);
  }, [exitImages, snapshot.retired, snapshot.pictureInPicture?.active, local]);
  const listeningOpen = !theme.isTV && local?.expanded === true && (local.kind === 'music' || local.kind === 'audiobook');
  const listeningOpenRef = useRef(listeningOpen); listeningOpenRef.current = listeningOpen;
  // The page this opening belongs to: captured when the surface opens, so a route change aborts
  // the lease and only returning to that same page renews it (with a fresh controller).
  const openingRoute = useRef(''), wasListening = useRef(false);
  if (listeningOpen && !wasListening.current) openingRoute.current = listeningRouteKey(navigationState?.route);
  else if (listeningOpen && openingRoute.current === '' && navigationState) openingRoute.current = listeningRouteKey(navigationState.route);
  wasListening.current = listeningOpen;
  const [listeningLease, setListeningLease] = useState<Readonly<{controller: AbortController; revision: number; authority: ShellAuthority}>>();
  useLayoutEffect(() => {
    if (!listeningOpen || !navigation || !navigationState || listeningRouteKey(navigationState.route) !== openingRoute.current) {setListeningLease(undefined); return;}
    const controller = new AbortController();
    setListeningLease({controller, revision: navigationState.revision, authority});
    return () => controller.abort();
  }, [listeningOpen, navigation, navigationState, authority]);
  const listeningLifetime = useMemo(() => listeningLease ? {signal: listeningLease.controller.signal,
    isCurrent: () => alive.current && listeningOpenRef.current && !listeningLease.controller.signal.aborted
      && sameAuthority(authority, listeningLease.authority) && navigation?.getSnapshot().revision === listeningLease.revision} : undefined,
    [listeningLease, authority, navigation]);
  const layoutCapture = useRef<number | undefined>(undefined), layoutCaptureRevision = useRef(0);
  const capture = useMemo(() => new NativeVideoSnapshotCapture({capture: (fence, options) => adapter.captureVideoSnapshot(fence, options), reportError: runtime.reportError,
    changed: (owner, image) => {if (alive.current) {
      if (image) retainedImage.current = {owner, image}; else if (retainedImage.current?.owner === owner) retainedImage.current = undefined;
      setPicture(previous => image ? {owner, authority, image} : previous?.owner === owner ? undefined : previous);
    }},
  }), [adapter, authority, runtime]);
  const listeningPorts = useMemo(() => {
    const lifetime = listeningLifetime;
    if (!lifetime) return undefined;
    return {
      policyLifetime: lifetime,
      ...(adapter.listeningRead ? {audiobook: {
        read: (fence: PlaybackFence) => {
          const current = adapter.getSnapshot();
          if (lifetime.signal.aborted || !lifetime.isCurrent() || !current.fence || !sameFence(current.fence, fence)) return;
          return adapter.listeningRead!(fence, lifetime);
        },
        setRate: (fence: PlaybackFence, rate: number) => {
          const current = adapter.getSnapshot();
          if (lifetime.signal.aborted || !lifetime.isCurrent() || !current.fence || !sameFence(current.fence, fence)) return;
          adapter.setRate(fence, rate);
        },
        ...(adapter.setSleepTimer ? {setSleepTimer: async (fence: PlaybackFence, minutes: number | null) => {
          const current = adapter.getSnapshot();
          if (lifetime.signal.aborted || !lifetime.isCurrent() || !current.fence || !sameFence(current.fence, fence)) return;
          await adapter.setSleepTimer!(fence, minutes);
        }} : {}),
      }} : {}),
      ...(adapter.retry ? {retry: {enabled: snapshot.capabilities.retry === true,
        run: (fence: PlaybackFence) => {void adapter.retry!(fence, lifetime).catch(runtime.reportError);}}} : {}),
      ...(runtime.playOn && snapshot.capabilities.cast ? {playOn: {enabled: true, open: (fence: PlaybackFence, anchorRef?: React.RefObject<View | null>) => {
        const current = adapter.getSnapshot();
        if (!lifetime.isCurrent() || current.capabilities.cast !== true || !current.fence || !sameFence(current.fence, fence)) return;
        runtime.playOn?.open(anchorRef);
      }}} : {}),
      ...(adapter.editQueue ? {queueEdit: {enabled: snapshot.capabilities.queueEdit === true, lease: lifetime,
        apply: (fence: PlaybackFence, intent: V1QueueEditIntent, queueId: string, expectedRevision: string) => {
          const current = adapter.getSnapshot();
          if (!lifetime.isCurrent() || current.capabilities.queueEdit !== true || !current.fence || !sameFence(current.fence, fence)) return;
          void adapter.editQueue!(fence, {queueId, expectedRevision, intent}, lifetime).catch(runtime.reportError);
        }}} : {}),
    };
  }, [listeningLifetime, adapter, snapshot.capabilities.retry, snapshot.capabilities.cast, snapshot.capabilities.queueEdit, runtime]);
  const playOn = runtime.playOn;
  const playOnState = useSyncExternalStore(playOn?.subscribe ?? noListeningSubscribe, playOn?.getSnapshot ?? noPlayOnState);
  const action = useMemo(() => {
    const bound=bindNativeDock(adapter,playOn?{remote:async command=>{await playOn.command(command.type,command.positionSeconds,command.remote.id,playOnState?.cast?.control);}}:{});
    return async (command: Parameters<typeof bound>[0]) => {
      if(command.fence.kind==='cast-view'){
        if(!playOn?.cast.viewIsCurrent(command.fence)||!command.remote)return false;
        await playOn.command(command.type,command.positionSeconds,command.remote.id,command.fence);return true;
      }
      // Cast custody cannot restore the sender's old queue. Continue here is an
      // explicit admitted start of the receiver's actual reported leaf instead.
      if(playOn&&command.type==='continue-here'&&command.remote&&adapter.dock.getSnapshot().remote?.kind==='cast'){
        if(!commandIsCurrent(adapter.dock,command))return false;
        await playOn.command(command.type,command.positionSeconds,command.remote.id,playOnState?.cast?.control);return true;
      }
      return bound(command);
    };
  }, [adapter, playOn,playOnState?.cast?.control]);
  const morph = usePlayerMorph({docked, fence: snapshot.fence, dockMotionHosted: true, travel: height, onRelease: (collapse, original, fromDocked) => {
    const current = adapter.getSnapshot();
    if (!alive.current || !sameAuthority(current.authority, authority) || !sameFence(current.fence, original)
      || !current.dock.local || (!current.dock.local.expanded) !== fromDocked) return;
    void adapter.onTransition(collapse ? adapter.dock.collapse(original) : adapter.dock.expand(original)).catch(runtime.reportError);
  }});
  const audioFence = !theme.isTV && !snapshot.dock.remote && fence && local && (local.kind === 'music' || local.kind === 'audiobook') && sameFence(local.fence, fence) ? fence : undefined;
  const coverOpening = useRef<ListeningPaintOpening<typeof navigationState> | undefined>(undefined);
  coverOpening.current = captureListeningOpening(coverOpening.current, listeningOpen, navigationState);
  const coverOpeningKey = coverOpening.current.key, coverScope = coverOpening.current.scope;
  const coverCurrent = useCallback(() => {
    if (runtime.retirementSignal?.aborted) return false;
    const current = adapter.getSnapshot();
    return alive.current && coverOpening.current?.key === coverOpeningKey && !current.retired && !current.dock.remote && sameAuthority(current.authority, authority) && !!audioFence && sameFence(current.fence, audioFence)
      && (!navigation || navigation.getSnapshot() === coverScope);
  }, [adapter, authority, audioFence, navigation, coverScope, runtime.retirementSignal, coverOpeningKey]);
  const coverSubscribe = useCallback((listener: () => void) => {
    const original = runtime.retirementSignal;
    if (original?.aborted) return () => {};
    // A same-fence source retirement need not publish a navigation revision or
    // replace this memoised UI binding. Observe its original playback owner too.
    const unsubscribePlayback = adapter.subscribe(listener);
    const unsubscribe = navigation?.subscribe(listener);
    let ended = false;
    const off = () => {if (ended) return; ended = true; try {unsubscribe?.();} finally {unsubscribePlayback();}};
    const retire = () => {off(); listener();};
    original?.addEventListener('abort', retire, {once: true});
    if (original?.aborted) retire();
    return () => {original?.removeEventListener('abort', retire); off();};
  }, [adapter, navigation, runtime.retirementSignal]);
  const audioArtwork = useMemo(() => audioFence && sameAuthority(snapshot.authority, authority) ? runtime.artwork(snapshot.entry?.posterUrl) : undefined,
    [audioFence, snapshot.authority, authority, runtime, snapshot.entry?.posterUrl]);
  const coverPublication = useMemo(() => audioFence ? {fence: audioFence, source: audioArtwork,
    controller: morph, scope: coverScope, opening: coverOpeningKey, pageOpen: listeningOpen, current: coverCurrent, subscribe: coverSubscribe} : undefined,
    [audioFence, audioArtwork, morph, coverScope, coverOpeningKey, listeningOpen, coverCurrent, coverSubscribe]);
  useListeningCoverPublication(coverPublication);
  usePlayerMorphPublication(morph, sameAuthority(snapshot.authority, authority) && !theme.isTV ? audioFence ?? (expandedReady ? pictureFence : undefined) : undefined);
  const {dockX, dockFade, dockTravelFade} = morph;
  const miniExitCurrent = useCallback((proof: NonNullable<typeof miniExitProof.current>) => {
    if (runtime.retirementSignal?.aborted) return false;
    if (navigation && navigation.getSnapshot() !== proof.scope) return false;
    const now = adapter.getSnapshot(), view = currentWindow.current;
    return alive.current && !now.retired && proof.sourceCurrent() && miniExitProof.current === proof && host.current === proof.host && hostLayoutNow.current === proof.hostLayout && sameAuthority(now.authority, authority)
      && sameFence(now.fence, proof.fence) && !!now.dock.local && !now.dock.local.expanded
      && ['video', 'live'].includes(now.dock.local.kind) && now.pictureInPicture?.active !== true
      && view.width === proof.viewport.width && view.height === proof.viewport.height
      && dockX.value === 0 && dockFade.value === 1 && !morph.dragging.value;
  }, [adapter, authority, dockX, dockFade, morph.dragging, runtime.retirementSignal, navigation]);
  const prepareMiniExit = useCallback(() => {
    const proof = miniExitProof.current;
    if (theme.isTV || !proof || !miniExitCurrent(proof) || touching.current || moving.current) return false;
    if (proof.prepared) {
      if (proof.claimSettled) void capture.clear();
      return true;
    }
    proof.prepared = true;
    // Begin only from this measured, settled mini. If a direct drag starts
    // while capture is pending, the same-source frame can still serve the normal
    // morph; the stricter mini pose is checked separately before any exit claim.
    const sourceCurrent = () => {
      if (runtime.retirementSignal?.aborted || !alive.current || host.current !== proof.host) return false;
      const now = adapter.getSnapshot();
      return !now.retired && sameAuthority(now.authority, authority) && sameFence(now.fence, proof.fence);
    };
    void capture.clear().then(() => miniExitCurrent(proof) ? capture.prepare(proof.fence, sourceCurrent, () => miniExitCurrent(proof)) : undefined).catch(runtime.reportError);
    return true;
  }, [theme.isTV, miniExitCurrent, capture, runtime, adapter, authority]);
  useEffect(() => {
    const proof = miniExitProof.current, frame = picture?.owner === capture ? picture.image : undefined;
    if (!proof || proof.claimed || !frame || !sameFence(frame.fence, proof.fence) || !miniExitCurrent(proof)) return;
    if (!frame.retainForExit) {
      proof.claimed = true; proof.claimSettled = true;
      if (!touching.current && !moving.current) void capture.clear();
      return;
    }
    if (exitDecoded !== frame) return;
    proof.claimed = true;
    void exitImages.offer(frame, true, proof.bounds, () => miniExitCurrent(proof) && retainedImage.current?.owner === capture
      && retainedImage.current.image === frame, {bounds: proof.canvasBounds, current: () => alive.current && !runtime.retirementSignal?.aborted
        && host.current === proof.host && hostLayoutNow.current === proof.hostLayout}).finally(() => {
        proof.claimSettled = true;
        if (retainedImage.current?.owner === capture && retainedImage.current.image === frame && !touching.current && !moving.current && !morph.dragging.value) void capture.clear();
      }).catch(runtime.reportError);
  }, [picture, exitDecoded, capture, exitImages, miniExitCurrent, morph.dragging, runtime]);
  const prepareLayout = useCallback(async (fence: PlaybackFence) => {
    if (!['android', 'ios'].includes(Platform.OS) || theme.isTV) return;
    const revision = ++layoutCaptureRevision.current; layoutCapture.current = revision;
    try {
      await capture.prepare(fence, () => {
        const current = adapter.getSnapshot();
        return alive.current && layoutCapture.current === revision && sameAuthority(current.authority, authority) && sameFence(current.fence, fence);
      });
    } finally {if (layoutCapture.current === revision) layoutCapture.current = undefined;}
  }, [adapter, authority, capture, theme.isTV]);
  const preparePicture = useCallback(() => {
    const current = adapter.getSnapshot();
    if (!['android', 'ios'].includes(Platform.OS) || theme.isTV || !alive.current || !sameAuthority(current.authority, authority) || !current.fence
      || !current.dock.local || !['video', 'live'].includes(current.dock.local.kind)) return;
    const fence = current.fence;
    void capture.prepare(fence, () => {
      const now = adapter.getSnapshot();
      return alive.current && sameAuthority(now.authority, authority) && sameFence(now.fence, fence)
        && (touching.current || moving.current || morph.dragging.value);
    });
  }, [adapter, authority, capture, morph.dragging, theme.isTV]);
  const onPresentationChange = useCallback((state: Readonly<{fence: PlaybackFence; docked: boolean; moving: boolean; hideSubtitles: boolean}>) => {
    const current = adapter.getSnapshot();
    if (!sameAuthority(current.authority, authority) || !sameFence(current.fence, state.fence)) return;
    moving.current = state.moving;
    if (state.moving) preparePicture(); else if (!touching.current && layoutCapture.current === undefined && !prepareMiniExit()) void capture.clear();
  }, [adapter, authority, capture, preparePicture, prepareMiniExit]);
  const touchStart = () => {touching.current = true; preparePicture();};
  const touchEnd = () => {touching.current = false; if (!moving.current && !morph.dragging.value && !prepareMiniExit()) void capture.clear();};
  useEffect(() => {
    const current = adapter.getSnapshot();
    if (!sameAuthority(current.authority, authority) || !capture.matches(current.fence)) {
      touching.current = false; moving.current = false; void capture.clear();
    }
  }, [adapter, authority, snapshot.fence, capture]);
  useEffect(() => {alive.current = true; return () => {alive.current = false; touching.current = false; moving.current = false; void capture.clear();};}, [capture]);
  useEffect(() => {
    const original = runtime.retirementSignal;
    if (original?.aborted) return;
    const back = BackHandler.addEventListener('hardwareBackPress', () => {
      if (original?.aborted) return false;
      const current = adapter.getSnapshot();
      const shell = runtime.getSnapshot();
      if (shell.selectionActive || shell.homeObscured) return false;
      const transition = nativeShellBackTransition(adapter.dock, current, authority, theme.isTV);
      if (!transition) return false;
      void adapter.onTransition(transition).catch(runtime.reportError);
      return true;
    });
    const off = () => back.remove();
    original?.addEventListener('abort', off, {once: true});
    if (original?.aborted) off();
    return () => {original?.removeEventListener('abort', off);off();};
  }, [adapter, authority, runtime, theme.isTV]);
  const onSlot = useCallback((bounds: NativeShellBounds, sourceCurrent?: () => boolean) => {
    if (runtime.retirementSignal?.aborted) return;
    slot.current = bounds;
    const node = host.current, source = adapter.getSnapshot().fence, revision = ++slotRevision.current;
    if (!node || !source || ![bounds.x, bounds.y, bounds.width, bounds.height].every(Number.isFinite) || bounds.width <= 0 || bounds.height <= 0) return;
    // The art slot is measured in window coordinates. Fabric's viewport origin
    // can include edge-to-edge insets; the persistent overlay uses local bounds.
    node.measureInWindow((x, y) => {
      if (runtime.retirementSignal?.aborted) return;
      const current = adapter.getSnapshot();
      if (!alive.current || dockX.value !== 0 || dockFade.value !== 1 || host.current !== node || hostLayoutNow.current !== hostLayout || revision !== slotRevision.current || !sameAuthority(current.authority, authority)
        || !sameFence(current.fence, source) || !Number.isFinite(x) || !Number.isFinite(y)) return;
      const next = {...bounds, x: bounds.x - x, y: bounds.y - y};
      if (navigation && navigation.getSnapshot() !== navigationState) return;
      const previousProof = miniExitProof.current, view = currentWindow.current;
      if (sourceCurrent && (!previousProof || !previousProof.sourceCurrent() || !sameFence(previousProof.fence, source) || previousProof.host !== node || previousProof.hostLayout !== hostLayout || previousProof.scope !== navigationState
        || previousProof.viewport.width !== view.width || previousProof.viewport.height !== view.height
        || Object.keys(bounds).some(key => previousProof.bounds[key as keyof NativeShellBounds] !== bounds[key as keyof NativeShellBounds]))) {
        exitImages.retireUnconsumed();
        miniExitProof.current = {fence: source, bounds: Object.freeze({...bounds}), canvasBounds: Object.freeze({...next}), hostLayout, viewport: Object.freeze({...view}), host: node, scope: navigationState, sourceCurrent,
          prepared: false, claimed: false, claimSettled: false};
      }
      if (sourceCurrent) prepareMiniExit();
      setDockMeasurement(previous => previous && sameFence(previous.fence, source)
        && Object.keys(next).every(key => previous.bounds[key as keyof NativeShellBounds] === next[key as keyof NativeShellBounds])
        ? previous : {fence: source, bounds: next});
    });
  }, [adapter, authority, dockX, dockFade, exitImages, prepareMiniExit, navigation, navigationState, runtime.retirementSignal, hostLayout]);
  const measureExpandedSlot = useCallback(() => {
    const source = watch?.getSnapshot(), current = adapter.getSnapshot(), node = host.current, revision = ++watchRevision.current;
    if (docked || !current.dock.local?.expanded || !watch || !node || !sameAuthority(current.authority, authority) || !watchSlotIsCurrent(source, authority, current.fence, currentWindow.current)) return;
    node.measureInWindow((x, y) => {
      const now = adapter.getSnapshot();
      if (!alive.current || !now.dock.local?.expanded || currentWatch.current !== watch || runtime.watch !== watch || host.current !== node || revision !== watchRevision.current
        || watch.getSnapshot() !== source || !sameAuthority(now.authority, authority) || !watchSlotIsCurrent(source, authority, now.fence, currentWindow.current)) return;
      const bounds = watchSlotInHost(source, {x, y});
      if (!bounds) return;
      setExpandedMeasurement(previous => previous?.reader === watch && previous.slot === source && previous.host === node
        && Object.keys(bounds).every(key => previous.bounds[key as keyof NativeShellBounds] === bounds[key as keyof NativeShellBounds]) ? previous : {reader: watch, slot: source, host: node, bounds});
    });
  }, [adapter, authority, docked, runtime, watch]);
  useEffect(() => {measureExpandedSlot();}, [measureExpandedSlot, watchSlot, window, snapshot.fence]);
  const horizontalVideo = docked && (local?.kind === 'video' || local?.kind === 'live');
  const dockMotion = useAnimatedStyle(() => ({opacity: horizontalVideo ? Math.min(dockFade.value, dockTravelFade?.value ?? 1) : 1, transform: [{translateX: horizontalVideo ? dockX.value : 0}]}));
  const equalizer = useSyncExternalStore(nativeEqualizer.subscribe, nativeEqualizer.getSnapshot);
  // music-plus (MC-8): the line is the committed plan plus the engine's own facts, or nothing.
  const signal = useMemo(() => {const input = nativeMusicSignal(snapshot, equalizer.value); return input ? musicSignalView(input) : undefined;}, [snapshot, equalizer.value]);
  // The song's own lyrics need the session-bound target the server validates: the queue
  // fence's session and source, and the entry's library. Keys, not objects, so a position
  // update cannot rebuild the reader; the reader itself is owned by the lyric panel.
  const lyricsKeys = nativeLyricsTargetKey(snapshot);
  const lyricsTarget = useMemo<LyricsTarget | undefined>(() => {
    if (!lyricsKeys) return undefined;
    const [sessionId, sessionGeneration, itemId, sourceId, libraryId] = JSON.parse(lyricsKeys) as [string, number, string, string, string];
    return {itemId, libraryId, sessionId, sessionGeneration, ...(sourceId ? {sourceId} : {})};
  }, [lyricsKeys]);
  const lyricsOffered = !!lyricsTarget && !!runtime.lyrics && runtime.lyricsEnabled?.() === true;
  if (!sameAuthority(snapshot.authority, authority)) return null;
  const initial = !theme.isTV && initialGeometry.current?.key === snapshot.initialPresentation?.key && !snapshot.retired && snapshot.playback.phase !== 'error'
    && snapshot.initialPresentation && fence && sameFence(snapshot.initialPresentation.fence, fence) ? snapshot.initialPresentation : undefined;
  const initialFull = initial && initialGeometry.current?.key === initial.key ? initialGeometry.current.frame : undefined;
  const initialFrame = initial && snapshot.firstVideoFrame && snapshot.firstVideoFrame.sessionId === initial.fence.sessionId
    && snapshot.firstVideoFrame.generation === initial.fence.generation;
  const Surface = adapter.Surface;
  const image = picture?.owner === capture && sameAuthority(picture.authority, authority) && fence && sameFence(picture.image.fence, fence) ? picture.image : undefined;
  const decodeProof = miniExitProof.current;
  const exitDecodeImage = image?.retainForExit && decodeProof?.prepared && !decodeProof.claimSettled && miniExitCurrent(decodeProof) ? image : undefined;
  const imageSettled = (state: 'ready' | 'failed') => {
    const current = adapter.getSnapshot();
    if (alive.current && image && retainedImage.current?.owner === capture && retainedImage.current.image === image && sameAuthority(current.authority, authority) && sameFence(current.fence, image.fence)) setImageState({image, state});
  };
  const presentation: DockPresentation | undefined = fence && snapshot.subject ? {fence, identity: liveWaitingIdentity(playbackIdentity(snapshot.subject, 'dock', runtime.text), snapshot.playback, () => runtime.text.t('next.dock.channelSourceWaiting')),
    artwork: audioArtwork ?? runtime.artwork(snapshot.entry?.posterUrl), ambient: runtime.ambient(snapshot.entry?.posterUrl), durationSeconds: snapshot.playback.duration > 0 ? snapshot.playback.duration : undefined,
    seekWindow: snapshot.seekWindow ? {start: snapshot.seekWindow.startSeconds, end: snapshot.seekWindow.endSeconds} : undefined,
    finished: snapshot.playback.phase === 'ended', skipBackSeconds: runtime.skipBackSeconds, skipForwardSeconds: runtime.skipForwardSeconds, capabilities: snapshot.capabilities} : undefined;
  const cast=playOnState?.cast,castSource=cast?.source;
  const castIdentity=castSource?.snapshot.subject?playbackIdentity(castSource.snapshot.subject,'dock',runtime.text):undefined;
  const observed=cast?.receiver,casting=cast?.cast.phase==='casting',known=!!observed&&observed.itemId===cast?.cast.itemId;
  const remotePresentation:DockPresentation|undefined=cast&&castSource&&castIdentity&&snapshot.dock.remote?.kind==='cast'&&snapshot.dock.remote.id===castSource.id?{
    fence:castSource.fence,identity:{...castIdentity,id:observed?.itemId||cast.cast.itemId||castIdentity.id,title:cast.title??runtime.text.t('player.queueNowPlaying'),subtitle:[]},
    artwork:runtime.artwork(cast.posterUrl),ambient:runtime.ambient(cast.posterUrl),device:cast.cast.deviceName??runtime.text.t('cast.theTV'),
    positionKnown:!!observed,durationSeconds:observed&&observed.durationSeconds>0?observed.durationSeconds:undefined,
    skipBackSeconds:runtime.skipBackSeconds,skipForwardSeconds:runtime.skipForwardSeconds,
    capabilities:{play:casting&&known&&observed.paused,pause:casting&&known&&!observed.paused,stop:cast.cast.phase!=='off'||cast.cast.transferPending===true,
      seek:casting&&known&&observed.durationSeconds>0,previous:false,next:false,cast:false}}:undefined;
  // A validated returning receiver is a view-only occupant. No local fence,
  // queue, runtime session or source is fabricated to keep this bar mounted.
  const viewFence=cast?.control,returningView=cast?.resumed,viewKind=cast?.displayKind,viewTitle=cast?.title;
  const castView:CastDockBinding|undefined=playOn&&cast&&returningView&&viewFence&&viewKind&&viewTitle&&
    (casting||cast.cast.remoteStopped)&&playOn.cast.viewIsCurrent(viewFence)?(()=>{
      const playback={kind:'cast' as const,fence:viewFence,positionSeconds:known?observed!.positionSeconds:0,playing:casting&&known&&!observed!.paused,expanded:false as const};
      return {isCurrent:playOn.cast.viewIsCurrent,occupant:{mode:'cast' as const,playback,
        remote:{id:returningView.id,kind:'cast' as const,phase:cast.cast.remoteStopped?'stopped' as const:'playing' as const,sequence:JSON.stringify(viewFence.remote),positionSeconds:playback.positionSeconds,playback},
        presentation:{fence:viewFence,identity:{id:viewFence.remote.occurrence.itemId,kind:viewKind,title:viewTitle,subtitle:[]},
          artwork:runtime.artwork(cast.posterUrl),ambient:runtime.ambient(cast.posterUrl),device:cast.cast.deviceName,
          positionKnown:known,durationSeconds:known&&observed!.durationSeconds>0?observed!.durationSeconds:undefined,
          skipBackSeconds:runtime.skipBackSeconds,skipForwardSeconds:runtime.skipForwardSeconds,
          capabilities:{play:casting&&known&&!cast.cast.controlPending&&observed!.paused,pause:casting&&known&&!cast.cast.controlPending&&!observed!.paused,
            stop:casting&&!cast.cast.controlPending,seek:casting&&known&&!cast.cast.controlPending&&observed!.durationSeconds>0,previous:false,next:false,cast:false}}}};
    })():undefined;
  // tablet-play9/10: a music or audiobook occupant that the viewer expanded is the
  // listening surface, drawn from the same dock occupant and the listening owner's
  // queue. The dock stays underneath; this never starts a second session.
  const dockedLocal = snapshot.dock.local;
  const listeningPaint = !theme.isTV ? localListeningOccupant(adapter.dock, snapshot.dock, presentation) : null;
  const nowPlaying = expandedListeningOccupant(adapter.dock, snapshot.dock, presentation);
  const previewListening = !nowPlaying ? listeningPaint : null;
  const listeningView: NowPlayingScreenProps | undefined = nowPlaying && (nowPlaying.mode === 'music' || nowPlaying.mode === 'audiobook') ? {
    occupant: nowPlaying, queue: snapshot.queue?.view ?? undefined, artwork: runtime.artwork,
    width, height, topInset: insets.top, bottomInset: insets.bottom, backgroundHosted: !theme.isTV,
    onAction: command => {void action(command).catch(runtime.reportError);},
    onQueuePlay: adapter.queuePlay ? (queueId, entryId, revision) => {void adapter.queuePlay!(queueId, entryId, revision).catch(runtime.reportError);} : undefined,
    ...(listeningPorts ?? {}),
    onAddToPlaylist: theme.isTV && runtime.listeningPlaylist?.canOpen(nowPlaying.playback.fence) ? () => runtime.listeningPlaylist?.open(nowPlaying.playback.fence) : undefined,
    queuePolicy: runtime.listeningQueuePolicy, signal,
    sleepAtMs: snapshot.playback.listeningDeadlines?.sleepAtMs ?? null,
    notice: snapshot.playback.listeningNotice ? listeningNoticeKey(snapshot.playback.listeningNotice) : undefined, reportError: runtime.reportError,
    lyrics: lyricsOffered && lyricsTarget && runtime.lyrics ? <NowPlayingLyrics read={runtime.lyrics} target={lyricsTarget}
      positionSeconds={playbackPosition(nowPlaying)} playing={nowPlaying.playback.playing}
      onSeek={seconds => {const current = adapter.getSnapshot(); if (current.fence && sameFence(current.fence, nowPlaying.playback.fence)) void adapter.seek(current.fence, seconds).catch(runtime.reportError);}}/> : undefined,
    onClose: () => {const current = adapter.getSnapshot(); if (current.dock.local?.expanded && current.fence && sameFence(current.fence, nowPlaying.playback.fence)) void adapter.onTransition(adapter.dock.collapse(current.fence)).catch(runtime.reportError);},
  } : undefined;
  return <View ref={host} collapsable={false} onLayout={() => {setHostLayout(++hostLayoutNow.current); if (slot.current) onSlot(slot.current); measureExpandedSlot();}} pointerEvents="box-none" testID="native-persistent-playback" style={{position: 'absolute', inset: 0, zIndex: theme.layers.player}}><DockVideoExitProvider>
    <Animated.View pointerEvents="box-none" style={[{position: 'absolute', inset: 0}, dockMotion]}>
      <PlayerMorph controller={morph} fence={pictureFence} docked={docked} fullFrame={fullFrame} dockFrame={dockSlot ?? fullFrame}
        onPresentationChange={onPresentationChange} prepareLayout={prepareLayout} retainedSnapshot={pictureFence && image ? {fence: image.fence, ready: imageState?.image === image && imageState.state === 'ready',
          failed: imageState?.image === image && imageState.state === 'failed', image: <Image source={{uri: image.image.uri}} onLoad={() => imageSettled('ready')} onError={() => imageSettled('failed')} resizeMode="contain" style={{width: '100%', height: '100%'}}/>} : undefined}>
        {context => <Surface dockBounds={pictureFence && dockSlot ? context.bounds : undefined} expandedBounds={pictureFence && expandedReady ? context.bounds : undefined} docked={context.docked} subtitlesVisible={context.subtitlesVisible} surfaceActive={!!pictureFence && context.surfaceActive}/>}
      </PlayerMorph>
      {pictureFence && local?.expanded && !theme.isTV && expandedReady && <GestureDetector gesture={morph.gesture}>
        <View onTouchStart={touchStart} onTouchEnd={touchEnd} onTouchCancel={touchEnd} accessible={false} style={watch ? {position: 'absolute', left: fullFrame.x, top: fullFrame.y, width: fullFrame.width, height: fullFrame.height} : {position: 'absolute', inset: 0}}/>
      </GestureDetector>}
      <View pointerEvents="box-none" onTouchStart={touchStart} onTouchEnd={touchEnd} onTouchCancel={touchEnd} style={{position: 'absolute', inset: 0}}>
        {(!theme.isTV || !nowPlaying) && <DockHost testID={theme.isTV && fence ? focusTestID(tvFocusIds(authority).surface('playback'), JSON.stringify(fence)) : undefined} dock={adapter.dock} snapshot={snapshot.dock} presentation={{local: presentation,remote:remotePresentation}} castView={castView} morph={morph} onVideoSlot={onSlot} takeVideoExit={exitImages.take} contentInset={contentInset}
          visible={snapshot.pictureInPicture?.active !== true}
          tvExpanded={!!dockedLocal?.expanded} onTVExpandedChange={open => {if (fence) void action({type: open ? 'expand' : 'collapse', fence}).catch(runtime.reportError);}}
          onAction={command => {void action(command).catch(runtime.reportError);}}/>}
        {/* O-159: an accepted Play is drawn at once; the real dock replaces this in place. */}
        {snapshot.starting && !snapshot.dock.local && !snapshot.retired && sameAuthority(snapshot.authority, authority) && <StartingDock key={snapshot.starting.key}
          title={snapshot.starting.display.title} icon={snapshot.starting.display.icon}
          reason={snapshot.starting.reason === 'channel_source' ? runtime.text.t('next.dock.channelSourceWaiting') : undefined}
          artwork={snapshot.starting.display.localArtwork ? {uri: snapshot.starting.display.localArtwork, cacheKey: snapshot.starting.display.localArtwork} : snapshot.starting.display.posterUrl ? runtime.artwork(snapshot.starting.display.posterUrl) : undefined}
          onStop={snapshot.starting.stop} frame={dockBarFrame(theme.isTV ? 'tv' : theme.platform, undefined, insets.bottom, contentInset)}/>}
        {!nowPlaying && dockedLocal?.expanded !== true && snapshot.playback.listeningNotice && <NativeListeningNotice key={snapshot.playback.listeningNotice}
          notice={listeningNoticeKey(snapshot.playback.listeningNotice)} bottomOffset={insets.bottom + (theme.platform === 'web' ? 100 : theme.platform === 'tablet' ? 66 : 62) + 10}/>}
        {previewListening && <ListeningPagePreview occupant={previewListening} queue={snapshot.queue?.view ?? undefined} sidePane={!!(snapshot.queue?.view || lyricsOffered)} width={width} height={height} topInset={insets.top} bottomInset={insets.bottom}/>}
        {theme.isTV ? <TVListeningRoute value={listeningView} scope={authorityKey(authority)} onPresenceChange={onTVListeningPresenceChange}/> : listeningView && <ListeningSongOpening adapter={adapter} detail={runtime.listeningSongDetail}
          fence={listeningView.occupant.playback.fence} lifetime={listeningLifetime}>{song => <NowPlayingScreen {...listeningView}
            song={{token: song.song, pending: song.pending, favorite: song.favorite}}/>}</ListeningSongOpening>}
        {theme.isTV && nowPlaying && runtime.listeningPlaylist?.render(runtime)}
      </View>
    </Animated.View>
    {listeningPaint && <ListeningPageBackground fallbackVisible={false} fence={listeningPaint.playback.fence} ambient={listeningPaint.presentation.ambient} height={height}/>}
    {!theme.isTV && <DockVideoExitCanvas style={{zIndex: theme.layers.player + 2}}/>}
    {exitDecodeImage && decodeProof && <View pointerEvents="none" accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{position: 'absolute', width: decodeProof.bounds.width, height: decodeProof.bounds.height, opacity: 0}}><Image accessible={false} accessibilityElementsHidden importantForAccessibility="no-hide-descendants"
      source={{uri: exitDecodeImage.image.uri}} fadeDuration={0} resizeMode="contain"
      style={{width: '100%', height: '100%'}}
      onLoad={() => {if (!decodeProof.claimed && !decodeProof.claimSettled && miniExitCurrent(decodeProof)
        && retainedImage.current?.owner === capture && retainedImage.current.image === exitDecodeImage) setExitDecoded(exitDecodeImage);}}
      onError={() => {
        if (miniExitProof.current !== decodeProof || retainedImage.current?.owner !== capture || retainedImage.current.image !== exitDecodeImage) return;
        decodeProof.claimed = true; decodeProof.claimSettled = true;
        setExitDecoded(current => current === exitDecodeImage ? undefined : current);
        if (!touching.current && !moving.current && !morph.dragging.value) void capture.clear();
      }}/></View>}
    {initial && initialFull && <InitialPicturePresentation key={initialPictureKey(initial.key)} offer={initial} host={host} full={initialFull}
      dock={dockSlot} docked={docked} firstFrame={!!initialFrame} controller={morph} travel={height}/>}
    {!theme.isTV && !snapshot.retired && sameAuthority(snapshot.authority, authority) && !snapshot.startFailure && !snapshot.capacityWait
      && snapshot.playback.preparation && <TouchPlaybackPreparation message={runtime.text.t(snapshot.playback.preparation.messageId)}/>}
    {preparation && <TVPlaybackPreparation message={runtime.text.t(preparation.messageId)}/>}
    {snapshot.startTimedOut && !snapshot.retired && sameAuthority(snapshot.authority, authority) && <StartTimedOut key={snapshot.startTimedOut.key} timedOut={snapshot.startTimedOut} runtime={runtime}/>}
    {(snapshot.startFailure || snapshot.capacityWait) && (theme.isTV
      ? <TVStartFailure failure={snapshot.startFailure} wait={snapshot.capacityWait} runtime={runtime}/>
      : <TouchStartFailure failure={snapshot.startFailure} wait={snapshot.capacityWait} runtime={runtime}/>)}
    {/* One Play on sheet for every surface: the dock, a title page's control and the touch
        listening output all open the same shell controller, so it is mounted once here. */}
    {runtime.playOn && <NativePlayOnSheets controller={runtime.playOn} artwork={runtime.artwork} ambient={runtime.ambient}
      airPlayVideo={snapshot.capabilities.airPlayVideo === true} skipBackSeconds={runtime.skipBackSeconds} skipForwardSeconds={runtime.skipForwardSeconds ?? 30}
      onLinkTV={() => {
        // A retained sheet may only leave for another page while the same shell and playback
        // context still own it; a retired viewer never navigates from a stale sheet.
        const live = adapter.getSnapshot();
        if (!runtime.openServers || !sameAuthority(live.authority, authority)) return;
        runtime.openServers('consent-kind'); runtime.playOn?.close();
      }}/>}
  </DockVideoExitProvider></View>;
}

/** Conditional presentation mounts a child whose own hook order is fixed. The
 * authority gate above stays before all listening presentation and reader work. */
function ListeningSongOpening({adapter, detail, fence, lifetime, children}: {
  adapter: NativeShellPlaybackAdapter;
  detail: Parameters<typeof useListeningSong>[0];
  fence: PlaybackFence;
  lifetime: Parameters<typeof useListeningSong>[2];
  children: (song: ReturnType<typeof useListeningSong>) => React.ReactNode;
}) {
  const current = useCallback(() => {
    if (!lifetime) return false;
    const live = adapter.getSnapshot();
    return lifetime.isCurrent() && !!live.fence && sameFence(live.fence, fence);
  }, [adapter, lifetime, fence]);
  const song = useListeningSong(detail, fence, lifetime, current);
  return <>{children(song)}</>;
}

/** The current song's lyric target, as a stable key string: the queue fence's session
 * and source plus the entry's library. Empty when no song is playing on a queue. */
function nativeLyricsTargetKey(snapshot: NativeShellPlaybackSnapshot): string {
  const dock = snapshot.dock.local, fence = snapshot.fence, entry = snapshot.entry;
  if (!dock || dock.expanded !== true || dock.kind !== 'music' || !fence || fence.kind !== 'queue' || !sameFence(dock.fence, fence) || !entry?.libraryId) return '';
  return JSON.stringify([fence.sessionId, fence.generation, fence.itemId, fence.sourceId ?? '', entry.libraryId]);
}
