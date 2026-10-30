import type {BulkMetadataPreview} from '../../../../packages/client-core/src/home-selection/metadata-preview';
import type {HomeSelectionEditor} from '../../../../packages/client-core/src/home-selection/types';
import type {BulkFieldEdit} from '../../../../packages/client-core/src/metadata-bulk';
import type {BulkChanges, BulkFieldValue} from '../kit/selection';

/** Preserve exact shared/mixed values and the honest saturated many-state bound. */
export function nativeBulkValues(preview: BulkMetadataPreview): Readonly<Record<string, BulkFieldValue>> {
  return Object.fromEntries(preview.schema.map(spec => {
    const field = preview.fields[spec.field];
    if (!field) throw new Error('invalid_metadata_preview');
    return [spec.field, field];
  }));
}

/** Only authored changes are submitted; displaying a shared or Mixed value authors nothing. */
export function nativeBulkDraft(changes: BulkChanges, lockEdited: boolean): HomeSelectionEditor['draft'] {
  const fields: Record<string, BulkFieldEdit> = {}, lists: {tags?: {add: readonly string[]; remove: readonly string[]}; labels?: {add: readonly string[]; remove: readonly string[]}} = {};
  let genres: HomeSelectionEditor['draft']['genres'];
  for (const [name, change] of Object.entries(changes)) {
    if ('value' in change) fields[name] = {value: change.value};
    else if (name === 'genres') genres = {add: change.add, remove: change.remove};
    else if (name === 'tags' || name === 'labels') lists[name] = {add: change.add, remove: change.remove};
    else throw new Error('unsupported_bulk_list_change');
  }
  return {...(Object.keys(fields).length ? {fields} : {}), ...(Object.keys(lists).length ? {lists} : {}), ...(genres ? {genres} : {}), lockEdited};
}

export function nativeBulkChanges(draft: HomeSelectionEditor['draft']): BulkChanges {
  const changes: Record<string, BulkChanges[string]> = {};
  for (const [name, value] of Object.entries(draft.fields ?? {})) {
    if (value.value !== undefined) changes[name] = {value: value.value};
    else throw new Error('unsupported_bulk_scalar_change');
  }
  for (const name of ['tags', 'labels'] as const) {const list = draft.lists?.[name]; if (list) changes[name] = {add: list.add ?? [], remove: list.remove ?? []};}
  if (draft.genres) changes.genres = {add: draft.genres.add ?? [], remove: draft.genres.remove ?? []};
  return changes;
}
