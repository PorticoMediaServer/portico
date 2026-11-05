import {encodedRoutePath} from '../../contracts/src/route-paths.ts';
import type {HttpLocalApi} from './index.ts';
export const ownerChannelLogoPath = (id: string) => encodedRoutePath('post_admin_library_channels_id_logo', {id: encodeURIComponent(id)});
export function ownerChannelLogoForm(file: File, operationId: string): FormData {
  const body = new FormData();body.append('file', file);body.append('operationId', operationId);return body;
}
export const ownerChannelLogoMultipart = (base64: string, operationId: string, boundary: string) => `--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="channel-logo.jpg"\r\nContent-Type: image/jpeg\r\nContent-Transfer-Encoding: base64\r\n\r\n${base64}\r\n--${boundary}\r\nContent-Disposition: form-data; name="operationId"\r\n\r\n${operationId}\r\n--${boundary}--\r\n`;
export function sendOwnerChannelLogo(api: Pick<HttpLocalApi, 'routeFetch' | 'baseUrl'>, path: string, token: string, body: BodyInit, signal: AbortSignal, contentType?: string): Promise<Response> {
  return api.routeFetch(api.baseUrl + path, {method: 'POST', body, signal, redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer', headers: {Authorization: 'Bearer ' + token, Accept: 'application/json', ...(contentType ? {'Content-Type': contentType} : {})}});
}
export const ownerItemArtworkPath = (id: string, kind: string) => encodedRoutePath('get_items_id_art_kind', {id, kind});
export const ownerLibraryChannelLogoPath = (id: string) => encodedRoutePath('get_library_channels_id_logo', {id: encodeURIComponent(id)});
/** Avatar paths/bodies were issued by the core identity/profile service. */
export const uploadOwnerProfileAvatar = (api: Pick<HttpLocalApi, 'routeFetch' | 'baseUrl'>, path: string, token: string, body: BodyInit, contentType: string, signal?: AbortSignal) => api.routeFetch(api.baseUrl + path, {method: 'POST', signal, headers: {Authorization: 'Bearer ' + token, 'Content-Type': contentType}, body});
export const uploadOwnerMemberAvatar = (api: Pick<HttpLocalApi, 'routeFetch' | 'baseUrl'>, path: string, token: string, body: BodyInit, contentType: string) => api.routeFetch(api.baseUrl + path, {method: 'POST', redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer', headers: {Authorization: 'Bearer ' + token, 'Content-Type': contentType}, body});
