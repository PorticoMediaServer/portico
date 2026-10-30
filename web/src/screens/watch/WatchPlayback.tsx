import {subscribePresentation} from '../../shell/fact-hooks';
import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {playbackIdentity} from '@core/presentation/kit-model/index.ts';
import {sameFence, type PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import {itemSubject} from '../../platform/playback/subject';
import type {WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime} from '../../platform/playback';
import type {PlayOnSnapshot} from '../../platform/playback/play-on';
import {CastPlayback} from '../../kit/player';
import {ListeningNotice} from '../../kit/media/ListeningNotice';
import {DockHost, DockNotice, MediaSurface, type DockBinding, type DockNoticeData, type DockPresentation} from '../../kit/dock';
import {WatchScreen} from './WatchScreen';
import {useVideoGesture} from './use-video-gesture';
import {WatchSkipPrompt} from './SkipPrompt';
import {WatchUndoPrompt} from './UndoPrompt';
import {WatchUpNextPrompt} from './UpNextPrompt';
import {WatchStillWatchingPrompt} from './StillWatchingPrompt';
import {watchStillWatching} from './still-watching';
import {preferenceValue} from '@core/preferences.ts';
import {useListeningOwner} from './listening/use-listening-owner';
import type {ListeningSettingsInput} from './listening/library-settings';
import {AudiobookHost} from './listening/AudiobookHost';
import {WatchMusicNowPlaying} from './music/NowPlaying';
import {MusicExpansionSurface, useMusicExpansion} from './music/expansion-owner';
import {WatchCapacityWait} from './CapacityWait';
import {WatchStartFailure, WatchStartTimedOut} from './StartFailure';
import {StartPreparationNotice} from './StartPreparationNotice';
import type {WatchBindings, WatchSurfaces, WatchGeometry} from './types';
import type {WatchDockPanelOffer} from './dock-panel-opening';
import type {LiveChromePort} from '../live/live-chrome-types';
import {createWatchPlayOn, remotePictureKeepsCaptions, type WatchPlaybackContext, type WatchPlayOnBinding} from './play-on-binding';
import {createWindowChromePublisher, type WindowChromePublisher} from '../../platform/window-chrome';
import {router} from '../../router';
import {settingsPlatform} from '../../platform/settings-platform';
import {feedbackAvailable, subscribeFeedback, openFeedback} from '../account/feedback';
import {dispatchWatchFeedback, watchFeedbackTarget} from './feedback';
import {fenceKey, localVideoSurfaceActive, playbackPresentation, PlaybackPresentations, retainedPlaybackNotice, startingDock} from '../../shell/playback-model';
import {PlaybackMediaOwner} from '../../shell/playback-media-owner';
import {useShellWidth} from '../../shell/viewport';
import {ExpandedPlaybackFocus} from './expanded-focus';
import styles from './playback-host.module.css';

export type WatchPlaybackProps = Readonly<{binding?: WatchBindings; context?: WatchPlaybackContext; surfaces?: WatchSurfaces; geometry?: WatchGeometry; live?: LiveChromePort}>;
const noSubscription = () => () => {};
const noSnapshot = () => undefined;
const noPresentations: readonly DockPresentation[] = Object.freeze([]);
const subscribeFullscreen = (changed: () => void) => {document.addEventListener('fullscreenchange', changed);return () => document.removeEventListener('fullscreenchange', changed);};
const fullscreenSnapshot = () => !!document.fullscreenElement;
const noFullscreen = () => false;

/** The browser's Picture-in-Picture surface, structurally, so the retained element is the only picture. */
export type PictureInPictureDocument = Readonly<{
  pictureInPictureEnabled: boolean;
  pictureInPictureElement: Element | null;
  exitPictureInPicture?: () => Promise<void>;
}>;

/** The floating picture belongs to this browser's ordinary video, never to another installed
 * platform: Desktop's native media owner draws its own presentation and has no browser transport. */
export function browserPictureInPictureSupported(host: PictureInPictureDocument | undefined, platform: string | undefined): boolean {
  if (!host || platform !== undefined && platform !== 'web') return false;
  return host.pictureInPictureEnabled === true && typeof host.exitPictureInPicture === 'function';
}

export {remotePictureKeepsCaptions} from './play-on-binding';

/** A genuine current full source: the runtime's own loaded, non-native, non-remote media
 * readiness for this exact fence, and no page-drawn caption that the floating picture would drop. */
export function pictureInPictureReady(runtime: WebShellPlaybackRuntime | undefined, snapshot: WebPlaybackRuntimeSnapshot | undefined): boolean {
  const fence = snapshot?.fence;
  if (!runtime || !snapshot || snapshot.disposed || !fence || snapshot.nativeVideoSurfaceActive) return false;
  if (snapshot.dock.remote?.phase === 'playing' || !remotePictureKeepsCaptions(snapshot)) return false;
  try { return runtime.browserMediaReady(fence); } catch { return false; }
}

/** After an awaited browser dispatch, the original runtime must still show the original full
 * fence and still permit the floating picture; a late request never keeps a retired picture. */
export function pictureInPictureCurrent(runtime: WebShellPlaybackRuntime, fence: PlaybackFence): boolean {
  const live = runtime.getSnapshot();
  return !!live.fence && sameFence(live.fence, fence) && pictureInPictureReady(runtime,live);
}

/** The current original snapshot's genuine slow-start state. Presentation only: disposed,
 * terminal, error and remote-owned surfaces withhold it, and the SDK clears it on settlement. */
export function currentStartPreparation(snapshot: WebPlaybackRuntimeSnapshot | undefined, remotePlaying: boolean) {
  if (!snapshot || snapshot.disposed || remotePlaying || snapshot.problem) return undefined;
  const playback = snapshot.playback;
  if (playback.phase === 'idle' || playback.phase === 'error' || playback.phase === 'ended' || playback.error) return undefined;
  return playback.preparation;
}

/** While the picture floats, a source that retires or turns native/remote, or a track that the
 * page can no longer draw into it, returns the one retained video to the page. */
export function pictureInPictureRetained(runtime: WebShellPlaybackRuntime | undefined, snapshot: WebPlaybackRuntimeSnapshot | undefined, owner: WebShellPlaybackRuntime | undefined): boolean {
  return !!owner && owner === runtime && !!snapshot && !snapshot.disposed && !!snapshot.fence
    && !snapshot.nativeVideoSurfaceActive && snapshot.dock.remote?.phase !== 'playing' && remotePictureKeepsCaptions(snapshot);
}

export type PictureInPictureOperations = Readonly<{toggle: () => Promise<void>; release: () => Promise<void>}>;

/** Enter/leave the floating picture on the one retained element. A request is serialized so a
 * browser dispatch that finishes after a replacement or a newer command cannot mutate the new
 * source's picture: only the newest command owns the result, and a revoked enter exits on arrival. */
export function createPictureInPictureOperations(input: Readonly<{
  document: PictureInPictureDocument;
  element: () => HTMLVideoElement | null;
  target: () => Readonly<{runtime: WebShellPlaybackRuntime; fence: PlaybackFence}> | null;
  current: (runtime: WebShellPlaybackRuntime, fence: PlaybackFence) => boolean;
}>): PictureInPictureOperations {
  type Operation={kind:'enter'|'exit';element:HTMLVideoElement;revoked:boolean};
  let pending:Operation|undefined,retained:HTMLVideoElement|null=null;
  const exitOwn=async(element:HTMLVideoElement)=>{
    if(input.document.pictureInPictureElement===element&&typeof input.document.exitPictureInPicture==='function')await input.document.exitPictureInPicture();
  };
  const exit=async(element:HTMLVideoElement)=>{
    const operation:Operation={kind:'exit',element,revoked:true};pending=operation;
    try{await exitOwn(element);}finally{if(pending===operation)pending=undefined;}
  };
  return {
    toggle:async()=>{
      // Coalesce while an enter/exit is awaiting the browser. An additional enter gesture
      // cancels the pending enter; it can never erase that request's revocation or dispatch
      // a competing request whose rejection would leave the first result unqualified.
      if(pending){if(pending.kind==='enter')pending.revoked=true;return;}
      const element=input.element();if(!element)return;retained=element;
      if(input.document.pictureInPictureElement===element){await exit(element);return;}
      if(input.document.pictureInPictureElement)throw new DOMException('Another picture source is active','InvalidStateError');
      const source=input.target();
      if(!source||!element.isConnected||typeof element.requestPictureInPicture!=='function')return;
      const operation:Operation={kind:'enter',element,revoked:false};pending=operation;
      let acknowledged=false;
      try{await element.requestPictureInPicture();acknowledged=true;}
      finally{
        try{
          let current=false;
          try{current=element.isConnected&&input.current(source.runtime,source.fence);}catch{/* retired */}
          if(!acknowledged||operation.revoked||!current)await exitOwn(element);
        }finally{if(pending===operation)pending=undefined;}
      }
    },
    release:async()=>{
      // The accepted request keeps custody until its own acknowledgement/retirement settles.
      // Never race an early exit against a late enter or a newer picture on this element.
      if(pending){pending.revoked=true;return;}
      const element=input.element()??retained;if(element)await exit(element);
    },
  };
}


/** Retain the producer sequence at the actual notice edge, not on every clock update.
 * The parent remounts this presentation for each runtime and full playback identity. */
function CurrentListeningNotice({snapshot, phone}: {snapshot: WebPlaybackRuntimeSnapshot; phone: boolean}) {
  const code = snapshot.playback.listeningNotice;
  const retained = useRef<{code: NonNullable<typeof code>; sequence: number} | undefined>(undefined);
  if (!code) retained.current = undefined;
  else if (retained.current?.code !== code) retained.current = {code, sequence: snapshot.sequence};
  return <ListeningNotice notice={retained.current} paused={snapshot.playback.intent === 'paused'}
    stopped={snapshot.disposed || snapshot.playback.phase === 'idle' || snapshot.playback.phase === 'ended'}
    errorShown={snapshot.playback.phase === 'error' || !!snapshot.playback.error || !!snapshot.problem}
    expanded={false} layout={phone ? 'phone' : 'desktop'} translate={defaultI18n.t}/>;
}

/** This parent and its one video stay mounted through routes AND authority handovers. */
export function WatchPlayback({binding, context, surfaces, geometry, live}: WatchPlaybackProps) {
  const runtime = binding?.runtime;
  const snapshot = useSyncExternalStore<WebPlaybackRuntimeSnapshot | undefined>(runtime?.subscribe ?? noSubscription, runtime?.getSnapshot ?? noSnapshot, runtime?.getSnapshot ?? noSnapshot);
  const canReport = useSyncExternalStore(subscribeFeedback, feedbackAvailable, () => false);
  const feedbackTarget = binding && snapshot ? watchFeedbackTarget(binding, snapshot) : undefined;
  const width = useShellWidth(), phone = width < 1024;
  const host = useRef<HTMLDivElement>(null), video = useRef<HTMLVideoElement>(null), dockSlot = useRef<HTMLSpanElement>(null), expandedSlot = useRef<HTMLDivElement>(null);
  const castAnchor = useRef<HTMLElement | null>(null), musicBar = useRef<HTMLDivElement | null>(null);
  const [castOwner, setCastOwner] = useState<{runtime: WebShellPlaybackRuntime; context: WatchPlaybackContext; value: WatchPlayOnBinding} | null>(null);
  useEffect(() => {
    // Desktop's native media owner has no browser-video Cast/AirPlay transport. Its real
    // platform is installed before the unchanged web entry, never inferred from the UA.
    if (!binding || !context || !video.current || !context.isCurrent() || settingsPlatform()?.environment.platform === 'desktop') return;
    const value = createWatchPlayOn(context, binding, video.current, import.meta.env.VITE_CAST_APPLICATION_ID);
    setCastOwner({runtime: binding.runtime, context, value});
    return value.dispose;
  }, [binding, context]);
  const cast = castOwner && castOwner.runtime === runtime && castOwner.context === context ? castOwner.value : undefined;
  const remoteState = useSyncExternalStore<PlayOnSnapshot | undefined>(cast?.controller.subscribe ?? noSubscription, cast?.controller.getSnapshot ?? noSnapshot, noSnapshot);
  // AirPlay also lifts only the video; page-drawn captions keep the picture on the page.
  // The panel also lists the server's Portico televisions, so a queue run offers Play on
  // even when the browser itself has no Cast or AirPlay transport.
  const castAvailable = !!cast && (remoteState?.airPlayAvailable || snapshot?.fence?.kind === 'queue' && (remoteState?.cast.available || !!cast.receivers));
  const remotePlaying = remoteState?.remote?.phase === 'playing';
  const media = useMemo(() => new PlaybackMediaOwner<HTMLVideoElement>(), []);
  const cache = useMemo(() => new PlaybackPresentations(runtime), [runtime]);
  const dockPanelsCurrent = useCallback(() => context?.isCurrent() === true && cast?.controller.getSnapshot().remote?.phase !== 'playing', [context, cast]);
  const musicCurrent = useCallback(() => dockPanelsCurrent() && !cast?.controller.getSnapshot().remote, [dockPanelsCurrent, cast]);
  const preferenceSubscribe=useCallback((changed:()=>void)=>subscribePresentation(context,changed),[context]);
  const preferenceRead=useCallback(()=>context?.preferences?.getSnapshot('web')??context?.facts?.preferences,[context]);
  const listeningPreferences=useSyncExternalStore(preferenceSubscribe,preferenceRead,preferenceRead);
  const lyricsEnabled=!!listeningPreferences&&preferenceValue<boolean>(listeningPreferences,'playback.showSyncedLyrics')===true;
  const listeningSettings=useMemo<ListeningSettingsInput|undefined>(()=>context?{context,navigate:router.navigate.bind(router)}:undefined,[context]);
  const listening=useListeningOwner(binding,snapshot,musicCurrent,lyricsEnabled,listeningSettings);
  const listeningControls=useMemo(()=>listening?.state.dockOpening?[...(listening.owner.lyricsOffered()?['lyrics' as const]:[]),...(listening.owner.queueOffered()?['queue' as const]:[]),
    ...(listening.owner.chaptersOffered()?['chapters' as const]:[]),...(listening.owner.speeds().length?['speed' as const]:[]),...(listening.owner.sleepOffered()?['sleep' as const]:[])]:[],[listening]);
  const musicMotion = useMusicExpansion(binding, snapshot, musicCurrent, dockSlot, musicBar, !!remoteState?.remote);
  const subscribeDockPanels = useCallback((changed: () => void) => cast?.controller.subscribe(changed) ?? (() => {}), [cast]);
  const [heldDockPanels, setHeldDockPanels] = useState<Readonly<{offer: WatchDockPanelOffer; context: WatchPlaybackContext | undefined; current: () => boolean}>>();
  const publishDockPanels = useCallback((offer: WatchDockPanelOffer) => {
    const held = {offer, context, current: dockPanelsCurrent}; setHeldDockPanels(held);
    return () => setHeldDockPanels(value => value === held ? undefined : value);
  }, [context, dockPanelsCurrent]);
  const offeredPanels = (surfaces?.available ?? []).filter(panel => panel === 'subtitles-audio' || panel === 'options').join('|');
  const dockPanels = heldDockPanels && heldDockPanels.context === context && heldDockPanels.current === dockPanelsCurrent && heldDockPanels.offer.binding === binding
    && dockPanelsCurrent() && heldDockPanels.offer.panels.join('|') === offeredPanels
    && heldDockPanels.offer.current() ? heldDockPanels.offer : undefined;
  const presentations = useMemo(() => snapshot && binding ? cache.update(snapshot, playbackPresentation(snapshot, binding.environment, binding.artwork, binding.skips)).map(value =>
    snapshot.fence && fenceKey(value.fence) === fenceKey(snapshot.fence) ? {...value, controls: [...new Set([...value.controls,
      ...(castAvailable ? ['play-on' as const] : []), ...listeningControls,...(dockPanels?.panels ?? [])])]} : value) : noPresentations, [snapshot, binding, cache, castAvailable, dockPanels,listeningControls]);
  const starting = useMemo(() => snapshot?.starting && binding ? startingDock(snapshot.starting, binding.environment, binding.artwork) : undefined, [snapshot?.starting, binding]);
  const dockBinding: DockBinding | undefined = useMemo(() => runtime && binding ? {
    getSnapshot: () => runtime.dock.getSnapshot(), onAction: action => {
      if(!action.remote&&['lyrics','queue','chapters','speed','sleep'].includes(action.kind)){
        if(listening&&snapshot?.fence&&sameFence(snapshot.fence,action.fence)){try{listening.owner.requestDock(listening.state,action.kind as 'lyrics'|'queue'|'chapters'|'speed'|'sleep',document.activeElement instanceof HTMLElement?document.activeElement:undefined);}catch(error){if(listening.owner.canReportDock(listening.state))binding.reportError(error);}}
      }else if (!action.remote && (action.kind === 'subtitles-audio' || action.kind === 'options')) {
        if (dockPanels && sameFence(dockPanels.fence, action.fence)) dockPanels.request(action.kind);
      } else if (action.kind === 'play-on' && cast && castAvailable) {
        const live = runtime.getSnapshot();
        if (!live.disposed && live.fence && fenceKey(live.fence) === fenceKey(action.fence)) {
          castAnchor.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
          cast.controller.openPlayOn();
        }
      } else void runtime.onAction(action).catch(binding.reportError);
    },
  } : undefined, [runtime, binding, cast, castAvailable, dockPanels,listening,snapshot]);
  const expanded = !!snapshot?.dock.local?.expanded;
  const focusOwner = useRef<ExpandedPlaybackFocus | null>(null);
  useLayoutEffect(() => {
    const owner = new ExpandedPlaybackFocus(document); focusOwner.current = owner;
    return () => {owner.dispose(); focusOwner.current = null;};
  }, []);
  useLayoutEffect(() => {
    focusOwner.current?.update(host.current, expanded && !snapshot?.disposed && !remotePlaying);
  });
  // A reused sequence from another runtime/session must never inherit an expiry.
  const noticeRuntime = useRef<{runtime?: WebShellPlaybackRuntime; epoch: number}>({epoch: 0});
  if (noticeRuntime.current.runtime !== runtime) noticeRuntime.current = {runtime, epoch: noticeRuntime.current.epoch + 1};
  const noticeIdentity = snapshot?.fence ? JSON.stringify([noticeRuntime.current.epoch, fenceKey(snapshot.fence),
    snapshot.playback.intentId, snapshot.playback.playbackRun]) : undefined;
  const activeVideo = localVideoSurfaceActive(snapshot) && !remotePlaying;
  const fullscreen = useSyncExternalStore(subscribeFullscreen, fullscreenSnapshot, noFullscreen);
  const [pipActive, setPipActive] = useState(false);
  const pipOwner = useRef<WebShellPlaybackRuntime | undefined>(undefined);
  const pipLive = useRef<{runtime?: WebShellPlaybackRuntime; snapshot?: WebPlaybackRuntimeSnapshot}>({});
  pipLive.current = {runtime, snapshot};
  const pipDocument = typeof document === 'undefined' ? undefined : document as PictureInPictureDocument;
  const pipSupported = browserPictureInPictureSupported(pipDocument, settingsPlatform()?.environment.platform);
  const pipReady = pipSupported && !!video.current?.isConnected && !video.current.disablePictureInPicture
    && typeof video.current.requestPictureInPicture==='function' && pictureInPictureReady(runtime, snapshot);
  const pipOperations = useMemo(() => pipDocument && pipSupported ? createPictureInPictureOperations({
    document: pipDocument,
    element: () => video.current,
    // The gesture reads the live full source, not a render that may predate this frame.
    target: () => {
      const liveRuntime = pipLive.current.runtime, live = liveRuntime?.getSnapshot(), element = video.current;
      if (!liveRuntime || !live?.fence || !element || !element.isConnected || element.disablePictureInPicture || typeof element.requestPictureInPicture !== 'function') return null;
      if (!pictureInPictureReady(liveRuntime, live)) return null;
      return {runtime: liveRuntime, fence: live.fence};
    },
    current: (liveRuntime, fence) => pictureInPictureCurrent(liveRuntime, fence),
  }) : undefined, [pipDocument, pipSupported]);
  useEffect(() => {
    const element = video.current;
    if (!element) return;
    const entered = () => { pipOwner.current = runtime; setPipActive(document.pictureInPictureElement === element); };
    const left = () => { pipOwner.current = undefined; setPipActive(false); };
    element.addEventListener('enterpictureinpicture', entered);
    element.addEventListener('leavepictureinpicture', left);
    return () => { element.removeEventListener('enterpictureinpicture', entered); element.removeEventListener('leavepictureinpicture', left); };
  }, [runtime]);
  useLayoutEffect(()=>()=>{void pipOperations?.release().catch(()=>{});},[pipOperations,runtime]);
  useEffect(() => {
    const liveRuntime=pipLive.current.runtime,live=liveRuntime?.getSnapshot();
    if (!pipActive || !pipOperations || pictureInPictureRetained(liveRuntime, live, pipOwner.current)) return;
    // Drawn captions, a native/remote takeover or a retired source cannot follow the floating
    // picture; return the one retained video to the page rather than dropping them.
    pipOwner.current = undefined;
    void pipOperations.release().catch(binding?.reportError ?? (() => {}));
  }, [binding, pipActive, pipOperations, runtime, snapshot]);
  const [controlsVisible, setControlsVisible] = useState(true), chrome = useRef<WindowChromePublisher | undefined>(undefined);
  useLayoutEffect(() => {
    if (!expanded || !activeVideo) return;
    const publisher = createWindowChromePublisher('video');chrome.current = publisher;
    return () => {publisher.dispose();if (chrome.current === publisher) chrome.current = undefined;};
  }, [expanded, activeVideo]);
  useLayoutEffect(() => {chrome.current?.update({controlsVisible, fullscreen});}, [expanded, activeVideo, controlsVisible, fullscreen]);
  useEffect(() => {
    if ((!expanded || !activeVideo) && document.fullscreenElement === host.current) void document.exitFullscreen().catch(binding?.reportError ?? (() => {}));
  }, [expanded, activeVideo, binding]);
  const [notice, setNotice] = useState<{runtime: WebShellPlaybackRuntime; value: DockNoticeData} | null>(null), [now, setNow] = useState(() => performance.now());
  const previous = useRef<{runtime?: WebShellPlaybackRuntime; audio?: string; video?: string}>({});
  useLayoutEffect(() => { setNotice(null); previous.current = {}; }, [runtime]);
  // Adoption is awaitable: the previous device owner must actually retire before this
  // runtime's factory runs, and the element stays unattached while that wait is in progress.
  const reportAdoption = useRef(binding?.reportError);
  reportAdoption.current = binding?.reportError;
  useLayoutEffect(() => {
    const element = video.current;
    if (!element) return;
    const life = new AbortController();
    let unmount: (() => void) | undefined;
    void media.adopt(runtime, element, {signal: life.signal, isCurrent: () => element.isConnected})
      .then(release => {
        if (life.signal.aborted) { release?.(); return; }
        unmount = release;
      }, error => { if (!life.signal.aborted) reportAdoption.current?.(error); });
    // Release synchronously even if the adoption promise has not settled yet; a newer
    // element's adoption is untouched.
    return () => { life.abort(); unmount?.(); media.release(element); cache.clear(); };
  }, [runtime, cache, media]);
  useEffect(() => {
    if (!runtime || !snapshot) { previous.current = {}; return; }
    const parked = snapshot.dock.parkedAudio, resume = snapshot.dock.resumeVideo;
    const audioKey = parked ? fenceKey(parked.fence) : undefined, videoKey = resume ? fenceKey(resume.fence) : undefined;
    const sameRuntime = previous.current.runtime === runtime;
    const changed: Pick<DockNoticeData, 'kind' | 'playback'> | undefined = parked && (!sameRuntime || audioKey !== previous.current.audio) ? {kind: 'paused-audio', playback: parked}
      : resume && (!sameRuntime || videoKey !== previous.current.video) ? {kind: 'stopped-video', playback: resume} : undefined;
    previous.current = {runtime, audio: audioKey, video: videoKey};
    if (changed) {
      const presentation = presentations.find(value => fenceKey(value.fence) === fenceKey(changed.playback.fence));
      if (presentation) { const changedAtMs = performance.now(); setNow(changedAtMs); setNotice({runtime, value: {...changed, presentation, changedAtMs}}); }
    }
  }, [runtime, snapshot, presentations]);
  const currentNotice = retainedPlaybackNotice(notice, runtime, snapshot?.dock, presentations);
  useEffect(() => {
    if (!currentNotice) return;
    const timers = [300, 8300].map(delay => setTimeout(() => setNow(performance.now()), Math.max(0, currentNotice.changedAtMs + delay - performance.now())));
    return () => timers.forEach(clearTimeout);
  }, [currentNotice]);
  useEffect(() => {
    if (!runtime) return;
    let previous = router.state.location.href;
    return router.subscribe('onResolved', () => {
      const href = router.state.location.href, navigated = href !== previous; previous = href;
      const current = runtime.getSnapshot();
      // Expansion and posture never navigate: the underlying route's real DOM/services stay
      // alive. Only a subsequent explicit destination change collapses the local overlay.
      if (navigated && current.dock.local?.expanded && current.fence && router.state.location.pathname !== '/player') runtime.collapse(current.fence);
    });
  }, [runtime]);
  const collapse = () => {
    const live = runtime?.getSnapshot();
    if (!runtime || !snapshot?.fence || !live?.fence || live.disposed || !live.dock.local?.expanded || live.authority !== snapshot.authority || fenceKey(live.fence) !== fenceKey(snapshot.fence)) return;
    runtime.collapse(live.fence);
  };
  const toggleFullscreen = async () => {
    const live = runtime?.getSnapshot();
    if (!runtime || !snapshot?.fence || !live?.fence || live.disposed || !live.dock.local?.expanded || !localVideoSurfaceActive(live) || live.authority !== snapshot.authority || fenceKey(live.fence) !== fenceKey(snapshot.fence)) return;
    if (document.fullscreenElement) await document.exitFullscreen();
    else {
      const target = host.current;
      if (!target?.isConnected) throw new DOMException('Playback surface retired', 'AbortError');
      await target.requestFullscreen();
    }
  };
  const policy = useMemo(() => runtime && binding ? {getSnapshot: runtime.getSnapshot, isCurrent: () => !runtime.getSnapshot().disposed,
    policyAction: runtime.policyAction.bind(runtime), replay: runtime.replay.bind(runtime), stop: runtime.stop.bind(runtime),
    reportError: binding.reportError} : undefined, [runtime, binding]);
  const layout = width < 1024 ? 'phone' : globalThis.matchMedia?.('(pointer: coarse)').matches ? 'tablet' : 'desktop';
  // f6e hides the player's own chrome only while this exact dialog is really admitted.
  const stillWatching = policy && snapshot ? watchStillWatching(policy, snapshot) : null;
  const drawnSurfaces: WatchSurfaces = {...surfaces, available: [...new Set([...(surfaces?.available ?? []), ...(castAvailable ? ['play-on' as const] : [])])], render: surfaces?.render ?? (() => null),
    more: [...(surfaces?.more ?? []), ...(canReport && feedbackTarget ? [{id: 'report-problem', label: defaultI18n.t('web.player.error.report'),
      action: () => {dispatchWatchFeedback(feedbackTarget, {available: feedbackAvailable, open: openFeedback});}}] : [])],
    playOn: cast && castAvailable ? {active: remoteState?.panel === 'play-on', open: anchor => {castAnchor.current = anchor ?? null;cast.controller.openPlayOn();}, close: () => cast.controller.closePanel()} : surfaces?.playOn,
    ...(stillWatching ? {modal: 'still-watching' as const} : {}),
    prompts: <>{surfaces?.prompts}{policy && snapshot && <><WatchSkipPrompt binding={policy} snapshot={snapshot} expanded={expanded} controlsVisible={controlsVisible} layout={layout} />
      <WatchUndoPrompt binding={policy} snapshot={snapshot} expanded={expanded} controlsVisible={controlsVisible} layout={layout} />
      <WatchUpNextPrompt binding={policy} snapshot={snapshot} expanded={expanded} controlsVisible={controlsVisible} layout={layout} artwork={binding!.artwork} environment={binding!.environment} />
      <WatchStillWatchingPrompt binding={policy} snapshot={snapshot} expanded={expanded} layout={layout} environment={binding!.environment} /></>}</>};
  const remote = remoteState?.remote;
  const visualId = remote?.phase === 'playing' ? remoteState?.cast.receiver?.itemId : remote?.itemId;
  const item = visualId ? cast?.item(visualId) : undefined;
  const source = remote?.source?.fence.kind === 'queue' && remote.source.fence.itemId === visualId ? remote.source : undefined;
  const described = remote && remote.itemId === visualId ? remote.metadata : undefined;
  const subject = item ? itemSubject(item) : source?.subject;
  const visuals = binding ? {itemId: subject ? visualId : undefined, identity: subject ? playbackIdentity(subject, 'full', binding.environment) : undefined,
    artwork: subject ? binding.artwork.foreground(item?.posterUrl ?? item?.backdropUrl ?? source?.posterUrl ?? source?.backdropUrl ?? described?.artworkPath, 256, 16 / 9, {group: 'playback', priority: 'high'}) : undefined,
    skipBackSeconds: binding.skips.back, skipForwardSeconds: binding.skips.forward} : undefined;
  const windowPort = typeof document !== 'undefined' ? {
    ...(document.fullscreenEnabled ? {fullscreen: {active: fullscreen, toggle: toggleFullscreen}} : {}),
    ...(pipOperations && (pipReady || pipActive) ? {pictureInPicture: {active: pipActive, toggle: () => pipOperations.toggle()}} : {}),
  } : undefined;
  const listeningPlayOn=drawnSurfaces.playOn?.open;
  const videoCurrent = useCallback(() => musicCurrent() && (!policy || !watchStillWatching(policy, policy.getSnapshot())), [musicCurrent, policy]);
  const videoGesture = useVideoGesture(binding, snapshot, phone && activeVideo && !snapshot?.nativeVideoSurfaceActive
    && !fullscreen && !pipActive && !drawnSurfaces.modal && !remoteState?.remote, videoCurrent, dockSlot, expandedSlot, musicBar);
  return <div ref={host} className={styles.dock} data-phone={phone} data-shell-dock data-watch-playback-host data-music-expanded={!phone && expanded && !remotePlaying && snapshot?.presentation?.kind === 'music' ? 'true' : undefined}>
    {snapshot && dockBinding && <div ref={musicBar} className={styles.localDock} data-covered={!!remote} data-music-retained={!!musicMotion.scope || undefined} data-video-retained={videoGesture.retain && expanded || undefined} inert={expanded && !remotePlaying || undefined} aria-hidden={expanded && !remotePlaying || undefined}><DockHost state={snapshot.dock} presentations={presentations} binding={dockBinding} layout={phone ? 'phone' : 'desktop'} artSlotRef={dockSlot} videoSurface={!!activeVideo} artworkSurface={!!musicMotion.scope} retainExpandedMusic={!!musicMotion.scope} retainExpandedVideo={videoGesture.retain} videoGesture={videoGesture.port} videoGestureActive={videoGesture.active} starting={starting} />
      {!expanded && !remote && noticeIdentity && <CurrentListeningNotice key={noticeIdentity} snapshot={snapshot} phone={phone}/>}
      <DockNotice notice={currentNotice} nowMs={now} binding={dockBinding} phone={phone} /></div>}
    <MediaSurface gesturePaint={videoGesture.paint} finished={snapshot?.presentation?.finished} expanded={expanded} dockSlot={dockSlot} expandedSlot={expandedSlot} hidden={!activeVideo} nativeRenderActive={snapshot?.nativeVideoSurfaceActive} className={styles.surface}>
      <video ref={video} playsInline preload="none" aria-label={playbackIdentityLabel(presentations, snapshot?.fence && fenceKey(snapshot.fence))} />
    </MediaSurface>
    {snapshot?.capacityWait && <WatchCapacityWait wait={snapshot.capacityWait} />}
    {snapshot && <StartPreparationNotice preparation={currentStartPreparation(snapshot, remotePlaying)} />}
    {binding && snapshot?.startTimedOut && <WatchStartTimedOut timedOut={snapshot.startTimedOut} reportError={binding.reportError}/>}
    {binding && snapshot?.startFailure && <WatchStartFailure failure={snapshot.startFailure} authority={snapshot.authority} context={context} reportError={binding.reportError}/>}
    <MusicExpansionSurface motion={musicMotion}/>
    {binding && snapshot && <WatchMusicNowPlaying context={context} playOn={listeningPlayOn} listening={listening} motion={musicMotion} isCurrent={dockPanelsCurrent} binding={binding} snapshot={snapshot} expanded={expanded && !remotePlaying} onCollapse={collapse} phone={phone} />}
    {binding&&snapshot?.presentation?.kind==='audiobook'&&<AudiobookHost binding={binding} snapshot={snapshot} host={listening} expanded={expanded&&!remotePlaying} phone={phone} playOn={listeningPlayOn?()=>listeningPlayOn():undefined}/>}
    {binding && snapshot && <WatchScreen videoGesture={videoGesture} binding={binding} snapshot={snapshot} expanded={expanded && !remotePlaying} onCollapse={collapse} surfaceSlotRef={expandedSlot} onControlsVisibleChange={setControlsVisible}
      publishDockPanels={publishDockPanels} dockPanelsCurrent={dockPanelsCurrent} subscribeDockPanels={subscribeDockPanels}
      surfaces={drawnSurfaces} geometry={geometry} live={live}
      windowPort={windowPort} />}
    {cast && binding && visuals && <div className={styles.remote}><CastPlayback controller={cast.controller} visuals={visuals} phone={phone} anchorRef={castAnchor} reportError={binding.reportError} receivers={cast.receivers} linkTv={cast.linkTv} /></div>}
  </div>;
}
function playbackIdentityLabel(presentations: readonly DockPresentation[], key: string | undefined) { return presentations.find(value => fenceKey(value.fence) === key)?.identity.title ?? defaultI18n.t('next.dock.nowPlaying'); }
