import type {AuthRecovery, HttpLocalApi} from '@core/index.ts';
import {ambientArtworkSource, type AmbientArtworkIdentity, type ArtworkRequestOptions} from '@core/artwork/index.ts';
import {artworkFailure, foregroundArtwork, channelLogoArtwork, memberProfileAvatarArtwork, trickplayTileArtwork,
  readAuthenticatedArtworkBytes, channelLogoArtworkSource, memberProfileAvatarArtworkSource} from '@core/artwork/media-sources.ts';
import {createArtworkRuntime, type ArtworkLoadRequest} from '../kit/artwork/source';
import type {ArtworkImage} from '../kit/artwork/types';
import type {ProfileAvatar} from '@core/identity-restrictions.ts';
import {trickplayTileUrl, type TrickplaySet} from '@core/trickplay.ts';

export {artworkFailure, foregroundArtwork, channelLogoArtwork, memberProfileAvatarArtwork, trickplayTileArtwork};

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
      const blob = await readAuthenticatedArtworkBytes({api, auth, identity, isCurrent, currentToken: () => options.currentToken()}, request, signal);
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
      if (!isCurrent()) return undefined;
      const selected = channelLogoArtworkSource(id, revision, identity);
      return selected ? runtime.source(selected) : undefined;
    },
    /** One sheet of a parsed preview set, by tile; the set is the server's (`readItemTrickplay`). */
    trickplayTile(set: TrickplaySet, tileIndex: number) {
      if (!isCurrent()) return undefined;
      let url: string;
      try {url = trickplayTileUrl(set, tileIndex);} catch {return undefined;}
      const selected = trickplayTileArtwork(url, identity, set.revision);
      return selected ? runtime.source(selected, {group: 'playback', priority: 'high'}) : undefined;
    },
    memberProfileAvatar(accountId: string, avatar: ProfileAvatar | undefined, pixels: number) {
      if (!isCurrent()) return undefined;
      const selected = memberProfileAvatarArtworkSource(accountId, avatar, pixels, identity);
      return selected ? runtime.source(selected) : undefined;
    },
    ambient(url: string | undefined, request?: Omit<ArtworkRequestOptions, 'signal'>) {
      const selected = ambientArtworkSource(url, identity);
      return selected ? runtime.ambient(selected, request) : undefined;
    },
  };
}
export type ShellArtwork = ReturnType<typeof authenticatedArtwork>;
