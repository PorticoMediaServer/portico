import {useLayoutEffect, useRef} from 'react';
import {springTiming, springs} from '@design';
import {Icon} from '../Icon';
import {PlayerMorph} from './motion';
import {dockMotionEvent, type DockMotionDetail} from './geometry-event';
import {useDockReducedMotion} from './use-motion';
import type {MediaSurfaceProps} from './types';
import styles from './dock.module.css';

type Frame = {left: number; top: number; width: number; height: number};
const dimensions = ['left', 'top', 'width', 'height'] as const;

/** Mount the owner's one real video element here for its entire life. Never portals or clones. */
export function MediaSurface({children, expanded, dockSlot, expandedSlot, hidden = false, finished = false, nativeRenderActive = false, gesturePaint, className}: MediaSurfaceProps) {
  const ref = useRef<HTMLDivElement | null>(null), reduced = useDockReducedMotion(ref);
  const animation = useRef<Animation | null>(null), first = useRef(true), epoch = useRef(0);
  const resumePaint = useRef<() => void>(() => {});
  useLayoutEffect(() => {
    const element = ref.current;if (!element || !gesturePaint) return;
    const adapter = {element, freeze: () => {
      const current = element.getBoundingClientRect();epoch.current++;animation.current?.cancel();animation.current = null;
      const frame = {left: current.left, top: current.top, width: current.width, height: current.height};
      element.style.width = `${frame.width}px`;element.style.height = `${frame.height}px`;element.style.transform = `translate(${frame.left}px, ${frame.top}px)`;return frame;
    }, resume: () => resumePaint.current()};
    gesturePaint.current = adapter;return () => {if (gesturePaint.current === adapter) gesturePaint.current = null;};
  }, [gesturePaint]);
  const physics = useRef<Partial<Record<keyof Frame, PlayerMorph>>>({});
  useLayoutEffect(() => {
    const element = ref.current; if (!element) return;
    let identity: HTMLElement | null = null;
    const enter = () => { element.dataset.hover = 'true'; }, leave = () => { delete element.dataset.hover; };
    const bindIdentity = () => {
      const next = dockSlot.current?.parentElement ?? null;
      if (next === identity) return;
      identity?.removeEventListener('pointerenter', enter); identity?.removeEventListener('pointerleave', leave); identity?.removeEventListener('focusin', enter); identity?.removeEventListener('focusout', leave);
      identity = next;
      identity?.addEventListener('pointerenter', enter); identity?.addEventListener('pointerleave', leave); identity?.addEventListener('focusin', enter); identity?.addEventListener('focusout', leave);
      if (identity?.matches(':hover') || identity?.contains(document.activeElement)) enter(); else leave();
    };
    const apply = (frame: Frame) => { element.style.width = `${frame.width}px`; element.style.height = `${frame.height}px`; element.style.transform = `translate(${frame.left}px, ${frame.top}px)`; };
    const measure = () => {
      bindIdentity();
      if (element.dataset.videoGesture) return;
      const slot = (expanded ? expandedSlot : dockSlot).current;
      if (!slot) { element.style.visibility = 'hidden'; return; }
      const target = slot.getBoundingClientRect(); if (!target.width || !target.height) { element.style.visibility = 'hidden'; return; }
      const token = ++epoch.current, now = performance.now(), current = element.getBoundingClientRect();
      const velocity = Object.fromEntries(dimensions.map(key => [key, physics.current[key]?.sample(now).velocity ?? 0])) as Record<keyof Frame, number>;
      animation.current?.cancel(); animation.current = null; element.style.visibility = hidden ? 'hidden' : 'visible';
      if (first.current || hidden) { first.current = false; apply(target); dimensions.forEach(key => { physics.current[key] = new PlayerMorph(target[key]); }); return; }
      const origin: Frame = {left: current.left, top: current.top, width: current.width, height: current.height};
      dimensions.forEach(key => { const spring = physics.current[key] ?? new PlayerMorph(origin[key]); spring.track(origin[key], velocity[key], now); spring.retarget(target[key], now, velocity[key]); physics.current[key] = spring; });
      apply(reduced ? target : origin);
      const duration = reduced ? 160 : springTiming(springs.player).durationMs;
      const keyframes: Keyframe[] = reduced ? [{opacity: 0}, {opacity: 1}] : Array.from({length: 49}, (_, i) => {
        const time = now + duration * i / 48, frame = Object.fromEntries(dimensions.map(key => [key, i === 48 ? target[key] : physics.current[key]!.sample(time).position])) as Frame;
        return {transform: `translate(${frame.left}px, ${frame.top}px) scale(${frame.width / Math.max(1, origin.width)}, ${frame.height / Math.max(1, origin.height)})`, opacity: 1};
      });
      const job = element.animate(keyframes, {duration, easing: 'linear', fill: 'forwards'}); animation.current = job;
      void job.finished.then(() => { if (token !== epoch.current) return; apply(target); job.cancel(); animation.current = null; }).catch(() => {});
    };
    const resume = () => {first.current = true;measure();};resumePaint.current = resume;
    measure();
    let drag: {frame: Frame; offset: number} | null = null;
    const follow = (event: Event) => {
      const slot = dockSlot.current;
      if (element.dataset.videoGesture) return;
      if (expanded || hidden || !slot || !(event.target instanceof HTMLElement) || !event.target.contains(slot)) return;
      const {detail} = event as CustomEvent<DockMotionDetail>;
      if (detail.kind === 'hold') {
        element.toggleAttribute('data-raised', detail.raised);
        const bar = event.target.getBoundingClientRect(), slotFrame = slot.getBoundingClientRect(), scale = bar.width / event.target.offsetWidth;
        if (!scale) return;
        const cx = bar.left + bar.width / 2, cy = bar.top + bar.height / 2;
        const base = {left: cx + (slotFrame.left - cx) / scale, top: cy + (slotFrame.top - cy) / scale, width: slotFrame.width / scale, height: slotFrame.height / scale};
        animation.current?.cancel(); animation.current = null; epoch.current++;
        apply(base);
        const frames = detail.scales.map(value => ({transform: `translate(${cx + (base.left - cx) * value}px, ${cy + (base.top - cy) * value}px) scale(${value})`}));
        if (!detail.duration) element.style.transform = frames[0]!.transform;
        else {
          const job = element.animate(frames.map((frame, index) => ({...frame, offset: index === 1 ? .3 : index / (frames.length - 1)})), {duration: detail.duration, easing: 'ease-out', fill: 'forwards'}); animation.current = job;
          void job.finished.then(() => { if (animation.current !== job) return; apply(base); job.cancel(); animation.current = null; }).catch(() => {});
        }
      } else if (detail.kind === 'begin') {
        const frame = element.getBoundingClientRect(); animation.current?.cancel(); animation.current = null; epoch.current++;
        drag = {frame, offset: detail.offset}; apply(frame);
      } else if (detail.kind === 'track' && drag) element.style.transform = `translate(${drag.frame.left + detail.offset - drag.offset}px, ${drag.frame.top}px)`;
      else if (detail.kind === 'release' && drag) {
        const origin = drag.frame, initial = drag.offset;
        animation.current?.cancel();
        const frames = detail.offsets.map((offset, i) => ({transform: `translate(${origin.left + offset - initial}px, ${origin.top}px)`, opacity: detail.opacity[i]}));
        const job = element.animate(frames, {duration: detail.duration, easing: 'linear', fill: 'forwards'}); animation.current = job;
        void job.finished.then(() => { if (!dockSlot.current?.isConnected) element.style.visibility = 'hidden'; }).catch(() => {});
      } else if (detail.kind === 'reset') { drag = null; measure(); }
    };
    document.addEventListener(dockMotionEvent, follow);
    const observer = new ResizeObserver(measure); if (expandedSlot.current) observer.observe(expandedSlot.current); if (dockSlot.current) observer.observe(dockSlot.current);
    window.addEventListener('resize', measure); window.addEventListener('scroll', measure, true);
    return () => { if (resumePaint.current === resume) resumePaint.current = () => {}; observer.disconnect(); document.removeEventListener(dockMotionEvent, follow); window.removeEventListener('resize', measure); window.removeEventListener('scroll', measure, true); identity?.removeEventListener('pointerenter', enter); identity?.removeEventListener('pointerleave', leave); identity?.removeEventListener('focusin', enter); identity?.removeEventListener('focusout', leave); };
  }, [expanded, reduced, hidden, dockSlot, expandedSlot, gesturePaint]);
  useLayoutEffect(() => () => { epoch.current++; animation.current?.cancel(); }, []);
  return <div ref={ref} className={[styles.mediaSurface, className].filter(Boolean).join(' ')} data-media-surface data-native-video-active={nativeRenderActive && !hidden && !finished || undefined} data-media-hidden={hidden || undefined} data-docked={!expanded || undefined} data-finished={finished || undefined} aria-hidden={!expanded || hidden || undefined}><div className={styles.surfacePicture} data-media-picture>{children}{!expanded && <span className={styles.surfaceExpand}><Icon name={finished ? 'i-back' : 'i-zoom'} size={22} /></span>}</div></div>;
}
