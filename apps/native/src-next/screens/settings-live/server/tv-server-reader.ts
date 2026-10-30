import type {DirectSnapshot} from '../../../../../../packages/client-core/src/profile-management';
import {routePath} from '../../../../../../packages/contracts/src/routes';
import type {NativeScreenLifetime} from '../lifetime';

type Context = {account: DirectSnapshot; api: {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}};

/** The same primary-profile membership boundary applies to both Server destinations. */
export function nativeServerAdmission(account: DirectSnapshot, lifetime: NativeScreenLifetime): 'owner' | 'administrator' | 'denied' {
  if (!lifetime.current() || !account.canManage || account.account.primaryProfileId !== lifetime.authority.profileId || account.account.disabled) return 'denied';
  return account.account.role === 'owner' ? 'owner' : account.account.role === 'admin' ? 'administrator' : 'denied';
}

/** The server reads an administrator's TV pages actually make. People is shared
 * with administrators; logs, coded events, storage, catalogue and remote access are not.
 * An administrator is admitted to these exact paths, never to the role's whole
 * surface, so a page bug cannot turn the tier into an owner read. Keep this list
 * beside the page that reads it. */
const administratorReads: ReadonlySet<string> = new Set([
  routePath('get_admin_access_devices'),
  routePath('get_admin_access_invitations'),
  routePath('get_admin_access_members'),
]);

/** A TV settings reader is GET-only for the owner or an administrator whose primary
 * profile this is. An administrator may read only the shared administrator pages
 * listed above. It cannot acquire a mutation or another scope. Recheck before every
 * request; the lifetime rejects retired in-flight responses. */
export function createTVServerReader(context: Context, lifetime: NativeScreenLifetime) {
  const peopleRoutes = {members: 'get_admin_access_members', invitations: 'get_admin_access_invitations', devices: 'get_admin_access_devices'} as const;
  return {peoplePage: <T,>(section: keyof typeof peopleRoutes, cursor?: string, signal?: AbortSignal): Promise<T> => {
    if (!Object.hasOwn(peopleRoutes, section) || cursor !== undefined && (typeof cursor !== 'string' || cursor.length > (section === 'invitations' ? 2048 : 4096) || (section === 'invitations' ? /[\p{Cc}\p{Cf}]/u : /[\x00-\x1f]/).test(cursor))) return Promise.reject(new Error('native_tv_server_read_denied'));
    if (nativeServerAdmission(context.account, lifetime) === 'denied') return Promise.reject(new Error('native_tv_server_read_denied'));
    const params = new URLSearchParams({limit: '50'});
    if (section === 'invitations') params.set('state', 'pending');
    if (cursor) params.set('cursor', cursor);
    const path = routePath(peopleRoutes[section]) + '?' + params.toString();
    return lifetime.request(next => context.api.request<T>(path, 'GET', undefined, next), signal);
  }, request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    const admission = nativeServerAdmission(context.account, lifetime);
    const base = path.split('?')[0];
    if (base === routePath('get_admin_logs') || base === '/v1/admin/access/events') return Promise.reject(new Error('native_tv_server_read_denied'));
    if (admission === 'denied' || method && method !== 'GET' || body !== undefined) return Promise.reject(new Error('native_tv_server_read_denied'));
    if (admission === 'administrator' && !administratorReads.has(path)) return Promise.reject(new Error('native_tv_server_read_denied'));
    return lifetime.request(next => context.api.request<T>(path, 'GET', undefined, next), signal);
  }};
}
