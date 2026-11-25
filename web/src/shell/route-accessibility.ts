import type {MessageId} from '@i18n';

const hidden = '[inert],[hidden],[aria-hidden="true"]';
const headingSelector = 'h1,[data-page-title]';

export function routeLabelKey(pathname: string): MessageId {
  if (pathname === '/') return 'next.home.shell.home';
  if (pathname.startsWith('/search')) return 'next.home.shell.search';
  if (pathname === '/libraries' || pathname.startsWith('/library/')) return 'next.home.shell.libraries';
  if (pathname === '/saved') return 'next.home.shell.saved';
  if (pathname === '/live') return 'next.home.shell.live';
  if (pathname === '/settings') return 'next.shell.settings';
  if (pathname === '/dashboard') return 'next.shell.dashboard';
  if (pathname === '/notifications') return 'next.home.shell.notifications';
  if (pathname === '/servers') return 'portico.landing.pickTitle';
  if (pathname === '/profiles' || pathname === '/account') return 'next.shell.account';
  if (pathname === '/sign-in') return 'accounts.signIn.submit';
  if (pathname === '/setup') return 'web.setup.welcome';
  if (pathname === '/about') return 'settings.about';
  if (pathname === '/help') return 'next.shell.help';
  if (pathname === '/watch-together') return 'next.shell.watchTogether';
  if (pathname === '/player') return 'next.dock.nowPlaying';
  if (pathname.startsWith('/item/') || pathname.startsWith('/person/')) return 'next.controls.details';
  return 'next.platform.appName';
}

/** Read visible page headings only. A modal, outgoing tree or hero on Home
 * does not become the name of the route, and opaque URL identifiers stay out. */
export function routeHeadingElement(main: HTMLElement, pathname: string): Readonly<{element: HTMLElement; text: string}> | undefined {
  if (pathname === '/') return;
  for (const heading of main.querySelectorAll<HTMLElement>(headingSelector)) {
    if (heading.closest(`${hidden},[role="dialog"],[role="alertdialog"]`) || !heading.getClientRects().length) continue;
    const text = (heading.getAttribute('aria-label') || heading.textContent || heading.querySelector('img[alt]')?.getAttribute('alt') || '').replace(/\s+/g, ' ').trim();
    if (text) return {element: heading, text};
  }
}

export function routeHeading(main: HTMLElement, pathname: string): string | undefined {
  return routeHeadingElement(main, pathname)?.text;
}

export function routeDocumentTitle(label: string, appName: string): string {
  return label === appName ? appName : `${label} — ${appName}`;
}

export function canAnnounceRoute(main: HTMLElement): boolean {
  if (!main.isConnected || main.closest(hidden)) return false;
  const active = main.ownerDocument.activeElement as HTMLElement | null;
  return !active?.closest('[role="dialog"],[role="alertdialog"]');
}

/** Called once for accepted path navigation, never from a title mutation. */
export function announceRoute(main: HTMLElement, label: string, announce: (label: string) => void): boolean {
  if (!canAnnounceRoute(main)) return false;
  const document = main.ownerDocument, active = document.activeElement as HTMLElement | null;
  // A destination can own a more useful autofocus target (for example sign-in).
  // Otherwise move off the old navigation control without changing restored scroll.
  if (!active || active === document.body || !main.contains(active)) main.focus({preventScroll: true});
  announce(label);
  return true;
}

function nodeMayHaveHeading(node: Node, tracked?: HTMLElement): boolean {
  if (node === tracked) return true;
  if (node.nodeType !== 1) return false;
  const element = node as Element;
  if (tracked && element.contains?.(tracked)) return true;
  return Boolean(element.matches?.(headingSelector) || element.querySelector?.(headingSelector));
}

/** Filter broad subtree childList mutations to those that touch or introduce a heading. */
export function mutationMayAffectHeading(records: readonly MutationRecord[], tracked?: HTMLElement): boolean {
  for (const record of records) {
    if (tracked && (record.target === tracked || tracked.contains?.(record.target))) return true;
    if (record.type === 'childList') {
      if (record.target.nodeType === 1 && (record.target as Element).closest?.(headingSelector)) return true;
      for (const node of record.addedNodes) if (nodeMayHaveHeading(node, tracked)) return true;
      for (const node of record.removedNodes) if (nodeMayHaveHeading(node, tracked)) return true;
    }
  }
  return false;
}

/** Narrow heading observation: Home needs no observer at all; other routes watch
 * `<main>` only for heading-relevant `childList` changes until a fresh heading
 * settles, then narrow to that single heading element. */
export function observeRouteHeading(
  main: HTMLElement,
  pathname: string,
  onChange: () => void,
  options: Readonly<{previousHeading?: string; Observer?: typeof MutationObserver}> = {},
) {
  if (pathname === '/') return {reconcile: () => {}, dispose: () => {}};
  const Observer = options.Observer ?? MutationObserver;
  let tracked: HTMLElement | undefined;
  let mainWatching = false;
  let disposed = false;
  const headingObserver = new Observer(() => { if (!disposed) onChange(); });
  const mainObserver = new Observer(records => {
    if (!disposed && mutationMayAffectHeading(records, tracked)) onChange();
  });
  const watchMain = () => {
    if (mainWatching || disposed) return;
    mainWatching = true;
    mainObserver.observe(main, {subtree: true, childList: true});
  };
  const stopMain = () => {
    if (!mainWatching) return;
    mainWatching = false;
    mainObserver.disconnect();
  };
  const reconcile = () => {
    if (disposed) return;
    const found = routeHeadingElement(main, pathname);
    if (found?.element !== tracked) {
      headingObserver.disconnect();
      tracked = found?.element;
      if (tracked) headingObserver.observe(tracked, {subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['aria-label']});
    }
    const settled = !!found && found.text !== options.previousHeading;
    if (settled) stopMain();
    else watchMain();
  };
  reconcile();
  return {
    reconcile,
    dispose() {
      disposed = true;
      stopMain();
      headingObserver.disconnect();
      tracked = undefined;
    },
  };
}

/** Mutation bursts (including clocks) need at most one heading/layout read per frame. */
export function routeTitleUpdates(main: HTMLElement, update: () => void,
  request: typeof requestAnimationFrame = requestAnimationFrame, cancel: typeof cancelAnimationFrame = cancelAnimationFrame) {
  let frame: number | undefined;
  return {
    schedule() {
      if (frame !== undefined || main.closest(hidden)) return;
      frame = request(() => {
        frame = undefined;
        if (!main.closest(hidden)) update();
      });
    },
    dispose() { if (frame !== undefined) cancel(frame); frame = undefined; },
  };
}
