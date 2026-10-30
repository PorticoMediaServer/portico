import {buildServerLink, destinationLocation, type ServerDestination} from '@core/server-links.ts';
import {copyText} from '../platform/copy-text';
import {serverScope} from '../router';

/** O-161/O-162: the full shareable address of a place, from the shared link builder. On Hosted it
 * is the one canonical link to this server (`https://web.getportico.tv/s/<serverId>/…`), which the
 * apps open too; a server's own UI shares its own plain address. Never a server address, never a
 * timestamp. */
export function shareableLink(destination: ServerDestination, params: Readonly<Record<string, string>> = {}): string | undefined {
  const current = serverScope.current();
  if (serverScope.hosted) return current ? buildServerLink({serverId: current, destination, params}, location.origin) : undefined;
  const own = destinationLocation(destination, params);
  return own && location.origin + own.pathname + own.search;
}
/** Call from the menu choice itself: the copy must run inside the person's gesture. */
export function copyLink(destination: ServerDestination, params: Readonly<Record<string, string>> = {}, source?: HTMLElement): Promise<void> {
  const href = shareableLink(destination, params);
  return href ? copyText(href, source) : Promise.reject(new Error('link_unavailable'));
}
