import type {AppSessionRecord} from '../../../../packages/client-core/src/app-sessions';
import type {RememberedServer} from '../../../../packages/client-core/src/server-connections';
import {parseDirectName} from '../../../../packages/client-core/src/network-locality';

/** Direct names moved to IP-encoded `<ip>.ptc-<namespace>.<domain>` (portico.direct) on 28 Oct.
 * A stored `c-…`, `c4-…` or `c6-…` route is stale by definition: it never answers, and each try
 * costs a whole connect timeout before the next route is attempted (E-mobile-01). */
const STALE_LABEL = /^c[46]?-[a-z0-9-]+$/;
export function staleDirectRoute(url: string): boolean {
  let host: string;
  try {host = new URL(url).hostname.toLowerCase();} catch {return false;}
  return STALE_LABEL.test(host.split('.')[0] ?? '') && !parseDirectName(host);
}
const STALE_IN_TEXT = /https?:\/\/c[46]?-[a-z0-9-]+\./i;

/** The same routes without stale direct names. Signed route documents can't be edited, so one
 * that names a stale host is dropped; the connection fetches a fresh one when it needs it. */
export function withoutStaleRoutes(routes: RememberedServer['routes']): RememberedServer['routes'] {
  const paired = routes.paired.filter(url => !staleDirectRoute(url));
  const lastGood = routes.lastGood && staleDirectRoute(routes.lastGood) ? undefined : routes.lastGood;
  const signed = (value: RememberedServer['routes']['current']) => value && STALE_IN_TEXT.test(JSON.stringify(value)) ? undefined : value;
  const current = signed(routes.current), previous = signed(routes.previous);
  if (paired.length === routes.paired.length && lastGood === routes.lastGood && current === routes.current && previous === routes.previous) return routes;
  return {...routes, paired, ...(lastGood ? {lastGood} : {lastGood: undefined}), current, previous};
}

/** Every saved server's route evidence, purged of stale direct names. Selection, credentials and
 * revision are unchanged: only addresses that can never answer are forgotten. */
export function purgeStaleRoutes(record: AppSessionRecord | undefined): AppSessionRecord | undefined {
  if (!record) return record;
  let changed = false;
  const servers = record.servers.map(server => {
    const routes = withoutStaleRoutes(server.connection.routes);
    if (routes === server.connection.routes) return server;
    changed = true;
    return {...server, connection: {...server.connection, routes}};
  });
  return changed ? {...record, servers} : record;
}
