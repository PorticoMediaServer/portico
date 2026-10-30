import {useCallback, useLayoutEffect, useRef, useState, type RefObject} from 'react';
import type {VideoGesturePort, VideoSurfacePaint} from '../../kit/dock/video-gesture-port';
import {useDockReducedMotion} from '../../kit/dock/use-motion';
import {fenceKey, localVideoSurfaceActive} from '../../shell/playback-model';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';
import type {WatchBindings} from './types';
import {VideoGestureOwner} from './video-gesture-owner';

export function useVideoGesture(binding: WatchBindings | undefined, snapshot: WebPlaybackRuntimeSnapshot | undefined,
  allowed: boolean, localCurrent: () => boolean, dock: RefObject<HTMLSpanElement | null>, full: RefObject<HTMLDivElement | null>, bar: RefObject<HTMLDivElement | null>) {
  const screen = useRef<HTMLDivElement | null>(null), paint = useRef<VideoSurfacePaint | null>(null);
  const gate = useRef<() => boolean>(() => false), holder = useRef<VideoGestureOwner | undefined>(undefined);
  const [published, setPublished] = useState<{owner: VideoGestureOwner; binding: WatchBindings; key: string; preview: boolean; active: boolean}>();
  const reduced = useDockReducedMotion(screen), reduction = useRef(reduced);
  useLayoutEffect(() => {reduction.current = reduced;holder.current?.motionChanged();}, [reduced]);
  const fence = snapshot?.fence, authority = snapshot?.authority;
  const key = fence && snapshot ? JSON.stringify([fenceKey(fence), snapshot.playback.intentId, snapshot.playback.playbackRun]) : '';
  const ready = allowed && !!binding && !!fence && !!snapshot && localVideoSurfaceActive(snapshot) && !snapshot.dock.remote;
  // The gesture owner keeps the snapshot and fence of the commit that admitted it;
  // re-creating it on every render's fresh snapshot would drop live gestures.
  const admitted = useRef({snapshot, fence});
  useLayoutEffect(() => {admitted.current = {snapshot, fence};});
  useLayoutEffect(() => {
    const {snapshot, fence} = admitted.current;
    if (!ready || !binding || !fence || !snapshot || !localCurrent()) return;
    const runtime = binding.runtime, original = snapshot;
    let owner: VideoGestureOwner;
    const scopeCurrent = () => {
      try {
      if (holder.current !== owner || !localCurrent() || document.fullscreenElement || document.pictureInPictureElement) return false;
      const live = runtime.getSnapshot();
      return !live.disposed && live.authority === authority && !!live.fence && fenceKey(live.fence) === fenceKey(fence)
        && live.playback.intentId === original.playback.intentId && live.playback.playbackRun === original.playback.playbackRun
        && localVideoSurfaceActive(live) && !live.dock.remote && live.presentation?.finished !== true && live.playback.phase !== 'error'
        && !!live.presentation && fenceKey(live.presentation.fence) === fenceKey(fence);
      } catch {return false;}
    };
    const current = () => scopeCurrent() && gate.current();
    owner = new VideoGestureOwner({current, nodes: () => paint.current && dock.current && full.current && screen.current && bar.current
      ? {surface: paint.current, dock: dock.current, full: full.current, screen: screen.current, bar: bar.current,
        copies: [...screen.current.querySelectorAll<HTMLElement>('[data-video-gesture-copy]')]} : undefined,
      reduced: () => reduction.current || document.documentElement.dataset.reduceMotion === 'true' || document.body.dataset.reduceMotion === 'true'
        || !!screen.current?.closest('[data-reduce-motion="true"]') || matchMedia('(prefers-reduced-motion: reduce)').matches,
      expanded: () => !!runtime.getSnapshot().dock.local?.expanded,
      state: (preview, active) => {if (holder.current === owner) setPublished(value => value?.owner === owner && (value.preview !== preview || value.active !== active) ? {...value, preview, active} : value);},
      commit: expanded => {
        if (!current()) return false;
        try {
          if (expanded) runtime.open(fence);else runtime.collapse(fence);
          return current() && !!runtime.getSnapshot().dock.local?.expanded === expanded;
        } catch (error) {if (current()) binding.reportError(error);return false;}
      }});
    holder.current = owner;setPublished({owner, binding, key, preview: false, active: false});
    const changed = () => {if (!scopeCurrent()) owner.dispose();else if (!gate.current()) owner.cancel();};
    const off = runtime.subscribe(changed);
    const layout = () => owner.cancel();window.addEventListener('resize', layout);window.addEventListener('scroll', layout, true);
    return () => {off();window.removeEventListener('resize', layout);window.removeEventListener('scroll', layout, true);owner.dispose();if (holder.current === owner) holder.current = undefined;};
  }, [binding, key, authority, ready, localCurrent, dock, full, bar]);
  const drawn = published && published.binding === binding && published.key === key && ready && published.owner === holder.current ? published : undefined;
  useLayoutEffect(() => {drawn?.owner.committed(!!snapshot?.dock.local?.expanded);drawn?.owner.observe();});
  const port: VideoGesturePort | undefined = drawn?.owner;
  const admit = useCallback((value: () => boolean) => {gate.current = value;return () => {if (gate.current === value) {gate.current = () => false;holder.current?.cancel();}};}, []);
  return {port, screen, paint, admit, preview: !!drawn?.preview, active: !!drawn?.active, retain: ready};
}
