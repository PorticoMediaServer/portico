import {isScopedScreenPath, scopedLinks, scopedPathname, type createScopedLinkStore, type ServerScope} from '../platform/server-scope';
import type {ShellBootstrap} from './bootstrap';

type ScopeRouter = Readonly<{
  state: Readonly<{location: Readonly<{pathname: string; href: string}>}>;
  subscribe(event: 'onResolved', listener: () => void): () => void;
  navigate(options: {href: string; replace: boolean} | {to: '/link-unavailable'; search: {reason: 'access' | 'offline'}; replace: boolean} | {to: '/servers'; search: {link: string}; replace: boolean}): Promise<void>;
}>;
type ScopeBootstrap = Pick<ShellBootstrap, 'subscribe' | 'getSnapshot' | 'selectServer'>;
type PageAddress = Readonly<{pathname: string; search: string; hash: string; replace(href: string): void}>;

const browserAddress = (): PageAddress => ({pathname: location.pathname, search: location.search, hash: location.hash,
  replace: href => history.replaceState(history.state, '', href)});

/** O-161: a scoped address opens on its own server.
 * - Already there: nothing happens; the route is the same route, so nothing remounts.
 * - Another server this browser is signed in to: switch, keeping the place; if it does not
 *   answer, come back and say so.
 * - Another server the Portico Account may reach: the Hosted server chooser opens it.
 * - Signed out: the link is kept through sign-in and profile choice, then opened.
 * - A server's own UI only serves itself; anything else cannot be opened there.
 * An unscoped address on Hosted is rewritten in place to its scoped form. */
export function bindServerScope(router: ScopeRouter, bootstrap: ScopeBootstrap, scope: ServerScope,
  links: ReturnType<typeof createScopedLinkStore> = scopedLinks, address: () => PageAddress = browserAddress) {
  let closed = false, handling: string | undefined, chooser = false, switching: Readonly<{from: string; to: string}> | undefined;
  const unavailable = (reason: 'access' | 'offline') => {
    scope.settle(); links.clear(); handling = undefined;
    void router.navigate({to: '/link-unavailable', search: {reason}, replace: true}).catch(() => {});
  };
  const sync = () => {
    if (closed) return;
    const snapshot = bootstrap.getSnapshot(), here = router.state.location;
    // A switch made for a link that then could not reach its server goes back, and says so.
    if ((snapshot.phase === 'unreachable' || snapshot.phase === 'error') && switching) {
      const {from} = switching;switching = undefined;
      void bootstrap.selectServer(from).then(() => {if (!closed) unavailable('offline');});
      return;
    }
    if (snapshot.phase !== 'ready' || !snapshot.context) return;
    const current = snapshot.context.session().viewer.serverId;
    scope.setCurrent(current);
    if (switching?.to === current) switching = undefined;
    // The server a link asks for: this document's own scoped address, or one kept across sign-in
    // (which may have left and reloaded the page).
    const link = links.peek(), target = scope.requested() ?? link?.serverId;
    if (target && target !== current) {
      if (handling === target) {
        // The chooser was offered for this link and left on another server: the link cannot open here.
        if (here.pathname === '/servers') chooser = true;
        else if (chooser && !switching) {chooser = false; unavailable('access');}
        return;
      }
      handling = target;chooser = false;
      if (!scope.hosted) {unavailable('access'); return;}
      if (snapshot.context.savedServers.some(server => server.id === target)) {
        switching = {from: current, to: target};
        void bootstrap.selectServer(target);
      } else void router.navigate({to: '/servers', search: {link: target}, replace: true}).catch(() => {});
      return;
    }
    handling = undefined;
    // A link kept across sign-in or a switch opens once its server is the current one.
    if (link?.serverId === current) {
      links.clear();
      if (here.href !== link.href) {void router.navigate({href: link.href, replace: true}).catch(() => {}); return;}
    }
    // On Hosted the address bar always names the server: an old unscoped address is replaced in place.
    const page = address();
    if (scope.hosted && isScopedScreenPath(page.pathname) && isScopedScreenPath(here.pathname)) page.replace(scopedPathname(here.pathname, current) + page.search + page.hash);
  };
  const offRouter = router.subscribe('onResolved', sync), offBootstrap = bootstrap.subscribe(sync);
  sync();
  return () => {closed = true; offRouter(); offBootstrap();};
}
