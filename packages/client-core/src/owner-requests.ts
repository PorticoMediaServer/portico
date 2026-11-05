import {accessListPath} from './server-admin/panel-logic.ts';
import {routePath} from '../../contracts/src/route-paths.ts';

/** Raw owner operations. Callers retain their original lifetime guards, parsing,
 * revision recovery and immutable command bodies. These functions deliberately
 * add no validation, retry, caching or promise continuation. */
export type OwnerRequestApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
const source = (id: string, part: string) => `${routePath('list_live_sources')}/${encodeURIComponent(id)}/${part}`;
export const readOwnerLiveSources = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('list_live_sources'), 'GET', undefined, signal);
export const discoverOwnerTuners = <T = unknown>(api: OwnerRequestApi, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('post_admin_live_sources_discover'), 'POST', body, signal);
export const previewOwnerRemoteSource = <T = unknown>(api: OwnerRequestApi, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('preview_remote_live_source'), 'POST', body, signal);
export const saveOwnerRemoteSource = <T = unknown>(api: OwnerRequestApi, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('publish_remote_live_source'), 'POST', body, signal);
export const previewOwnerSourceFiles = <T = unknown>(api: OwnerRequestApi, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('preview_live_source'), 'POST', body, signal);
export const saveOwnerSourceFiles = <T = unknown>(api: OwnerRequestApi, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('save_live_source'), 'POST', body, signal);
export const changeOwnerLiveSource = <T = unknown>(api: OwnerRequestApi, id: string, action: 'refresh' | 'enabled' | 'remove', body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(source(id, action), 'POST', body, signal);
export const readOwnerSourceConfiguration = <T = unknown>(api: OwnerRequestApi, id: string, signal?: AbortSignal): Promise<T> => api.request<T>(source(id, 'configuration'), 'GET', undefined, signal);
export const writeOwnerSourceConfiguration = <T = unknown>(api: OwnerRequestApi, id: string, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(source(id, 'configuration'), 'PUT', body, signal);
export const importOwnerSourceLogos = <T = unknown>(api: OwnerRequestApi, id: string, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(source(id, 'logos'), 'POST', body, signal);
export const readOwnerChannelMap = <T = unknown>(api: OwnerRequestApi, id: string, query: string, signal?: AbortSignal): Promise<T> => api.request<T>(source(id, 'channel-map') + query, 'GET', undefined, signal);
export const writeOwnerChannelMap = <T = unknown>(api: OwnerRequestApi, id: string, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(source(id, 'channel-map'), 'PUT', body, signal);
export const readOwnerLiveDefaults = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_live_settings'), 'GET', undefined, signal);
export const writeOwnerLiveDefaults = <T = unknown>(api: OwnerRequestApi, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('set_live_settings'), 'PUT', body, signal);
export const readOwnerTuners = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_live_tuners'), 'GET', undefined, signal);
export const readOwnerRecordingOwners = <T = unknown>(api: OwnerRequestApi, query: string, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_admin_dvr_recording_owners') + query, 'GET', undefined, signal);
export const readOwnerRecordingPermissions = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_admin_dvr_recording_permissions'), 'GET', undefined, signal);
export const writeOwnerRecordingPermission = <T = unknown>(api: OwnerRequestApi, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('put_admin_dvr_recording_permissions'), 'PUT', body, signal);
export const readOwnerRecordingRules = <T = unknown>(api: OwnerRequestApi, query: string, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_admin_dvr_recording_groups') + query, 'GET', undefined, signal);
export const deleteOwnerRecordingRule = <T = unknown>(api: OwnerRequestApi, id: string, query: string, signal?: AbortSignal): Promise<T> => api.request<T>(`${routePath('get_admin_dvr_recording_groups')}/${encodeURIComponent(id)}?${query}`, 'DELETE', undefined, signal);
export const readOwnerRecordingGuide = <T = unknown>(api: OwnerRequestApi, query: string, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_guide') + '?' + query, 'GET', undefined, signal);
export const readOwnerDvrDefaults = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_admin_dvr_settings'), 'GET', undefined, signal);
export const writeOwnerDvrDefaults = <T = unknown>(api: OwnerRequestApi, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('put_admin_dvr_settings'), 'PUT', body, signal);

export const readOwnerConfiguration = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_settings_configuration'), 'GET', undefined, signal);
export const readOwnerUpdates = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_admin_updates'), 'GET', undefined, signal);
export const readOwnerStorageUsage = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_admin_storage_usage'), 'GET', undefined, signal);
export const readOwnerDataUsage = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_data_storage_usage'), 'GET', undefined, signal);
export const readOwnerConnectivity = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_connectivity_status'), 'GET', undefined, signal);
export const readOwnerRemoteAccess = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_networking_remote'), 'GET', undefined, signal);
export const readOwnerCertificate = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_networking_certificate'), 'GET', undefined, signal);
export const readOwnerSecuritySummary = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_admin_security_summary'), 'GET', undefined, signal);
export const ownerCertificateRequest = <T = unknown>(api: OwnerRequestApi, action: 'status' | 'config' | 'retry', body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(routePath(action === 'status' ? 'get_networking_certificate' : action === 'config' ? 'post_networking_certificate_config' : 'post_networking_certificate_retry'), action === 'status' ? 'GET' : 'POST', body, signal);
export const broadcastOwnerNotification = <T = unknown>(api: OwnerRequestApi, body: unknown, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('post_admin_notifications_broadcast'), 'POST', body, signal);

export const readOwnerAccessDevices = <T = unknown>(api: OwnerRequestApi, query: string, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_admin_access_devices') + query, 'GET', undefined, signal);
export const readOwnerPeoplePage = <T = unknown>(api: OwnerRequestApi, section: 'members' | 'invitations' | 'devices', cursor?: string, signal?: AbortSignal): Promise<T> => {
  const params = new URLSearchParams({limit: '50'});
  if (section === 'invitations') params.set('state', 'pending');
  if (cursor) params.set('cursor', cursor);
  return api.request<T>(routePath(({members: 'get_admin_access_members', invitations: 'get_admin_access_invitations', devices: 'get_admin_access_devices'} as const)[section]) + '?' + params.toString(), 'GET', undefined, signal);
};

export const readOwnerSignInSettings = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_sign_in_settings'), 'GET', undefined, signal);
export const readOwnerMetadataSettings = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_metadata_settings'), 'GET', undefined, signal);
export const readOwnerTranscodeCapacity = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_admin_transcode_capacity'), 'GET', undefined, signal);

export type OwnerRecordingRuleCommand = Readonly<{path: string; method: 'POST' | 'PUT' | 'DELETE'; body?: unknown}>;
export const ownerRecordingRuleChange = (id: string | undefined, body: unknown): OwnerRecordingRuleCommand => ({path: routePath('post_admin_dvr_recording_groups') + (id ? '/' + encodeURIComponent(id) : ''), method: id ? 'PUT' : 'POST', body});
export const ownerRecordingRuleRemoval = (id: string, revision: number, operationId: string): OwnerRecordingRuleCommand => ({path: routePath('get_admin_dvr_recording_groups') + '/' + encodeURIComponent(id) + '?' + new URLSearchParams({expectedRevision: String(revision), operationId}), method: 'DELETE'});
/** Sends the original retained core command; no reconstruction on an uncertain retry. */
export const sendOwnerRecordingRuleCommand = <T = unknown>(api: OwnerRequestApi, command: OwnerRecordingRuleCommand, signal?: AbortSignal): Promise<T> => api.request<T>(command.path, command.method, command.body, signal);

export const readOwnerLibraries = <T = unknown>(api: OwnerRequestApi, signal?: AbortSignal): Promise<T> => api.request<T>(routePath('get_libraries'), 'GET', undefined, signal);

export const readOwnerMemberDevices = <T = unknown>(api: OwnerRequestApi, accountId: string, cursor: string, signal?: AbortSignal): Promise<T> => api.request<T>(accessListPath('devices', cursor) + '&account=' + encodeURIComponent(accountId), 'GET', undefined, signal);
