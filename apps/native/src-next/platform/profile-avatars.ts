import {useSyncExternalStore} from 'react';
import {NativeModules, Platform, type ImageSourcePropType} from 'react-native';
import {ApiError, type HttpLocalApi, type ProfileAvatar} from '../../../../packages/client-core/src/index';
import {AVATAR_MAX_BYTES, type IdentityUpload} from '../../../../packages/client-core/src/identity-client';

type PictureAPI = Pick<HttpLocalApi, 'mediaUrl' | 'subscribeRoute' | 'getRouteSnapshot'>;
const noSubscription = () => () => {};
const noRoute = () => null;

export const profilePicturePickerAvailable = () => !Platform.isTV && typeof NativeModules.PorticoImagePicker?.pick === 'function';

/** The app's one-photo picker scales its result to 1024 pixels. Never retain a
 * result after its caller's exact profile/screen opening has ended. */
export async function pickProfilePicture(lease: {current: () => boolean}): Promise<{bytes: Uint8Array; contentType: string} | undefined> {
  if (!lease.current() || !profilePicturePickerAvailable()) return undefined;
  const result = await NativeModules.PorticoImagePicker.pick();
  if (!lease.current() || !result || typeof result !== 'object') return undefined;
  const value = result as {base64?: unknown; contentType?: unknown; bytes?: unknown};
  if (typeof value.base64 !== 'string' || typeof value.contentType !== 'string' || typeof value.bytes !== 'number'
    || !/^image\/(jpeg|png)$/.test(value.contentType) || !Number.isSafeInteger(value.bytes) || value.bytes < 1 || value.bytes > AVATAR_MAX_BYTES
    || value.base64.length !== 4 * Math.ceil(value.bytes / 3) || !/^[A-Za-z0-9+/]*={0,2}$/.test(value.base64)) return undefined;
  try {
    const binary = globalThis.atob(value.base64);
    if (binary.length !== value.bytes) return undefined;
    return {bytes: Uint8Array.from(binary, character => character.charCodeAt(0)), contentType: value.contentType};
  } catch {return undefined;}
}

/** Re-render image sources when the selected server moves between verified routes. */
export function useAvatarRoute(api: PictureAPI | undefined) {
  return useSyncExternalStore(api?.subscribeRoute ?? noSubscription, api?.getRouteSnapshot ?? noRoute);
}

export function profilePictureSource(api: Pick<PictureAPI, 'mediaUrl'> | undefined, token: string | undefined, avatar: ProfileAvatar | undefined, pixels: number): ImageSourcePropType | undefined {
  if (!api || !token || !avatar) return undefined;
  try {
    const path = `${avatar.url}${avatar.url.includes('?') ? '&' : '?'}size=${Math.round(pixels)}`;
    const uri = api.mediaUrl(path);
    return uri ? {uri, headers: {Authorization: `Bearer ${token}`}} : undefined;
  } catch { return undefined; }
}

/** HttpLocalApi owns renewal, deadlines and verified routing. Its JSON body is a native bridge
 * envelope: the avatar-only transport boundary decodes it to bytes before sending the request.
 * This keeps credentials/retries on the same path as all other profile operations. */
export function profileAvatarUpload(api: Pick<HttpLocalApi, 'requestRaw'>): IdentityUpload {
  return async (path, body, contentType) => {
    const own = /^\/v1\/direct\/profiles\/[A-Za-z0-9_-]{1,128}\/avatar$/.test(path);
    const managed = /^\/v1\/admin\/access\/members\/[A-Za-z0-9_-]{1,128}\/profiles\/[A-Za-z0-9_-]{1,128}\/avatar\?expectedRevision=([1-9][0-9]*)$/.exec(path);
    if (!own && !(managed && Number.isSafeInteger(Number(managed[1])))) throw new Error('Invalid avatar upload path.');
    if (managed && !/^image\/(jpeg|png)$/.test(contentType)) throw new Error('Invalid avatar upload type.');
    const bytes = body instanceof Blob ? new Uint8Array(await body.arrayBuffer()) : body instanceof Uint8Array ? body : new Uint8Array(body);
    if (!bytes.length || bytes.length > AVATAR_MAX_BYTES) throw new Error('Invalid avatar upload size.');
    // Chunk the conversion so a full-size picture never exceeds the JS argument stack.
    let binary = '';
    for (let i = 0; i < bytes.length; i += 32768) binary += String.fromCharCode(...bytes.subarray(i, i + 32768));
    const response = await api.requestRaw(path, 'POST', {headers: {'Content-Type': contentType}, body: {nativeAvatarBase64: globalThis.btoa(binary)}});
    if (response.status < 200 || response.status >= 300) {
      const value = response.body as {error?: {code?: string; message?: string; retryable?: boolean}} | undefined;
      throw new ApiError(response.status, value?.error?.code ?? 'request_failed', value?.error?.message ?? 'Avatar upload failed.', value?.error?.retryable ?? response.status >= 500);
    }
    return response.body;
  };
}
