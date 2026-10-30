import {routePath} from '../../contracts/src/index.ts';
import {isVersionedArtwork} from './artwork/versioned.ts';

/** A selected Reading cover is a versioned local route for this exact item. */
export function validReadingCover(itemId: string, value: unknown): boolean {
  if (value === undefined) return true;
  return typeof value === 'string' && isVersionedArtwork(value, routePath('get_items_id_art_kind', {id: itemId, kind: 'cover'}) + '?v=');
}
