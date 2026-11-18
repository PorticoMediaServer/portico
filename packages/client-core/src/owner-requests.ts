import {accessListPath, sendServerMessage} from './server-admin/panel-logic.ts';
import {routePath} from '../../contracts/src/route-paths.ts';

/** Raw owner operations. Callers retain their original lifetime guards, parsing,
 * revision recovery and immutable command bodies. These functions deliberately
 * add no validation, retry, caching or promise continuation. */
export type OwnerRequestApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
export const ownerCertificateRequest = <T = unknown>(api: OwnerRequestApi, action: 'status' | 'config' | 'retry', body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(routePath(action === 'status' ? 'get_networking_certificate' : action === 'config' ? 'post_networking_certificate_config' : 'post_networking_certificate_retry'), action === 'status' ? 'GET' : 'POST', body, signal);
/** Web's General tool still drafts a title and a body; the rebuilt server keeps one message (rule 80). */
export const broadcastOwnerNotification = (api: OwnerRequestApi, draft: Readonly<{audience?: string; title?: string; body?: string}>, signal?: AbortSignal) =>
  sendServerMessage(api, draft.audience === 'account-admin' ? 'admins' : 'everyone', [draft.title?.trim(), draft.body?.trim()].filter(Boolean).join('\n\n'), signal);

export const readOwnerSignInSettings = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_sign_in_settings'), 'GET', undefined, signal);
export const readOwnerMetadataSettings = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_metadata_settings'), 'GET', undefined, signal);

export const readOwnerLibraries = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_libraries'), 'GET', undefined, signal);

export const readOwnerMemberDevices = <T = unknown>(api: OwnerRequestApi, accountId: string, cursor: string, signal?: AbortSignal): Promise<T> => api.request<T>(accessListPath('devices', cursor) + '&account=' + encodeURIComponent(accountId), 'GET', undefined, signal);
