import {routePattern} from '../../../contracts/src/routes.ts';

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
  variant: 'ambient-v1';
  maxEdge: 64;
}>;

const artworkPatterns = [
  routePattern('get_items_id_art_kind'),
  routePattern('get_metadata_kind_id_art_role'),
].map(pattern => new RegExp('^' + pattern.split('/').map(part => part.startsWith('{')
  ? '[^/]+'
  : part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('/') + '$'));

/**
 * Derives only the implemented ambient representation of a server-provided
 * versioned selected-art URL. The foreground URL stays untouched. Pass it to the
 * ordinary artwork scheduler/transport, including its real Retry-After policy.
 * No renderer, fetch, title selection, palette or colour is supplied here.
 */
export function ambientArtworkSource(artworkUrl: string | null | undefined, identity: AmbientArtworkIdentity): AmbientArtworkSource | null {
  if (!artworkUrl || artworkUrl.length > 4096 || !artworkUrl.startsWith('/') || artworkUrl.startsWith('//') || /[\s\\#\x00-\x1f\x7f]/.test(artworkUrl)) return null;
  if (![identity.serverId, identity.viewerId, identity.authorityKey].every(value => typeof value === 'string' && value.length > 0 && value.length <= 512 && !/[\x00-\x1f\x7f]/.test(value))) return null;
  const [path, query = ''] = artworkUrl.split('?');
  if (!path || artworkUrl.indexOf('?') !== artworkUrl.lastIndexOf('?') || !artworkPatterns.some(pattern => pattern.test(path))) return null;
  try {
    if (path.split('/').some(part => {
      const decoded = decodeURIComponent(part);
      return decoded === '.' || decoded === '..' || /[/\\\x00-\x1f\x7f]/.test(decoded);
    })) return null;
  } catch { return null; }
  const params = new URLSearchParams(query);
  const allowed = new Set(['v', 'subject', 'w', 'size', 'sd', 'ld', 'r']);
  for (const key of params.keys()) if (!allowed.has(key) || params.getAll(key).length !== 1) return null;
  const version = params.get('v');
  if (version === null || !/^[a-f0-9]{64}$/.test(version)) return null;
  const subject = params.get('subject');
  if (subject !== null) {
    const bytes = Array.from(subject).reduce((total, char) => {
      const point = char.codePointAt(0)!;
      return total + (point <= 0x7f ? 1 : point <= 0x7ff ? 2 : point <= 0xffff ? 3 : 4);
    }, 0);
    if (bytes > 160 || /[\x00-\x1f\x7f]/.test(subject)) return null;
  }
  for (const key of ['w', 'sd', 'ld', 'r']) params.delete(key);
  params.set('size', 'ambient-v1');
  params.sort();
  const url = path + '?' + params.toString();
  return Object.freeze({
    url,
    cacheKey: JSON.stringify([identity.serverId, identity.viewerId, identity.authorityKey, url]),
    version,
    variant: 'ambient-v1',
    maxEdge: 64,
  });
}
