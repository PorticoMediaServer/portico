import {serverPath} from '../query/server-api.ts';
import {retryAfterSecondsFrom, type AuthRecovery, type HttpLocalApi} from '../index.ts';
import {ambientArtworkSource, type AmbientArtworkIdentity} from './ambient.ts';
import {artworkRenditionFor} from './renditions.ts';
import type {ArtworkFailure} from './scheduler.ts';
import type {ProfileAvatar} from '../identity-restrictions.ts';
import {throwIfAborted} from '../abort-lifetime.ts';

/** pixels is the painted width in device pixels and aspect its width ÷ height:
 * the box the picture covers. The slot whose actual pixels fill that box is
 * requested by name and token, so each picture downloads once per slot. */
/** A picture on the rebuilt server: named by a long hash, never changing, fetched with no token. */
const pictureAddress = /^\/v1\/images\/[A-Za-z0-9_-]{1,128}$/;
const sizedPicture = /^\/v1\/images\/[A-Za-z0-9_-]{1,128}\?w=\d{1,5}&h=\d{1,5}$/;
export function foregroundArtwork(url: string | undefined, pixels: number, aspect: number, identity: AmbientArtworkIdentity) {
  if (url && pictureAddress.test(url)) {
    // The server keeps a small and a large size and sends the one that covers the box asked for.
    // Sizes are asked in steps of 160 so a card that grows a little reuses what is cached.
    const ratio = aspect > 0 && Number.isFinite(aspect) ? aspect : 1, width = Math.max(160, Math.ceil(pixels / 160) * 160);
    const selected = `${url}?w=${width}&h=${Math.ceil(width / ratio)}`;
    return {url: selected, cacheKey: JSON.stringify([identity.serverId, selected])};
  }
  // The accepted ambient parser also validates selected route, digest and query identity.
  if (!ambientArtworkSource(url, identity)) return undefined;
  const ratio = aspect > 0 && Number.isFinite(aspect) ? aspect : 1;
  const selected = artworkRenditionFor(url!, {width: pixels, height: pixels / ratio, dpr: 1}).url;
  return {url: selected, cacheKey: JSON.stringify([identity.serverId, identity.viewerId, identity.authorityKey, selected])};
}
/** A member's profile picture, as the rebuilt profile answer names it for People (`?v=…&member=…`), at one of the
 * server's two sizes. Read with the owner's or admin's sign-in. */
const memberProfileAvatarPattern = /^\/v1\/profiles\/[A-Za-z0-9_-]{1,128}\/avatar\?v=[a-z0-9]{1,16}&member=[A-Za-z0-9_-]{1,128}&size=(?:small|large)$/;
/** The server's unscaled sprite sheet, read with the viewer's sign-in. */
const scrubPreviewPath = serverPath('GET /v1/items/{id}/scrub-previews', {id: '__item__'});
const trickplayTilePattern = new RegExp('^' + scrubPreviewPath.replace('__item__', '([^/?#]{1,512})')
  + '\\?version=[A-Za-z0-9_.%-]{1,256}&sheet=(?:0|[1-9]\\d{0,4})$');
export function trickplayTileArtwork(url: string, identity: AmbientArtworkIdentity, revision = '') {
  if (url.length > 1024 || /[\s\\#\x00-\x1f\x7f]/.test(url) || ![identity.serverId, identity.viewerId, identity.authorityKey].every(value => typeof value === 'string' && value.length > 0 && value.length <= 512 && !/[\x00-\x1f\x7f]/.test(value))) return undefined;
  const match = trickplayTilePattern.exec(url);
  if (!match) return undefined;
  try {const id = decodeURIComponent(match[1]!); if (!id || id === '.' || id === '..' || /[\x00-\x1f\x7f]/.test(id)) return undefined;} catch {return undefined;}
  return {url, cacheKey: JSON.stringify([identity.serverId, identity.viewerId, identity.authorityKey, url, revision])};
}
/** People's pictures use their own authorized route and the picture's version. They enter
 * the same foreground queue as other authenticated art. */
export function memberProfileAvatarArtwork(url: string, identity: AmbientArtworkIdentity) {
  if (url.length > 1024 || !memberProfileAvatarPattern.test(url) || ![identity.serverId, identity.viewerId, identity.authorityKey].every(value => typeof value === 'string' && value.length > 0 && value.length <= 512 && !/[\x00-\x1f\x7f]/.test(value))) return undefined;
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
      throwIfAborted(signal);
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
  if (!isCurrent() || !(ambientArtworkSource(request.url, identity) || request.kind === 'foreground' && (sizedPicture.test(request.url) || memberProfileAvatarArtwork(request.url, identity) || trickplayTileArtwork(request.url, identity)))) throw Object.assign(new Error('Artwork authority changed.'), {status: 403});
  const token = await auth.token(options.currentToken());
  throwIfAborted(signal);
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

export function memberProfileAvatarArtworkSource(accountId: string, avatar: ProfileAvatar | undefined, pixels: number, identity: AmbientArtworkIdentity) {
  // Only the picture of this person's own profile, with the management query the profile answer gave it.
  if (!avatar || !avatar.url.startsWith(serverPath('GET /v1/profiles/{id}/avatar', {id: avatar.profileId}) + '?') || !avatar.url.endsWith('&member=' + encodeURIComponent(accountId))) return undefined;
  return memberProfileAvatarArtwork(avatar.url + '&size=' + (pixels > 160 ? 'large' : 'small'), identity);
}
