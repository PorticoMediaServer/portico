import type {HttpLocalApi} from './index.ts';
import type {OwnerRequestApi} from './owner-requests.ts';
import {call, type V1Http} from './playback-v1/http.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
export const readOwnerReportRecipient = <T = unknown>(api: OwnerRequestApi, signal: AbortSignal): Promise<T> => api.request<T>(routePath('get_app_report_recipient'), 'GET', undefined, signal);
export const sendOwnerAppReport = (api: Pick<HttpLocalApi, 'requestRaw'>, key: string, body: unknown, signal: AbortSignal) => api.requestRaw(routePath('post_app_reports'), 'POST', {headers: {'Idempotency-Key': key}, body, signal});
export const readOwnerDashboardSessions = (http: V1Http, query: URLSearchParams, signal?: AbortSignal) => call(http, {method: 'GET', path: routePath('list_admin_sessions') + '?' + query, signal});
export const openOwnerLogEvents = (api: Pick<HttpLocalApi, 'routeFetch' | 'baseUrl'>, headers: Record<string, string>, token: string, signal: AbortSignal) => api.routeFetch(api.baseUrl + routePath('get_admin_logs_events'), {headers: {...headers, Authorization: 'Bearer ' + token}, signal});
