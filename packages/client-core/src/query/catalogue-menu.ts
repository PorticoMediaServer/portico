import {refreshMetadataPath} from '../presentation/entry-actions.ts';
import type {CatalogueReadApi} from './catalogue-reads.ts';

/** The title menu quietly skips a kind without a refresh route. Its captured
 * screen lifetime still owns dispatch admission and result reporting. */
export function refreshAvailableCatalogueMetadata(api: CatalogueReadApi, kind: string | undefined, id: string, signal?: AbortSignal) {
  const path = refreshMetadataPath(kind, id);
  if (path) return api.request(path, 'POST', {}, signal);
}
