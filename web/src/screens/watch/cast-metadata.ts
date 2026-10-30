import type {ItemResponse} from '@core/media-item.ts';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import {parseMediaItem} from '@core/media-item.ts';
import type {CastApi, CastMetadata} from '@core/cast/index.ts';
import {castCoverPath, castCoverTarget} from './cast-cover';

/** A display read under the original caller's authenticated API. One optional
 * relation comes only from the admitted item's own projection; it cannot select
 * another item, a new viewer or a replacement API. */
export async function readWatchCastMetadata(api: CastApi, itemId: string, assertCurrent: () => void, purpose: 'identity' | 'cover'): Promise<Readonly<{
  item: ItemResponse; metadata: CastMetadata;
}>> {
  assertCurrent();
  const raw = await api.request<ItemResponse>(encodedRoutePath('get_items_id', {id: encodeURIComponent(itemId)}));
  assertCurrent();
  const item = parseMediaItem(raw);
  if (item.id !== itemId) throw new Error('cast_item_mismatch');
  let artworkPath = item.posterUrl ?? item.backdropUrl;
  // Root50's title/permission check must not wait for optional cover enrichment.
  const target = purpose === 'cover' ? castCoverTarget(raw) : undefined;
  if (target) {
    try {
      assertCurrent();
      const related = await api.request<ItemResponse>(encodedRoutePath('get_items_id', {id: encodeURIComponent(target.id)}));
      assertCurrent();
      artworkPath = castCoverPath(target, related);
    } catch {
      // Optional art refusal leaves the permitted title usable. A retired
      // original scope still throws, so it cannot publish or enter the cache.
      assertCurrent();
    }
  }
  assertCurrent();
  return Object.freeze({item: raw, metadata: Object.freeze({title: item.title, kind: item.kind, artworkPath,
    ...(raw.episode ? {seriesTitle: raw.episode.showTitle, season: raw.episode.seasonNumber, episode: raw.episode.number} : {}),
  })});
}
