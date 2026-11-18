import type {HttpLocalApi} from './index.ts';
/** Avatar paths/bodies were issued by the core identity/profile service; the selected session renews on a 401. */
export const uploadOwnerProfileAvatar = (api: Pick<HttpLocalApi, 'uploadPicture'>, path: string, body: Blob | ArrayBuffer | ArrayBufferView, contentType: string, signal?: AbortSignal) => api.uploadPicture(path, 'POST', body, contentType, signal);
export const uploadOwnerMemberAvatar = (api: Pick<HttpLocalApi, 'uploadPicture'>, path: string, body: Blob | ArrayBuffer | ArrayBufferView, contentType: string) => api.uploadPicture(path, 'POST', body, contentType);
