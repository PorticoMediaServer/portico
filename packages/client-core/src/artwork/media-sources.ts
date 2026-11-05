import {encodedRoutePath, routePattern} from '../../../contracts/src/routes.ts';
import {retryAfterSecondsFrom, type AuthRecovery, type HttpLocalApi} from '../index.ts';
import {ambientArtworkSource, type AmbientArtworkIdentity} from './ambient.ts';
import {artworkRenditionFor} from './renditions.ts';
import type {ArtworkFailure} from './scheduler.ts';
import type {ProfileAvatar} from '../identity-restrictions.ts';

/** pixels is the painted width in device pixels and aspect its width ÷ height:
 * the box the picture covers. The slot whose actual pixels fill that box is
 * requested by name and token, so each picture downloads once per slot. */
export function foregroundArtwork(url: string | undefined, pixels: number, aspect: number, identity: AmbientArtworkIdentity) {
  // The accepted ambient parser also validates selected route, digest and query identity.
  if (!ambientArtworkSource(url, identity)) return undefined;
  const ratio = aspect > 0 && Number.isFinite(aspect) ? aspect : 1;
  const selected = artworkRenditionFor(url!, {width: pixels, height: pixels / ratio, dpr: 1}).url;
  return {url: selected, cacheKey: JSON.stringify([identity.serverId, identity.viewerId, identity.authorityKey, selected])};
}
const channelLogoPattern = new RegExp('^' + routePattern('get_library_channels_id_logo').replace('{id}', '[A-Za-z0-9_-]{1,128}') + '$');
const memberProfileAvatarPattern = new RegExp('^' + routePattern('get_admin_access_members_id_profiles_profile_avatar').replace('{id}', '([^/?#]+)').replace('{profileId}', '([^/?#]+)') + '\\?v=([1-9]\\d{0,15})&size=(64|160|512)$');
/** A logo is a separate foreground representation. Its revision is the checked
 * upload receipt's integer, never an ambient digest or a channel config revision. */
