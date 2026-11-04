import {useCallback, useRef} from 'react';
import type {PersonPage} from '../../../../../packages/client-core/src/people';

export type CreditRefocus = Readonly<{focusId: string; serial: number}>;

/** TV person credits (#697 review): a re-read after the server refuses a credit cursor (the viewer's access was decided
 * again) replaces the shown page, and the credit window is built again holding only the new first page (seeded, so it is
 * there on the first render). The shelf's slots are keyed by index: a focused credit the new first page holds keeps its
 * card and focus, but one beyond it turns into a placeholder, and focus would fall wherever the platform recovers it
 * (the Filters chip, the biography). The shelf names its own in-row target: the credit that had focus when the new first
 * page holds it, else that page's last credit (the first when nothing had focus). Row asks for it only when it held
 * focus, on the top layer, while mounted, when the replacement commits (kit/focus/replacement-refocus). */
export function useCreditRefocus(page: PersonPage, slotKey: (index: number) => string) {
  return useCreditRowRefocus(page, () => page.credits.length, slotKey);
}

/** The same rule for any index-keyed credits row: `source` is what a replacement swaps (the flat row's page, a TV
 * department row's window, which a re-read builds again seeded with that department's new first page), and `held` is how
 * many credits the new one holds from its first render (read only when `source` changes). */
export function useCreditRowRefocus(source: object, held: () => number, slotKey: (index: number) => string) {
  const focused = useRef<number | undefined>(undefined);
  const replacement = useRef<{source: object; serial: number; refocus?: CreditRefocus} | undefined>(undefined);
  const prior = replacement.current;
  if (!prior) replacement.current = {source, serial: 0};
  else if (prior.source !== source) {
    const serial = prior.serial + 1, count = held();
    replacement.current = {source, serial, refocus: count ? Object.freeze({focusId: slotKey(Math.min(focused.current ?? 0, count - 1)), serial}) : undefined};
  }
  const onFocus = useCallback((index: number) => {focused.current = index;}, []);
  return {refocus: replacement.current!.refocus, onFocus};
}
