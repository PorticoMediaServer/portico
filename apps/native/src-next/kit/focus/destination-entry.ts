import type {FocusAddress, FocusRegistry} from './registry';

export type NativeDestinationActivation = Readonly<{visible: boolean; intent: 'enter' | 'restore' | 'top'; revision: number}>;

/** A successful native host request is the receipt. Missing data or layout is
 * retried when hosts attach; restoration/content changes never move focus. */
export function createDestinationEntryFocus(registry: Pick<FocusRegistry, 'focus'> & Partial<Pick<FocusRegistry, 'capture'>>,
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
    const before = registry.capture?.();
    if (!address || !registry.focus(address)) return false;
    completed = ticket;
    // Android TV drops a request made before the element (or the layer it is in) can take focus natively: a grid's
    // first card arriving, a page whose layer is uncovered in the same commit. Ask again on the next frames, for at
    // most about half a second, while focus is still where it was; any move (it landed, or the person moved), a newer
    // entry, or the page being covered or left stops it. With nothing focused at the ask a move to something the
    // registry does not track cannot be seen, so then it asks once more only.
    const capture = registry.capture;
    if (options.reask !== false && capture && typeof requestAnimationFrame === 'function') {
      const mine = ++loop, limit = before ? 30 : 1; asking = ticket;
      const at = (value: ReturnType<typeof capture>) => value?.regionId === before?.regionId && value?.childId === before?.childId;
      let frames = 0;
      const again = () => {
        if (mine !== loop || completed !== ticket) return;
        if (!at(capture()) || ++frames > limit) {asking = undefined; return;}
        registry.focus(address); requestAnimationFrame(again);
      };
      requestAnimationFrame(again);
    }
    return true;
  };
  return Object.assign(request, {stop});
}
