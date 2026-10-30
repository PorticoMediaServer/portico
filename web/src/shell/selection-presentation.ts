import type {BulkEdit, BulkReceipt} from '@core/metadata-bulk.ts';
import type {BulkMetadataPreview, HomeSelectionMember, HomeSelectionPicker} from '@core/home-selection/index.ts';
import type {BulkEditorDraft, BulkFieldValues, BulkResultView, PickerResult} from '../kit/selection';
import {bulkReceiptView} from '../kit/selection/presentation';
import {defaultI18n} from '@i18n';
import type {EditorAudience} from '../kit/shell';

/** Describes an already-admitted editor; this never grants an edit command. */
export function selectionEditorAudience(role: string): EditorAudience {
  return role === 'owner' ? {authority: 'owner'} : role === 'admin' || role === 'administrator'
    ? {authority: 'administrator', label: defaultI18n.t('next.home.allowedAdministrators')}
    : {authority: 'editor', label: defaultI18n.t('next.home.allowedEditors')};
}

/** A display projection of actual receipts. It is deliberately not a transport receipt. */
export function editorResultView(receipts: readonly BulkReceipt[]): BulkResultView | undefined {
  return receipts.length ? bulkReceiptView(receipts) : undefined;
}
export function editorValues(preview: BulkMetadataPreview): Readonly<Record<string, BulkFieldValues>> {
  return preview.fields;
}
/** Dialog labels come only from the controller's bounded, fenced source members. */
export function selectionMemberDisplay(member: HomeSelectionMember) {
  if (member.entry) return {id: member.entry.id, title: member.entry.title, posterUrl: member.entry.posterUrl};
  if (member.collection) return {id: member.collection.collection.id, title: member.collection.collection.name, posterUrl: member.collection.members[0]?.posterUrl};
  if (member.channel) return {id: JSON.stringify([member.channel.channel.sourceId, member.channel.channel.id]), title: member.channel.channel.name, posterUrl: member.channel.channel.logoPath};
  throw new Error('home-selection-display-required');
}
export function editorUiDraft(draft: Pick<BulkEdit, 'fields' | 'lists' | 'genres' | 'lockEdited'>): BulkEditorDraft {
  const list = (value: BulkEdit['genres']) => ({add: value?.add ?? [], remove: value?.remove ?? []});
  return {fields: Object.fromEntries(Object.entries(draft.fields ?? {}).flatMap(([key, edit]) => edit.value !== undefined ? [[key, edit.value]] : [])),
    lists: {...Object.fromEntries(Object.entries(draft.lists ?? {}).map(([key, edit]) => [key, list(edit)])), ...(draft.genres ? {genres: list(draft.genres)} : {})}, lockEdited: draft.lockEdited ?? true};
}
export function editorCoreDraft(draft: BulkEditorDraft): Pick<BulkEdit, 'fields' | 'lists' | 'genres' | 'lockEdited'> {
  const {genres, ...lists} = draft.lists;
  return {fields: Object.fromEntries(Object.entries(draft.fields).map(([key, value]) => [key, {value}])), lists,
    ...(genres ? {genres} : {}), lockEdited: draft.lockEdited};
}

/** Expanded failure leaves are returned by the authorized receipt read, independently of selected parents. */
export function pickerResultView(picker: HomeSelectionPicker, reason: (code: string) => string, jobFacts: PickerResult['jobFacts'] = []): PickerResult | undefined {
  const {destination, receipt} = picker;
  if (!destination || !receipt) return undefined;
  if (receipt.kind !== destination.kind || receipt.destinationId !== destination.id || receipt.failureEntries.length !== receipt.failures.length) throw new Error('invalid-picker-receipt-display');
  return {destination: {id: destination.id, name: destination.name, size: destination.count}, inserted: receipt.added, alreadyIn: receipt.alreadyPresent,
    failed: receipt.failed, unknown: receipt.unknown, pending: receipt.pending, complete: receipt.complete, jobFacts,
    failures: receipt.failures.map((failure, index) => {
      const entry = receipt.failureEntries[index];
      if (entry.id !== failure.itemId) throw new Error('invalid-picker-failure-display');
      return {id: entry.id, title: entry.title, reason: reason(failure.code)};
    })};
}
