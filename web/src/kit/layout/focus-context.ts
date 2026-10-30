import {useLayoutEffect, useRef, type FocusEvent, type KeyboardEvent, type PointerEvent, type RefObject} from 'react';

/**
 * What the caller actually had before an item left: its own bounded root, its own ordered item
 * nodes (outgoing ones included), the order that survived, and its own surviving section/page
 * starting controls. Nothing here is derived from a selector, a route, a library listing or a clock.
 */
export type RemovalFocusContext = Readonly<{
  /** The bounded root the caller committed; no target outside it is ever chosen. */
  root: HTMLElement | null;
  /** The ordered item nodes exactly as they were before the removal, outgoing items included. */
  order: readonly HTMLElement[];
  /** The ordered item nodes that survived this removal; only these may receive focus. */
  surviving: readonly HTMLElement[];
  /** Ordered section/page starting controls that were committed before the removal. */
  sectionStarts?: readonly HTMLElement[];
}>;

export type RemovalFocusTarget =
  | Readonly<{kind: 'next' | 'previous' | 'section' | 'region'; element: HTMLElement}>
  | Readonly<{kind: 'none'; element: null}>;

/** The kit's own primary-control convention, used only inside a node the caller already handed over. */
const primary = '[data-layout-primary],button:not(:disabled),a[href],[tabindex="0"]';

const none: RemovalFocusTarget = Object.freeze({kind: 'none', element: null});

/**
 * Every element inside one captured item that could take focus, in draw order. The first one may be
 * hidden or disabled, so the caller of this list picks the first *eligible* stop, not the first stop.
 */
export function removalFocusStops(item: HTMLElement): readonly HTMLElement[] {
  const stops: HTMLElement[] = [];
  if (item.matches(primary)) stops.push(item);
  stops.push(...item.querySelectorAll<HTMLElement>(primary));
  // A sparse slot's tabindex=-1 is a real fallback, not a reason to bypass its primary control.
  if (!item.matches(primary) && item.hasAttribute('tabindex')) stops.push(item);
  return stops;
}

/**
 * A candidate is eligible only while it is really there: connected, inside the bound the caller
 * gave (when it gave one), not inert, not hidden from assistive tech or by CSS, laid out, and not
 * disabled. `getClientRects` alone does not catch `visibility: hidden` or `collapse`.
 */
export function removalFocusEligible(node: HTMLElement | null | undefined, root: HTMLElement | null): node is HTMLElement {
  if (!node || !node.isConnected) return false;
  if (root && (!root.isConnected || !root.contains(node))) return false;
  if (node.closest('[inert],[hidden],[aria-hidden="true"]')) return false;
  const visibility = getComputedStyle(node).visibility;
  if (visibility === 'hidden' || visibility === 'collapse') return false;
  if (node.getClientRects().length === 0) return false;
  if (node.matches(':disabled') || node.getAttribute('aria-disabled') === 'true') return false;
  return node.matches(primary) || node.hasAttribute('tabindex');
}

/** The first eligible stop of one captured item, or null when every stop is unusable. */
function stopOf(item: HTMLElement, bound: HTMLElement | null): HTMLElement | null {
  for (const candidate of removalFocusStops(item)) if (removalFocusEligible(candidate, bound)) return candidate;
  return null;
}

/**
 * The authored order: the next surviving eligible item, then the previous surviving eligible one,
 * then a surviving section/page start, then the surviving region itself. An item that was also
 * removed in the same commit is never a target even while it is still drawn as an outgoing row.
 * An explicit no-target is returned when the caller has no real context left — there is no body,
 * route or successor-document fallback.
 */
