import type {MessageId} from '@i18n';

const hidden = '[inert],[hidden],[aria-hidden="true"]';

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
export function routeHeading(main: HTMLElement, pathname: string): string | undefined {
  if (pathname === '/') return;
  for (const heading of main.querySelectorAll<HTMLElement>('h1,[data-page-title]')) {
    if (heading.closest(`${hidden},[role="dialog"],[role="alertdialog"]`) || !heading.getClientRects().length) continue;
    const text = (heading.getAttribute('aria-label') || heading.textContent || heading.querySelector('img[alt]')?.getAttribute('alt') || '').replace(/\s+/g, ' ').trim();
    if (text) return text;
  }
}

export function routeDocumentTitle(label: string, appName: string): string {
  return label === appName ? appName : `${label} — ${appName}`;
}

/** Called once for accepted path navigation, never from a title mutation. */
export function announceRoute(main: HTMLElement, label: string, announce: (label: string) => void): boolean {
  if (!main.isConnected || main.closest(hidden)) return false;
  const document = main.ownerDocument, active = document.activeElement as HTMLElement | null;
  if (active?.closest('[role="dialog"],[role="alertdialog"]')) return false;
  // A destination can own a more useful autofocus target (for example sign-in).
  // Otherwise move off the old navigation control without changing restored scroll.
  if (!active || active === document.body || !main.contains(active)) main.focus({preventScroll: true});
  announce(label);
  return true;
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
