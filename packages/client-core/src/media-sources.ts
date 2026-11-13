import {ApiError, type HttpLocalApi, type ProfileAvatar} from './index.ts';
import {AVATAR_MAX_BYTES, type IdentityUpload} from './identity-client.ts';

type MediaUrlApi = Pick<HttpLocalApi, 'mediaUrl'>;
export type ProfilePictureSource = Readonly<{uri: string; headers: {Authorization: string}}>;

/** Resolve the server-published download capability through its captured verified route. */
export function resolvePublishedDownloadUrl(api: MediaUrlApi, path: string) {
  return api.mediaUrl(path);
}

export function profilePictureSource(api: MediaUrlApi | undefined, token: string | undefined, avatar: ProfileAvatar | undefined, pixels: number): ProfilePictureSource | undefined {
  if (!api || !token || !avatar) return undefined;
  try {
    const path = `${avatar.url}${avatar.url.includes('?') ? '&' : '?'}size=${pixels > 160 ? 'large' : 'small'}`;
    const uri = api.mediaUrl(path);
    return uri ? {uri, headers: {Authorization: `Bearer ${token}`}} : undefined;
  } catch { return undefined; }
}

/** The avatar-only JSON envelope crosses the native bridge, never the server wire.
 * Keep renewal, routing and retries on the captured HttpLocalApi. */
export function createNativeProfileAvatarUpload(api: Pick<HttpLocalApi, 'requestRaw'>): IdentityUpload {
  return async (path, body, contentType) => {
    if (!/^\/v1\/profiles\/[A-Za-z0-9_-]{1,128}\/avatar(?:\?member=[A-Za-z0-9_-]{1,128})?$/.test(path)) throw new Error('Invalid avatar upload path.');
    const bytes = body instanceof Blob ? new Uint8Array(await body.arrayBuffer()) : body instanceof Uint8Array ? body : new Uint8Array(body);
    if (!bytes.length || bytes.length > AVATAR_MAX_BYTES) throw new Error('Invalid avatar upload size.');
    // Chunk the conversion so a full-size picture never exceeds the JS argument stack.
    let binary = '';
    for (let i = 0; i < bytes.length; i += 32768) binary += String.fromCharCode(...bytes.subarray(i, i + 32768));
    const response = await api.requestRaw(path, 'PUT', {headers: {'Content-Type': contentType}, body: {nativeAvatarBase64: globalThis.btoa(binary)}});
    if (response.status < 200 || response.status >= 300) {
      const value = (response.body as {error?: {code?: string; message?: string; retry?: boolean}} | undefined)?.error;
      throw new ApiError(response.status, value?.code ?? 'request_failed', value?.message ?? 'Avatar upload failed.', value?.retry ?? response.status >= 500);
    }
    return response.body;
  };
}
