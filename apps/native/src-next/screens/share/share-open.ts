import {readCatalogueItem} from '../../../../../packages/client-core/src/query/catalogue-reads';
import {parseServerLinkTarget, type ServerLinkTarget} from '../../../../../packages/client-core/src/server-links';
import {decideShareLink, type ParsedShare, type ShareContext} from './share-link';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {nativeEntryRoute, nativeEntryRouteSupported, type NativeShellRoute} from '../../shell/routes';
import type {ShellDestination} from '../../shell/model';
import type {NativeContentLinkTarget} from '../../platform/content-links';

export type ShareRead = (path: string, signal: AbortSignal) => Promise<unknown>;
const shell = (destination: ShellDestination): NativeShellRoute => Object.freeze({kind: 'shell', destination: Object.freeze(destination)});

/** O-162: the native screen a shared destination opens, pushed over that server's Home (so Back
 * lands on Home). `undefined` means Home itself. The destination vocabulary is client-core's
 * (`SERVER_DESTINATIONS`); a screen the phone has no page for (the player, a Home row) opens Home. */
export async function shareRoute(target: ServerLinkTarget, read: ShareRead, signal: AbortSignal): Promise<NativeShellRoute | undefined> {
  const params = target.params;
  switch (target.destination) {
    case 'home': case 'player': case 'homeRow': return undefined;
    case 'search': return shell(params.q ? {kind: 'search', query: params.q} : {kind: 'search'});
    case 'readingSearch': return Object.freeze({kind: 'search-reading', query: params.q ?? ''});
    case 'libraries': return shell({kind: 'libraries'});
    case 'library': return params.libraryId ? shell({kind: 'library', libraryId: params.libraryId}) : undefined;
    case 'live': return shell({kind: 'live'});
    case 'saved': return shell({kind: 'saved'});
    case 'person': return params.personId ? Object.freeze({kind: 'person', personId: params.personId}) : undefined;
    case 'together': return Object.freeze({kind: 'together'});
    case 'dashboard': return Object.freeze({kind: 'server-dashboard'});
    case 'notifications': return Object.freeze({kind: 'notifications'});
    case 'item': case 'read': return params.itemId ? (await itemEntryRoute(params.itemId, read, signal)).route : undefined;
    default: return undefined;
  }
}


/** An id alone can't draw a title page: read the item from this server first (collections,
 * playlists, albums and books are all `item`; their own pages come from the entry's kind). */
export async function itemEntryRoute(itemId: string, read: ShareRead, signal: AbortSignal): Promise<Readonly<{entry: ContentEntry; route?: NativeShellRoute}>> {
  const item = await readCatalogueItem({request: path => read(path, signal)}, itemId, signal);
  const kind = item.kind === 'track' ? 'song' : item.kind === 'audiobook' ? 'audiobook_file' : item.kind;
  const entry = {...item, kind} as unknown as ContentEntry;
  return {entry, route: nativeEntryRouteSupported(entry) ? nativeEntryRoute(entry) : undefined};
}

/** What `portico://play/{id}` starts at once: a film, an episode or an extra (the Top Shelf's
 * Continue Watching cards). Anything else (a show, an album, a book) opens its page, whose own
 * Play decides what to start. */
const directlyPlayable = new Set(['movie', 'episode', 'extra']);
export type ContentLinkDeps = Readonly<{
  read: ShareRead; signal: AbortSignal; online: boolean;
  take: () => boolean; selectHome: () => void; open: (route: NativeShellRoute) => void;
  /** The ordinary Play/Resume of that title on this server. */
  play: (entry: ContentEntry) => Promise<void>;
  /** This device keeps downloads (phones and tablets; a TV has no Downloads page). */
  downloads: boolean;
}>;
export type ContentLinkOutcome = 'opened' | 'played' | 'no-access' | 'offline' | 'old-invite' | 'superseded';

/** N2-1: the page each app link opens over this server's Home (so Back lands on Home). */
function appLinkRoute(link: Exclude<NativeContentLinkTarget, {action: 'item' | 'play' | 'old-invite'}>, deps: ContentLinkDeps): NativeShellRoute | undefined {
  switch (link.action) {
    // web.getportico.tv/account names no server; on a server it is that viewer's Profile & account (web's /account alias).
    case 'account': return shell({kind: 'settings', page: 'profile'});
    case 'together': return Object.freeze({kind: 'together', joinCode: link.code});
    case 'downloads': return deps.downloads ? Object.freeze({kind: 'downloads'}) : undefined;
  }
}

/** N5-1: a content link against the server open now. The title page opens over Home (so Back
 * lands on the title, then Home); `play` then resumes it. A title this viewer can't read says so. */
export async function openContentLink(link: NativeContentLinkTarget, deps: ContentLinkDeps): Promise<ContentLinkOutcome> {
  if (link.action !== 'item' && link.action !== 'play') {
    if (!deps.take()) return 'superseded';
    deps.selectHome();
    // An old Portico Account invitation can't be used any more: the caller says to ask for a new one.
    if (link.action === 'old-invite') return 'old-invite';
    const route = appLinkRoute(link, deps);
    if (route) deps.open(route);
    return 'opened';
  }
  let found: Awaited<ReturnType<typeof itemEntryRoute>>;
  try {found = await itemEntryRoute(link.itemId, deps.read, deps.signal);} catch {
    if (!deps.take()) return 'superseded';
    return deps.online ? 'no-access' : 'offline';
  }
  if (!deps.take()) return 'superseded';
  deps.selectHome();
  if (found.route) deps.open(found.route);
  if (link.action !== 'play' || !directlyPlayable.has(found.entry.kind)) return 'opened';
  await deps.play(found.entry);
  return 'played';
}

export type SharedLinkDeps = Readonly<ShareContext & {
  read: ShareRead; signal: AbortSignal;
  /** Claim the link being acted on; false when a newer link replaced it meanwhile. */
  take: () => boolean;
  selectHome: () => void;
  open: (route: NativeShellRoute) => void;
  switchServer: (serverId: string) => Promise<void>;
}>;
export type SharedLinkOutcome = 'opened' | 'switching' | 'waiting' | 'no-access' | 'offline' | 'superseded';
const parse = (url: string): ParsedShare<ServerLinkTarget> | undefined => {
  const target = parseServerLinkTarget(url);
  return target && {serverId: target.serverId, destination: target};
};

/** One shared link against this device's state, for cold starts, warm opens and resumes alike. */
export async function openSharedLink(webUrl: string, deps: SharedLinkDeps): Promise<SharedLinkOutcome> {
  const decision = decideShareLink(webUrl, parse, deps);
  if (decision.kind === 'sign-in') return 'waiting';
  if (decision.kind === 'unavailable') {if (!deps.take()) return 'superseded'; return decision.reason === 'offline' ? 'offline' : 'no-access';}
  if (decision.kind === 'switch') {await deps.switchServer(decision.serverId); return 'switching';}
  const route = await shareRoute(decision.destination, deps.read, deps.signal).catch(() => undefined);
  if (!deps.take()) return 'superseded';
  // Home's first screen under the link, so Back from it lands on this server's Home.
  deps.selectHome();
  if (route) deps.open(route);
  return 'opened';
}
