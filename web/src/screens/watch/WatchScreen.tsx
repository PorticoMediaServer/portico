import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import {playbackIdentity} from '@core/presentation/kit-model/index.ts';
import {sameFence, type PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import {Icon, type IconName} from '../../kit/Icon';
import {Artwork} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {PlayerChromeFrame, type PlayerForm} from '../../kit/frames';
import {PlayerControl, PlayerPill, PlayerTitle, PlayerVolume} from '../../kit/player';
import {Menu, PanelPresence, useLeavingPresence, type MenuEntry} from '../../kit/overlays';
import {KeyboardShortcuts} from '../../kit/states';
import {LossNotice, SeekLine} from '../../kit/media';
import {videoChapterOffer, videoChapters} from '@core/presentation/video-chapters.ts';
import {WatchChaptersDrawer} from './ChaptersDrawer';
import {dispatchWatch, watchClock, watchIsPortrait, watchLineValue, watchSeek, watchSeekBounds, watchShortcutTarget, watchStepIntent, type WatchIntent} from './model';
import {settledPlaybackChanged, settledPlaybackKey, type SettledPlayback} from './screen-state';
import type {WatchPanel, WatchRect, WatchScreenProps} from './types';
import {revealVideoControlsOnTab, videoControlsMayHide} from './video-controls-focus';
import {useReducedMotion} from '../../kit/overlays/shared';
import styles from './watch.module.css';
import {PlaybackWaiting, playbackWaiting} from '../../kit/states/PlaybackWaiting';
import {fenceKey} from '../../shell/playback-model';
import {PlaybackFailure} from '../../kit/states/PlaybackFailure';
import {ScrubPreview} from './ScrubPreview';
import {watchPreviewFrame} from './preview';
import {useCoarsePointer} from '../../kit/theme';
import {WatchPictureInput} from './picture';
import {watchQueueNeighbours} from './queue-neighbours';
import {WatchDockPanelOpening, type WatchDockPanel} from './dock-panel-opening';
import {PlayerEndCard as WatchEndCardView} from '../../kit/player/EndCard';
import {PlayerLive} from '../../kit/player/PlayerLive';
import {LivePage} from '../../kit/player/LivePage';
import {WatchPromptFocus, type PromptFocusHost} from './prompt-focus';
import {bufferedSegments} from './buffered-ranges';

const t = defaultI18n.t;
const subscribeWindow = (changed: () => void) => { window.addEventListener('resize', changed); return () => window.removeEventListener('resize', changed); };
const windowSize = () => `${window.innerWidth}:${window.innerHeight}`;
const serverSize = () => '1440:810';
const rectStyle = (rect: WatchRect | undefined): CSSProperties | undefined => rect ? {position: 'absolute', left: rect.left, top: rect.top, width: rect.width, height: rect.height, right: 'auto', bottom: 'auto'} : undefined;


/** The "Ends 4:48 PM" clock formatter, one per locale rather than one per progress tick. */
const endClocks = new Map<string, Intl.DateTimeFormat>();
const endClock = (locale: string) => {
  let format = endClocks.get(locale);
  if (!format) { format = new Intl.DateTimeFormat(locale, {hour: 'numeric', minute: '2-digit'}); endClocks.set(locale, format); }
  return format;
};

/** Resolve the next subtitle selection for the `C` shortcut (null turns subtitles off; string enables a track; undefined does nothing). */
export function watchSubtitleToggleTarget(snapshot: WatchScreenProps['snapshot'], rememberedId?: string): string | null | undefined {
  const fence = snapshot.fence, plan = snapshot.offers?.subtitlePlan;
  if (!fence || fence.kind !== 'queue' || !plan || plan.sessionId !== fence.sessionId || plan.generation !== fence.generation || plan.sourceId !== fence.sourceId) return undefined;
  if (plan.mode === 'track') return plan.offAvailable ? null : undefined;
  const candidates = plan.resources.filter(r => r.sourceId === fence.sourceId && !r.retired && r.enabled && r.discoveryId === undefined && Number.isSafeInteger(Number(r.offsetUs) / 1000));
  if (!candidates.length) return undefined;
  const preferred = (rememberedId ? candidates.find(r => r.id === rememberedId) : undefined)
    ?? candidates.find(r => r.default)
    ?? candidates[0];
  return preferred?.id;
}

/** Resolve the next subtitle timing offset (ms) for `G` (-100ms) / `H` (+100ms), or null when timing cannot change. */
export function watchSubtitleTimingNextMs(snapshot: WatchScreenProps['snapshot'], deltaMs: -100 | 100): number | null {
  const fence = snapshot.fence, plan = snapshot.offers?.subtitlePlan;
  if (!fence || fence.kind !== 'queue' || !plan || plan.sessionId !== fence.sessionId || plan.generation !== fence.generation || plan.sourceId !== fence.sourceId) return null;
  if (plan.mode !== 'track' || !plan.selected?.enabled) return null;
  const offsetUs = Number(plan.offsetUs);
  if (!Number.isSafeInteger(offsetUs)) return null;
  const baseMs = offsetUs / 1000;
  if (!Number.isSafeInteger(baseMs)) return null;
  const nextMs = baseMs + deltaMs;
  return Number.isSafeInteger(nextMs) && Math.abs(nextMs) <= 600000 ? nextMs : null;
}

/** Resolve the PageUp (-1) / PageDown (+1) chapter or queue step intent. */
export function watchChapterStepIntent(snapshot: WatchScreenProps['snapshot'], direction: -1 | 1): WatchIntent | null {
  const p = snapshot.presentation;
  if (!p) return null;
  const enabled = (kind: string) => p.controls.includes(kind as never);
  const chapters = videoChapters(snapshot.options?.chapters, p.durationSeconds, p.positionSeconds, number => String(number));
  if (chapters.length > 0 && enabled('seek')) {
    const idx = chapters.findIndex(chapter => chapter.current);
    const target = direction < 0
      ? (idx > 0 ? chapters[idx - 1]!.startSeconds : idx === 0 && p.positionSeconds > chapters[0]!.startSeconds ? chapters[0]!.startSeconds : undefined)
      : (idx >= 0 && idx + 1 < chapters.length ? chapters[idx + 1]!.startSeconds : idx < 0 ? chapters.find(c => c.startSeconds > p.positionSeconds)?.startSeconds : undefined);
    if (target !== undefined) {
      const clamped = watchSeek(snapshot, target);
      if (clamped !== null) return {kind: 'seek', seconds: clamped};
    }
  }
  const fallback = direction < 0 ? 'previous' : 'next';
  return enabled(fallback) ? {kind: fallback} : null;
}

/** Only drawn controls and geometry targets. The parent's MediaSurface remains its sole video. */
export function WatchScreen({binding, snapshot, expanded, onCollapse, onControlsVisibleChange, publishDockPanels, dockPanelsCurrent, subscribeDockPanels, surfaceSlotRef, surfaces, live, windowPort, geometry, videoGesture}: WatchScreenProps) {
  const size = useSyncExternalStore(subscribeWindow, windowSize, serverSize).split(':').map(Number), width = size[0]!, height = size[1]!;
  const coarsePointer = useCoarsePointer(), touchTablet = coarsePointer && width >= 600;
  const root = useRef<HTMLDivElement>(null), chrome = useRef<HTMLDivElement>(null), body = useRef<HTMLDivElement>(null), panelAnchor = useRef<HTMLButtonElement>(null), moreAnchor = useRef<HTMLButtonElement>(null);
  const gestureScreen = videoGesture?.screen;
  const attachRoot = useCallback((node: HTMLDivElement | null) => {root.current = node;if (gestureScreen) gestureScreen.current = node;}, [gestureScreen]);
  const gestureInput = useRef<{id: number; port: NonNullable<WatchScreenProps['videoGesture']>['port']} | undefined>(undefined);
  const gestureClick = useRef(false), gestureGate = useRef(false);
  const promptSeek = useRef<HTMLInputElement | null>(null);
  const bodyScroll = useRef<{identity?: string; top: number; left: number; anchor?: HTMLElement; offset?: number}>({top: 0, left: 0});
  const [panel, setPanel] = useState<WatchPanel | null>(null), [more, setMore] = useState(false), [shortcuts, setShortcuts] = useState(false), [visible, setVisible] = useState(true), [fullWindow, setFullWindow] = useState(false), [liveGuideOpen, setLiveGuideOpen] = useState(false);
  const [controlsExit, setControlsExit] = useState<'idle' | 'tap'>('idle');
  // motion.html 3.5/3.6: the controls arrive after the picture lands and leave (quick 120) as it starts to go.
  const [landing, setLanding] = useState(false), [leaving, setLeaving] = useState(false);
  const reducedMotion = useReducedMotion();
  type Scrub = {snapshot: WatchScreenProps['snapshot']; fence: PlaybackFence; seconds: number; pointerId: number};
  const [scrub, setScrub] = useState<Scrub | null>(null), [hoverSeconds, setHoverSeconds] = useState<number | null>(null);
  const scrubDrag = useRef<Scrub | null>(null);
  const liveDrag = useRef(false);
  const [pictureInput] = useState(() => new WatchPictureInput());
  const pointerKind = useRef('mouse');
  const [tapFeedback, setTapFeedback] = useState<{direction: 'left' | 'right'; seconds: number; x: number; y: number; sequence: number} | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [controlsFocus, requestControlsFocus] = useState<{backwards: boolean}>();
  const controlsVisible = useRef(visible);
  controlsVisible.current = visible;
  const lastLayout = useRef<string | null>(null);
  const current = useRef({binding, snapshot, expanded, gestureActive: !!videoGesture?.active});
  current.current = {binding, snapshot, expanded, gestureActive: !!videoGesture?.active};
  const presentation = snapshot.presentation, fence = snapshot.fence;
  const playbackKey = settledPlaybackKey(fence?.playback);
  const settledPlayback = useRef<SettledPlayback | undefined>(undefined);
  const lastSubtitle = useRef<{key: string; id: string} | null>(null);
  if (fence?.kind === 'queue' && snapshot.offers?.subtitlePlan?.mode === 'track' && snapshot.offers.subtitlePlan.selected?.id
    && snapshot.offers.subtitlePlan.sessionId === fence.sessionId && snapshot.offers.subtitlePlan.generation === fence.generation
    && snapshot.offers.subtitlePlan.sourceId === fence.sourceId) {
    lastSubtitle.current = {key: playbackKey, id: snapshot.offers.subtitlePlan.selected.id};
  }
  const sourceKey = fence ? JSON.stringify(fence) : '';
  const valid = !snapshot.disposed && !!presentation && !!fence && sameFence(fence, presentation.fence)
    && snapshot.authority === binding.runtime.getSnapshot().authority && sameFence(binding.runtime.getSnapshot().fence, fence);
  const liveChrome = live && fence && sameFence(live.fence, fence) && snapshot.authority === binding.runtime.getSnapshot().authority && live.isCurrent() ? live : undefined;
  const shapePortrait = watchIsPortrait(width, height);
  const mode = geometry?.mode ?? (!fullWindow && shapePortrait ? 'portrait' : 'full');
  const portrait = mode === 'portrait' || mode === 'book', permanent = mode === 'tabletop';
  const mobile = width < 1024;
  const bodyVisible = mode !== 'full';
  const externalPanel = surfaces?.playOn?.active === true;
  // Playback info f1: the film keeps playing with no player controls under the panel.
  const infoHidesChrome = panel === 'info' && !mobile;
  const open = expanded && valid && presentation.kind === 'video' && snapshot.dock.remote?.phase !== 'playing';
  // A-05: a closed panel (options, episodes, queue, playback info) keeps painting while it leaves.
  const panelPresence = useLeavingPresence(open ? panel : null);
  const openedBefore = useRef(open), leavingNow = useRef(false); leavingNow.current = leaving;
  const gesturePreview = !!videoGesture?.preview;
  // W02: the commit that closes the player already counts as leaving. The layout effect below sets `leaving`
  // after that commit, and a style flush in between would see the root hidden (display: none), which drops the
  // controls' opacity transition and paints the close with no fade.
  const closing = leaving || (!open && openedBefore.current);
  // The commit that shows the root already holds the controls back: the landing state below is
  // set after that commit, and a style read in between would paint them at full opacity and fade
  // them out (A-03). A closed root carries no visible controls either (a gesture preview keeps its own).
  const landingPending = open && !openedBefore.current && !leaving && !gesturePreview;
  const chromeVisible = (visible || permanent || liveGuideOpen) && (open || gesturePreview) && !infoHidesChrome && !landing && !landingPending && !leaving;
  useLayoutEffect(() => {
    if (open === openedBefore.current) return;
    openedBefore.current = open;
    // A reopen inside the close fade is a source-change blink, not a new opening: no landing hold.
    if (open) { const blink = leavingNow.current; setLeaving(false); if (!blink && !gesturePreview) setLanding(true); }
    else { setLanding(false); setLeaving(true); }
  }, [open, gesturePreview]);
  useEffect(() => { if (!landing) return; const timer = setTimeout(() => setLanding(false), reducedMotion ? 160 : 400); return () => clearTimeout(timer); }, [landing, reducedMotion]);
  useEffect(() => { if (!leaving) return; const timer = setTimeout(() => setLeaving(false), reducedMotion ? 160 : 120); return () => clearTimeout(timer); }, [leaving, reducedMotion]);
  useLayoutEffect(() => {
    const element = body.current;
    if (!element) return;
    if (bodyScroll.current.identity !== surfaces?.bodyIdentity) bodyScroll.current = {identity: surfaces?.bodyIdentity, top: 0, left: 0};
    if (!bodyVisible || !open) return;
    const saved = bodyScroll.current, anchor = saved.anchor;
    element.scrollTop = anchor?.isConnected && element.contains(anchor) && saved.offset !== undefined
      ? element.scrollTop + anchor.getBoundingClientRect().top - element.getBoundingClientRect().top - saved.offset : saved.top;
    element.scrollLeft = saved.left;
  }, [bodyVisible, mode, open, width, height, geometry?.revision, surfaces?.bodyIdentity]);
  const identity = presentation ? playbackIdentity(presentation.subject, 'full', binding.environment) : undefined;
  const can = (kind: string) => !!presentation?.controls.includes(kind as never);
  const send = useCallback((intent: WatchIntent) => {
    const value = current.current;
    if (!value.expanded || value.gestureActive) return;
    void dispatchWatch(value.binding.runtime, value.snapshot, intent).catch(value.binding.reportError);
  }, []);
  const effect = (action: () => void | Promise<void>) => {
    const value = current.current, live = value.binding.runtime.getSnapshot();
    if (!value.expanded || value.gestureActive || live.disposed || live.dock.remote?.phase === 'playing' || !value.snapshot.fence || !sameFence(live.fence, value.snapshot.fence) || live.authority !== value.snapshot.authority) return;
    try { void Promise.resolve(action()).catch(value.binding.reportError); } catch (error) { value.binding.reportError(error); }
  };
  const clearTimer = useCallback(() => { if (timer.current !== undefined) clearTimeout(timer.current); timer.current = undefined; }, []);
  const revealFor = useCallback((milliseconds: number) => {
    clearTimer(); setVisible(true);
    if (!open || permanent || panel || externalPanel || more || shortcuts || liveGuideOpen || scrubDrag.current || liveDrag.current || !presentation?.playing) return;
    timer.current = setTimeout(() => {
      if (!videoControlsMayHide(chrome.current, document.activeElement)) return;
      if (chrome.current?.contains(document.activeElement) && document.activeElement instanceof HTMLElement) document.activeElement.blur();
      setControlsExit('idle'); setVisible(false);
    }, milliseconds);
  }, [clearTimer, open, permanent, panel, externalPanel, more, shortcuts, liveGuideOpen, presentation?.playing]);
  const reveal = useCallback(() => revealFor(coarsePointer ? 4000 : 3000), [revealFor, coarsePointer]);
  useEffect(() => {
    const layout = `${width}:${height}:${geometry?.revision ?? ''}`, changed = lastLayout.current !== null && lastLayout.current !== layout;
    lastLayout.current = layout;
    // Q7 window/posture changes reveal for 3s; ordinary touch activity uses 4s.
    revealFor(changed ? 3000 : coarsePointer ? 4000 : 3000);
    return clearTimer;
  }, [revealFor, clearTimer, coarsePointer, width, height, geometry?.revision, sourceKey]);
  useEffect(() => { onControlsVisibleChange?.(open && chromeVisible); }, [onControlsVisibleChange, open, chromeVisible]);
  useLayoutEffect(() => {
    // A fence replacement blanks playback for a frame; only a settled new runtime or a new
    // playback run starts screen state over (a quality change in the same run keeps it).
    if (!settledPlaybackChanged(settledPlayback.current, binding.runtime, fence?.playback)) return;
    settledPlayback.current = {runtime: binding.runtime, key: playbackKey};
    setPanel(null); setMore(false); setShortcuts(false); setFullWindow(false); setVisible(true); setLiveGuideOpen(false);
  }, [binding.runtime, playbackKey, fence?.playback]);
  useEffect(() => {if (!open) setLiveGuideOpen(false);}, [open]);
  const expandedNow = useRef(expanded); expandedNow.current = expanded;
  useLayoutEffect(() => { pictureInput.cancel(); setTapFeedback(null); scrubDrag.current = null; setScrub(null); setHoverSeconds(null); }, [binding.runtime, snapshot.authority, sourceKey, pictureInput]);
  useEffect(() => () => pictureInput.cancel(), [pictureInput]);
  useLayoutEffect(() => { setFullWindow(false); }, [shapePortrait]);
  // A source change may blink expanded/open for a frame while the new stack starts.
  // Only a settled collapse clears the transient player chrome; the options panel is
  // owned by the playback identity effect above and survives a collapse blink.
  useEffect(() => {
    if (expanded) return;
    const timer = setTimeout(() => {
      if (expandedNow.current) return;
      pictureInput.cancel(); setTapFeedback(null); clearTimer(); setMore(false);
      setShortcuts(false); scrubDrag.current = null; setScrub(null); setHoverSeconds(null);
    }, 250);
    return () => clearTimeout(timer);
  }, [expanded, clearTimer, pictureInput]);
  useEffect(() => { if (panel || more || shortcuts) pictureInput.cancel(); }, [panel, more, shortcuts, pictureInput]);
  const showPanel = (next: WatchPanel, anchor?: HTMLButtonElement) => {
    if (next === 'chapters' ? !chapterOffer.drawer : !surfaces?.available.includes(next)) return;
    if (next === 'play-on' && surfaces?.playOn) {setMore(false);surfaces.playOn.open(anchor);reveal();return;}
    if (anchor) panelAnchor.current = anchor;
    setMore(false); setPanel(next); reveal();
  };
  const dockOwnerRef = useRef<WatchDockPanelOpening | undefined>(undefined);
  const [dockOwner, setDockOwner] = useState<WatchDockPanelOpening>();
  const [dockOpening, setDockOpening] = useState(0);
  const dockPanels = (surfaces?.available ?? []).filter((panel): panel is WatchDockPanel => panel === 'options' || panel === 'subtitles-audio');
  const dockPanelKey = dockPanels.join('|');
  const dockAdmitted = !!dockPanelsCurrent && dockPanelsCurrent() && !surfaces?.modal && !mobile && valid;
  // The panel opening keeps the fence, dock panels and showing port of the commit
  // that admitted it; a later render's closures cannot widen its authority.
  const dockAdmission = useRef({fence, dockPanels, showPanel});
  useLayoutEffect(() => {dockAdmission.current = {fence, dockPanels, showPanel};});
  useLayoutEffect(() => {
    const {fence, dockPanels, showPanel} = dockAdmission.current;
    if (!publishDockPanels || !dockPanelsCurrent || !dockAdmitted || !fence || presentation?.kind !== 'video' || !dockPanelKey) return;
    const owner = new WatchDockPanelOpening({binding, authority: snapshot.authority, fence, panels: dockPanels,
      snapshot: binding.runtime.getSnapshot, isCurrent: () => dockOwnerRef.current === owner && dockPanelsCurrent(),
      expand: () => binding.runtime.open(fence), show: next => showPanel(next),
      renew: () => setDockOpening(value => value + 1), reportError: binding.reportError});
    dockOwnerRef.current = owner;
    const off = binding.runtime.subscribe(owner.observe);
    const offSender = subscribeDockPanels?.(owner.observe) ?? (() => {});
    setDockOwner(owner);
    return () => {
      owner.dispose(); off(); offSender();
      if (dockOwnerRef.current === owner) dockOwnerRef.current = undefined;
      setDockOwner(value => value === owner ? undefined : value);
    };
  }, [binding, snapshot.authority, sourceKey, dockAdmitted, presentation?.kind, dockPanelKey, publishDockPanels, dockPanelsCurrent, subscribeDockPanels]);
  const drawnDockOwner = dockOwner && dockOwnerRef.current === dockOwner && dockOwner.owner.binding === binding
    && dockOwner.owner.authority === snapshot.authority && !!fence && sameFence(dockOwner.owner.fence, fence)
    && dockOwner.panels.join('|') === dockPanelKey && dockAdmitted ? dockOwner : undefined;
  useLayoutEffect(() => {
    if (!drawnDockOwner || expanded || !publishDockPanels) return;
    const offer = drawnDockOwner.offer();
    if (!offer) return;
    const unpublish = publishDockPanels(offer);
    return () => {offer.retire(); unpublish();};
  }, [drawnDockOwner, expanded, dockOpening, publishDockPanels]);
  useLayoutEffect(() => {if (expanded) drawnDockOwner?.commitExpanded();}, [drawnDockOwner, expanded]);
  const toggleFullscreen = () => {
    if (touchTablet) return;
    if (windowPort?.fullscreen) effect(windowPort.fullscreen.toggle);
    else if (width < 1024) { setFullWindow(value => !value); reveal(); }
  };
  const pictureTarget = () => ({runtime: binding.runtime, snapshot, reportError: binding.reportError,
    isActive: () => current.current.binding.runtime === binding.runtime && current.current.expanded && !current.current.gestureActive});
  const onKeydown = useRef<(event: KeyboardEvent) => void>(undefined);
  onKeydown.current = (event: KeyboardEvent) => {
    if (current.current.gestureActive) return;
    if (!panel && !externalPanel && !more && !shortcuts && !stillWatching && !watchShortcutTarget(event.target)
      && revealVideoControlsOnTab(event, chromeVisible, backwards => {reveal(); requestControlsFocus({backwards});})) return;
    if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey || watchShortcutTarget(event.target) || panel || externalPanel || more || shortcuts) return;
    if ((event.key === ' ' || event.key === 'Enter') && event.target instanceof Element && event.target.closest('button,a')) return;
    const step = liveChrome ? null : watchStepIntent(current.current.binding.runtime, current.current.snapshot, event.key);
    if (step) { event.preventDefault(); send(step); reveal(); return; }
    let intent: WatchIntent | undefined;
    const p = current.current.snapshot.presentation;
    if (!p) return;
    const key = event.key.toLowerCase(), enabled = (kind: string) => p.controls.includes(kind as never);
    if (key === ' ' || key === 'k') { const kind = p.finished ? 'replay' : p.playing ? 'pause' : 'play'; if (enabled(kind)) intent = {kind}; }
    else if (key === 'arrowleft' || key === 'j' || key === 'arrowright' || key === 'l') {
      const back = key === 'arrowleft' || key === 'j', position = watchSeek(current.current.snapshot, p.positionSeconds + (back ? -binding.skips.back : binding.skips.forward));
      if (position !== null) intent = {kind: 'seek', seconds: position};
    } else if (liveChrome && !liveGuideOpen && (key === 'arrowup' || key === 'arrowdown')) {
      event.preventDefault();
      if (key === 'arrowup' ? liveChrome.up : liveChrome.down) effect(() => liveChrome.tune(key === 'arrowup' ? 1 : -1));
      reveal(); return;
    } else if (!liveChrome && !coarsePointer && (key === 'arrowup' || key === 'arrowdown') && enabled('volume')) intent = {kind: 'volume', volume: Math.min(1, Math.max(0, p.volume + (key === 'arrowup' ? .05 : -.05)))};
    else if (!coarsePointer && key === 'm' && enabled('volume')) intent = {kind: 'mute'};
    else if (event.shiftKey && (key === 'n' || key === 'p') && enabled(key === 'n' ? 'next' : 'previous')) intent = {kind: key === 'n' ? 'next' : 'previous'};
    else if (!liveChrome && (key === 'pageup' || key === 'pagedown')) {
      const chapterIntent = watchChapterStepIntent(current.current.snapshot, key === 'pageup' ? -1 : 1);
      if (chapterIntent) intent = chapterIntent;
    } else if (key === 'c' && !event.shiftKey && fence?.kind === 'queue' && typeof current.current.binding.runtime.selectSubtitle === 'function') {
      const remembered = lastSubtitle.current?.key === playbackKey ? lastSubtitle.current.id : undefined;
      const target = watchSubtitleToggleTarget(current.current.snapshot, remembered);
      if (target !== undefined) {
        if (target === null && current.current.snapshot.offers?.subtitlePlan?.selected?.id) {
          lastSubtitle.current = {key: playbackKey, id: current.current.snapshot.offers.subtitlePlan.selected.id};
        }
        event.preventDefault();
        effect(() => current.current.binding.runtime.selectSubtitle(fence, target));
        reveal(); return;
      }
    } else if (liveChrome && key === 'g' && !event.shiftKey) {
      event.preventDefault();
      const buttons = [...(root.current?.querySelectorAll<HTMLButtonElement>('[data-live-chrome] button[aria-expanded]') ?? [])];
      const guideButton = buttons.find(button => button.textContent?.trim() === t('player.live.guide')) ?? buttons[0];
      guideButton?.click();
      reveal(); return;
    } else if (!liveChrome && (key === 'g' || key === 'h') && !event.shiftKey && fence?.kind === 'queue' && typeof current.current.binding.runtime.setSubtitleOffset === 'function') {
      const nextMs = watchSubtitleTimingNextMs(current.current.snapshot, key === 'g' ? -100 : 100);
      if (nextMs !== null) {
        event.preventDefault();
        effect(() => current.current.binding.runtime.setSubtitleOffset(fence, nextMs));
        reveal(); return;
      }
    } else if ((/^[0-9]$/.test(key) || key === 'home' || key === 'end') && enabled('seek')) {
      const bounds = watchSeekBounds(current.current.snapshot);
      if (bounds) intent = {kind: 'seek', seconds: key === 'home' ? bounds.start : key === 'end' ? bounds.end : bounds.start + (bounds.end - bounds.start) * Number(key) / 10};
    } else if (key === 'f') { event.preventDefault(); toggleFullscreen(); reveal(); return; }
    else if (key === 'i' && windowPort?.pictureInPicture) { event.preventDefault(); effect(windowPort.pictureInPicture.toggle); reveal(); return; }
    else if (key === 'z' && windowPort?.fit) { event.preventDefault(); effect(windowPort.fit.toggle); reveal(); return; }
    else if (key === '?') { event.preventDefault(); setShortcuts(true); reveal(); return; }
    else if (key === 'escape') { event.preventDefault(); if (fullWindow) setFullWindow(false); else if (windowPort?.fullscreen?.active) effect(windowPort.fullscreen.toggle); else onCollapse(); return; }
    if (intent) { event.preventDefault(); send(intent); reveal(); }
  };
  useEffect(() => {
    if (!open) return;
    const keydown = (event: KeyboardEvent) => onKeydown.current?.(event);
    window.addEventListener('keydown', keydown);
    return () => window.removeEventListener('keydown', keydown);
  }, [open]);
  useLayoutEffect(() => {
    if (!controlsFocus || !chromeVisible) return;
    const controls = [...chrome.current?.querySelectorAll<HTMLElement>('button:not(:disabled),[role="slider"][tabindex="0"]') ?? []]
      .filter(element => !element.closest('[inert],[hidden]') && element.getClientRects().length > 0);
    (controlsFocus.backwards ? controls.at(-1) : controls[0])?.focus({preventScroll: true});
    requestControlsFocus(undefined);
  }, [controlsFocus, chromeVisible]);
  const bounds = watchSeekBounds(snapshot), position = presentation?.positionSeconds ?? 0, duration = presentation?.durationSeconds ?? 0;
  const activeScrub = scrub && fence && sameFence(scrub.fence, fence) && scrub.snapshot.authority === snapshot.authority ? scrub : null;
  const drawnPosition = activeScrub?.seconds ?? position;
  // Every title's chapters are ticks on the line; only one without episodes also gets the Chapters drawer (INTEGRATION.md §7).
  const chapters = videoChapters(snapshot.options?.chapters, duration, position, number => t('next.watch.chapterNumber', {number}));
  const chapterOffer = videoChapterOffer(chapters, !!surfaces?.available.includes('episodes'), duration);
  const previewSeconds = activeScrub?.seconds ?? hoverSeconds;
  const buffered = bufferedSegments(fence ? binding.runtime.bufferedRanges(fence) : [], bounds);
  const hoverPercent = bounds && previewSeconds !== null ? Math.min(100, Math.max(0, (previewSeconds - bounds.start) / (bounds.end - bounds.start) * 100)) : null;
  const trickplay = surfaces?.trickplay;
  // W1-A2: the preview sheets are read when a scrub or hover first asks for a frame, never at start.
  const asking = previewSeconds !== null && open && chromeVisible;
  useEffect(() => {if (asking) trickplay?.prepare?.();}, [asking, trickplay]);
  const preview = open && chromeVisible && !panel && !more && !shortcuts ? watchPreviewFrame(snapshot, trickplay, previewSeconds) : null;
  const endsAt = presentation?.playing && duration > position && presentation.rate > 0
    ? endClock(binding.environment.locale ?? 'en-US').format(new Date(Date.now() + (duration - position) / presentation.rate * 1000)) : undefined;
  const mainKind = presentation?.finished ? 'replay' : presentation?.playing ? 'pause' : 'play';
  const episode = identity?.subtitle.some(part => part.id === 'episode-label');
  // desktop-player f1: Previous and Next follow the queue and name the title they would play.
  const neighbours = watchQueueNeighbours(snapshot);
  const neighbourLabel = (kind: 'previous' | 'next') => {
    if (episode) return t(kind === 'previous' ? 'next.dock.previousEpisode' : 'next.dock.nextEpisode');
    const named = neighbours[kind];
    return named ? t(kind === 'previous' ? 'next.dock.previousNamed' : 'next.dock.nextNamed', {title: named})
      : t(kind === 'previous' ? 'next.dock.previous' : 'next.dock.next');
  };
  const skip = (delta: number) => { const seconds = watchSeek(snapshot, position + delta); if (seconds !== null) send({kind: 'seek', seconds}); };
  const panelButton = (name: WatchPanel, icon: IconName, label: string, onPicture = false) => surfaces?.available.includes(name) ? <PlayerControl icon={icon} label={label} onPicture={onPicture} tool={!onPicture} open={panel === name} onPress={button => showPanel(name, button)} /> : null;
  const transport = <>
    <PlayerControl icon="i-prev" label={neighbourLabel('previous')} disabled={!can('previous')} onPress={() => send({kind: 'previous'})} />
    <PlayerControl icon={mobile ? 'i-rew' : 'i-back'} label={t('next.dock.back', {seconds: binding.skips.back})} skip={binding.skips.back} disabled={!can('seek')} onPress={() => skip(-binding.skips.back)} />
    <PlayerControl primary icon={mainKind === 'replay' ? 'i-back' : mainKind === 'pause' ? 'i-pause' : 'i-play'} label={t(mainKind === 'replay' ? 'next.dock.replay' : mainKind === 'pause' ? 'next.dock.pause' : 'next.dock.play')} disabled={!can(mainKind)} onPress={() => send({kind: mainKind})} />
    <PlayerControl icon={mobile ? 'i-ffw' : 'i-fwd'} label={t('next.dock.forward', {seconds: binding.skips.forward})} skip={binding.skips.forward} disabled={!can('seek') || presentation?.finished} onPress={() => skip(binding.skips.forward)} />
    <PlayerControl icon="i-next" label={neighbourLabel('next')} disabled={!can('next')} onPress={() => send({kind: 'next'})} />
  </>;
  // The three drawn forms (frame 7): an upright phone's strip, a short or full-window phone with the transport in the
  // picture, and the wide player. The player's own geometry decides the strip; the frame draws inside it.
  const form: PlayerForm = portrait ? 'upright' : mobile && (height <= 500 || width < 600) ? 'sideways' : 'wide';
  // The title's second line: the episode's number said first and brighter, then the rest (desktop-player: "S1 E2  Infected").
  const episodeLead = identity?.subtitle.find(part => part.id === 'episode-label')?.text;
  const subtitleRest = identity?.subtitle.filter(part => part.id !== 'episode-label').map(part => part.text).join('  ') || undefined;
  const quality = surfaces?.quality && fence && sameFence(surfaces.quality.fence, fence) ? surfaces.quality : undefined;
  // desktop-player f4b: the film-end card stands in for the transport once nothing follows.
  const endCard = surfaces?.end && presentation?.finished && snapshot.queueView?.next.available !== true ? surfaces.end : undefined;
  const tracks = surfaces?.tracks && fence && sameFence(surfaces.tracks.fence, fence) ? surfaces.tracks : undefined;
  const namedTrack = (section: 'audio' | 'subtitles', label: string | undefined) => tracks && label !== undefined && surfaces?.available.includes(section)
    ? <PlayerPill icon={section === 'subtitles' ? 'i-cc' : 'i-audio'} name={t(section === 'subtitles' ? 'player.opt.subtitles' : 'player.opt.audio')} value={label}
      label={`${t(section === 'subtitles' ? 'player.opt.subtitles' : 'player.opt.audio')} ${label}`} open={panel === section} onPress={button => showPanel(section, button)} /> : null;
  const qualityButton = quality && surfaces?.available.includes('quality')
    ? <PlayerPill icon={portrait ? 'i-quality' : undefined} qualifier={quality.automatic ? t('next.controls.auto') : undefined} value={quality.label} label={quality.accessibleLabel}
      open={panel === 'quality'} onPress={button => showPanel('quality', button)} /> : null;
  const menuItems: MenuEntry[] = [
    ...(surfaces?.available.includes('info') ? [{id: 'info', label: t('player.opt.info'), icon: 'i-info' as const, onSelect: () => showPanel('info', moreAnchor.current ?? undefined)}] : []),
    ...(windowPort?.fit ? [{id: 'fit', label: t(windowPort.fit.value === 'cover' ? 'web.player.fit.contain' : 'web.player.fit.fill'), icon: 'i-fith' as const, onSelect: () => effect(windowPort.fit!.toggle)}] : []),
    ...(surfaces?.more ?? []).map(item => ({id: item.id, label: item.label, onSelect: () => effect(item.action)})),
    ...(liveChrome ? [{id: 'live-recordings', label: t('guide.viewRecordings'), icon: 'i-rec' as const, onSelect: liveChrome.openRecordings}] : []),
    {id: 'shortcuts', label: t('player.shortcuts'), icon: 'i-kbd', onSelect: () => setShortcuts(true)},
  ];
  const liveRight = <>
    {!coarsePointer && can('volume') && <PlayerVolume value={presentation?.volume ?? 1} muted={!!presentation?.muted} label={t('player.volume')} muteLabel={t(presentation?.muted ? 'player.unmute' : 'player.mute')}
      onChange={volume => send({kind: 'volume', volume})} onMute={() => send({kind: 'mute'})} />}
    {panelButton('subtitles-audio', 'i-cc', t('next.dock.subtitlesAudio'))}
    {qualityButton}
    {windowPort?.pictureInPicture && <PlayerControl tool icon="i-pip" label={t('player.pictureInPicture')} active={windowPort.pictureInPicture.active} onPress={() => effect(windowPort.pictureInPicture!.toggle)} />}
    {!touchTablet && (portrait || !windowPort?.fullscreen || !mobile) && <PlayerControl tool icon="i-full" label={t('next.states.shortcut.fullscreen')} active={fullWindow || windowPort?.fullscreen?.active} onPress={toggleFullscreen} />}
  </>;
  const stillWatching = surfaces?.modal === 'still-watching';
  const gestureAllowed = valid && mobile && !panel && !externalPanel && !more && !shortcuts
    && !liveGuideOpen && !scrub && !stillWatching && !presentation?.finished && snapshot.playback.phase !== 'error';
  const gesturePort = videoGesture?.port;
  useLayoutEffect(() => {
    gestureGate.current = gestureAllowed && !scrubDrag.current;
    if (!gestureGate.current) gesturePort?.cancel();
  }, [gestureAllowed, gesturePort]);
  const admitGesture = videoGesture?.admit;
  useLayoutEffect(() => admitGesture?.(() => gestureGate.current), [admitGesture, binding, sourceKey]);

  // The runtime mints a fresh fence object every progress tick; prompt ownership
  // follows the fence's exact content, never that object identity.
  const promptFenceKey = fence ? fenceKey(fence) : '';
  const promptSource = useMemo<Omit<PromptFocusHost, 'source' | 'seekPresent' | 'focusAllowed' | 'controlsVisible'> | undefined>(() => { /* Preserve the original ownership/invalidation keys. */ void sourceKey;
    if (!promptFenceKey) return;
    const original = {binding, authority: snapshot.authority, fenceKey: promptFenceKey, intentId: snapshot.playback.intentId, run: snapshot.playback.playbackRun};
    return {root, seek: promptSeek, current: () => {
      try {
        const live = original.binding.runtime.getSnapshot();
        return !live.disposed && live.authority === original.authority && !!live.fence && fenceKey(live.fence) === original.fenceKey
          && live.playback.intentId === original.intentId && live.playback.playbackRun === original.run
          && live.presentation?.kind === 'video' && !!live.presentation.fence && fenceKey(live.presentation.fence) === original.fenceKey
          && live.dock.local?.expanded === true && !!live.dock.local.fence && fenceKey(live.dock.local.fence) === original.fenceKey && live.dock.remote?.phase !== 'playing';
      } catch {return false;}
    }};
  }, [binding, snapshot.authority, sourceKey, promptFenceKey, snapshot.playback.intentId, snapshot.playback.playbackRun]);
  // While playback has failed only the transport gives way (states.html frames 8 and 10): nothing can play until the
  // viewer chooses, and the failure's words and Try again take its place. The top and bottom rows stay.
  const playbackFailed = snapshot.playback.phase === 'error';
  const seekPresent = !endCard && chromeVisible && !!bounds, gestureInactive = !videoGesture?.active;
  const promptFocusAllowed = gestureInactive && !panel && !externalPanel && !more && !shortcuts && !stillWatching;
  const promptHost = useMemo<PromptFocusHost | undefined>(() => promptSource ? {...promptSource, source: promptSource, seekPresent,
    controlsVisible: chromeVisible, focusAllowed: promptFocusAllowed} : undefined,
  [promptSource, seekPresent, chromeVisible, promptFocusAllowed]);
  return <div ref={attachRoot} className={styles.watch} hidden={!open && !closing && !videoGesture?.preview} inert={!open || undefined} aria-hidden={!open || undefined} data-video-preview={!open && videoGesture?.preview || undefined} tabIndex={-1} data-watch-screen data-mode={mode} data-still-watching={stillWatching || undefined} data-touch-expanded={coarsePointer && width >= 1024 || undefined} data-controls-visible={chromeVisible} data-live-guide-open={liveGuideOpen || undefined} data-controls-exit={closing ? 'close' : controlsExit} data-panel-open={!!panel} data-scrubbing={!!activeScrub && mobile}
    onBlurCapture={event => {if (!chrome.current?.contains(event.relatedTarget as Node | null)) reveal();}}
    onPointerMove={event => { if (event.pointerType === 'mouse') reveal(); }} onFocusCapture={reveal}>
    <div ref={surfaceSlotRef} className={styles.pictureSlot} style={rectStyle(geometry?.picture)} data-watch-picture-slot data-media-slot="expanded" aria-hidden="true" />
    <div ref={chrome} data-video-gesture-copy className={styles.chrome} style={rectStyle(geometry?.controls)} inert={stillWatching || undefined}>
      {open && snapshot.playback.phase === 'error' && <PlaybackFailure key={playbackKey} phone={mobile} portrait={portrait} owner={surfaces?.failureOwner} error={snapshot.playback.error}
        onRetry={fence && binding.runtime.canRetryFailure(fence) ? () => binding.runtime.retryFailure(fence) : undefined}
        onVersions={surfaces?.available.includes('versions') ? () => showPanel('versions') : undefined}
        onReport={surfaces?.more?.find(action => action.id === 'report-problem') ? () => effect(() => surfaces.more!.find(action => action.id === 'report-problem')!.action()) : undefined}
        closeLabel={surfaces?.convertingOwnerAction ? t('web.player.error.turnOnConverting') : undefined}
        onClose={surfaces?.convertingOwnerAction ?? onCollapse} onError={binding.reportError}>{surfaces?.streamLimit}</PlaybackFailure>}
      <PlaybackWaiting key={playbackKey} {...playbackWaiting(open, snapshot)}/>
      <button type="button" className={styles.pictureTap} style={videoGesture?.port ? {touchAction: 'none'} : undefined} aria-label={t('player.stageLabel', {title: identity?.title ?? ''})}
        onPointerDown={event => {
          if (gestureInput.current) return;
          pointerKind.current = event.pointerType;event.preventDefault();gestureClick.current = false;
          const port = videoGesture?.port;
          if (gestureGate.current && port?.begin(event, event.currentTarget, 0)) {
            gestureInput.current = {id: event.pointerId, port};event.currentTarget.setPointerCapture(event.pointerId);
          }
        }}
        onPointerMove={event => {const g = gestureInput.current;if (g?.id === event.pointerId && g.port?.move(event)) {
          gestureClick.current = true;pictureInput.cancel();event.preventDefault();
        }}}
        onPointerUp={event => {const g = gestureInput.current;if (g?.id !== event.pointerId) return;gestureInput.current = undefined;
          if (g.port?.release(event)) gestureClick.current = true;
          if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
        }}
        onLostPointerCapture={event => {const g = gestureInput.current;if (g?.id === event.pointerId) {g.port?.cancel();gestureInput.current = undefined;gestureClick.current = true;}}}
        onPointerCancel={event => {const g = gestureInput.current;if (g?.id === event.pointerId) {g.port?.release(event, true);gestureInput.current = undefined;gestureClick.current = true;}pictureInput.cancel();}}
        onClick={event => {
          if (gestureClick.current || videoGesture?.active) {gestureClick.current = false;event.preventDefault();return;}
          if (externalPanel) {pictureInput.cancel();surfaces?.playOn?.close();return;}
          if (panel) { pictureInput.cancel(); setPanel(null); return; }
          if (more || shortcuts) return;
          const target = pictureTarget();
          if (event.detail === 0 || pointerKind.current === 'mouse') { pictureInput.mouse(target, event.detail); return; }
          const rect = event.currentTarget.getBoundingClientRect();
          const x = rect.width > 0 ? (event.clientX - rect.left) / rect.width : .5, y = rect.height > 0 ? (event.clientY - rect.top) / rect.height : .5;
          pictureInput.tap(target, {zone: x < 1 / 3 ? 'left' : x > 2 / 3 ? 'right' : 'middle', tablet: touchTablet,
            back: binding.skips.back, forward: binding.skips.forward,
            single: () => {
              if (!controlsVisible.current) { controlsVisible.current = true; reveal(); }
              else if (presentation?.playing && !permanent && videoControlsMayHide(chrome.current, document.activeElement)) {
                if (chrome.current?.contains(document.activeElement) && document.activeElement instanceof HTMLElement) document.activeElement.blur();
                controlsVisible.current = false; clearTimer(); setControlsExit('tap'); setVisible(false);
              }
            },
            fullscreen: toggleFullscreen,
            feedback: (direction, seconds) => setTapFeedback(value => ({direction, seconds, x, y, sequence: (value?.sequence ?? 0) + 1})),
          });
        }}
        onDoubleClick={() => { if (pointerKind.current === 'mouse') pictureInput.doubleClick(pictureTarget(), toggleFullscreen); }}
        onWheel={event => { if (!coarsePointer && !panel && !more && !shortcuts) pictureInput.wheel(pictureTarget(), event.deltaY); }} />
      {tapFeedback && <div key={tapFeedback.sequence} className={styles.tapFeedback} data-direction={tapFeedback.direction} aria-hidden="true" onAnimationEnd={() => setTapFeedback(null)}>
        <span className={styles.tapDot} style={{left: `${(tapFeedback.x - (tapFeedback.direction === 'right' ? .56 : 0)) / .44 * 100}%`, top: `${tapFeedback.y * 100}%`}} />
        <Icon name={tapFeedback.direction === 'left' ? 'i-back' : 'i-fwd'} size={40} />
        <span>{t('duration.long.seconds', {seconds: tapFeedback.seconds})}</span>
      </div>}
      {liveChrome ? (open || closing) && <div className={styles.liveOverlay} data-visible={chromeVisible} inert={!chromeVisible || videoGesture?.active || undefined}>
        <PlayerLive port={liveChrome} playing={!!presentation?.playing} onCollapse={onCollapse} onStop={() => send({kind: 'stop'})}
          onPlayPause={() => send({kind: mainKind})} onSkip={skip} onSeek={seconds => {if (can('seek')) send({kind: 'seek', seconds});}}
          onMore={anchor => {moreAnchor.current = anchor;setPanel(null);setMore(value => !value);}}
          right={liveRight} onMiniGuideOpenChange={setLiveGuideOpen}
          onDragChange={dragging => {liveDrag.current = dragging; if (dragging) {clearTimer(); setVisible(true);} else reveal();}} canPlayPause={can(mainKind)} canSeek={can('seek')} reportError={binding.reportError}/>
      </div> : <>
        {/* desktop-player f4b: the end card stands on its veil (over the film's blurred backdrop when it has one); the frame's
            own scrim gives way while a card covers the stage. */}
        {endCard && <>{endCard.backdrop && <div className={styles.endAmbient} aria-hidden="true"><Artwork source={endCard.backdrop} alt="" width="100%" height="100%" /></div>}<div className={styles.endVeil} aria-hidden="true" /></>}
        {/* frame 7: the chrome is the kit's PlayerChromeFrame over the retained video (the picture and the watch page under an
            upright phone's strip stay where the player's geometry puts them; the frame fills the chrome's box). */}
        <PlayerChromeFrame fill form={form} picture={null} chromeVisible={chromeVisible && !stillWatching && !videoGesture?.active} scrubbing={!!activeScrub && mobile}
          close={<PlayerControl onPicture icon="i-down" label={t('player.minimize')} onPress={onCollapse} />}
          title={identity && <span className={styles.titleRow}><PlayerTitle title={identity.title} lead={episodeLead} subtitle={subtitleRest} /><span className={styles.together}>{surfaces?.together}</span></span>}
          tools={<>
            {!endCard && panelButton('play-on', 'i-cast', t('player.playOn'), true)}
            {mobile && windowPort?.pictureInPicture && <PlayerControl onPicture icon="i-pip" label={t('player.pictureInPicture')} onPress={() => effect(windowPort.pictureInPicture!.toggle)} active={windowPort.pictureInPicture.active} />}
            {portrait && panelButton('subtitles-audio', 'i-cc', t('next.dock.subtitlesAudio'), true)}
            {mobile && panelButton('options', 'i-gear', t('next.dock.options'), true)}
            {!mobile && !endCard && <PlayerControl onPicture icon="i-more" label={t('player.more')} active={more} onPress={button => { moreAnchor.current = button; setPanel(null); setMore(value => !value); }} />}
            {!mobile && <PlayerControl onPicture icon="i-close" label={t('player.close')} onPress={() => send({kind: 'stop'})} />}
          </>}
          scrubber={<SeekLine variant="player" dir="ltr" inputRef={promptSeek}
            value={watchLineValue(bounds, drawnPosition, duration)}
            buffered={buffered.map(range => ({start: range.left / 100, end: (range.left + range.width) / 100}))}
            hover={hoverPercent === null ? null : hoverPercent / 100}
            hoverLabel={!preview && previewSeconds !== null ? watchClock(previewSeconds) : undefined}
            marks={duration > 0 ? [...(presentation?.policy?.markers ?? []).map(marker => ({id: marker.id, kind: marker.kind, start: marker.startSeconds / duration, end: marker.endSeconds / duration, ...(marker.kind === 'credits' ? {fromEnd: true} : {})})),
              ...chapterOffer.ticks] : []}
            overlay={preview && bounds && previewSeconds !== null ? <ScrubPreview frame={preview.frame} source={preview.source} seconds={previewSeconds} phone={mobile}
              percent={(previewSeconds - bounds.start) / (bounds.end - bounds.start) * 100} /> : undefined}
            input={{tabIndex: 0, min: bounds?.start ?? 0,
              max: bounds?.end ?? (duration > 0 ? duration : 1),
              step: 1,
              value: bounds ? Math.min(bounds.end, Math.max(bounds.start, drawnPosition)) : duration > 0 ? Math.min(duration, drawnPosition) : 0,
              disabled: !bounds,
              'aria-label': t('player.position'),
              'aria-valuetext': t('player.clockOf', {position: watchClock(drawnPosition), duration: watchClock(duration)}),
              onPointerDown: event => {
                  if (scrubDrag.current || !bounds || !fence) return;
                  const next = {snapshot, fence, seconds: drawnPosition, pointerId: event.pointerId};
                  scrubDrag.current = next; setScrub(next); clearTimer(); setVisible(true);
                  event.currentTarget.setPointerCapture(event.pointerId);
                },
              onPointerMove: event => {
                  if (coarsePointer || scrubDrag.current || event.pointerType !== 'mouse' || !bounds) return;
                  const rect = event.currentTarget.getBoundingClientRect();
                  if (rect.width > 0) setHoverSeconds(watchSeek(snapshot, bounds.start + (event.clientX - rect.left) / rect.width * (bounds.end - bounds.start)));
                },
              onPointerLeave: () => { if (!scrubDrag.current) setHoverSeconds(null); },
              onPointerUp: event => {
                  const captured = scrubDrag.current;
                  if (!captured || captured.pointerId !== event.pointerId) return;
                  const seconds = watchSeek(captured.snapshot, event.currentTarget.valueAsNumber), value = current.current, live = value.binding.runtime.getSnapshot();
                  scrubDrag.current = null; setScrub(null); setHoverSeconds(null); reveal();
                  if (seconds !== null && value.expanded && live.fence && sameFence(live.fence, captured.fence) && live.authority === captured.snapshot.authority && live.dock.remote?.phase !== 'playing') {
                    void dispatchWatch(value.binding.runtime, captured.snapshot, {kind: 'seek', seconds}).catch(value.binding.reportError);
                  }
                },
              onPointerCancel: () => { scrubDrag.current = null; setScrub(null); setHoverSeconds(null); reveal(); },
              onBlur: () => { if (!scrubDrag.current) setHoverSeconds(null); },
              onChange: event => {
                  const captured = scrubDrag.current, seconds = watchSeek(captured?.snapshot ?? snapshot, event.currentTarget.valueAsNumber);
                  if (seconds === null) return;
                  if (captured) { const next = {...captured, seconds}; scrubDrag.current = next; setScrub(next); }
                  else { setHoverSeconds(seconds); send({kind: 'seek', seconds}); }
                }}} />}
          times={{position: watchClock(drawnPosition), ...(duration > 0 ? {remaining: `−${watchClock(Math.max(0, duration - drawnPosition))}`, duration: watchClock(duration)} : {}),
            ...(!mobile && endsAt ? {endsAt: t('guide.ends', {time: endsAt})} : {})}}
          transport={playbackFailed || endCard ? null : transport}
          leading={surfaces?.available.includes('episodes')
            ? <Button size="small" variant="glass" icon="i-stack" onClick={event => showPanel('episodes', event.currentTarget)}>{t('next.controls.episodes')}</Button>
            : <>{chapterOffer.drawer && <Button size="small" variant="glass" icon="i-list" onClick={event => showPanel('chapters', event.currentTarget)}>{t('player.opt.chapters')}</Button>}
              {surfaces?.available.includes('queue') && <Button size="small" variant="glass" icon="i-queue" onClick={event => showPanel('queue', event.currentTarget)}>{t('player.queue')}</Button>}</>}
          trailing={<>
            {!coarsePointer && can('volume') && <PlayerVolume value={presentation?.volume ?? 1} muted={!!presentation?.muted} label={t('player.volume')} muteLabel={t(presentation?.muted ? 'player.unmute' : 'player.mute')}
              onChange={volume => send({kind: 'volume', volume})} onMute={() => send({kind: 'mute'})} />}
            {!portrait && !mobile && panelButton('subtitles-audio', 'i-cc', t('next.dock.subtitlesAudio'))}
            {!portrait && mobile && <>{namedTrack('subtitles', tracks?.subtitles)}{namedTrack('audio', tracks?.audio)}</>}
            {qualityButton}
            {!mobile && surfaces?.available.includes('speed') && <PlayerPill value={t('next.dock.rate', {rate: presentation?.rate ?? 1})} label={t('next.dock.speed', {rate: presentation?.rate ?? 1})} open={panel === 'speed'} onPress={button => showPanel('speed', button)} />}
            {!mobile && windowPort?.pictureInPicture && <PlayerControl tool icon="i-pip" label={t('player.pictureInPicture')} active={windowPort.pictureInPicture.active} onPress={() => effect(windowPort.pictureInPicture!.toggle)} />}
            {!touchTablet && (portrait || !windowPort?.fullscreen || !mobile) && <PlayerControl tool icon="i-full" label={t('next.states.shortcut.fullscreen')} active={fullWindow || windowPort?.fullscreen?.active} onPress={toggleFullscreen} />}
          </>}
          card={endCard ? <WatchEndCardView card={endCard} canReplay={can('replay')} onReplay={() => send({kind: 'replay'})} onClose={() => send({kind: 'stop'})} layout={mobile ? 'phone' : 'desktop'} reportError={binding.reportError}
            artwork={url => binding.artwork.foreground(url, 512, 16 / 9, {group: 'playback', priority: 'high'})} /> : undefined} />
      </>}
    </div>
    {open && surfaces?.loss && <LossNotice notice={surfaces.loss.notice} enabled={surfaces.loss.enabled} layout={mobile ? 'phone' : 'desktop'} />}
    <div className={styles.notices}>{open && surfaces?.notices}</div>
    <div className={styles.prompts} inert={videoGesture?.active || undefined}><WatchPromptFocus.Provider value={open ? promptHost : undefined}>{open && surfaces?.prompts}</WatchPromptFocus.Provider></div>
    <div ref={body} data-video-gesture-copy inert={videoGesture?.active || undefined} className={styles.body} style={rectStyle(geometry?.body)} data-watch-retained-body onScroll={event => {
      if (!bodyVisible || !open) return;
      const element = event.currentTarget, top = element.getBoundingClientRect().top;
      const anchor = Array.from(element.querySelectorAll<HTMLElement>('[data-episode-id],[data-entry-id],[data-item-id],h2,h3')).find(value => value.getBoundingClientRect().bottom > top);
      bodyScroll.current = {identity: surfaces?.bodyIdentity, top: element.scrollTop, left: element.scrollLeft, anchor, offset: anchor ? anchor.getBoundingClientRect().top - top : undefined};
    }}>
      {/* A live channel has no title page: under an upright picture the watch page is the channel's (the owner's 18 Nov ruling). */}
      {surfaces?.body ?? (liveChrome && <LivePage port={liveChrome} reportError={binding.reportError}/>)}
    </div>
    {panelPresence.shown === 'chapters' ? <PanelPresence leaving={panelPresence.leaving}><WatchChaptersDrawer chapters={chapters} onSeek={seconds => send({kind: 'seek', seconds})} onClose={() => setPanel(null)} /></PanelPresence>
      : surfaces && panelPresence.shown && <PanelPresence leaving={panelPresence.leaving}>{surfaces.render({panel: panelPresence.shown, snapshot, available: surfaces.available, reportProblem: surfaces.more?.find(action => action.id === 'report-problem'), layout: width < 1024 ? 'phone' : 'desktop', landscape: width < 1024 && width > height, phoneTop: portrait ? geometry?.picture ? geometry.picture.top + geometry.picture.height : width * 9 / 16 + (window.visualViewport?.offsetTop ?? 0) : 0, close: () => setPanel(null), show: next => showPanel(next), returnFocusRef: panelAnchor})}</PanelPresence>}
    <Menu className={styles.playerMenu} open={more} onOpenChange={setMore} title={t('player.more')} anchorRef={moreAnchor} returnFocusRef={moreAnchor} presentation="anchored" width={300} items={menuItems} />
    <KeyboardShortcuts open={shortcuts} onOpenChange={setShortcuts} skipBackSeconds={binding.skips.back} skipForwardSeconds={binding.skips.forward} returnFocusRef={moreAnchor} />
  </div>;
}
