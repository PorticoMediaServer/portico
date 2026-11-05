import type {HttpLocalApi} from '../index.ts';
import type {V1Http, V1Request, V1Response} from './http.ts';

type ScopedSubtitleHttp = {
  send(request: V1Request, transport: V1Http): Promise<V1Response>;
};

/** SubtitleService supplies the multipart request. Keep its original scoped dispatch
 * and the API's upload policy while adapting its parsed receipt to the V1 seam. */
export async function uploadPlaybackSubtitle<T>(http: ScopedSubtitleHttp, api: Pick<HttpLocalApi, 'requestSubtitleUpload'>,
  path: string, method: 'POST' | 'PUT', body: FormData, signal?: AbortSignal): Promise<T> {
  const response = await http.send({path, method, body, signal}, {send: async request => ({
    status: 200, headers: {}, body: await api.requestSubtitleUpload(request.path, method, body, request.signal),
  })});
  return response.body as T;
}
