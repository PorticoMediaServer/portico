import {serverPath} from '../query/server-api.ts';

export type AmbientArtworkIdentity = Readonly<{
  serverId: string;
  viewerId: string;
  /** Current session/permission fence; change it when authority changes. */
  authorityKey: string;
}>;

export type AmbientArtworkSource = Readonly<{
  url: string;
  cacheKey: string;
  version: string;
  variant: 'ambient-v1' | 'ambient-v2';
  maxEdge: 64;
}>;

const imagePrefix = serverPath('GET /v1/images/{id}', {id: '__image__'}).replace('__image__', '');

/**
 * Derives only the implemented ambient representation of a server-provided
 * content-hash image URL. The foreground URL stays untouched. Pass it to the
 * ordinary artwork scheduler/transport, including its real Retry-After policy.
 * No renderer, fetch, title selection, palette or colour is supplied here.
 */
export function ambientArtworkSource(artworkUrl: string | null | undefined, identity: AmbientArtworkIdentity): AmbientArtworkSource | null {
  return ambientSource(artworkUrl, identity, 'ambient-v1');
}

/** Native apps use the server's saturated wash; the web keeps v1 and its own filters. */
export function ambientArtworkSourceV2(artworkUrl: string | null | undefined, identity: AmbientArtworkIdentity): AmbientArtworkSource | null {
  return ambientSource(artworkUrl, identity, 'ambient-v2');
}

function ambientSource(artworkUrl: string | null | undefined, identity: AmbientArtworkIdentity, variant: AmbientArtworkSource['variant']): AmbientArtworkSource | null {
  if (!artworkUrl || artworkUrl.length > 4096 || !artworkUrl.startsWith('/') || artworkUrl.startsWith('//') || /[\s\\#\x00-\x1f\x7f]/.test(artworkUrl)) return null;
  if (![identity.serverId, identity.viewerId, identity.authorityKey].every(value => typeof value === 'string' && value.length > 0 && value.length <= 512 && !/[\x00-\x1f\x7f]/.test(value))) return null;
  const [path, query = ''] = artworkUrl.split('?');
  if (!path?.startsWith(imagePrefix) || artworkUrl.indexOf('?') !== artworkUrl.lastIndexOf('?')) return null;
  const version = path.slice(imagePrefix.length);
  if (!/^[a-f0-9]{64}$/.test(version)) return null;
  const params = new URLSearchParams(query);
  for (const key of params.keys()) if (!['w', 'h', 'size'].includes(key) || params.getAll(key).length !== 1) return null;
  params.delete('w');
  params.delete('h');
  params.set('size', variant);
  const url = path + '?' + params.toString();
  return Object.freeze({
    url,
    cacheKey: JSON.stringify([identity.serverId, identity.viewerId, identity.authorityKey, url]),
    version,
    variant,
    maxEdge: 64,
  });
}