export function resolveRemovalFocus(context: RemovalFocusContext, outgoing: HTMLElement): RemovalFocusTarget {
  const {root, order, surviving, sectionStarts = []} = context;
  // The captured order is identity, not a key: an outgoing node is refused as a target, while a new
  // node reusing its key is never mistaken for the old one.
  if (!root || !root.isConnected || !order.includes(outgoing)) return none;
  const index = order.indexOf(outgoing);
  const later = order.slice(index + 1).filter(item => surviving.includes(item));
  const earlier = order.slice(0, index).filter(item => surviving.includes(item)).reverse();
  for (const item of later) { const element = stopOf(item, root); if (element) return {kind: 'next', element}; }
  for (const item of earlier) { const element = stopOf(item, root); if (element) return {kind: 'previous', element}; }
  // A section or page start may sit outside the list root; it is still an actual node the caller
  // handed over, so it is bounded by its own connectedness, never by a search.
  for (const item of sectionStarts) { const element = stopOf(item, null); if (element) return {kind: 'section', element}; }
  return removalFocusEligible(root, root) ? {kind: 'region', element: root} : none;
}

/** Focus the resolved target, or do nothing at all when the caller has no target. */
export function applyRemovalFocus(context: RemovalFocusContext, outgoing: HTMLElement): RemovalFocusTarget {
  const target = resolveRemovalFocus(context, outgoing);
  if (target.element) target.element.focus({preventScroll: true});
  return target;
}

export type RemovalFocusHandlers = Readonly<{
  onKeyDownCapture: (event: KeyboardEvent<HTMLElement>) => void;
  onPointerDownCapture: (event: PointerEvent<HTMLElement>) => void;
  onFocusCapture: (event: FocusEvent<HTMLElement>) => void;
  onBlurCapture: (event: FocusEvent<HTMLElement>) => void;
}>;

/**
 * The apply/cancel half. The caller supplies its own current ordered item nodes and section starts
 * each render; the committed root, order and section starts are published only in layout, so a
 * render cannot swap in a replacement root or a new section control. Focus moves only when the node
 * that actually held keyboard focus is removed in that commit — an unrelated removal in the same
 * commit does not hide it, and a pointer-only interaction, focus that already moved elsewhere, or a
 * replaced/detached original root leaves focus alone with the explicit no-target result.
 */
export function useRemovalFocus(root: RefObject<HTMLElement | null>, order: readonly HTMLElement[], sectionStarts: readonly HTMLElement[] = []): RemovalFocusHandlers {
  const committed = useRef<{root: HTMLElement | null; order: readonly HTMLElement[]; starts: readonly HTMLElement[]}>({root: null, order: [], starts: []});
  const focused = useRef<HTMLElement | null>(null), keyboard = useRef(false);
  useLayoutEffect(() => {
    const before = committed.current, element = root.current;
    // A replacement root under the same ref is a different list: never adopt its nodes as the old
    // context, and never carry focus across it.
    if (before.root !== element) {
      committed.current = {root: element, order, starts: sectionStarts};
      focused.current = null;
      return;
    }
    const held = focused.current;
    committed.current = {root: element, order, starts: sectionStarts};
    if (!before.order.length || !held) return;
    // The removal that owns the captured focus is the one containing it, even when an earlier,
    // unrelated row also left in the same commit.
    const removed = before.order.find(item => !order.includes(item) && item.contains(held));
    if (!removed) return;
    const active = document.activeElement;
    // A blur caused by the outgoing node leaving or becoming inert still counts; a blur from
    // clicking elsewhere leaves the node connected, so it never counts.
    const stillFocused = active === held || (active instanceof Node && removed.contains(active));
    const lostToRemoval = !held.isConnected || removed.hasAttribute('inert');
    const owns = stillFocused || (lostToRemoval && (active === document.body || active === null));
    if (!keyboard.current || !owns) return;
    const target = applyRemovalFocus({root: before.root, order: before.order, surviving: order, sectionStarts: before.starts}, removed);
    focused.current = target.element;
  }, [order, sectionStarts, root]);
  return {
    onKeyDownCapture: () => { keyboard.current = true; },
    onPointerDownCapture: () => { keyboard.current = false; },
    onFocusCapture: event => {
      const target = event.target as HTMLElement, element = root.current;
      if (!element || !element.contains(target)) return;
      focused.current = target;
      keyboard.current = target.matches(':focus-visible');
    },
    onBlurCapture: event => {
      // Focus that moves to another node outside this root is gone; a null related target is the
      // blur caused by the removal itself, and the layout effect above decides that case.
      const next = event.relatedTarget as HTMLElement | null, element = root.current;
      if (next && (!element || !element.contains(next))) focused.current = null;
    },
  };
}
