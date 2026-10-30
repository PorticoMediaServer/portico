import {parseServerLinkTarget, type ServerLinkTarget} from '../../../../../packages/client-core/src/server-links';
import {decideShareLink, type ParsedShare, type ShareContext} from './share-link';
import {parseMediaItem} from '../../../../../packages/client-core/src/media-item';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {encodedRoutePath} from '../../../../../packages/contracts/src/routes';
import {nativeEntryRoute, nativeEntryRouteSupported, type NativeShellRoute} from '../../shell/routes';
import type {ShellDestination} from '../../shell/model';

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
    case 'item': case 'read': {
      // An id alone can't draw a title page: read the item from this server first (collections,
      // playlists, albums and books are all `item`; their own pages come from the entry's kind).
      if (!params.itemId) return undefined;
      const item = parseMediaItem(await read(encodedRoutePath('get_items_id', {id: encodeURIComponent(params.itemId)}), signal));
      const kind = item.kind === 'track' ? 'song' : item.kind === 'audiobook' ? 'audiobook_file' : item.kind;
      const entry = {...item, kind} as unknown as ContentEntry;
      return nativeEntryRouteSupported(entry) ? nativeEntryRoute(entry) : undefined;
    }
    default: return undefined;
  }
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
