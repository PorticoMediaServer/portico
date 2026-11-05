/** Anonymous connection facts retain their original wire and clock bounds. */
import {routePath} from '../../../contracts/src/routes.ts';
import {routeOrigin, routeJSON} from '../route-identity.ts';
import {clockOrigin, observeResponseClock} from '../server-clock.ts';

export async function readConnectionFacts(origin: string, signal: AbortSignal, fetcher: typeof fetch, normalizeOrigin = false): Promise<unknown> {
  const sentAt = Date.now();
  const response = await fetcher((normalizeOrigin ? routeOrigin(origin) : origin) + routePath('get_connection'), {
    method: 'GET', redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer', signal, headers: {Accept: 'application/json'},
  });
  observeResponseClock(clockOrigin(origin), response, sentAt);
  return routeJSON(response, signal, 8192);
}
