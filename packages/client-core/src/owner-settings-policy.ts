import type {HttpLocalApi} from './index.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import {serverPath} from './query/server-api.ts';
import {namedSettingsForm} from './server-admin/server-forms.ts';
/** Exact comparison set for the administrator's shared TV pages. This never
 * issues a request or admits an owner-only path. The app retains its role fence. */
export const ownerTvAdministratorReads: ReadonlySet<string> = new Set([
  // People: the accounts, who is playing now, and the invitations. The owner's stream limits stay owner-only.
  serverPath('GET /v1/admin/members'),
  serverPath('GET /v1/admin/sessions'),
  serverPath('GET /v1/admin/invitations'),
]);
/** TV page readers need ETags, but can never acquire a writer through the raw transport. */
export function withOwnerTvReadRaw<A extends Pick<HttpLocalApi, 'request'>>(api: A, http: Partial<Pick<HttpLocalApi, 'requestRaw'>>, guard: (path: string) => void): A & Pick<HttpLocalApi, 'requestRaw'> {
 const requestRaw: HttpLocalApi['requestRaw'] = async (path, method, options) => {
  if (method !== 'GET' || options?.body !== undefined) throw new Error('native_tv_server_read_denied');
  guard(path);
  if (!http.requestRaw) throw new Error('native_tv_server_read_unavailable');
  const answer = await http.requestRaw(path, 'GET', options); guard(path); return answer;
 };
 return Object.assign(Object.create(api) as A, {request: api.request.bind(api), requestRaw});
}
export const readOwnerOptionalConfiguration = <T = unknown>(api: Pick<HttpLocalApi, 'request'>, signal: AbortSignal): Promise<T> => api.request<T>(routePath('get_settings_configuration'), 'GET', undefined, signal, {manageGate: false});
/** The Streaming document, read quietly for the owner's own Quality page, which only shows its away-from-home limit. */
export const ownerAwayLimitForm = (deps: Parameters<typeof namedSettingsForm>[0], api: Pick<HttpLocalApi, 'request'>) => namedSettingsForm({...deps, api: {request: (path, method, body, signal) => api.request(path, method, body, signal, {manageGate: false})}}, 'streaming');
