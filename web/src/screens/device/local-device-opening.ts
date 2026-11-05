import {serverNow} from '@core/server-clock.ts';
import type {HttpLocalApi, LocalSession} from '@core/index.ts';
import {createLocalDeviceApprovalTransport} from '@core/query/connection-operations.ts';

export type LocalDeviceLocation = Readonly<{pathname: string; href: string; state: Readonly<{__TSR_key?: unknown}>}>;
type Api = Pick<HttpLocalApi, 'baseUrl' | 'getRouteConnection' | 'request'>;
export type LocalDeviceOpening = Readonly<{
  identity: object;
  api: Pick<HttpLocalApi, 'request'>;
  signal: AbortSignal;
  current(): boolean;
  dispose(): void;
}>;
const retired = () => new DOMException('Device approval entry retired', 'AbortError');

/** Original local page/API/session only. This creates no session or approval and
 * cannot substitute the Hosted DevicePage or another selected server. */
export function captureLocalDeviceOpening(input: Readonly<{
  pageOrigin: string; api: Api; session(): LocalSession; isCurrent(): boolean;
  location: LocalDeviceLocation; getLocation(): LocalDeviceLocation; now?: () => number;
}>): LocalDeviceOpening | undefined {
  const {api, location, pageOrigin} = input;
  const session = input.session, isCurrent = input.isCurrent, getLocation = input.getLocation, now = input.now ?? serverNow;
  let connection: ReturnType<Api['getRouteConnection']>, original: LocalSession;
  try {
    if (!isCurrent() || !['http:', 'https:'].includes(new URL(pageOrigin).protocol) || new URL(pageOrigin).origin !== pageOrigin) return;
    connection = api.getRouteConnection();original = session();
  } catch {return;}
  const pin = connection?.pin, routeState = connection?.getSnapshot();
  const entry = location.state.__TSR_key, href = location.href;
  if (location.pathname !== '/device' || typeof entry !== 'string' || !entry || !isCurrent()
    || !connection || routeState?.phase !== 'ready' || ![connection.logicalOrigin, routeState.origin].includes(pageOrigin) || api.baseUrl !== connection.logicalOrigin
    || !pin?.serverId || !pin.publicKey || !pin.fingerprint || original.viewer.serverId !== pin.serverId
    || !original.sessionFamilyId || !original.viewer.accountId || !original.viewer.profileId || !original.viewer.role
    || !['local', 'hosted'].includes(original.viewer.authority)) return;
  const principal = Object.freeze({...original.viewer}), family = original.sessionFamilyId, horizon = original.authorizationHorizon;
  const key = Object.freeze({...pin}), logicalOrigin = connection.logicalOrigin, deadline = Date.parse(horizon);
  if (!Number.isFinite(deadline) || now() >= deadline) return;
  const life = new AbortController(), request = api.request.bind(api);
  let live = true;
  const dispose = () => {if (!live) return;live = false;life.abort();};
  const current = () => {
    if (!live || life.signal.aborted) return false;
    let valid = false;
    try {
      const actual = session(), route = getLocation(), latest = api.getRouteConnection();
      valid = isCurrent() && route === location && route.pathname === '/device' && route.href === href && route.state.__TSR_key === entry
        && api.baseUrl === logicalOrigin && latest === connection && latest.logicalOrigin === logicalOrigin
        && latest.pin.serverId === key.serverId && latest.pin.publicKey === key.publicKey && latest.pin.fingerprint === key.fingerprint
        && !['identity_mismatch', 'unconfirmed'].includes(latest.getSnapshot().phase)
        && actual.sessionFamilyId === family && actual.authorizationHorizon === horizon && now() < deadline
        && (['serverId', 'accountId', 'profileId', 'authority', 'role'] as const).every(field => actual.viewer[field] === principal[field]);
    } catch { /* Retired original getters do not select a replacement. */ }
    if (!valid) dispose();return valid;
  };
  const check = () => {if (!current()) throw retired();};
  const scoped = createLocalDeviceApprovalTransport(request, {signal: life.signal, check, retired});
  if (!current()) return;
  return Object.freeze({identity: Object.freeze({}), api: scoped, signal: life.signal, current, dispose});
}
