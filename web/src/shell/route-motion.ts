import {browserOwnsBackTransition} from './model';

/** motion.html 3.1: a page change is a View Transition. A same-page change (a library
 * tab, a Settings section, any query-only update) keeps the page and its controls still;
 * the screen's own content motion owns it. Browsers without transition types keep the
 * page transition for every navigation. */
export const pageViewTransition = Object.freeze({types: ({pathChanged}: {pathChanged: boolean}) => pathChanged ? ['page'] : false as const});
type MotionRouter = {update(options: {defaultViewTransition: boolean | typeof pageViewTransition}): void;
  state?: {matches: readonly unknown[]};
  subscribe?: (event: 'onRendered', listener: () => void) => () => void;
};
type NavigationSignal = Event & {hasUAVisualTransition?: boolean; navigationType?: string};
let ownedByBrowser = false;
let reset: (() => void) | undefined;
export const routeMotionOwnedByBrowser = () => ownedByBrowser;
export function finishRouteMotion(): void { reset?.(); }

/** Capture runs before the history listener. The actual content commit releases the fence. */
export function bindShellRouteMotion(router: MotionRouter, browser: Window = window): () => void {
  const iphoneSafari = /iPhone|iPod/.test(browser.navigator.userAgent) && /Safari/.test(browser.navigator.userAgent) && !/CriOS|FxiOS|EdgiOS/.test(browser.navigator.userAgent);
  const suppress = (event: NavigationSignal, traversal: boolean) => {
    if (!browserOwnsBackTransition(event, {iphoneSafari, traversal})) return;
    ownedByBrowser = true; router.update({defaultViewTransition: false});
  };
  const pop = (event: PopStateEvent) => suppress(event, true);
  const navigate = (event: Event) => { const signal = event as NavigationSignal; suppress(signal, signal.navigationType === 'traverse'); };
  const navigation = (browser as Window & {navigation?: EventTarget}).navigation;
  const release = () => { ownedByBrowser = false; router.update({defaultViewTransition: pageViewTransition}); };
  // The first native snapshot cannot be a prerequisite for mounting the router.
  // A real commit (including one before this effect) enables ordinary motion.
  const offRendered = router.subscribe?.('onRendered', () => {if (!ownedByBrowser) release();});
  if (router.state?.matches.length) release();
  reset = release;
  browser.addEventListener('popstate', pop, {capture: true});
  navigation?.addEventListener('navigate', navigate);
  return () => { offRendered?.(); browser.removeEventListener('popstate', pop, {capture: true}); navigation?.removeEventListener('navigate', navigate); if (reset === release) { release(); reset = undefined; } };
}
