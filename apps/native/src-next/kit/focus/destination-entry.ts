import type {FocusAddress, FocusRegistry} from './registry';

export type NativeDestinationActivation = Readonly<{visible: boolean; intent: 'enter' | 'restore' | 'top'; revision: number}>;

/** A successful native host request is the receipt. Missing data or layout is
 * retried when hosts attach; restoration/content changes never move focus. */
export function createDestinationEntryFocus(registry: Pick<FocusRegistry, 'focus'> & Partial<Pick<FocusRegistry, 'capture'>>) {
  let completed: string | undefined;
  return (activation: NativeDestinationActivation, address: FocusAddress | undefined): boolean => {
    if (!activation.visible) return false;
    const ticket = JSON.stringify([activation.revision, activation.intent]);
    if (completed === ticket) return false;
    if (activation.intent === 'restore') {completed = ticket; return false;}
    const before = registry.capture?.();
    if (!address || !registry.focus(address)) return false;
    completed = ticket;
    // Android TV drops a request made before the element (or the layer it is in) can take focus natively: a grid's
    // first card arriving, a page whose layer is uncovered in the same commit. Ask again on the next frames, for at
    // most about half a second, while focus is still where it was; any move (it landed, or the person moved) stops it.
    const capture = registry.capture, at = (value: ReturnType<NonNullable<typeof capture>>) => value?.regionId === before?.regionId && value?.childId === before?.childId;
    if (capture && typeof requestAnimationFrame === 'function') {
      let frames = 0;
      const again = () => {if (completed !== ticket || !at(capture()) || ++frames > 30) return; registry.focus(address); requestAnimationFrame(again);};
      requestAnimationFrame(again);
    }
    return true;
  };
}
