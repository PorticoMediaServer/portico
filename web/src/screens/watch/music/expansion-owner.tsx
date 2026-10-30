import {useCallback, useLayoutEffect, useMemo, useRef, useState, type RefObject} from 'react';
import {Artwork} from '../../../kit/artwork';
import type {ArtworkSource} from '../../../kit/artwork';
import {useDockReducedMotion} from '../../../kit/dock/use-motion';
import {dockMotionEvent} from '../../../kit/dock/geometry-event';
import type {WebPlaybackRuntimeSnapshot} from '../../../platform/playback';
import {fenceKey} from '../../../shell/playback-model';
import type {WatchBindings} from '../types';
import {MusicExpansionPaint} from './expansion-paint';
import styles from './music-expansion-owner.module.css';

let lifetime = 0;
type Scope = Readonly<{id: number; binding: WatchBindings; artwork?: ArtworkSource; current: () => boolean}>;
/** Retains only current music paint. No snapshot, media, queue, command or loading deadline is retained. */
export function useMusicExpansion(binding: WatchBindings | undefined, snapshot: WebPlaybackRuntimeSnapshot | undefined, localCurrent: () => boolean,
  dock: RefObject<HTMLSpanElement | null>, bar: RefObject<HTMLDivElement | null>, remoteOwned: boolean) {
  const fence = snapshot?.fence, presentation = snapshot?.presentation;
  const music = !!binding && !!fence && !snapshot?.disposed && presentation?.kind === 'music' && presentation.subject.kind === 'music'
    && fenceKey(presentation.fence) === fenceKey(fence) && !snapshot.dock.remote && !remoteOwned;
  const key = music ? JSON.stringify([fenceKey(fence!), presentation!.subject.kind === 'music' ? presentation!.subject.item.id : null,
    snapshot!.playback.intentId, snapshot!.playback.playbackRun, presentation!.posterUrl, presentation!.backdropUrl]) : undefined;
  const authority = snapshot?.authority;
  // The scope's lifetime is the exact content key above, never a per-tick fence or
  // presentation object identity: progress republishes equal objects.
  const fenceValue = fence ? fenceKey(fence) : undefined;
  const posterUrl = presentation?.posterUrl, backdropUrl = presentation?.backdropUrl;
  const scope = useMemo<Scope | undefined>(() => {
    if (!music || !binding || !key || fenceValue === undefined || !localCurrent()) return;
    const original = key, runtime = binding.runtime;
    return {id: ++lifetime, binding, artwork: binding.artwork.foreground(posterUrl ?? backdropUrl, 560, 1, {group: 'playback', priority: 'high'}) ?? undefined, current: () => {
      try {
        if (!localCurrent()) return false;
        const live = runtime.getSnapshot(), value = live.presentation;
        return !live.disposed && live.authority === authority && !!live.fence && value?.kind === 'music' && value.subject.kind === 'music' && fenceKey(value.fence) === fenceKey(live.fence)
          && !live.dock.remote && JSON.stringify([fenceKey(live.fence), value.subject.item.id, live.playback.intentId, live.playback.playbackRun, value.posterUrl, value.backdropUrl]) === original;
      } catch {return false;}
    }};
  }, [binding, key, fenceValue, authority, localCurrent, music, posterUrl, backdropUrl]);
  const [held, setHeld] = useState<Scope>();
  const admitted = scope?.current() ? scope : undefined;
  const open = !!admitted && !!snapshot?.dock.local?.expanded;
  useLayoutEffect(() => {if (open && scope) setHeld(scope);else if (held && held !== scope) setHeld(undefined);}, [open, scope, held]);
  const frame = useRef<HTMLElement | null>(null), cover = useRef<HTMLDivElement | null>(null);
  const settled = useCallback((original: Scope) => setHeld(value => value === original ? undefined : value), []);
  return {scope: admitted, open, present: !!admitted && (open || held === admitted), frame, cover, dock, bar, settled};
}
export type MusicExpansion = ReturnType<typeof useMusicExpansion>;

