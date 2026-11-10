/** Connection, sign-in and setup operations. These preserve their caller-owned authority,
 * cancellation, parsers and recovery; they deliberately do not enter the viewer query cache. */
import {routePath, routeBody, type RouteResponse} from '../../../contracts/src/routes.ts';
import {routePath as hostedPath, routeBody as hostedBody, encodedRoutePath as hostedEncodedPath} from '../../../contracts/src/hosted/routes.ts';
import {routeOrigin, routeJSON} from '../route-identity.ts';
import {clockOrigin, observeResponseClock} from '../server-clock.ts';
import {HttpLocalApi, type Viewer} from '../index.ts';
import {installationIdentity} from '../installation.ts';
import type {ViewerRequester} from './viewer-queries.ts';
import {callServer} from './server-api.ts';

export function readServerSystem(api: ViewerRequester, signal?: AbortSignal): Promise<unknown> {
  return callServer(api, 'GET /v1/system', {signal});
}
/** The signed-in account and its profiles, for `parseDirectSnapshot`. */
export async function readDirectAccount(api: ViewerRequester, signal?: AbortSignal): Promise<unknown> {
  const [me, profiles] = await Promise.all([callServer(api, 'GET /v1/me', {signal}), callServer(api, 'GET /v1/profiles', {signal})]);
  return {me, profiles};
}
/** Who the server says this sign-in acts for now (`GET /v1/me`), in the shape a session keeps.
 * `known` supplies the server id, which the answer does not repeat. */
