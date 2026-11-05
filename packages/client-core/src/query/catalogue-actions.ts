import {encodedRoutePath, routePath} from '../../../contracts/src/routes.ts';
import type {BulkEdit} from '../metadata-bulk.ts';
import {metadataAgentPath} from '../server-admin/metadata-agent.ts';
import {refreshMetadataPath} from '../presentation/entry-actions.ts';

export interface CatalogueActionsApi {
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}
/** Existing command owners retain their captured proofs and uncertain-outcome
 * state. These operations never retry or replace an operation's body. */
export function readMetadataResponse(api: CatalogueActionsApi, kind: string, id: string, signal?: AbortSignal) {
  return api.request(encodedRoutePath('get_metadata_kind_id', {kind: encodeURIComponent(kind), id: encodeURIComponent(id)}), 'GET', undefined, signal);
}
export function submitMetadataBulk(api: CatalogueActionsApi, body: BulkEdit, signal?: AbortSignal) {
  return api.request(routePath('post_metadata_bulk'), 'POST', body, signal);
}
export function previewCatalogueDeletion(api: CatalogueActionsApi, itemIds: readonly string[], signal?: AbortSignal) {
  return api.request(routePath('post_admin_media_delete_preview'), 'POST', {itemIds}, signal);
}
export function deleteCatalogueItems(api: CatalogueActionsApi, body: unknown, signal?: AbortSignal) {
  return api.request(routePath('post_admin_media_delete'), 'POST', body, signal);
}
export function readLibraryMetadataAgentResponse(api: CatalogueActionsApi, libraryId: string, signal?: AbortSignal) {
  return api.request(metadataAgentPath(libraryId), 'GET', undefined, signal);
}
export function refreshCatalogueMetadata(api: CatalogueActionsApi, kind: string | undefined, id: string, signal?: AbortSignal) {
  const path = refreshMetadataPath(kind, id);
  if (!path) throw new Error('metadata_refresh_unavailable');
  return api.request(path, 'POST', {}, signal);
}
