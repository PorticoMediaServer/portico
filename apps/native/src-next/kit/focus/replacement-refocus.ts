import {useCallback, useEffect, useRef, useSyncExternalStore} from 'react';
import {useCovered} from './CoveredLayer';
import type {FocusRegistry} from './registry';

export type ReplacementRefocus = Readonly<{focusId: string; serial: number}>;

/** A TV region whose children were replaced (not appended), for example a row read again from its first page after the
 * server refused its cursor. When focus was on a child the replacement removed, it moves to `refocus.focusId`, in the
 * region, instead of wherever the platform recovers (tvOS asks the screen's preferred view). Row has the same rule inline
 * for its slots (`paging.refocus`); this is the one for a region that is not a Row (the songs grid on TV search).
 *
 * - Whether the region held focus on a removed child is decided once per replacement (its serial), on the render that
 *   brings it, while the old children are still attached.
 * - The request waits until the target is attached in the region, goes out once, and is queued behind the registry's own
 *   recovery of the removed child, so the named target is the last word.
 * - Only the top layer takes focus: a covered region never asks, at the effect or when the queued request runs, and
 *   uncovering later does not ask either. Unmounting cancels the request. */
export function useReplacementRefocus(registry: FocusRegistry | null, regionId: string | undefined, refocus: ReplacementRefocus | undefined,
  children: readonly string[]) {
  const covered = useRef(false);
  covered.current = useCovered();
  const subscribe = useCallback((listener: () => void) => regionId && registry ? registry.subscribeRegion(regionId, listener) : () => {}, [regionId, registry]);
  const attached = useCallback(() => !!refocus && !!regionId && !!registry && registry.order(regionId).includes(refocus.focusId), [refocus, regionId, registry]);
  const ready = useSyncExternalStore(subscribe, attached, attached);
  const held = useRef<{serial?: number; lost: boolean; asked?: number}>({lost: false});
  if (refocus && held.current.serial !== refocus.serial) {
    const active = regionId && registry ? registry.capture() : undefined;
    held.current = {serial: refocus.serial, lost: !!active && active.regionId === regionId && !children.includes(active.childId)};
  }
  const lost = held.current.lost ? held.current.serial : undefined, focusId = refocus?.focusId;
  useEffect(() => {
    if (covered.current || lost === undefined || held.current.asked === lost || !ready || !regionId || !registry || !focusId) return;
    let live = true;
    queueMicrotask(() => {
      if (!live || covered.current || held.current.asked === lost || !registry.order(regionId).includes(focusId)) return;
      held.current.asked = lost; registry.focus({regionId, childId: focusId});
    });
    return () => {live = false;};
  }, [focusId, lost, ready, regionId, registry]);
}