export function channelLogoArtwork(url: string, identity: AmbientArtworkIdentity) {
  if (url.length > 256 || /[\s\\#\x00-\x1f\x7f]/.test(url) || ![identity.serverId, identity.viewerId, identity.authorityKey].every(value => typeof value === 'string' && value.length > 0 && value.length <= 512 && !/[\x00-\x1f\x7f]/.test(value))) return undefined;
  const parts = url.split('?');if (parts.length > 2 || !channelLogoPattern.test(parts[0]!)) return undefined;
  if (parts.length === 2) {
    const query = parts[1]!;if (!/^v=[1-9]\d{0,15}$/.test(query) || !Number.isSafeInteger(Number(query.slice(2)))) return undefined;
  }
  return {url, cacheKey: JSON.stringify([identity.serverId, identity.viewerId, identity.authorityKey, url])};
}
const trickplayTilePattern = new RegExp('^' + routePattern('get_v1_items_id_trickplay_set_tiles_tile_jpg').replace(/[.]/g, '\\.')
  .replace('{id}', '([^/?#]{1,512})').replace('{set}', '[a-f0-9]{64}').replace('{tile}', '(?:0|[1-9]\\d{0,4})') + '$');
/** A video's preview sheet (W1-A2): the server's own sprite, unscaled. The route takes no query, so it is never
 * a rendition; the crop needs the sheet's real pixels. The set id and revision name its content. */
export function trickplayTileArtwork(url: string, identity: AmbientArtworkIdentity, revision = '') {
  if (url.length > 1024 || /[\s\\#\x00-\x1f\x7f]/.test(url) || ![identity.serverId, identity.viewerId, identity.authorityKey].every(value => typeof value === 'string' && value.length > 0 && value.length <= 512 && !/[\x00-\x1f\x7f]/.test(value))) return undefined;
  const match = trickplayTilePattern.exec(url);
  if (!match) return undefined;
  try {const id = decodeURIComponent(match[1]!); if (!id || id === '.' || id === '..' || /[\x00-\x1f\x7f]/.test(id)) return undefined;} catch {return undefined;}
  return {url, cacheKey: JSON.stringify([identity.serverId, identity.viewerId, identity.authorityKey, url, revision])};
}
/** People's pictures use their own authorized route and the upload receipt's
 * version. They enter the same foreground queue as other authenticated art. */
export function memberProfileAvatarArtwork(url: string, identity: AmbientArtworkIdentity) {
  if (url.length > 1024 || /[\s\\#\x00-\x1f\x7f]/.test(url) || ![identity.serverId, identity.viewerId, identity.authorityKey].every(value => typeof value === 'string' && value.length > 0 && value.length <= 512 && !/[\x00-\x1f\x7f]/.test(value))) return undefined;
  const match = memberProfileAvatarPattern.exec(url);
  if (!match || !Number.isSafeInteger(Number(match[3]))) return undefined;
  try {
    if (!match.slice(1, 3).every(value => {const id = decodeURIComponent(value!); return id.length > 0 && id.length <= 128 && id !== '.' && id !== '..' && !/[\x00-\x1f\x7f]/.test(id) && encodeURIComponent(id) === value;})) return undefined;
  } catch {return undefined;}
  return {url, cacheKey: JSON.stringify([identity.serverId, identity.viewerId, identity.authorityKey, url])};
}
export function artworkFailure(error: unknown): ArtworkFailure {
  const failure = error as {status?: number; retryAfterSeconds?: number; name?: string};
  return {retryable: failure.name !== 'AbortError' && (failure.status === undefined || failure.status === 408 || failure.status === 429 || failure.status >= 500 || failure.status === 404 && failure.retryAfterSeconds !== undefined), retryAfterSeconds: failure.retryAfterSeconds};
}
async function imageBytes(response: Response, ceiling: number, signal: AbortSignal): Promise<Blob> {
  const declared = Number(response.headers.get('content-length'));
  if (declared > ceiling) { await response.body?.cancel(); throw Object.assign(new Error('Artwork exceeds its size bound.'), {status: 413}); }
  const type = response.headers.get('content-type')?.split(';')[0];
  if (!type || !['image/png', 'image/jpeg', 'image/webp', 'image/avif'].includes(type) || !response.body) {
    await response.body?.cancel(); throw Object.assign(new Error('Artwork is not an accepted image.'), {status: 415});
  }
  const reader = response.body.getReader(), chunks: Uint8Array<ArrayBuffer>[] = []; let count = 0;
  try {
    for (;;) {
      signal.throwIfAborted();
      const read = await reader.read(); if (read.done) break;
      count += read.value.byteLength;
      if (count > ceiling) throw Object.assign(new Error('Artwork exceeds its size bound.'), {status: 413});
      chunks.push(new Uint8Array(read.value));
    }
    return new Blob(chunks, {type});
  } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
}


export type AuthenticatedArtworkMedia = Readonly<{
  api: Pick<HttpLocalApi, 'baseUrl' | 'routeFetch'>;
  auth: AuthRecovery;
  currentToken: () => string;
  identity: AmbientArtworkIdentity;
  isCurrent: () => boolean;
}>;

/** The original authenticated image request, renewal and bounded byte reader.
 * Platforms retain image decoding and painting under their captured lifetime. */
export async function readAuthenticatedArtworkBytes(options: AuthenticatedArtworkMedia,
  request: Readonly<{url: string; kind: 'foreground' | 'ambient'}>, signal: AbortSignal): Promise<Blob> {
  const {api, auth, identity, isCurrent} = options;
  if (!isCurrent() || !(ambientArtworkSource(request.url, identity) || request.kind === 'foreground' && (channelLogoArtwork(request.url, identity) || memberProfileAvatarArtwork(request.url, identity) || trickplayTileArtwork(request.url, identity)))) throw Object.assign(new Error('Artwork authority changed.'), {status: 403});
  const token = await auth.token(options.currentToken());
  signal.throwIfAborted();
  if (!isCurrent()) throw Object.assign(new Error('Artwork authority changed.'), {status: 403});
  const fetchImage = (access: string) => api.routeFetch(api.baseUrl + request.url, {method: 'GET', signal, redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer', headers: {Authorization: 'Bearer ' + access, Accept: 'image/avif,image/webp,image/png,image/jpeg'}});
  let response = await fetchImage(token);
  if (response.status === 401 && !signal.aborted) {
    await response.body?.cancel();
    const renewed = await auth.recover(token);
    if (!renewed || !isCurrent()) throw Object.assign(new Error('Artwork sign-in ended.'), {status: 401});
    response = await fetchImage(renewed);
  }
  if (!response.ok) {
    const failure = {status: response.status, retryAfterSeconds: retryAfterSecondsFrom(response.headers.get('Retry-After'))};
    if (response.status === 403) void auth.permissionDenied?.();
    await response.body?.cancel(); throw Object.assign(new Error('Artwork could not be loaded.'), failure);
  }
  return imageBytes(response, request.kind === 'ambient' ? 20000 : 12 * 1024 * 1024, signal);
}

export function channelLogoArtworkSource(id: string, revision: number | undefined, identity: AmbientArtworkIdentity) {
  if (!/^[A-Za-z0-9_-]{1,128}$/.test(id) || revision !== undefined && (!Number.isSafeInteger(revision) || revision < 1)) return undefined;
  const url = encodedRoutePath('get_library_channels_id_logo', {id: encodeURIComponent(id)}) + (revision === undefined ? '' : '?v=' + revision);
  return channelLogoArtwork(url, identity);
}

export function memberProfileAvatarArtworkSource(accountId: string, avatar: ProfileAvatar | undefined, pixels: number, identity: AmbientArtworkIdentity) {
  if (!avatar || !Number.isSafeInteger(avatar.version) || avatar.version < 1) return undefined;
  const url = encodedRoutePath('get_admin_access_members_id_profiles_profile_avatar', {id: encodeURIComponent(accountId), profileId: encodeURIComponent(avatar.profileId)}) + '?v=' + avatar.version;
  if (avatar.url !== url) return undefined;
  const size = pixels <= 64 ? 64 : pixels <= 160 ? 160 : 512;
  return memberProfileAvatarArtwork(url + '&size=' + size, identity);
}
