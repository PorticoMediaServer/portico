/** O-161 (Justin, 29 Oct): Hosted web paths name the server they belong to, as Plex's do, so a
 * copied or shared address opens that thing on that server: `/s/<serverId>/item/<id>`. The server
 * is its stable pinned ID, never an address. Routes inside the app stay unscoped; the router's
 * URL rewrite adds the prefix on the way out and removes it (noting the server) on the way in.
 * A server serving its own UI keeps plain paths: it is the only server there. */

// The link grammar is shared with the apps (client-core, frozen in REQUESTS for O-162).
import {isScopedScreenPath, parseServerLinkPath, scopedPathname, validServerId} from '@core/server-links.ts';
export {isScopedScreenPath, scopedPathname, validServerId};
export const parseScopedPath = parseServerLinkPath;

type Rewrite = Readonly<{input: (options: {url: URL}) => URL | undefined; output: (options: {url: URL}) => URL | undefined}>;
export type ServerScope = Readonly<{
  /** Hosted web scopes its paths; a server's own UI does not. */
  hosted: boolean;
  /** The signed-in server, once the shell is ready. Scoped hrefs use it. */
  setCurrent(serverId: string | undefined): void;
  current(): string | undefined;
  /** The server the last scoped address asked for, if it is not settled yet. */
  requested(): string | undefined;
  settle(): void;
  rewrite: Rewrite;
}>;

/** `onRequested` keeps a scoped address that names another (or a not-yet-known) server, before
 * anything can redirect away from it: sign-in may even leave the page. */
export function createServerScope(hosted: boolean, onRequested?: (link: ScopedLink) => void): ServerScope {
  let current: string | undefined, requested: string | undefined;
  const rewrite: Rewrite = Object.freeze({
    // Every address the router reads: a scoped one names its server and opens the plain route.
    input: ({url}) => {
      const scoped = parseScopedPath(url.pathname);
      if (!scoped) return undefined;
      const next = new URL(url.href);
      next.pathname = scoped.pathname;
      if (scoped.serverId !== current) {requested = scoped.serverId; onRequested?.({serverId: scoped.serverId, href: next.pathname + next.search + next.hash});}
      return next;
    },
    // Every address the router writes: on Hosted, a content or location screen carries the current server.
    output: ({url}) => {
      if (!hosted || !current || !isScopedScreenPath(url.pathname)) return undefined;
      const next = new URL(url.href);
      next.pathname = scopedPathname(url.pathname, current);
      return next;
    },
  });
  return Object.freeze({
    hosted, rewrite,
    setCurrent(serverId) {current = validServerId(serverId) ? serverId : undefined; if (requested === current) requested = undefined;},
    current: () => current,
    requested: () => requested,
    settle() {requested = undefined;},
  });
}

/** A scoped address kept across sign-in and profile choice, so the person lands where the link
 * pointed. Same-tab sessionStorage; only a strictly valid scoped path, never an arbitrary URL. */
export type ScopedLink = Readonly<{serverId: string; href: string}>;
const linkKey = 'portico.web.scoped-link.v1';
type LinkStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
export function createScopedLinkStore(storage: () => LinkStorage | undefined = () => undefined) {
  let remembered: ScopedLink | undefined;
  const valid = (value: unknown): value is ScopedLink => {
    if (!value || typeof value !== 'object') return false;
    const {serverId, href} = value as Record<string, unknown>;
    if (!validServerId(serverId) || typeof href !== 'string' || href.length > 2048 || !href.startsWith('/') || href.startsWith('//') || /[\\\s\x00-\x1f\x7f]/.test(href)) return false;
    try {const url = new URL(href, 'https://scope.invalid');return url.origin === 'https://scope.invalid' && isScopedScreenPath(url.pathname);} catch {return false;}
  };
  const clear = () => {remembered = undefined;try {storage()?.removeItem(linkKey);} catch { /* in-memory only */ }};
  return Object.freeze({
    remember(link: ScopedLink) {
      if (!valid(link)) return;
      remembered = Object.freeze({serverId: link.serverId, href: link.href});
      try {storage()?.setItem(linkKey, JSON.stringify(remembered));} catch { /* in-memory only */ }
    },
    peek(): ScopedLink | undefined {
      try {const raw = storage()?.getItem(linkKey);if (raw) {const value: unknown = JSON.parse(raw);if (valid(value)) return Object.freeze({serverId: value.serverId, href: value.href});clear();}} catch { /* unreadable: forget it */ }
      return remembered;
    },
    take(): ScopedLink | undefined {const value = this.peek();clear();return value;},
    clear,
  });
}

/** This tab's kept scoped link. */
export const scopedLinks = createScopedLinkStore(() => typeof window === 'undefined' ? undefined : window.sessionStorage);