/** One persistent original Artwork instance moves between the two real committed slots.
 * The parent's sole real media element and all action bindings remain with their existing owner. */
export function MusicExpansionSurface({motion}: {motion: MusicExpansion}) {
  const scope = motion.scope;
  return scope ? <OwnedMusicCover key={scope.id} scope={scope} motion={motion}/> : null;
}
function OwnedMusicCover({scope, motion}: {scope: Scope; motion: MusicExpansion}) {
  const surface = useRef<HTMLDivElement | null>(null), reduced = useDockReducedMotion(surface);
  const owner = useRef<MusicExpansionPaint | null>(null), refresh = useRef<() => void>(() => {});
  const source = scope.artwork, settled = motion.settled;
  useLayoutEffect(() => {
    const original = new MusicExpansionPaint(scope.current, () => settled(scope));owner.current = original;
    let expanded = scope.binding.runtime.getSnapshot().dock.local?.expanded;
    const off = scope.binding.runtime.subscribe(() => {
      if (!scope.current()) {original.dispose();return;}
      const next = scope.binding.runtime.getSnapshot().dock.local?.expanded;
      if (next !== expanded) {expanded = next;original.freeze();}
    });
    return () => {off();if (owner.current === original) owner.current = null;original.dispose();};
  }, [scope, settled]);
  useLayoutEffect(() => {
    const original = owner.current, element = surface.current, dock = motion.dock.current;
    if (!original || !element || !dock || !scope.current()) return;
    let live = true, nextFrame: number | undefined;
    const paint = () => {
      if (!live || owner.current !== original || !scope.current() || surface.current !== element || motion.dock.current !== dock
        || !!scope.binding.runtime.getSnapshot().dock.local?.expanded !== motion.open) return;
      const frame = motion.frame.current;
      original.update({surface: element, dock, bar: motion.bar.current, frame, cover: motion.cover.current,
        copies: [...(motion.bar.current?.querySelectorAll<HTMLElement>('[data-dock-expansion-copy]') ?? [])],
        groups: [...(frame?.querySelectorAll<HTMLElement>('[data-music-content-group]') ?? [])]}, motion.open,
        reduced || window.matchMedia('(prefers-reduced-motion: reduce)').matches || !!element.closest('[data-reduce-motion="true"]')
        || document.documentElement.dataset.reduceMotion === 'true' || document.body.dataset.reduceMotion === 'true');
      // The original bar owns its entry/hold/swipe animations. Sample only its actual
      // ancestor jobs while collapsed; no second timing recipe or media owner is created.
      if (!motion.open && !frame && nextFrame === undefined) {
        let node: HTMLElement | null = dock, moving = false;
        while (node) {
          moving ||= node.getAnimations().some(job => job.playState === 'running' || job.pending);
          if (node === motion.bar.current) break;
          node = node.parentElement;
        }
        if (moving) nextFrame = requestAnimationFrame(() => {nextFrame = undefined;paint();});
      }
    };
    const follow = (event: Event) => {if (event.target instanceof HTMLElement && event.target.contains(dock)) paint();};
    refresh.current = paint;paint();
    const observer = new ResizeObserver(paint);observer.observe(dock);if (motion.bar.current) observer.observe(motion.bar.current);if (motion.cover.current) observer.observe(motion.cover.current);
    document.addEventListener(dockMotionEvent, follow);window.addEventListener('resize', paint);window.addEventListener('scroll', paint, true);
    return () => {live = false;if (nextFrame !== undefined) cancelAnimationFrame(nextFrame);if (refresh.current === paint) refresh.current = () => {};observer.disconnect();document.removeEventListener(dockMotionEvent, follow);window.removeEventListener('resize', paint);window.removeEventListener('scroll', paint, true);};
    // Read the actual committed nodes on every commit: a phone/desktop renderer may replace a
    // slot under the same ref. Queued callbacks from the previous setup remain closed above.
  });
  return <div ref={surface} className={styles.cover} aria-hidden="true" data-music-cover-surface>
    <Artwork source={source} alt="" onReady={() => refresh.current()}/>
  </div>;
}
