import {ownerTvAdministratorReads as administratorReads, withOwnerTvReadRaw} from '../../../../../../packages/client-core/src/owner-settings-policy';
import type {HttpLocalApi} from '../../../../../../packages/client-core/src';
import type {DirectSnapshot} from '../../../../../../packages/client-core/src/profile-management';
import {serverPath} from '../../../../../../packages/client-core/src/query/server-api';
import type {NativeScreenLifetime} from '../lifetime';
type Context = {account: DirectSnapshot; api: {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>} & Partial<Pick<HttpLocalApi, 'requestRaw'>>};

/** The same primary-profile membership boundary applies to both Server destinations. */
export function nativeServerAdmission(account: DirectSnapshot, lifetime: NativeScreenLifetime): 'owner' | 'administrator' | 'denied' {
  if (!lifetime.current() || !account.canManage || account.account.primaryProfileId !== lifetime.authority.profileId || account.account.disabled) return 'denied';
  return account.account.role === 'owner' ? 'owner' : account.account.role === 'admin' ? 'administrator' : 'denied';
}

/** Troubleshooting is the one Server page a TV never shows (definition 12 rule 2, G35): the server's messages and their
 * live feed, problem reports with their logs, and the activity record, with anything under them. */
const troubleshooting = [serverPath('GET /v1/admin/logs'), serverPath('GET /v1/admin/reports'), serverPath('GET /v1/admin/audit')];
const troubleshootingRead = (base: string) => troubleshooting.some(path => base === path || base.startsWith(path + '/'));

/** A TV settings reader is GET-only for the owner or an administrator whose primary
 * profile this is. An administrator may read only the shared administrator pages
 * listed above. It cannot acquire a mutation or another scope. Recheck before every
 * request; the lifetime rejects retired in-flight responses. */
export function createTVServerReader(context: Context, lifetime: NativeScreenLifetime) {
  const guard = (path: string) => {
    const admission = nativeServerAdmission(context.account, lifetime);
    const base = path.split('?')[0];
    if (troubleshootingRead(base) || admission === 'denied' || admission === 'administrator' && !administratorReads.has(path)) throw new Error('native_tv_server_read_denied');
  };
  const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    try {guard(path);} catch (error) {return Promise.reject(error);}
    if (method && method !== 'GET' || body !== undefined) return Promise.reject(new Error('native_tv_server_read_denied'));
    return lifetime.request(next => context.api.request<T>(path, 'GET', undefined, next), signal);
  }};
  return withOwnerTvReadRaw(api, context.api, guard);
}
