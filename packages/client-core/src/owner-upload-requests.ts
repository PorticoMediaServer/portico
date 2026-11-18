import {encodedRoutePath} from '../../contracts/src/route-paths.ts';
import type {HttpLocalApi} from './index.ts';
export const ownerItemArtworkPath = (id: string, kind: string) => encodedRoutePath('get_items_id_art_kind', {id, kind});
/** Avatar paths/bodies were issued by the core identity/profile service; the selected session renews on a 401. */
export const uploadOwnerProfileAvatar = (api: Pick<HttpLocalApi, 'uploadPicture'>, path: string, body: Blob | ArrayBuffer | ArrayBufferView, contentType: string, signal?: AbortSignal) => api.uploadPicture(path, 'POST', body, contentType, signal);
export const uploadOwnerMemberAvatar = (api: Pick<HttpLocalApi, 'uploadPicture'>, path: string, body: Blob | ArrayBuffer | ArrayBufferView, contentType: string) => api.uploadPicture(path, 'POST', body, contentType);
