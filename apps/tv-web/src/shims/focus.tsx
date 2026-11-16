import React, {forwardRef, useCallback, useLayoutEffect, useRef} from 'react';
import * as Web from 'react-native-web';
import {distance, type Direction, type Rect} from './focus-geometry';

/**
 * The focus engine a TV browser does not have: the part of tvOS (and of Fire TV's system) that
 * moves focus when an arrow key is pressed. The shared screens never move focus by key
 * themselves. The kit (`apps/native/src-next/kit/focus`) decides regions, entry, memory and edges
 * and hands the engine only React Native's TV properties, which this file gives meaning to:
 *
 * - `TVFocusGuideView`: a region. `focusable={false}` puts everything inside out of reach (a
 *   page that is not the front one, a player's hidden controls). `trapFocusUp/Down/Left/Right`:
 *   focus may not leave it that way. `autoFocus`: focus arriving from outside goes to its
 *   destination (`setDestinations`), else to the control inside that had focus last, else to the
 *   one the key would reach anyway.
 * - `nextFocusUp/Down/Left/Right` on a control: where that key goes from it (a control or a
 *   region), before any geometry.
 * - `hasTVPreferredFocus`: where focus starts when nothing has it.
 * - `requestTVFocus()`: focus this, now.
 *
 * Inside a region, the key goes to the nearest reachable control in its direction, as the native
 * engines do: the one whose edge is closest along the key's axis, with sideways distance counted
 * against it. Focus is moved with `preventScroll`; the kit's rows place themselves on focus
 * (`FocusAnchor`), and any other scroll view is moved just enough to show the control.
 */

type Target = Element | null | undefined;

const nextFocus = new WeakMap<Element, Partial<Record<Direction, Target>>>();
const destinations = new WeakMap<Element, Target[]>();
const remembered = new WeakMap<Element, Element>();
const preferred = new Set<Element>();

const GUIDE = 'data-tv-guide';
const CLOSED = 'data-tv-closed';
const trapName = (direction: Direction) => `data-tv-trap-${direction}`;

/** What a control's `nextFocus*` props point at (findNodeHandle gives the element itself). */
export function setNextFocus(element: Element, edges: Partial<Record<Direction, unknown>>): void {
  const kept: Partial<Record<Direction, Target>> = {};
  for (const direction of ['up', 'down', 'left', 'right'] as const) {
    const target = edges[direction];
    if (target instanceof Element) kept[direction] = target;
  }
  if (Object.keys(kept).length) nextFocus.set(element, kept);
  else nextFocus.delete(element);
}

export function setPreferred(element: Element, wanted: boolean): void {
  if (wanted) preferred.add(element);
  else preferred.delete(element);
}

const isGuide = (element: Element) => element.hasAttribute(GUIDE);

function visible(element: Element): boolean {
  const rect = element.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) return false;
  const style = getComputedStyle(element);
  return style.visibility !== 'hidden';
}

/** A control the remote can reach now: focusable, shown, and in no closed region. */
export function reachable(element: Element | null): element is HTMLElement {
  if (!(element instanceof HTMLElement) || !element.isConnected) return false;
  if (element.tabIndex < 0 || element.hasAttribute('disabled') || element.getAttribute('aria-disabled') === 'true' && element.tabIndex < 0) return false;
  if (element.closest(`[${CLOSED}]`)) return false;
  return visible(element);
}

function controls(scope: ParentNode = document): HTMLElement[] {
  return Array.from(scope.querySelectorAll<HTMLElement>('[tabindex="0"], input, textarea')).filter(reachable);
}

/** The control a region hands focus to when focus arrives from outside it. */
function entry(guide: Element, fallback?: HTMLElement): HTMLElement | undefined {
  for (const destination of destinations.get(guide) ?? []) {
    if (!destination || !guide.contains(destination)) continue;
    const found = resolve(destination);
    if (found) return found;
  }
  const last = remembered.get(guide);
  if (last && reachable(last) && guide.contains(last)) return last;
  if (fallback) return fallback;
  return controls(guide)[0];
}

