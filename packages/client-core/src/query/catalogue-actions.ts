import {encodedRoutePath, routePath} from '../../../contracts/src/routes.ts';
import type {BulkEdit} from '../metadata-bulk.ts';
import {metadataAgentPath} from '../server-admin/metadata-agent.ts';
import {refreshMetadataPath} from '../presentation/entry-actions.ts';
import {formatBytes} from '../presentation/content.ts';
import {serverPath} from './server-api.ts';
import type {DeletePreview, DeleteResult} from '../administration.ts';
import type {DeletionPreview, DeletionResult, SelectionDeleted, SelectionDeletion} from '../../../contracts/src/next/api.ts';
import {saveBulkMetadata} from '../metadata-bulk-rebuilt.ts';

export interface CatalogueActionsApi {
  request(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<unknown>;
}
/** Existing command owners retain their captured proofs and uncertain-outcome
 * state. These operations never retry or replace an operation's body. */
export function readMetadataResponse(api: CatalogueActionsApi, kind: string, id: string, signal?: AbortSignal) {
  return api.request(encodedRoutePath('get_metadata_kind_id', {kind: encodeURIComponent(kind), id: encodeURIComponent(id)}), 'GET', undefined, signal);
}
/** One bulk edit, saved as the rebuilt server's `edit_items` job; the answer is the editor's per-title receipt. */
export function submitMetadataBulk(api: CatalogueActionsApi, body: BulkEdit, signal?: AbortSignal) {
  return saveBulkMetadata(api as never, body, signal);
}
/** A title to delete, as the screen holds it. */
export type DeletionTarget = Readonly<{itemId: string; libraryId: string; kind: string; title?: string}>;

const errorCode = (error: unknown) => (error as {code?: unknown} | null)?.code;
const refusalOf = (error: unknown) => error instanceof Error && error.message ? error.message : 'This can’t be deleted.';

/**
 * What deleting these titles takes, before it is confirmed (definition 03 rules 63 to 69): one title is
 * `GET /v1/admin/items/{id}/deletion`, several `POST /v1/admin/items/deletion`. One title is confirmed by typing its
 * name, several by typing the count. A library with deleting off, or files on remote storage, is a preview that says
 * why and cannot be confirmed.
 */
export async function readCatalogueDeletion(api: CatalogueActionsApi, targets: readonly DeletionTarget[], signal?: AbortSignal): Promise<DeletePreview> {
  if (!targets.length) throw new Error('Choose at least one title to delete.');
  const one = targets.length === 1;
  let answer: DeletionPreview | SelectionDeletion, refusal: string | undefined;
  try {
    answer = one
      ? await api.request(serverPath('GET /v1/admin/items/{id}/deletion', {id: targets[0]!.itemId}), 'GET', undefined, signal) as DeletionPreview
      : await api.request(serverPath('POST /v1/admin/items/deletion'), 'POST', {ids: targets.map(target => target.itemId)}, signal) as SelectionDeletion;
    refusal = 'deleteRefusal' in answer && answer.deleteRefusal ? answer.deleteRefusal : undefined;
  } catch (error) {
    if (errorCode(error) !== 'deleting_not_allowed' && errorCode(error) !== 'remote_storage') throw error;
    answer = {count: targets.length, titles: 0, files: 0, bytes: 0, listEntries: 0, otherPeople: 0, alsoRemoves: []};
    refusal = refusalOf(error);
  }
  const title = 'title' in answer ? answer.title : '';
  const titles = typeof answer.titles === 'number' ? answer.titles : 0;
  return Object.freeze({revision: 0, confirmation: one ? title || targets[0]!.title || '' : String(targets.length), confirmationKind: one ? 'title' as const : 'count' as const,
    totalBytes: answer.bytes ?? 0, totalFiles: answer.files ?? 0, totalBytesText: formatBytes(answer.bytes ?? 0), blocked: refusal ? Object.freeze([targets[0]!.libraryId]) : Object.freeze([]),
    targets: Object.freeze(targets.map(target => Object.freeze({itemId: target.itemId, title: one ? title || target.title || '' : target.title ?? '', kind: target.kind, libraryId: target.libraryId,
      libraryName: '', allowMediaDeletion: !refusal, titles: one ? titles : 1, bytes: one ? answer.bytes ?? 0 : 0, files: Object.freeze([]), dependents: Object.freeze([])}))),
    titlesTotal: titles, listEntries: answer.listEntries ?? 0, otherPeople: answer.otherPeople ?? 0,
    alsoRemoves: Object.freeze((answer.alsoRemoves ?? []).map(ref => Object.freeze({id: ref.id, title: ref.title}))), ...(refusal ? {refusal} : {})});
}

/**
 * Deletes the titles and their files (`DELETE /v1/admin/items/{id}`, or `POST /v1/admin/items/delete` for several):
 * files are always deleted, never only Portico's record (definition 03, decision O03). The answer says title by title
 * what was removed; a title one of whose files was kept stays, and the files kept are named with why (rule 68).
 */
export async function deleteCatalogueTitles(api: CatalogueActionsApi, targets: readonly DeletionTarget[], signal?: AbortSignal): Promise<DeleteResult> {
  if (!targets.length) throw new Error('Choose at least one title to delete.');
  let removed: ReadonlySet<string>, answer: DeletionResult | SelectionDeleted;
  if (targets.length === 1) {
    const one = await api.request(serverPath('DELETE /v1/admin/items/{id}', {id: targets[0]!.itemId, query: {confirm: targets[0]!.title ?? ''}}), 'DELETE', undefined, signal) as DeletionResult;
    answer = one; removed = new Set(one.removed ? [targets[0]!.itemId] : []);
  } else {
    const many = await api.request(serverPath('POST /v1/admin/items/delete', {query: {confirm: targets.length}}), 'POST', {ids: targets.map(target => target.itemId)}, signal) as SelectionDeleted;
    answer = many;
    // The server counts what it removed; when that is not everything, each title is looked for: one still there was kept.
    if (many.removed >= targets.length) removed = new Set(targets.map(target => target.itemId));
    else {
      const gone = new Set<string>();
      for (const target of targets) {
        try {await api.request(serverPath('GET /v1/items/{id}', {id: target.itemId}), 'GET', undefined, signal);}
        catch (error) {if (errorCode(error) === 'not_found') gone.add(target.itemId);}
      }
      removed = gone;
    }
  }
  const kept = Object.freeze((answer.kept ?? []).map(file => Object.freeze({path: file.path, reason: file.reason})));
  const receipts = Object.freeze(targets.map(target => Object.freeze({itemId: target.itemId, title: target.title ?? '', removed: removed.has(target.itemId),
    filesDeleted: 0, filesKept: 0, bytes: 0, code: removed.has(target.itemId) ? '' : 'file_not_removed'})));
  return Object.freeze({revision: 0, removed: removed.size, failed: targets.length - removed.size, bytesDeleted: answer.bytes ?? 0, filesRetained: false, receipts, kept});
}
export function readLibraryMetadataAgentResponse(api: CatalogueActionsApi, libraryId: string, signal?: AbortSignal) {
  return api.request(metadataAgentPath(libraryId), 'GET', undefined, signal);
}
export function refreshCatalogueMetadata(api: CatalogueActionsApi, kind: string | undefined, id: string, signal?: AbortSignal) {
  const path = refreshMetadataPath(kind, id);
  if (!path) throw new Error('metadata_refresh_unavailable');
  return api.request(path, 'POST', {}, signal);
}
