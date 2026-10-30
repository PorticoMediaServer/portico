/** O-161/O-162: one shareable link format everywhere (Justin, 29 Oct), Plex-style:
 * `https://web.getportico.tv/s/<serverId>/<screen>`. `<serverId>` is the server's stable pinned
 * ID, never an address. Web reads and writes it in the address bar; the apps open it as a
 * universal/app link and run the same flow (switch server, sign in, choose a profile, continue,
 * or say the link cannot be opened). This grammar is frozen: change it only with every client. */

/** The canonical origin of shared links. `getportico.tv` is accepted for the same paths. */
export const SERVER_LINK_ORIGIN = 'https://web.getportico.tv';
const linkHosts: readonly string[] = ['web.getportico.tv', 'getportico.tv'];

/** Screens that show a server's content or a place on it. Account, server choice, setup,
 * sign-in, settings, device, help and about are never scoped. */
const scopedScreen = /^\/(?:|search|search\/reading|libraries|library\/[^/]+|live|saved|item\/[^/]+|home\/rows\/[^/]+|player|person\/[^/]+|read\/[^/]+|watch-together|dashboard|notifications)$/;
const serverIdPattern = /^[A-Za-z0-9._~-]{1,128}$/;

export const isScopedScreenPath = (pathname: string): boolean => scopedScreen.test(pathname);
export const validServerId = (value: unknown): value is string => typeof value === 'string' && serverIdPattern.test(value) && value !== '.' && value !== '..';

export type ServerLinkPath = Readonly<{serverId: string; pathname: string}>;
/** `/s/<serverId>` + a scoped screen path, else undefined. `/s/<id>` and `/s/<id>/` are Home. */
export function parseServerLinkPath(pathname: string): ServerLinkPath | undefined {
  const match = /^\/s\/([^/]+)(\/.*)?$/.exec(pathname);
  if (!match) return undefined;
  let serverId: string;
  try {serverId = decodeURIComponent(match[1]!);} catch {return undefined;}
  const rest = !match[2] || match[2] === '/' ? '/' : match[2];
  return validServerId(serverId) && isScopedScreenPath(rest) ? Object.freeze({serverId, pathname: rest}) : undefined;
}
/** The scoped path of a screen path on `serverId`; a path that is not a scoped screen is unchanged. */
export function scopedPathname(pathname: string, serverId: string): string {
  if (!isScopedScreenPath(pathname) || !validServerId(serverId)) return pathname;
  return `/s/${encodeURIComponent(serverId)}${pathname === '/' ? '' : pathname}`;
}

export type ServerLink = ServerLinkPath & Readonly<{search: string; hash: string}>;
/** A whole shared link: https, one of the link hosts, no user, port or credentials, a scoped path.
 * Anything else is not a Portico server link (an app hands it back to the browser). */
export function parseServerLink(href: string): ServerLink | undefined {
  let url: URL;
  try {url = new URL(href);} catch {return undefined;}
  if (url.protocol !== 'https:' || !linkHosts.includes(url.hostname.toLowerCase()) || url.port || url.username || url.password) return undefined;
  const path = parseServerLinkPath(url.pathname);
  return path ? Object.freeze({...path, search: url.search, hash: url.hash}) : undefined;
}
/** The shareable link to a screen on a server; `search` and `hash` are kept as given. */
export function serverLink(serverId: string, pathname: string, search = '', hash = '', origin = SERVER_LINK_ORIGIN): string | undefined {
  if (!validServerId(serverId) || !isScopedScreenPath(pathname)) return undefined;
  return origin + scopedPathname(pathname, serverId) + (search && !search.startsWith('?') ? '?' + search : search) + (hash && !hash.startsWith('#') ? '#' + hash : hash);
}

/** Every shareable destination on a server, with its path parameter (if any). The names are the
 * frozen vocabulary the apps route on; `params` carries the path parameter by name plus the
 * screen's own query parameters, all as strings. */