/** A target that is a control, or a region's entry. */
function resolve(target: Target, fallback?: HTMLElement): HTMLElement | undefined {
  if (!target || !target.isConnected) return undefined;
  if (isGuide(target)) return target.hasAttribute(CLOSED) ? undefined : entry(target, fallback);
  if (reachable(target)) return target;
  const inner = controls(target)[0];
  return inner;
}

function guidesAround(element: Element): Element[] {
  const found: Element[] = [];
  for (let node = element.parentElement; node; node = node.parentElement) if (isGuide(node)) found.push(node);
  return found;
}

const rectOf = (element: Element): Rect => element.getBoundingClientRect();

/** Where `direction` goes from `from`, inside `scope`. */
function nearest(from: HTMLElement, direction: Direction, scope: ParentNode): HTMLElement | undefined {
  const origin = rectOf(from);
  let best: HTMLElement | undefined, bestScore = Infinity;
  for (const candidate of controls(scope)) {
    if (candidate === from || candidate.contains(from) || from.contains(candidate)) continue;
    const score = distance(origin, rectOf(candidate), direction);
    if (score < bestScore) {best = candidate; bestScore = score;}
  }
  return best;
}

/** Move focus one step from `from`. Answers whether anything moved. */
export function move(direction: Direction): boolean {
  const from = document.activeElement;
  if (!reachable(from)) return start();
  const explicit = nextFocus.get(from)?.[direction];
  if (explicit) {
    const target = resolve(explicit);
    if (target && target !== from) return focus(target);
  }
  const around = guidesAround(from);
  // The innermost region that keeps focus from leaving this way bounds the search.
  const trap = around.find(guide => guide.hasAttribute(trapName(direction)));
  const found = nearest(from, direction, trap ?? document);
  if (!found) return false;
  // Entering regions from outside: the outermost one entered with a destination or a memory decides.
  const entered = guidesAround(found).filter(guide => !guide.contains(from)).reverse();
  for (const guide of entered) {
    if (!guide.hasAttribute('data-tv-autofocus')) continue;
    const target = entry(guide, found);
    if (target && target !== found) return focus(target);
  }
  return focus(found);
}

/** Focus where the screen starts when nothing has focus: a preferred control, else the first. */
export function start(): boolean {
  for (const element of Array.from(preferred).reverse()) if (reachable(element)) return focus(element);
  const first = controls()[0];
  return first ? focus(first) : false;
}

function remember(element: Element): void {
  for (const guide of guidesAround(element)) remembered.set(guide, element);
}

/** Scroll views the kit does not place itself are moved just enough to show the control. */
function reveal(element: HTMLElement): void {
  const target = element.getBoundingClientRect();
  for (let node = element.parentElement; node && node !== document.body; node = node.parentElement) {
    const style = getComputedStyle(node);
    const scrollsY = /(auto|scroll)/.test(style.overflowY) && node.scrollHeight > node.clientHeight;
    const scrollsX = /(auto|scroll)/.test(style.overflowX) && node.scrollWidth > node.clientWidth;
    if (!scrollsX && !scrollsY) continue;
    const box = node.getBoundingClientRect();
    const scale = box.height && node.offsetHeight ? box.height / node.offsetHeight : 1;
    if (scrollsY) {
      if (target.top < box.top) node.scrollTop -= (box.top - target.top) / scale;
      else if (target.bottom > box.bottom) node.scrollTop += Math.min(target.bottom - box.bottom, target.top - box.top) / scale;
    }
    if (scrollsX) {
      if (target.left < box.left) node.scrollLeft -= (box.left - target.left) / scale;
      else if (target.right > box.right) node.scrollLeft += Math.min(target.right - box.right, target.left - box.left) / scale;
    }
  }
}

export function focus(element: HTMLElement): boolean {
  if (!reachable(element)) return false;
  element.focus({preventScroll: true});
  if (document.activeElement !== element) return false;
  remember(element);
  // The control's own focus handlers have run: a row that places itself has done so.
  reveal(element);
  return true;
}

