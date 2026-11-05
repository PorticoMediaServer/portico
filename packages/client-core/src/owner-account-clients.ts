import {HttpLocalApi} from './index.ts';
import {routePath} from '../../contracts/src/hosted/routes.ts';
import {AccountNotificationsClient} from './account-notifications.ts';
type NetworkFetch = ConstructorParameters<typeof HttpLocalApi>[2];
/** The captured Hosted credential remains with the original work/read authority. */
export function readOwnerHostedSecurity<T = unknown>(url: string, token: string, fetch: NetworkFetch, signal: AbortSignal): Promise<T> {
  return new HttpLocalApi(url, token, fetch).request<T>(routePath('account_security_get'), 'GET', undefined, signal);
}
export function ownerHostedNotifications(url: string, token: string, fetch: NetworkFetch, fallback: AbortSignal): AccountNotificationsClient {
  return new AccountNotificationsClient((path, method, body, signal) => new HttpLocalApi(url, token, fetch).request(path, method, body, signal ?? fallback)); // query-read: kept (a Portico Account preference over the hosted API, not a server document in the viewer's cache)
}

/** Personal token operations keep the original scope and GET default. */
export function ownerAccessTokenApi(scope: {request<T>(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<T>}) {
  return {request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => scope.request<T>(path, method ?? 'GET', body, signal)};
}
