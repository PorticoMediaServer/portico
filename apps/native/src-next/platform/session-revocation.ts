import {HttpLocalApi} from '../../../../packages/client-core/src';
import {routePath} from '../../../../packages/contracts/src/routes';
import type {RouteConnection} from '../../../../packages/client-core/src/route-connection';
import type {RememberedServer} from '../../../../packages/client-core/src/server-connections';
import {refreshStoredSession} from '../../../../packages/client-core/src/server-connections';
import {NativeQueueStore} from './queue-storage';
import {nativeNetworkFetch, nativeServerEnvironment} from './server-connections';

/** E-mobile-01 ruling (Claude, 29 Oct): ending a replaced or temporary session stays mandatory but
 * never blocks the way to Home. The credential has already left the app's session store (it can
 * never be used as a sign-in again); its only copy is this revocation-only queue, in the same
 * encrypted native store, written BEFORE the first attempt so a closed app can't lose it. Each try
 * is bounded and goes over the route that is live right now, never the session's stored routes. */
export const REVOCATION_TIMEOUT_MS = 4000;
const KEY = 'accounts.revocations.v1';
type Pending = Readonly<{id: string; record: RememberedServer}>;
const read = (raw: unknown): Pending[] => Array.isArray(raw) ? raw.filter((value): value is Pending =>
  !!value && typeof value === 'object' && typeof (value as Pending).id === 'string' && !!(value as Pending).record?.session) : [];
const store = () => new NativeQueueStore();
/** One entry per server and profile replacement: a newer replacement supersedes the older one. */
const idOf = (record: RememberedServer) => `${record.pin.serverId}:${record.session.viewer.accountId}:${record.session.viewer.profileId}`;
/** The queue never grows past this; the oldest entry is dropped first. */
export const REVOCATION_QUEUE_CAP = 8;
/** A credential is worth revoking only while it could still be used: until its family's
 * authorization horizon when it can renew, otherwise until its access token expires. */
const expiry = (record: RememberedServer) => Date.parse(record.session.refreshToken ? record.session.authorizationHorizon : record.session.expiresAt);
const usable = (entry: Pending, now: number) => Number.isFinite(expiry(entry.record)) && expiry(entry.record) > now;

/** The only reader and writer of the queue. Every write drops expired entries and applies the cap. */
async function change(update: (pending: Pending[]) => Pending[]): Promise<Pending[]> {
  return store().change(KEY, raw => {
    const now = Date.now();
    const next = update(read(raw).filter(entry => usable(entry, now))).filter(entry => usable(entry, now)).slice(-REVOCATION_QUEUE_CAP);
    return {record: next.length ? next : null, value: next};
  });
}

/** Gone: the server ended it (204) or no longer knows it (401/404). Anything else is retried later. */
const gone = (error: unknown) => {const status = (error as {status?: unknown} | null)?.status; return status === 401 || status === 404;};

/** One bounded sign-out of `record` over `live`. Renews first when the access token has expired. */
export async function revokeOverLiveRoute(record: RememberedServer, live: RouteConnection, timeoutMs = REVOCATION_TIMEOUT_MS): Promise<'revoked' | 'gone'> {
  // Hermes has no AbortSignal.timeout; one timer bounds the whole renewal and sign-out.
  const controller = new AbortController(), timer = setTimeout(() => controller.abort(), timeoutMs), signal = controller.signal;
  let captured: RememberedServer | undefined = record;
  const env = {...nativeServerEnvironment(record.pin.serverId), storage: {read: async () => captured, change: async (update: (value: RememberedServer | undefined) => RememberedServer | undefined) => {captured = update(captured);}}};
  const api = new HttpLocalApi(record.logicalOrigin, record.session.accessToken, nativeNetworkFetch);
  api.useRoutes(live);
  try {
    if (Date.parse(record.session.expiresAt) <= Date.now() + 5000 && record.session.refreshToken) {
      const renewed = await refreshStoredSession({env, api, family: {sessionFamilyId: record.session.sessionFamilyId, serverId: record.pin.serverId}, signal});
      api.setAccessToken(renewed.accessToken);
    }
    await api.request<void>(routePath('delete_sessions_current'), 'DELETE', undefined, signal);
    return 'revoked';
  } catch (error) {
    if (gone(error)) return 'gone';
    throw error;
  } finally {clearTimeout(timer);}
}

/** Queue first, then try once in the background. Never awaited by the path to Home. */
export function revokeInBackground(record: RememberedServer, live: RouteConnection | undefined): Promise<void> {
  const id = idOf(record);
  return change(pending => [...pending.filter(value => value.id !== id), {id, record}]).then(async () => {
    if (!live) return;
    try {await revokeOverLiveRoute(record, live); await change(pending => pending.filter(value => value.id !== id));}
    catch { /* Stays queued: the next launch or reconnect to this server tries again. */ }
  }, () => { /* The encrypted store refused; nothing else to do now. */ });
}

/** Retry every queued revocation for `serverId` over its live route. Bounded per entry. */
export async function drainRevocations(serverId: string, live: RouteConnection): Promise<number> {
  const queued = (await change(value => value)).filter(value => value.record.pin.serverId === serverId);
  let done = 0;
  for (const entry of queued) {
    try {await revokeOverLiveRoute(entry.record, live); await change(pending => pending.filter(value => value.id !== entry.id)); done++;}
    catch { /* Next time. */ }
  }
  return done;
}
