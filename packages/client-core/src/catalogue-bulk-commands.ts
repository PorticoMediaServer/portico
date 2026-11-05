import type {DeletePreview} from './administration.ts';
import {bulkTargetLimit, type BulkEdit, type BulkTarget} from './metadata-bulk.ts';
import type {RepairFieldSpec} from './metadata-repair.ts';

export type CatalogueBulkChanges = Readonly<Record<string, {value: string} | {add: readonly string[]; remove: readonly string[]}>>;
export type CatalogueBulkBase = Omit<BulkEdit, 'operationId' | 'targets'>;
export type CatalogueSelectionDraft = Readonly<{
  fields: Readonly<Record<string, string>>;
  lists: Readonly<Record<string, Readonly<{add: readonly string[]; remove: readonly string[]}>>>;
  lockEdited: boolean;
}>;

/** Preserve the existing web editor's field/list/genre projection and key order. */
export function catalogueSelectionBulkDraft(draft: CatalogueSelectionDraft): Pick<BulkEdit, 'fields' | 'lists' | 'genres' | 'lockEdited'> {
  const {genres, ...lists} = draft.lists;
  return {fields: Object.fromEntries(Object.entries(draft.fields).map(([key, value]) => [key, {value}])), lists,
    ...(genres ? {genres} : {}), lockEdited: draft.lockEdited};
}

/** The web editor's existing wire order places its ID and targets before changes.
 * Its owner allocates IDs and retains this exact body across uncertain retries. */
export function catalogueSelectionBulkEdit(base: CatalogueBulkBase, targets: readonly BulkTarget[], start: number, operationId: string): BulkEdit {
  return {operationId, targets: targets.slice(start, start + bulkTargetLimit).map(({kind, id, expectedRevision}) => ({kind, id, expectedRevision})), ...base};
}

/** Web deletion uses the captured selection order, which can differ from preview
 * order, and freezes both the ID array and its retained command. */
export function catalogueSelectionDeletionCommand(targets: readonly Readonly<{itemId: string}>[], preview: Pick<DeletePreview, 'revision'>,
  choice: Readonly<{confirmation: string; removeFiles: boolean}>, operationId: string) {
  return Object.freeze({itemIds: Object.freeze(targets.map(item => item.itemId)), expectedRevision: preview.revision,
    confirmation: choice.confirmation, deleteFiles: choice.removeFiles, operationId});
}

/** Build the captured change set before its owner mints any operation IDs. */
export function catalogueBulkBase(schema: readonly RepairFieldSpec[], changes: CatalogueBulkChanges, lockEdited: boolean): CatalogueBulkBase | undefined {
  const fields: Record<string, {value?: string; values?: readonly string[]}> = {};
  const lists: Record<string, {add?: readonly string[]; remove?: readonly string[]}> = {};
  let genres: {add?: readonly string[]; remove?: readonly string[]} | undefined;
  for (const spec of schema) {
    const change = changes[spec.field];
    if (!change) continue;
    if ('value' in change) {fields[spec.field] = {value: change.value}; continue;}
    const list = {add: change.add, remove: change.remove};
    if (spec.field === 'genres') genres = list; else lists[spec.field] = list;
  }
  if (!Object.keys(fields).length && !Object.keys(lists).length && !genres) return;
  return {...(Object.keys(fields).length ? {fields} : {}), ...(Object.keys(lists).length ? {lists} : {}), ...(genres ? {genres} : {}), lockEdited};
}

/** The owner retains ID allocation, validation timing, receipts and retry state.
 * Target projection happens after that captured owner's ID promise settles. */
export function catalogueBulkEdit(base: CatalogueBulkBase, targets: readonly BulkTarget[], start: number, operationId: string): BulkEdit {
  return {...base, operationId, targets: targets.slice(start, start + bulkTargetLimit).map(({kind, id, expectedRevision}) => ({kind, id, expectedRevision}))};
}

/** One retained deletion command; the captured owner reuses it after an unknown
 * outcome rather than rebuilding it from a later confirmation or file choice. */
export function catalogueDeletionCommand(preview: Pick<DeletePreview, 'targets' | 'revision'>,
  choice: Readonly<{confirmation: string; removeFiles: boolean}>, operationId: string) {
  return Object.freeze({itemIds: preview.targets.map(target => target.itemId), expectedRevision: preview.revision,
    confirmation: choice.confirmation, deleteFiles: choice.removeFiles, operationId});
}
