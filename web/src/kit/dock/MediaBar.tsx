import {useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type PointerEvent} from 'react';
import {springTiming, springs} from '@design';
import {Menu} from '../overlays';
import {Control, Identity, Progress, StartingStop, TerminalActions, Transport, useDockActions} from './parts';
import {remoteTerminal} from './model';
import {dockText} from './text';
import {GestureRelease, PlayerMorph} from './motion';
import {signalDockMotion} from './geometry-event';
import {useDockHandover, useDockReducedMotion} from './use-motion';
import {MEDIA_BAR_DISMISS_MS, MEDIA_BAR_REDUCED_MS, MEDIA_BAR_SNAP_MS, dismissOffsets, dragOpacity, paintedOpacity, paintedX, releaseOpacity} from './media-bar-drag';
import type {VideoGesturePort} from './video-gesture-port';
import type {DockRendererProps} from './types';
import styles from './dock.module.css';

export function MediaBar(props: DockRendererProps) {
  const {view, binding, t = dockText, className, style} = props, root = useRef<HTMLElement | null>(null), anchor = useRef<HTMLButtonElement | null>(null), menuAnchor = useRef<HTMLSpanElement | null>(null);
  const [held, setHeld] = useState(false), [holding, setHolding] = useState(false), reduced = useDockReducedMotion(root), send = useDockActions({view, binding});
  const holdTimer = useRef<ReturnType<typeof setTimeout> | null>(null), animation = useRef<Animation | null>(null), suppressClick = useRef(false);
  const gesture = useRef<{id: number; x: number; y: number; lastX: number; lastY: number; time: number; vx: number; vy: number; dx: number; dy: number; moved: boolean; vertical?: VideoGesturePort; verticalActive?: boolean} | null>(null);
  const epoch = useRef(0);
  const movement = useRef(new PlayerMorph(0, springs.settle));
  useDockHandover(root, `${view.playback.fence.sessionId}:${view.playback.fence.generation}:${view.mode}`);
  const clearHold = () => { if (holdTimer.current) clearTimeout(holdTimer.current); holdTimer.current = null; setHolding(false); };
  useEffect(() => () => { if (holdTimer.current) clearTimeout(holdTimer.current); animation.current?.cancel(); }, []);
  useLayoutEffect(() => {
    epoch.current += 1; animation.current?.cancel(); animation.current = null; gesture.current?.vertical?.cancel(); gesture.current = null; clearHold();
    if (root.current) {root.current.style.transform = '';root.current.style.opacity = '';signalDockMotion(root.current, {kind: 'reset'});}
    setHeld(false);
  }, [view.playback.fence.sessionId, view.playback.fence.generation, view.mode]);
  useLayoutEffect(() => {
    if (!root.current) return;
    const element = root.current;
    signalDockMotion(element, {kind: 'hold', raised: held, scales: reduced ? [1] : held ? [.98, 1.02, 1] : holding ? [.98] : [1], duration: held && !reduced ? springTiming(springs.snap).durationMs : 0});
    if (!held || reduced) return;
    const job = root.current.animate([{transform: 'scale(.98)'}, {transform: 'scale(1.02)', offset: .3}, {transform: 'scale(1)'}], {duration: springTiming(springs.snap).durationMs, easing: 'ease-out'});
    return () => job.cancel();
  }, [held, holding, reduced]);
  const down = (event: PointerEvent<HTMLElement>) => {
    if (event.button !== 0 || held || view.starting || remoteTerminal(view.remote) || props.videoGesture && gesture.current) return;
    const element = event.currentTarget, painted = animation.current ? {position: paintedX(element), opacity: paintedOpacity(element)} : {position: 0, opacity: 1};
    animation.current?.cancel(); animation.current = null; element.style.transform = painted.position ? `translateX(${painted.position}px)` : ''; element.style.opacity = painted.opacity === 1 ? '' : String(painted.opacity); suppressClick.current = false;
    movement.current.track(painted.position, 0, performance.now());
    gesture.current = {id: event.pointerId, x: event.clientX - painted.position, y: event.clientY, lastX: event.clientX, lastY: event.clientY, time: event.timeStamp, vx: 0, vy: 0, dx: painted.position, dy: 0, moved: painted.position !== 0, vertical: props.videoGesture};
    if (event.target instanceof Element && (!event.target.closest('button,input,[role="slider"]') || event.target.closest('[data-dock-identity]'))) gesture.current.vertical?.begin(event, element, 1);
    event.currentTarget.setPointerCapture(event.pointerId);
    signalDockMotion(event.currentTarget, {kind: 'begin', offset: painted.position});
    holdTimer.current = setTimeout(() => { suppressClick.current = true; setHolding(false); props.videoGesture?.cancel(); setHeld(true); }, 500); setHolding(true);
  };
  const move = (event: PointerEvent<HTMLElement>) => {
    const g = gesture.current; if (!g || g.id !== event.pointerId || held) return;
    const dt = Math.max(1, event.timeStamp - g.time); g.vx = (event.clientX - g.lastX) * 1000 / dt; g.vy = (event.clientY - g.lastY) * 1000 / dt;
    g.lastX = event.clientX; g.lastY = event.clientY; g.time = event.timeStamp; g.dx = event.clientX - g.x; g.dy = event.clientY - g.y;
    if (Math.hypot(g.dx, g.dy) < 6 && !g.moved) return;
    g.moved = true; suppressClick.current = true; clearHold();
    if ((g.verticalActive || Math.abs(g.dy) > Math.abs(g.dx)) && g.vertical?.move(event)) {g.verticalActive = true;event.preventDefault();return;}
    if (Math.abs(g.dx) >= Math.abs(g.dy)) { g.vertical?.cancel(); event.currentTarget.setPointerCapture(event.pointerId); event.currentTarget.style.transform = `translateX(${g.dx}px)`; event.currentTarget.style.opacity = String(dragOpacity(g.dx, event.currentTarget.clientWidth)); signalDockMotion(event.currentTarget, {kind: 'track', offset: g.dx}); }
  };
  const release = (event: PointerEvent<HTMLElement>, cancelled = false) => {
    clearHold(); const g = gesture.current; gesture.current = null; if (!g || g.id !== event.pointerId) return;
    const reservedVertical = !!g.vertical && (g.verticalActive || Math.abs(g.dy) > Math.abs(g.dx));
    const handledVertical = g.vertical?.release(event, cancelled);
    // An installed original port owns refusal as well as admission. Its moved
    // vertical gesture can never borrow the legacy no-port expansion action.
    if (handledVertical || reservedVertical) {
      suppressClick.current = true;
      if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
      event.currentTarget.style.transform = '';event.currentTarget.style.opacity = '';
      signalDockMotion(event.currentTarget, {kind: 'reset'});return;
    }
    if (!g.moved) { event.currentTarget.style.transform = ''; event.currentTarget.style.opacity = ''; signalDockMotion(event.currentTarget, {kind: 'reset'}); return; }
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    if (!cancelled && Math.abs(g.dy) > Math.abs(g.dx) && (g.dy < -35 || g.vy < -600)) { event.currentTarget.style.transform = ''; event.currentTarget.style.opacity = ''; signalDockMotion(event.currentTarget, {kind: 'reset'}); send({kind: 'expand'}); return; }
    const vx = event.timeStamp - g.time > 80 ? 0 : g.vx;
    const element = event.currentTarget, layoutWidth = element.getBoundingClientRect().width, start = performance.now();
    const decision = cancelled ? {dismiss: false, target: 0, velocity: vx} : GestureRelease.dismiss(g.dx, vx, layoutWidth);
    const fromOpacity = paintedOpacity(element), out = decision.dismiss && !cancelled;
    let duration: number, offsets: number[], opacity: number[], keyframes: Keyframe[];
    if (reduced) {
      duration = MEDIA_BAR_REDUCED_MS; offsets = [g.dx, g.dx];
      opacity = [fromOpacity, out ? 0 : 1];
      keyframes = opacity.map(value => ({opacity: value}));
    } else if (out) {
      duration = MEDIA_BAR_DISMISS_MS; offsets = dismissOffsets(g.dx, decision.target);
      opacity = releaseOpacity(fromOpacity, 0);
      keyframes = offsets.map((offset, i) => ({transform: `translateX(${offset}px)`, opacity: opacity[i]}));
    } else {
      duration = MEDIA_BAR_SNAP_MS; movement.current.track(g.dx, decision.velocity, start); movement.current.retarget(decision.target, start, decision.velocity);
      offsets = Array.from({length: 34}, (_, i) => movement.current.sample(start + duration * i / 33).position);
      opacity = releaseOpacity(fromOpacity, 1, offsets.length);
      keyframes = offsets.map((offset, i) => ({transform: `translateX(${offset}px)`, opacity: opacity[i]}));
    }
    if (reduced) element.style.transform = '';
    const epochAtRelease = epoch.current;
    const job = element.animate(keyframes, {duration, easing: 'linear', fill: 'forwards'}); animation.current = job;
    signalDockMotion(element, {kind: 'release', offsets, opacity, duration});
    void job.finished.then(() => { if (animation.current !== job || epoch.current !== epochAtRelease) return; if (decision.dismiss) send({kind: 'stop'}); element.style.transform = ''; element.style.opacity = ''; signalDockMotion(element, {kind: 'reset'}); job.cancel(); animation.current = null; }).catch(() => {});
  };
  return <>
    <span ref={menuAnchor} className={styles.menuAnchor} style={style} aria-hidden="true" />
    {held && <div className={styles.phoneScrim} onClick={() => setHeld(false)} />}
    <section ref={root} className={[styles.phoneBar, held && styles.held, className].filter(Boolean).join(' ')} data-mode={view.mode} data-holding={holding || undefined} aria-label={t('next.dock.nowPlaying')} style={{...style, ...(props.videoGesture ? {touchAction: 'none'} : {})}}
      onPointerDown={down} onPointerMove={move} onPointerUp={event => release(event)} onPointerCancel={event => release(event, true)} onLostPointerCapture={event => {const g = gesture.current;if (g?.id === event.pointerId) {g.vertical?.cancel();gesture.current = null;clearHold();}}}
      onClickCapture={event => { if (suppressClick.current) { event.preventDefault(); event.stopPropagation(); suppressClick.current = false; } }}
      onContextMenu={event => { event.preventDefault(); clearHold(); if (!view.starting) setHeld(true); }}>
      <span className={styles.phoneIdentity} data-dock-identity inert={props.videoGestureActive || undefined}><Identity {...props} identityRef={anchor} phone /></span>
      {view.starting ? <StartingStop view={view} t={t} phone /> : remoteTerminal(view.remote) ? <TerminalActions {...props} phone /> : view.remote && view.remote.phase !== 'playing' ? <Control icon="i-stop" label={t('next.dock.stopConnecting')} onClick={() => send({kind: 'stop'})} /> : props.videoGesture ? <span style={{display: 'contents'}} inert={props.videoGestureActive || undefined}><Transport {...props} phone /></span> : <Transport {...props} phone />}
      {!view.starting && <Progress {...props} line />}
    </section>
    <Menu open={held && !view.playback.expanded} onOpenChange={open => setHeld(open)} title={t('next.dock.nowPlaying')} presentation="anchored" anchorRef={menuAnchor} returnFocusRef={anchor} placement="top-start" width={250} sideOffset={8} className={styles.holdMenu} items={[
      {id: 'open', label: t('next.dock.open'), icon: 'i-up', onSelect: () => send({kind: 'expand'})},
      ...(view.presentation.controls.includes('play-on') ? [{id: 'play-on', label: t('next.dock.playOn'), icon: 'i-cast' as const, onSelect: () => send({kind: 'play-on'})}] : []),
      {id: 'stop', label: t('next.dock.stop'), icon: 'i-stop', destructive: true, onSelect: () => send({kind: 'stop'})},
    ]} />
  </>;
}
