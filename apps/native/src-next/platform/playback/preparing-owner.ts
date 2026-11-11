import {clockOrigin, serverNow} from '../../../../../packages/client-core/src/server-clock';
import type {HttpLocalApi, LocalSession} from '../../../../../packages/client-core/src/index';
import {stopOriginalPreparingSession} from '../../../../../packages/client-core/src/playback-v1/preparing-cleanup';
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
    if (!pin || route.pin.serverId !== principal.viewer.serverId || !Number.isFinite(until)) return undefined;
    const current = () => {
      try {
        const live = read();
        return serverNow(clockOrigin(origin)) < until && api.baseUrl === origin && api.getCertificateSessionEpoch() === epoch
          && api.getRouteConnection() === route && route.pin.publicKey === pin && route.pin.serverId === principal.viewer.serverId
          // Exact token on purpose: the stop runs with the original token, which the server refuses after a rotation anyway.
          && route.getSnapshot().phase === 'ready' && !!live && live.accessToken === principal.accessToken
          && live.sessionFamilyId === principal.sessionFamilyId && live.tokenGeneration === principal.tokenGeneration
          && live.expiresAt === principal.expiresAt && live.authorizationHorizon === principal.authorizationHorizon && live.deviceId === principal.deviceId && live.installationId === principal.installationId
          && live.viewer.serverId === principal.viewer.serverId && live.viewer.authority === principal.viewer.authority
          && live.viewer.accountId === principal.viewer.accountId && live.viewer.profileId === principal.viewer.profileId && live.viewer.role === principal.viewer.role;
      } catch {return false;}
    };
    if (!current()) return undefined;
    // Retain the original pin-bound fetch without refresh or route subscriptions.
    const fetcher = route.fetch;
    return Object.freeze<NativePreparingOwner>({current, stop: (offered, signal, uninstalled, claim) =>
      stopOriginalPreparingSession(fetcher, origin, principal.accessToken, current, offered, signal, uninstalled, claim)});
  } catch {return undefined;}
}
