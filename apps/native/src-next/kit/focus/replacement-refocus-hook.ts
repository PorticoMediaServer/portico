import type React from 'react';
import type {FocusRegistry} from './registry';

export type ReplacementRefocus = Readonly<{focusId: string; serial: number}>;
type Hooks = Pick<typeof React, 'useEffect' | 'useRef'>;

/** A TV region whose children were replaced (not appended), for example a row read again from its first page after the
 * server refused its cursor. When focus was on a child the replacement took away, it moves to `refocus.focusId`, in the
 * region, instead of wherever the platform recovers (tvOS asks the screen's preferred view). Row uses it for its slots
 * (`paging.refocus` / `refocus`); the songs grid on TV search uses it for its region.
 *
 * - Whether the region held focus on a child the replacement took away (`lostChild`) is decided once per replacement
 *   (its serial), on the render that brings it, while the old children are still attached.
 * - The request is decided once too, when that render commits (once per serial, whatever changes after): it is queued behind the registry's own recovery of the
 *   removed child, so the named target is the last word, and it goes out only if the target is attached by then. A
 *   target that arrives later is never asked for: focus is never taken from where the person has since moved it. (A
 *   replacement's items are in hand when it renders: a windowed list seeds its first page, `initialPage`.)
 * - Only the top layer takes focus: a covered region never asks, at commit or when the queued request runs, and
 *   uncovering later does not ask either. Unmounting before the request runs cancels it.
 *
 * Returns the serial of a replacement that took focus away (Row keeps that target's slot mounted), else undefined. */
export function replacementRefocusHook(react: Hooks) {
  return function useReplacementRefocus(registry: FocusRegistry | null | undefined, regionId: string | undefined, refocus: ReplacementRefocus | undefined,
    covered: boolean, lostChild: (childId: string) => boolean): number | undefined {
    const coveredNow = react.useRef(false);
    coveredNow.current = covered;
    // `decided`: the serial whose request has been decided (asked, or found not attached). Nothing re-asks for it, not even
    // a later change of region, registry or target (the effect running again).
    const held = react.useRef<{serial?: number; lost: boolean; decided?: number}>({lost: false});
    if (refocus && held.current.serial !== refocus.serial) {
      const active = regionId && registry ? registry.capture() : undefined;
      held.current = {serial: refocus.serial, lost: !!active && active.regionId === regionId && lostChild(active.childId)};
    }
    const lost = held.current.lost ? held.current.serial : undefined, focusId = refocus?.focusId;
    react.useEffect(() => {
      if (coveredNow.current || lost === undefined || held.current.decided === lost || !regionId || !registry || !focusId) return;
      held.current.decided = lost;
      let live = true;
      queueMicrotask(() => {if (live && !coveredNow.current && registry.order(regionId).includes(focusId)) registry.focus({regionId, childId: focusId});});
      return () => {live = false;};
    }, [focusId, lost, regionId, registry]);
    return lost;
  };
}