export const SERVER_DESTINATIONS = Object.freeze({
  home: {path: '/'}, search: {path: '/search'}, readingSearch: {path: '/search/reading'}, libraries: {path: '/libraries'},
  library: {path: '/library/', param: 'libraryId'}, live: {path: '/live'}, saved: {path: '/saved'}, item: {path: '/item/', param: 'itemId'},
  homeRow: {path: '/home/rows/', param: 'rowId'}, player: {path: '/player'}, person: {path: '/person/', param: 'personId'},
  read: {path: '/read/', param: 'itemId'}, together: {path: '/watch-together'}, dashboard: {path: '/dashboard'}, notifications: {path: '/notifications'},
} as const satisfies Record<string, Readonly<{path: string; param?: string}>>);
export type ServerDestination = keyof typeof SERVER_DESTINATIONS;
export type ServerLinkTarget = Readonly<{serverId: string; destination: ServerDestination; params: Readonly<Record<string, string>>}>;
/** Justin, 29 Oct: no timestamp links. A `t` is never written into a link and never read from one. */
const forbiddenParams: readonly string[] = ['t'];
const pathParam = /^[^/?#\s\x00-\x1f\x7f]{1,512}$/;

function destinationOf(pathname: string): Readonly<{destination: ServerDestination; params: Record<string, string>}> | undefined {
  for (const [name, spec] of Object.entries(SERVER_DESTINATIONS) as [ServerDestination, {path: string; param?: string}][]) {
    if (!('param' in spec) || !spec.param) {if (pathname === spec.path) return {destination: name, params: {}}; continue;}
    if (!pathname.startsWith(spec.path)) continue;
    let value: string;
    try {value = decodeURIComponent(pathname.slice(spec.path.length));} catch {return undefined;}
    if (pathParam.test(value)) return {destination: name, params: {[spec.param]: value}};
  }
  return undefined;
}
/** A shared link as the destination it names: `{serverId, destination, params}`, or undefined when
 * it is not a Portico server link (an app hands such a link back to the browser). */
export function parseServerLinkTarget(href: string): ServerLinkTarget | undefined {
  const link = parseServerLink(href);
  const found = link && destinationOf(link.pathname);
  if (!link || !found) return undefined;
  const params: Record<string, string> = {...found.params};
  for (const [key, value] of new URLSearchParams(link.search)) if (!forbiddenParams.includes(key) && !(key in params)) params[key] = value;
  return Object.freeze({serverId: link.serverId, destination: found.destination, params: Object.freeze(params)});
}
/** A destination's own plain path and query (`/item/abc`, `?q=…`), the same on every server; a
 * missing or unsafe path parameter yields none. A timestamp is never included. */
export function destinationLocation(destination: ServerDestination, params: Readonly<Record<string, string>>): Readonly<{pathname: string; search: string}> | undefined {
  const spec: {path: string; param?: string} = SERVER_DESTINATIONS[destination];
  if (!spec) return undefined;
  let pathname = spec.path;
  if (spec.param) {
    const value = params[spec.param];
    if (typeof value !== 'string' || !pathParam.test(value)) return undefined;
    pathname += encodeURIComponent(value);
  }
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) if (key !== spec.param && !forbiddenParams.includes(key) && typeof value === 'string') query.set(key, value);
  const search = query.toString();
  return Object.freeze({pathname, search: search ? `?${search}` : ''});
}
/** The one canonical shareable link to a destination on a server. A missing or unsafe path
 * parameter, or a timestamp, yields no link. */
export function buildServerLink(target: ServerLinkTarget, origin = SERVER_LINK_ORIGIN): string | undefined {
  const location = validServerId(target.serverId) ? destinationLocation(target.destination, target.params) : undefined;
  return location && serverLink(target.serverId, location.pathname, location.search, '', origin);
}

/** Invitations and the other app-link entry pages (not server-scoped; they predate O-161):
 * `/join#code=…&server=…` (a server invitation), `/join#invite=…` (an old account invitation),
 * `/together/join#code=…` (Watch Together). Values stay in the fragment, never the query. */
export type InviteLink =
  | Readonly<{kind: 'server-invite'; code: string; server: string}>
  | Readonly<{kind: 'account-invite'; invite: string}>
  | Readonly<{kind: 'together'; code: string}>;
const fragmentValue = /^[A-Za-z0-9._~:/%-]{1,512}$/;
export function parseInviteLink(href: string): InviteLink | undefined {
  let url: URL;
  try {url = new URL(href);} catch {return undefined;}
  if (url.protocol !== 'https:' || !linkHosts.includes(url.hostname.toLowerCase()) || url.port || url.username || url.password || url.search) return undefined;
  const pairs = new URLSearchParams(url.hash.slice(1)), value = (key: string) => {const v = pairs.get(key);return v !== null && fragmentValue.test(v) ? v : undefined;};
  const path = url.pathname.replace(/\/$/, '');
  if (path === '/join') {
    const code = value('code'), server = value('server'), invite = value('invite');
    if (code && server && [...pairs.keys()].length === 2) return Object.freeze({kind: 'server-invite', code, server});
    if (invite && [...pairs.keys()].length === 1) return Object.freeze({kind: 'account-invite', invite});
    return undefined;
  }
  if (path === '/together/join') {const code = value('code');return code && [...pairs.keys()].length === 1 ? Object.freeze({kind: 'together', code}) : undefined;}
  return undefined;
}
export function buildInviteLink(link: InviteLink, origin = SERVER_LINK_ORIGIN): string | undefined {
  const check = (...values: string[]) => values.every(value => fragmentValue.test(value));
  if (link.kind === 'server-invite') return check(link.code, link.server) ? `${origin}/join#code=${encodeURIComponent(link.code)}&server=${encodeURIComponent(link.server)}` : undefined;
  if (link.kind === 'account-invite') return check(link.invite) ? `${origin}/join#invite=${encodeURIComponent(link.invite)}` : undefined;
  return check(link.code) ? `${origin}/together/join#code=${encodeURIComponent(link.code)}` : undefined;
}
