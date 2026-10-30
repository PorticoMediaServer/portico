import {useOverlayPortalTarget} from './portal-target';
import {useCallback, useEffect, useLayoutEffect, useRef, type ReactNode} from 'react';
import {createPortal} from 'react-dom';
import {springs} from '@design';
import {Button} from '../controls/Button';
import {Icon, type IconName} from '../Icon';
import {dismissToast, springFrames} from './model';
import {classes, usePresence, useReducedMotion, useSurfaceMotion} from './shared';
import type {OverlayChange, OverlayDismissReason, OverlayStyle} from './types';
import styles from './overlays.module.css';

export type OverlayAction = Readonly<{label: ReactNode; onAction: () => void; icon?: IconName; accessibilityLabel?: string}>;
export type ToastAction = OverlayAction & Readonly<{variant?: 'glass' | 'text'}>;
export type ToastData = Readonly<{id: string; message: ReactNode; action?: ToastAction; icon?: IconName; artwork?: ReactNode; tone?: 'default' | 'success' | 'warning' | 'error'; durationMs?: number}>;
export type ToastProps = Omit<ToastData, 'id'> & {open: boolean; onOpenChange: OverlayChange; className?: string; testID?: string};

/** One notification surface. The host supplies replacement, placement and lifetime. */
export function Toast({open, onOpenChange, message, action, icon, artwork, tone = 'default', className, testID}: ToastProps) {
  const exitTransform = useRef<string | null>(null), motionRef = useSurfaceMotion(open, 'toast', exitTransform), elementRef = useRef<HTMLDivElement | null>(null), animation = useRef<Animation | null>(null);
  const ref = useCallback((element: HTMLDivElement | null) => { elementRef.current = element; motionRef(element); }, [motionRef]);
  const drag = useRef<{pointer: number; startX: number; startY: number; baseX: number; baseY: number; x: number; y: number; time: number; velocityX: number; velocityY: number} | null>(null);
  const presence = usePresence(open, 160);
  useEffect(() => () => animation.current?.cancel(), []);
  if (!presence) return null;
  return <div ref={ref} role="status" aria-live="polite" aria-atomic="true" className={classes(styles.toast, className)} data-tone={tone} data-testid={testID} data-state={open ? 'open' : 'closed'}
    onPointerDown={event => {
      if (event.button !== 0 || (event.target as HTMLElement).closest('button,a')) return;
      const painted = getComputedStyle(event.currentTarget), matrix = painted.transform === 'none' ? new DOMMatrixReadOnly() : new DOMMatrixReadOnly(painted.transform);
      const opacity = painted.opacity;
      animation.current?.cancel(); event.currentTarget.getAnimations().forEach(value => value.cancel());
      event.currentTarget.style.transform = `translate(${matrix.m41}px,${matrix.m42}px)`; event.currentTarget.style.opacity = opacity; exitTransform.current = null;
      drag.current = {pointer: event.pointerId, startX: event.clientX, startY: event.clientY, baseX: matrix.m41, baseY: matrix.m42, x: event.clientX, y: event.clientY, time: event.timeStamp, velocityX: 0, velocityY: 0};
      event.currentTarget.setPointerCapture(event.pointerId);
    }} onPointerMove={event => {
      const current = drag.current; if (!current || current.pointer !== event.pointerId) return;
      const dt = event.timeStamp - current.time;
      if (dt > 0) { current.velocityX = (event.clientX - current.x) / dt * 1000; current.velocityY = (event.clientY - current.y) / dt * 1000; }
      current.x = event.clientX; current.y = event.clientY; current.time = event.timeStamp;
      event.currentTarget.style.transform = `translate(${current.baseX + event.clientX - current.startX}px,${current.baseY + Math.max(0, event.clientY - current.startY)}px)`;
    }} onPointerUp={event => {
      const current = drag.current; if (!current || current.pointer !== event.pointerId) return;
      drag.current = null;
      const x = current.baseX + event.clientX - current.startX, y = current.baseY + Math.max(0, event.clientY - current.startY), paused = event.timeStamp - current.time > 100;
      if (dismissToast(x, y, paused ? 0 : current.velocityX, paused ? 0 : current.velocityY)) {
        const horizontal = Math.abs(x) > y;
        const exitX = x + (horizontal ? Math.sign(x || current.velocityX) * Math.max(80, Math.abs(current.velocityX) * .12) : 0);
        const exitY = y + (horizontal ? 0 : Math.max(80, current.velocityY * .12));
        exitTransform.current = `translate(${exitX}px,${exitY}px)`; onOpenChange(false, 'gesture');
      }
      else {
        const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches || !!event.currentTarget.closest('[data-reduce-motion="true"]');
        const xs = springFrames(x, 0, current.velocityX, springs.snap, 371), ys = springFrames(y, 0, current.velocityY, springs.snap, 371);
        if (!reduced) animation.current = event.currentTarget.animate(xs.map((value, index) => ({transform: `translate(${value}px,${ys[index]}px)`})), {duration: 371, easing: 'linear'});
        event.currentTarget.style.transform = 'none'; event.currentTarget.style.opacity = '1';
      }
      if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    }} onPointerCancel={event => { drag.current = null; event.currentTarget.style.transform = 'none'; event.currentTarget.style.opacity = '1'; }} onLostPointerCapture={event => { if (drag.current) { drag.current = null; event.currentTarget.style.transform = 'none'; } }}>
    {artwork && <span className={styles.toastArtwork}>{artwork}</span>}{icon && <Icon name={icon} size={18} className={styles.feedbackIcon} />}
    <span className={styles.toastMessage}>{message}</span>
    {action && <Button variant={action.variant ?? 'glass'} size="xs" icon={action.icon} iconSize={16} aria-label={action.accessibilityLabel} className={styles.toastAction} onClick={() => { action.onAction(); onOpenChange(false, 'action'); }}>{action.label}</Button>}
  </div>;
}

