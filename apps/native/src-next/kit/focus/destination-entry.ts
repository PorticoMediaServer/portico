import {isStartFocusDeferred, type FocusAddress, type FocusRegistry} from './registry';

export type NativeDestinationActivation = Readonly<{visible: boolean; intent: 'enter' | 'restore' | 'top'; revision: number}>;

/** A successful native host request is the receipt. Missing data or layout is
 * retried when hosts attach; restoration/content changes never move focus. */
export function createDestinationEntryFocus(registry: Pick<FocusRegistry, 'focus'> & Partial<Pick<FocusRegistry, 'capture' | 'child' | 'config'>>,
  options: Readonly<{reask?: boolean}> = {}) {
  let completed: string | undefined, loop = 0, asking: string | undefined;
  /** Ends any re-ask still running (the page was covered, left or unmounted). One that had not landed is not done:
   * if the same page is shown again it asks again. */
  const stop = () => {loop++; if (asking !== undefined && completed === asking) completed = undefined; asking = undefined;};
  const request = (activation: NativeDestinationActivation, address: FocusAddress | undefined): boolean => {
    if (!activation.visible) {stop(); return false;}
    const ticket = JSON.stringify([activation.revision, activation.intent]);
    if (completed === ticket) return false;
    if (activation.intent === 'restore') {completed = ticket; return false;}
    if (!address) return false;
    const before = registry.capture?.();
    const capture = registry.capture;
    const at = (value: ReturnType<NonNullable<typeof capture>>) => value?.regionId === before?.regionId && value?.childId === before?.childId;
    // On Android TV, requesting focus in the exact commit that uncovers a CoveredLayer (TVFocusGuideView
    // switching from FOCUS_BLOCK_DESCENDANTS to FOCUS_AFTER_DESCENDANTS, e.g. entering Search from the rail)
    // leaves native focus corrupted on the rail while the destination control still draws focused. Defer
    // the request to the next frame after native layout/props settle, with at most one follow-up frame check.
    if (isStartFocusDeferred() && typeof requestAnimationFrame === 'function') {
      if (registry.config?.(address.regionId)?.enabled === false) return false;
      if (registry.child) {
        const host = registry.child(address);
        if (!host || (!host.requestTVFocus && !host.focus)) return false;
      }
      completed = ticket;
      const mine = ++loop; asking = ticket;
      requestAnimationFrame(() => {
        if (mine !== loop || completed !== ticket) return;
        if (capture && !at(capture())) {asking = undefined; return;}
        registry.focus(address);
        if (options.reask === false || !capture) {asking = undefined; return;}
        requestAnimationFrame(() => {
          if (mine !== loop || completed !== ticket) return;
          asking = undefined;
          if (at(capture())) registry.focus(address);
        });
      });
      return true;
    }
    if (!registry.focus(address)) return false;
    completed = ticket;
    // Check once on the next frame while focus is still where it was; never poll across multiple frames or fight input.
    if (options.reask !== false && capture && typeof requestAnimationFrame === 'function') {
      const mine = ++loop; asking = ticket;
      requestAnimationFrame(() => {
        if (mine !== loop || completed !== ticket) return;
        asking = undefined;
        if (at(capture())) registry.focus(address);
      });
    }
    return true;
  };
  return Object.assign(request, {stop});
}
