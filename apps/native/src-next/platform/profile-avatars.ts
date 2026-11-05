import {useSyncExternalStore} from 'react';
import {NativeModules, Platform, type ImageSourcePropType} from 'react-native';
import {type HttpLocalApi, type ProfileAvatar} from '../../../../packages/client-core/src/index';
import {AVATAR_MAX_BYTES, type IdentityUpload} from '../../../../packages/client-core/src/identity-client';
import {createNativeProfileAvatarUpload, profilePictureSource as coreProfilePictureSource} from '../../../../packages/client-core/src/media-sources';

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
  return coreProfilePictureSource(api, token, avatar, pixels);
}

/** The core operation owns avatar validation, bytes and the native transport envelope. */
export function profileAvatarUpload(api: Pick<HttpLocalApi, 'requestRaw'>): IdentityUpload {
  return createNativeProfileAvatarUpload(api);
}