// `requestTVFocus()` is how the kit focuses a control (`kit/focus/registry.ts`); host refs here
// are the elements themselves. A request made while its control is still arriving (inside a page
// that is not open yet, not laid out yet) is tried again for a moment, as the Fire TV app does
// (`seekFocus`), unless focus has meanwhile landed somewhere the viewer can see.
const RETRY_MS = [16, 50, 150, 300, 600];
(HTMLElement.prototype as unknown as {requestTVFocus: () => void}).requestTVFocus = function requestTVFocus(this: HTMLElement) {
  const element = this;
  const attempt = () => {const target = resolve(element); return !!target && focus(target);};
  if (attempt()) return;
  const before = document.activeElement;
  for (const delay of RETRY_MS) {
    setTimeout(() => {
      if (document.activeElement !== before && reachable(document.activeElement)) return;
      if (resolve(element) && document.activeElement !== resolve(element)) attempt();
    }, delay);
  }
};

// Focus that goes nowhere (its control was removed) is given a home on the next frame, as the
// native engines do: the remembered control of a region still on screen, else the start.
document.addEventListener('focusout', event => {
  if (event.relatedTarget) return;
  requestAnimationFrame(() => {
    if (reachable(document.activeElement)) return;
    start();
  });
}, true);
// A screen that appears while nothing has focus gets its start, as on tvOS, once it has settled:
// the kit asks for each screen's own starting focus as its parts arrive (`kit/focus`), and a
// control focused before that would take its place. Only added and removed views are watched.
const SETTLE_MS = 300, LATEST_MS = 1200;
let settling: ReturnType<typeof setTimeout> | undefined, latest: ReturnType<typeof setTimeout> | undefined;
const home = () => {
  clearTimeout(settling); clearTimeout(latest); settling = latest = undefined;
  if (!reachable(document.activeElement)) start();
};
new MutationObserver(() => {
  if (reachable(document.activeElement)) return;
  clearTimeout(settling);
  settling = setTimeout(home, SETTLE_MS);
  latest ??= setTimeout(home, LATEST_MS);
}).observe(document.documentElement, {childList: true, subtree: true});
document.addEventListener('focusin', event => {
  if (event.target instanceof Element) remember(event.target);
}, true);

type GuideProps = Web.ViewProps & {
  autoFocus?: boolean; focusable?: boolean; destinations?: unknown[];
  trapFocusUp?: boolean; trapFocusDown?: boolean; trapFocusLeft?: boolean; trapFocusRight?: boolean;
};

/** `TVFocusGuideView` as the Apple TV build means it (see the top of this file). */
export const TVFocusGuideView = forwardRef<unknown, GuideProps>(function TVFocusGuideView({
  autoFocus, focusable = true, destinations: given, trapFocusUp, trapFocusDown, trapFocusLeft, trapFocusRight, ...view
}, ref) {
  const host = useRef<HTMLElement | null>(null);
  const attach = useCallback((element: HTMLElement | null) => {
    host.current = element;
    if (element) {
      (element as unknown as {setDestinations: (targets: unknown[]) => void}).setDestinations = (targets: unknown[]) => {
        destinations.set(element, targets.filter((target): target is Element => target instanceof Element));
      };
    }
    if (typeof ref === 'function') ref(element);
    else if (ref) (ref as React.MutableRefObject<unknown>).current = element;
  }, [ref]);
  useLayoutEffect(() => {
    const element = host.current;
    if (!element) return;
    element.setAttribute(GUIDE, '');
    const flag = (name: string, on: boolean | undefined) => (on ? element.setAttribute(name, '') : element.removeAttribute(name));
    flag(CLOSED, !focusable);
    flag('data-tv-autofocus', autoFocus);
    flag(trapName('up'), trapFocusUp);
    flag(trapName('down'), trapFocusDown);
    flag(trapName('left'), trapFocusLeft);
    flag(trapName('right'), trapFocusRight);
    if (given) destinations.set(element, given.filter((target): target is Element => target instanceof Element));
    // A region that closes while it holds focus hands it on.
    if (!focusable && element.contains(document.activeElement)) requestAnimationFrame(() => {if (!reachable(document.activeElement)) start();});
  });
  return <Web.View {...view} ref={attach as never}/>;
});
