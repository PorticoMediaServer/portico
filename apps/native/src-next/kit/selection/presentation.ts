import {selectionText} from './copy';
import type {BulkChanges, BulkFieldChange, BulkFieldValue, SelectionOutcome} from './types';

export function failedSelectionIds(outcomes: readonly Pick<SelectionOutcome, 'id' | 'ok'>[]): readonly string[] {
  return [...new Set(outcomes.filter(row => !row.ok).map(row => row.id))];
}

export function changeScalar(changes: BulkChanges, field: string, value: string): BulkChanges {
  return {...changes, [field]: {value}};
}
export function removeFieldChange(changes: BulkChanges, field: string): BulkChanges {
  return Object.fromEntries(Object.entries(changes).filter(([key]) => key !== field));
}
export function setListChange(changes: BulkChanges, field: string, add: readonly string[], remove: readonly string[]): BulkChanges {
  return add.length || remove.length ? {...changes, [field]: {add, remove}} : removeFieldChange(changes, field);
}

export function changeList(changes: BulkChanges, field: string, value: string, operation: 'add' | 'remove'): BulkChanges {
  const prior: BulkFieldChange | undefined = changes[field];
  const list = prior && 'add' in prior ? prior : {add: [], remove: []};
  const opposite = operation === 'add' ? 'remove' : 'add';
  const next = {...list, [operation]: [...new Set([...list[operation], value])], [opposite]: list[opposite].filter(item => item !== value)};
  return setListChange(changes, field, next.add, next.remove);
}

// SIM-14: one shared receipt/picker/delete-gate reducer for web and native.
export {bulkReceiptView, pickerResultSucceeded, deleteSelectionGate, DELETE_SELECTION_LIMIT, type DeleteGate, type DeleteGateReason} from '../../../../../packages/client-core/src/selection-outcomes';

/** A lower bound never masquerades as an exact number of field values. */
export function bulkFieldStateLabel(field: BulkFieldValue): string {
  return selectionText(field.state === 'many' ? 'next.selection.native.manyValues' : 'next.selection.native.mixed');
}
export function bulkFieldHint(field: BulkFieldValue | undefined, count: number, changed: boolean): string {
  if (!field) return '';
  if (changed) return field.state !== 'shared' ? selectionText('next.selection.native.wasMixed') : '';
  if (field.state === 'shared' && field.value !== undefined) return selectionText('next.selection.native.all', {count});
  if (field.state === 'mixed') return selectionText('next.selection.native.distinct', {count: field.distinctCount});
  return '';
}
