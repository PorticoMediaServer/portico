import {routePath as hostedPath} from '../../../contracts/src/hosted/routes.ts';
import type {ViewerRequester} from './viewer-queries.ts';
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

export type HostedSender = {send<T>(path: string, method: string, body?: unknown, signal?: AbortSignal, token?: string): Promise<T>};

export function listHostedServers<T = unknown>(api: HostedSender, signal?: AbortSignal, token?: string): Promise<T> {
  return api.send<T>(hostedPath('servers_list'), 'GET', undefined, signal, token);
}

export function readHostedAccountIdentity<T = unknown>(api: ViewerRequester, signal?: AbortSignal): Promise<T> {
  return api.request<T>(hostedPath('me_get'), 'GET', undefined, signal);
}

export function readHostedSecurity<T = unknown>(api: ViewerRequester, signal?: AbortSignal): Promise<T> {
  return api.request<T>(hostedPath('account_security_get'), 'GET', undefined, signal);
}

export function readHostedServers<T = unknown>(api: ViewerRequester, signal?: AbortSignal): Promise<T> {
  return api.request<T>(hostedPath('servers_list'), 'GET', undefined, signal);
}
