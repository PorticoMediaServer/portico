import type {FocusAddress, FocusRegistry} from './registry';

export type NativeDestinationActivation = Readonly<{visible: boolean; intent: 'enter' | 'restore' | 'top'; revision: number}>;

/** A successful native host request is the receipt. Missing data or layout is
 * retried when hosts attach; restoration/content changes never move focus. */
export function createDestinationEntryFocus(registry: Pick<FocusRegistry, 'focus'>) {
  let completed: string | undefined;
  return (activation: NativeDestinationActivation, address: FocusAddress | undefined): boolean => {
    if (!activation.visible) return false;
    const ticket = JSON.stringify([activation.revision, activation.intent]);
    if (completed === ticket) return false;
    if (activation.intent === 'restore') {completed = ticket; return false;}
    if (!address || !registry.focus(address)) return false;
    completed = ticket; return true;
  };
}
