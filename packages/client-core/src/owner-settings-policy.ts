import type {HttpLocalApi} from './index.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import {serverPath} from './query/server-api.ts';
import {connectivityForm} from './server-admin/server-forms.ts';
/** Exact comparison set for the administrator's shared TV pages. This never
 * issues a request or admits an owner-only path. The app retains its role fence. */
export const ownerTvAdministratorReads: ReadonlySet<string> = new Set([
  // People: the accounts, who is playing now, and the invitations. The owner's stream limits stay owner-only.
  serverPath('GET /v1/admin/members'),
  serverPath('GET /v1/admin/sessions'),
  serverPath('GET /v1/admin/invitations'),
]);
export const readOwnerOptionalConfiguration = <T = unknown>(api: Pick<HttpLocalApi, 'request'>, signal: AbortSignal): Promise<T> => api.request<T>(routePath('get_settings_configuration'), 'GET', undefined, signal, {manageGate: false});
export const ownerAwayLimitForm = (deps: Parameters<typeof connectivityForm>[0], api: Pick<HttpLocalApi, 'request'>) => connectivityForm({...deps, api: {request: (path, method, body, signal) => api.request(path, method, body, signal, {manageGate: false})}});
