import {requestDeadlineMs, type HttpLocalApi, type LocalSession} from '../../../../../packages/client-core/src/index';
import {encodedRoutePath} from '../../../../../packages/contracts/src/routes';
import {SessionsClient} from '../../../../../packages/client-core/src/playback-v1/sessions';
import {readBoundedJson} from '../../../../../packages/client-core/src/bounded-json';
import {NativePreparingStarts, type NativePreparingOwner} from './preparing-start';

const owners = new WeakMap<HttpLocalApi, NativePreparingStarts>();
export function nativePreparingStarts(api: HttpLocalApi): NativePreparingStarts {
  let owner = owners.get(api);
  if (!owner) {owner = new NativePreparingStarts(); owners.set(api, owner);}
  return owner;
}

/** The original API route/pin and exact authenticated token generation. This
 * cleanup transport has NO refresh or Hosted recovery callback, and never
 * stores/selects a route or changes ordinary restore/request behavior. */
export function captureNativePreparingOwner(api: HttpLocalApi, read: () => LocalSession | undefined): NativePreparingOwner | undefined {
  try {
    const session = read(), route = api.getRouteConnection();
    if (!session || !route || !session.accessToken || !session.deviceId || !session.installationId || !session.sessionFamilyId || !session.tokenGeneration
      || !['local', 'hosted'].includes(session.viewer.authority)) return undefined;
    const {refreshToken: _refresh, ...selected} = session;
    const principal = Object.freeze({...selected, viewer: Object.freeze({...session.viewer})});
    const pin = route.pin.publicKey, origin = api.baseUrl, epoch = api.getCertificateSessionEpoch();
    const until = Math.min(Date.parse(principal.expiresAt), Date.parse(principal.authorizationHorizon));
    if (!pin || route.pin.serverId !== principal.viewer.serverId || principal.serverIdentity && principal.serverIdentity.publicKey !== pin || !Number.isFinite(until)) return undefined;
    const current = () => {
      try {
        const live = read();
        return Date.now() < until && api.baseUrl === origin && api.getCertificateSessionEpoch() === epoch
          && api.getRouteConnection() === route && route.pin.publicKey === pin && route.pin.serverId === principal.viewer.serverId
          && route.getSnapshot().phase === 'ready' && !!live && live.accessToken === principal.accessToken
          && live.sessionFamilyId === principal.sessionFamilyId && live.tokenGeneration === principal.tokenGeneration
          && live.expiresAt === principal.expiresAt && live.authorizationHorizon === principal.authorizationHorizon && live.deviceId === principal.deviceId && live.installationId === principal.installationId
          && live.viewer.serverId === principal.viewer.serverId && live.viewer.authority === principal.viewer.authority
          && live.viewer.accountId === principal.viewer.accountId && live.viewer.profileId === principal.viewer.profileId && live.viewer.role === principal.viewer.role;
      } catch {return false;}
    };
    if (!current()) return undefined;
    // Retain the original pin-bound fetch. Do not construct a second route-
    // subscribing HttpLocalApi, or borrow its mutable refresh/recovery callback.
    const fetcher = route.fetch;
    return Object.freeze<NativePreparingOwner>({current, stop: async (offered, signal, uninstalled, claim) => {
      const allowed = () => !signal.aborted && current() && uninstalled();
      const readPath = encodedRoutePath('get_playback_session', {id: encodeURIComponent(offered.id)});
      const stopPath = encodedRoutePath('stop_playback_session', {id: encodeURIComponent(offered.id)});
      const client = new SessionsClient({send: async request => {
        if (!allowed() || request.body !== undefined || !(request.method === 'GET' && request.path === readPath
          || request.method === 'DELETE' && request.path === stopPath)) throw new Error('Original preparing owner retired');
        const round = new AbortController(), abort = () => round.abort();
        signal.addEventListener('abort', abort, {once: true}); if (signal.aborted) abort();
        const timer = setTimeout(abort, requestDeadlineMs(request.path, request.method));
        try {
          const response = await fetcher(origin + request.path, {method: request.method, signal: round.signal,
            headers: {Accept: 'application/json', Authorization: 'Bearer ' + principal.accessToken},
            redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer', cache: 'no-store'});
          if (round.signal.aborted || !allowed()) {void response.body?.cancel().catch(() => {}); throw new Error('Original preparing owner retired');}
          const headers: Record<string, string> = {}; response.headers.forEach((value, name) => {headers[name] = value;});
          let body: unknown;
          if (request.method === 'GET' && response.status === 200) {
            if (response.body) body = await readBoundedJson(response, 1_048_576, round.signal);
            else {
              // RN's native networkRequest already bounds the buffered body;
              // its Response shim need not expose a ReadableStream. Keep the
              // same explicit JSON/byte limit before interpreting that buffer.
              const length = response.headers.get('Content-Length');
              if (response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase() !== 'application/json'
                || length !== null && (!/^\d+$/.test(length) || Number(length) > 1_048_576)) throw new Error('Unreadable preparing session');
              const text = await response.text();
              if (new TextEncoder().encode(text).length > 1_048_576) throw new Error('Unreadable preparing session');
              body = JSON.parse(text);
            }
          } else {void response.body?.cancel().catch(() => {});}
          if (round.signal.aborted || !allowed()) throw new Error('Original preparing owner retired');
          return {status: response.status, headers, body};
        } finally {clearTimeout(timer); signal.removeEventListener('abort', abort);}
      }});
      const observed = await client.get(offered.id, signal);
      if (observed.id !== offered.id || observed.kind !== offered.kind || observed.presentation.generation !== offered.presentation.generation || observed.role !== 'local'
        || observed.state === 'ended' || observed.itemId !== offered.itemId || observed.versionId !== offered.versionId
        || observed.queue?.queueId !== offered.queue?.queueId || observed.queue?.entryId !== offered.queue?.entryId
        || observed.discTitle?.id !== offered.discTitle?.id || observed.discTitle?.revision !== offered.discTitle?.revision || !claim()) return;
      await client.stop(offered.id, undefined, signal);
    }});
  } catch {return undefined;}
}
