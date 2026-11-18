import {routePath} from '../../contracts/src/index.ts';
import {isVersionedArtwork} from './artwork/versioned.ts';
import {serverPath} from './query/server-api.ts';

const imagePrefix = serverPath('GET /v1/images/{id}', {id: '__image__'}).replace('__image__', '');

/** Older offline reading manifests can still carry a versioned cover for their exact item. */
export function validReadingCover(itemId: string, value: unknown): boolean {
  if (value === undefined) return true;
  return typeof value === 'string' && (isVersionedArtwork(value, routePath('get_items_id_art_kind', {id: itemId, kind: 'cover'}) + '?v=')
    || value.startsWith(imagePrefix) && /^[A-Za-z0-9_-]{1,128}$/.test(value.slice(imagePrefix.length)));
}
