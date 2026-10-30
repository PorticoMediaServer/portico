import type {HttpLocalApi, LocalSession} from '@core/index.ts';
import {localV1Http} from '@core/playback-v1/local-http.ts';
import {SessionsClient} from '@core/playback-v1/sessions.ts';
import {decodeOpen} from '../../../../packages/contracts/src/runtime.ts';
import type {GetPlaybackSessionResponse} from '../../../../packages/contracts/src/types.ts';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import {call, enc} from '@core/playback-v1/http.ts';
import {WebPreparingStarts} from './preparing-start';

/** Captured once by the shell, before any Home/playback stack exists. A retired
 * player view may release only its original uninstalled preparing resource;
 * changing the authenticated device/family/viewer/pin/origin denies that release.
 * Token refresh within the same original horizon does not lend new authority. */
export function createWebPreparingStarts(api: HttpLocalApi, session: LocalSession, readSession: () => LocalSession, isCurrent: () => boolean): WebPreparingStarts | undefined {
  const route = api.getRouteConnection(), pin = route?.pin.publicKey, origin = api.baseUrl;
  const principal = Object.freeze({...session.viewer, family: session.sessionFamilyId, device: session.deviceId,
    installation: session.installationId, horizon: session.authorizationHorizon});
  const horizon = Date.parse(principal.horizon);
  if (!route || !pin || !principal.device || !principal.installation || !principal.family || !Number.isFinite(horizon) ||
    principal.authority !== 'local' && principal.authority !== 'hosted') return undefined;
  const current = () => {
    try {
      if (!isCurrent() || Date.now() >= horizon || api.baseUrl !== origin || api.getRouteConnection() !== route ||
        route.pin.publicKey !== pin || route.pin.serverId !== principal.serverId || route.getSnapshot().phase !== 'ready') return false;
      const now = readSession();
      return now.sessionFamilyId === principal.family && now.deviceId === principal.device && now.installationId === principal.installation &&
        now.authorizationHorizon === principal.horizon && now.viewer.serverId === principal.serverId && now.viewer.authority === principal.authority &&
        now.viewer.accountId === principal.accountId && now.viewer.profileId === principal.profileId && now.viewer.role === principal.role;
    } catch {return false;}
  };
  if (!current()) return undefined;
  const original = localV1Http(api);
  const cleanupHttp = (signal: AbortSignal, uninstalled: () => boolean) => ({send: async (request: Parameters<typeof original.send>[0]) => {
    if (signal.aborted || !current() || !uninstalled()) throw new DOMException('Original playback authority retired', 'AbortError');
    const response = await original.send({...request, signal});
    if (signal.aborted || !current() || !uninstalled()) throw new DOMException('Original playback authority retired', 'AbortError');
    return response;
  }});
  return new WebPreparingStarts({isCurrent: current, stop: async (session, signal, uninstalled, claimStop) => {
    // This is the original authenticated API and its ordinary bounded request
    // lifetime, separate from the aborted Play signal.
    const http = cleanupHttp(signal, uninstalled);
    const client = new SessionsClient(http), observed = await client.get(session.id, signal);
    // Read only this captured resource; a changed generation/target is refusal,
    // never a new cleanup candidate or an adoption into the current player.
    if (observed.id !== session.id || observed.presentation.generation !== session.presentation.generation || observed.role !== 'local' ||
      observed.state === 'ended' || observed.itemId !== session.itemId || observed.versionId !== session.versionId ||
      observed.queue?.queueId !== session.queue?.queueId || observed.queue?.entryId !== session.queue?.entryId ||
      observed.discTitle?.id !== session.discTitle?.id || observed.discTitle?.revision !== session.discTitle?.revision) return;
    if (!claimStop()) return;
    await client.stop(session.id, undefined, signal);
  }, stopChannel: async (body, actual, signal, uninstalled, claimStop) => {
    const http = cleanupHttp(signal, uninstalled);
    // The general playable Session intentionally omits channelId. Read the
    // official view instead; never fabricate a Session from late response facts.
    const response = await call(http, {method: 'GET', path: encodedRoutePath('get_playback_session', {id: enc(actual.id)}), signal});
    // O-159 open tier (R-09): a newer server's enum value in an optional member must not leave Stop doing nothing.
    const observed = decodeOpen('GetPlaybackSessionResponse', response.body) as GetPlaybackSessionResponse;
    if (observed.id !== actual.id || observed.presentation.generation !== actual.generation || observed.role !== 'local' ||
      observed.state === 'ended' || !['live', 'channel'].includes(observed.kind) || observed.channelId !== body.channelId ||
      observed.queue || observed.itemId || observed.versionId || observed.discTitle) return;
    // The guide generation stays in the original POST receipt; SessionView does
    // not echo it. A later guide/source read cannot replace that admission.
    if (!claimStop()) return;
    await new SessionsClient(http).stop(actual.id, undefined, signal);
  }});
}
