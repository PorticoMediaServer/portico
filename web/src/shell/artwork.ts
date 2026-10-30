import {encodedRoutePath, routePattern} from '../../../packages/contracts/src/routes.ts';
import {retryAfterSecondsFrom, type AuthRecovery, type HttpLocalApi} from '@core/index.ts';
import {ambientArtworkSource, artworkRenditionFor, type AmbientArtworkIdentity, type ArtworkFailure, type ArtworkRequestOptions} from '@core/artwork/index.ts';
import {createArtworkRuntime, type ArtworkLoadRequest} from '../kit/artwork/source';
import type {ArtworkImage} from '../kit/artwork/types';
import type {ProfileAvatar} from '@core/identity-restrictions.ts';

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

/** Real credentials go through the selected route transport, never an img URL or localStorage. */
export function authenticatedArtwork(options: {
  api: Pick<HttpLocalApi, 'baseUrl' | 'routeFetch'>;
  auth: AuthRecovery;
  currentToken: () => string;
  identity: AmbientArtworkIdentity;
  isCurrent: () => boolean;
}) {
  const {api, auth, identity, isCurrent} = options;
  const runtime = createArtworkRuntime({
    scopeKey: JSON.stringify([identity.serverId, identity.viewerId, identity.authorityKey]),
    classify: artworkFailure,
    load: async (request: ArtworkLoadRequest, signal: AbortSignal): Promise<ArtworkImage> => {
      if (!isCurrent() || !(ambientArtworkSource(request.url, identity) || request.kind === 'foreground' && (channelLogoArtwork(request.url, identity) || memberProfileAvatarArtwork(request.url, identity)))) throw Object.assign(new Error('Artwork authority changed.'), {status: 403});
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
      const blob = await imageBytes(response, request.kind === 'ambient' ? 20000 : 12 * 1024 * 1024, signal);
      let width: number, height: number;
      try {
        const image = await createImageBitmap(blob);
        width = image.width; height = image.height; image.close();
      } catch { throw Object.assign(new Error('Artwork could not be decoded.'), {status: 422}); }
      if (request.kind === 'ambient' && Math.max(width, height) > 64) throw Object.assign(new Error('Ambient artwork exceeds its dimensions.'), {status: 422});
      signal.throwIfAborted();
      if (!isCurrent()) throw Object.assign(new Error('Artwork authority changed.'), {status: 403});
      return {src: URL.createObjectURL(blob), bytes: blob.size, width, height};
    },
    release: image => URL.revokeObjectURL(image.src),
  });
  return {
    ...runtime,
    foreground(url: string | undefined, pixels: number, aspect: number, request?: Omit<ArtworkRequestOptions, 'signal'>) {
      const selected = foregroundArtwork(url, pixels, aspect, identity);
      return selected ? runtime.source(selected, request) : undefined;
    },
    channelLogo(id: string, revision?: number) {
      if (!isCurrent() || !/^[A-Za-z0-9_-]{1,128}$/.test(id) || revision !== undefined && (!Number.isSafeInteger(revision) || revision < 1)) return undefined;
      const url = encodedRoutePath('get_library_channels_id_logo', {id: encodeURIComponent(id)}) + (revision === undefined ? '' : '?v=' + revision);
      const selected = channelLogoArtwork(url, identity);
      return selected ? runtime.source(selected) : undefined;
    },
    memberProfileAvatar(accountId: string, avatar: ProfileAvatar | undefined, pixels: number) {
      if (!isCurrent() || !avatar || !Number.isSafeInteger(avatar.version) || avatar.version < 1) return undefined;
      const url = encodedRoutePath('get_admin_access_members_id_profiles_profile_avatar', {id: encodeURIComponent(accountId), profileId: encodeURIComponent(avatar.profileId)}) + '?v=' + avatar.version;
      if (avatar.url !== url) return undefined;
      const size = pixels <= 64 ? 64 : pixels <= 160 ? 160 : 512;
      const selected = memberProfileAvatarArtwork(url + '&size=' + size, identity);
      return selected ? runtime.source(selected) : undefined;
    },
    ambient(url: string | undefined, request?: Omit<ArtworkRequestOptions, 'signal'>) {
      const selected = ambientArtworkSource(url, identity);
      return selected ? runtime.ambient(selected, request) : undefined;
    },
  };
}
export type ShellArtwork = ReturnType<typeof authenticatedArtwork>;