export type ToastHostProps = {toast: ToastData | null; onDismiss: (id: string, reason: OverlayDismissReason) => void; bottomInset?: number; className?: string; toastClassName?: string; testID?: string};
/** Controlled single occupant: callers replace `toast`; no global queue or business state. */
export function ToastHost({toast, onDismiss, bottomInset, className, toastClassName, testID}: ToastHostProps) {
  const portalTarget = useOverlayPortalTarget();
  const current = useRef(toast), last = useRef(toast), dismiss = useRef(onDismiss), host = useRef<HTMLDivElement | null>(null);
  const replacement = useRef<Animation | null>(null), reduced = useReducedMotion(host);
  const clock = useRef<{pause: (source: string) => void; resume: (source: string) => void} | null>(null);
  current.current = toast; dismiss.current = onDismiss; if (toast) last.current = toast;
  const id = toast?.id, hasAction = !!toast?.action;
  const duration = toast?.durationMs !== undefined && Number.isFinite(toast.durationMs) && toast.durationMs > 0 ? toast.durationMs : hasAction ? 8000 : 4000;
  useEffect(() => {
    let timer: number | undefined, remaining = duration, deadline = 0;
    const pauses = new Set<string>();
    const clear = () => { if (timer !== undefined) window.clearTimeout(timer); timer = undefined; };
    const start = () => { clear(); if (!current.current || pauses.size) return; deadline = performance.now() + remaining; timer = window.setTimeout(() => { const value = current.current; if (value) dismiss.current(value.id, 'action'); }, remaining); };
    const pause = (source: string) => { if (!pauses.size) { remaining = Math.max(0, deadline - performance.now()); clear(); } pauses.add(source); };
    const resume = (source: string) => { if (!pauses.delete(source)) return; if (!pauses.size) start(); };
    if (document.hidden) pauses.add('hidden');
    if (host.current?.matches(':hover')) pauses.add('hover');
    if (host.current?.contains(document.activeElement)) pauses.add('focus');
    clock.current = {pause, resume};
    start();
    const visibility = () => document.hidden ? pause('hidden') : resume('hidden'), release = () => resume('touch'), blur = () => pause('window'), focus = () => resume('window');
    document.addEventListener('visibilitychange', visibility);
    document.addEventListener('pointerup', release); document.addEventListener('pointercancel', release); window.addEventListener('blur', blur); window.addEventListener('focus', focus);
    return () => { clear(); clock.current = null; document.removeEventListener('visibilitychange', visibility); document.removeEventListener('pointerup', release); document.removeEventListener('pointercancel', release); window.removeEventListener('blur', blur); window.removeEventListener('focus', focus); };
  }, [id, duration]);
  useLayoutEffect(() => {
    replacement.current?.cancel();
    if (id && host.current) replacement.current = host.current.animate([{opacity: 0}, {opacity: 1}], {duration: reduced ? 160 : 180, easing: 'linear'});
    return () => replacement.current?.cancel();
  }, [id, reduced]);
  const value = toast ?? last.current;
  if (!value || !portalTarget) return null;
  return createPortal(<div ref={host} className={classes(styles.toastHost, className)} data-testid={testID} style={bottomInset === undefined ? undefined : {'--overlay-toast-bottom': `${bottomInset}px`} as OverlayStyle}
    onPointerEnter={() => clock.current?.pause('hover')} onPointerLeave={() => clock.current?.resume('hover')} onPointerDown={() => clock.current?.pause('touch')} onPointerUp={() => clock.current?.resume('touch')} onPointerCancel={() => clock.current?.resume('touch')}
    onFocusCapture={() => clock.current?.pause('focus')} onBlurCapture={event => { if (!event.currentTarget.contains(event.relatedTarget as Node)) clock.current?.resume('focus'); }}>
    <Toast {...value} className={toastClassName} open={toast !== null} onOpenChange={(open, reason) => { if (!open && current.current?.id === value.id) dismiss.current(value.id, reason); }} />
  </div>, portalTarget);
}

type BannerBase = {children: ReactNode; open?: boolean; className?: string; testID?: string};
export type BannerProps = BannerBase & ({variant: 'quiet'; action?: never; icon?: never} | {variant?: 'notice'; action?: OverlayAction; icon?: IconName});
export function Banner({children, open = true, className, testID, ...props}: BannerProps) {
  const present = usePresence(open, 160), motionRef = useSurfaceMotion(open, 'banner');
  if (!present) return null;
  const quiet = props.variant === 'quiet';
  return <div ref={motionRef} role="status" aria-live="polite" className={classes(quiet ? styles.quietBanner : styles.banner, className)} data-state={open ? 'open' : 'closed'} data-testid={testID}>
    {quiet ? <span className={styles.amberDot} aria-hidden="true" /> : props.icon && <Icon name={props.icon} size={20} className={styles.feedbackIcon} />}
    <span>{children}</span>{!quiet && props.action && <Button variant="primary" size="xs" className={styles.bannerAction} icon={props.action.icon} aria-label={props.action.accessibilityLabel} onClick={props.action.onAction}>{props.action.label}</Button>}
  </div>;
}