export async function readViewerIdentity(api: ViewerRequester, known: Pick<Viewer, 'serverId'>, signal?: AbortSignal): Promise<{viewer: Viewer}> {
  const me = await callServer(api, 'GET /v1/me', {signal});
  if (!me || typeof me !== 'object' || typeof me.role !== 'string' || typeof me.account?.id !== 'string') throw Object.assign(new Error('The server returned an unreadable account.'), {code: 'invalid_response'});
  return {viewer: {accountId: me.account.id, profileId: typeof me.profile?.id === 'string' ? me.profile.id : '', serverId: known.serverId, authority: 'local', role: me.role}};
}
/** The platform owns shelf delivery and renewal; core builds its feed-token request. */
export function issueTopShelfToken(api: {request(path: string, method: 'POST', body: unknown, signal?: AbortSignal): Promise<unknown>}, installationId: string, signal?: AbortSignal): Promise<unknown> {
  return api.request(routePath('post_topshelf_token'), 'POST', {installationId}, signal);
}
/** Signs this device out (`DELETE /v1/auth/session`). Safe to repeat. */
export function revokeCurrentSession(api: ViewerRequester, signal?: AbortSignal): Promise<void> {
  return callServer(api, 'DELETE /v1/auth/session', {signal}).then(() => {});
}
export function readOwnerClaim<T = unknown>(api: ViewerRequester, signal?: AbortSignal): Promise<T> {
  return api.request<T>(routePath('get_networking_claim'), 'GET', undefined, signal);
}
export function cancelOwnerClaim<T = unknown>(api: ViewerRequester, expected: unknown, signal?: AbortSignal): Promise<T> {
  return api.request<T>(routePath('post_networking_claim_cancel'), 'POST', {expected}, signal);
}
export function prepareOwnerClaim<T = unknown>(api: ViewerRequester, purpose: 'switch' | 'change-owner', previousOwnerRole: string | undefined, expected: unknown, signal?: AbortSignal): Promise<T> {
  return api.request<T>(routePath('post_networking_claim_prepare'), 'POST', {purpose, ...(purpose === 'change-owner' ? {previousOwnerRole} : {}), expected}, signal);
}
export function exchangeOwnerReturn<T = unknown>(api: ViewerRequester, input: {handoff: {transactionId: string}; record: {state: string; verifier: string; installationId: string}; registrationId: string; serverChallenge: string; purpose: string}, signal?: AbortSignal): Promise<T> {
  return api.request<T>(routePath('post_auth_portico_return_exchange'), 'POST', {
    transactionId: input.handoff.transactionId, state: input.record.state, verifier: input.record.verifier,
    registrationId: input.registrationId, installationId: input.record.installationId, serverChallenge: input.serverChallenge, purpose: input.purpose,
  }, signal);
}
export function previewLocalInvitation<T = unknown>(api: ViewerRequester, capability: unknown, signal?: AbortSignal): Promise<T> {
  return api.request<T>(routePath('preview_invitation'), 'POST', capability, signal);
}
export function acceptLocalInvitation<T = unknown>(api: ViewerRequester, capability: object, username: string, password: string, name: string, signal?: AbortSignal): Promise<T> {
  return api.request<T>(routePath('post_access_invitations_accept'), 'POST', {...capability, username, password, ...(name.trim() ? {name: name.trim()} : {})}, signal);
}
export function createLocalTVAuthorization<T = unknown>(api: ViewerRequester, requestId: string, signal?: AbortSignal): Promise<T> {
  return api.request<T>(routePath('post_quick_connect'), 'POST', routeBody('post_quick_connect', {requestId, ...tvIdentity()}), signal);
}
export function pollLocalTVAuthorization<T = unknown>(api: ViewerRequester, deviceCode: string, signal?: AbortSignal): Promise<T> {
  return api.request<T>(routePath('post_quick_connect_token'), 'POST', routeBody('post_quick_connect_token', {deviceCode}), signal);
}
export function cancelLocalTVAuthorization<T = unknown>(api: ViewerRequester, deviceCode: string, signal?: AbortSignal): Promise<T> {
  return api.request<T>(routePath('post_quick_connect_cancel'), 'POST', routeBody('post_quick_connect_cancel', {deviceCode}), signal);
}
/** The original local approval opening exposes only its two core-built POSTs. */
export function createLocalDeviceApprovalTransport(request: HttpLocalApi['request'], opening: {
  signal: AbortSignal; check(): void; retired(): Error;
}): Pick<HttpLocalApi, 'request'> {
  const paths = new Set<string>([routePath('post_quick_connect_review'), routePath('post_quick_connect_decision')]);
  return Object.freeze({request: async <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    opening.check();
    if (method !== 'POST' || !paths.has(path)) throw opening.retired();
    const stop = signal ? AbortSignal.any([opening.signal, signal]) : opening.signal;
    stop.throwIfAborted();
    const value = await request<T>(path, method, body, stop);
    opening.check();stop.throwIfAborted();return value;
  }});
}
export function createHostedTVAuthorization<T = unknown>(api: ViewerRequester, requestId: string, publicKey: string, signal?: AbortSignal): Promise<T> {
  return api.request<T>(hostedPath('device_authorization_create'), 'POST', hostedBody('device_authorization_create', {requestId, ...tvIdentity(), protocol: 'tv-x25519-v1', publicKey}), signal);
}
export function pollHostedTVAuthorization<T = unknown>(api: ViewerRequester, deviceCode: string, signal?: AbortSignal): Promise<T> {
  return api.request<T>(hostedPath('tv_authorization_token'), 'POST', hostedBody('tv_authorization_token', {deviceCode}), signal);
}
export function cancelHostedTVAuthorization<T = unknown>(api: ViewerRequester, deviceCode: string, signal?: AbortSignal): Promise<T> {
  return api.request<T>(hostedPath('device_authorization_cancel'), 'POST', hostedBody('device_authorization_cancel', {deviceCode}), signal);
}
export function requestHostedRecovery(api: {request(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<unknown>}, identifier: string, requestId: string, signal?: AbortSignal): Promise<unknown> {
  return api.request(hostedPath('account_recovery_request'), 'POST', hostedBody('account_recovery_request', {identifier, requestId}), signal);
}
export function completeHostedRecovery(api: {request(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<unknown>}, token: string, password: string, factor: string, signal: AbortSignal): Promise<unknown> {
  return api.request(hostedPath('account_recovery_complete'), 'POST', hostedBody('account_recovery_complete', {token, newPassword: password, factor: factor.trim()}), signal);
}
export function confirmHostedEmailLink(api: {request(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<unknown>}, token: string, signal: AbortSignal): Promise<unknown> {
  return api.request(hostedPath('email_link_confirm'), 'POST', hostedBody('email_link_confirm', {token}), signal);
}
export function leaveHostedServer(api: {request(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<unknown>}, serverId: string, signal: AbortSignal): Promise<unknown> {
  return api.request(hostedEncodedPath('server_leave', {id: encodeURIComponent(serverId)}), 'POST', hostedBody('server_leave', {}), signal);
}
export function readHostedAccountIdentity<T = unknown>(api: ViewerRequester, signal?: AbortSignal): Promise<T> {
  return api.request<T>(hostedPath('me_get'), 'GET', undefined, signal);
}
export function lookupHostedClaim<T = unknown>(api: ViewerRequester, code: string, signal?: AbortSignal): Promise<T> {
  return api.request<T>(hostedPath('server_claim_lookup'), 'POST', {code}, signal);
}
export function approveHostedClaim<T = unknown>(api: ViewerRequester, code: string, signal?: AbortSignal): Promise<T> {
  return api.request<T>(hostedPath('server_claim_approve_code'), 'POST', {code}, signal);
}
/** Browser account transport keeps the captured token argument and cookie/CSRF policy. */
export type HostedSender = {send<T>(path: string, method: string, body?: unknown, signal?: AbortSignal, token?: string): Promise<T>};
export function listHostedServers<T = unknown>(api: HostedSender, signal?: AbortSignal, token?: string): Promise<T> {
  return api.send<T>(hostedPath('servers_list'), 'GET', undefined, signal, token);
}
export function lookupBrowserHostedClaim<T = unknown>(api: HostedSender, code: string, signal?: AbortSignal, token?: string): Promise<T> {
  return api.send<T>(hostedPath('server_claim_lookup'), 'POST', {code}, signal, token);
}
export function decideBrowserHostedClaim<T = unknown>(api: HostedSender, decision: 'approve' | 'deny', code: string, signal?: AbortSignal, token?: string): Promise<T> {
  return api.send<T>(hostedPath(decision === 'approve' ? 'server_claim_approve_code' : 'server_claim_deny'), 'POST', {code}, signal, token);
}
export function completeBrowserHostedReturn<T = unknown>(api: HostedSender, transactionId: string, signal?: AbortSignal, token?: string): Promise<T> {
  return api.send<T>(hostedEncodedPath('server_return_complete', {id: encodeURIComponent(transactionId)}), 'POST', {}, signal, token);
}
/** The platform supplies its certificate/redirect-refusing executor. The core owns
 * the anonymous connection URL, exact headers, clock observation and response bound. */
export async function readConnectionFacts(origin: string, signal: AbortSignal, fetcher: typeof fetch, normalizeOrigin = false): Promise<unknown> {
  const sentAt = Date.now();
  const response = await fetcher((normalizeOrigin ? routeOrigin(origin) : origin) + routePath('get_connection'), {
    method: 'GET', redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer', signal, headers: {Accept: 'application/json'},
  });
  observeResponseClock(clockOrigin(origin), response, sentAt);
  return routeJSON(response, signal, 8192);
}

function tvIdentity() {
  const value = installationIdentity();
  if (!value) throw Object.assign(new Error('native_tv_consent_invalid'), {code: 'invalid_consent'});
  return {deviceName: value.name, platform: value.platform, appVersion: value.appVersion};
}

export function readHostedSecurity<T = unknown>(api: ViewerRequester, signal?: AbortSignal): Promise<T> {
  return api.request<T>(hostedPath('account_security_get'), 'GET', undefined, signal);
}
export function readHostedServers<T = unknown>(api: ViewerRequester, signal?: AbortSignal): Promise<T> {
  return api.request<T>(hostedPath('servers_list'), 'GET', undefined, signal);
}

/** Anonymous TV authorization has no remembered bearer; native IO is injected. */
export function createHostedTVAuthorizationApi(origin: string, fetcher: typeof fetch): HttpLocalApi {
  return new HttpLocalApi(origin, '', fetcher);
}

export function cancelHostedIdentity(api: {request(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<unknown>}, signal?: AbortSignal): Promise<unknown> {
  return api.request(hostedPath('identity_cancel'), 'POST', undefined, signal);
}
