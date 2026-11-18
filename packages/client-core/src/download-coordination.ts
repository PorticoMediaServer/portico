export {readDownloadPreparationResponse} from './query/download-documents.ts';
import {encodedRoutePath} from '../../contracts/src/route-paths.ts';
import type {DownloadsApi} from './downloads-service.ts';

export function grantPreparedDownloadResponse(api: DownloadsApi, id: string, operationId: string, signal?: AbortSignal) {
  return api.request<unknown>(encodedRoutePath('create_download_grant', {id: encodeURIComponent(id)}), 'POST', {operationId}, signal);
}
