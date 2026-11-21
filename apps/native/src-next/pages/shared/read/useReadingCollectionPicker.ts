import {useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ReadingCollection} from '../../../../../../packages/client-core/src/reading-collection';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {PickerResult} from '../../../kit/selection';
import type {NativeShellRuntime} from '../../../shell/runtime';

/** Add to collection for reading titles: the profile's collections (search, pages, create), one choice and its receipt
 * (added, already in, failed, unknown, pending). Moved from the old picker as it was. */
export function useReadingCollectionPicker({controller, runtime}: {controller: ReadingCollection; runtime: NativeShellRuntime}) {
  const {t} = defaultI18n, state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [creating, setCreating] = useState(false), [name, setName] = useState('');
  const run = (work: () => Promise<void>) => {void work().catch(runtime.reportError);};
  const receipt = state.receipt, destination = state.destination;
  const result: PickerResult | undefined = receipt && destination ? {destination: {id: destination.id, title: destination.name, size: destination.entryCount},
    inserted: receipt.added, processed: receipt.added + receipt.alreadyPresent + receipt.failed, alreadyIn: receipt.alreadyPresent,
    failed: receipt.failed, unknown: receipt.unknown, pending: receipt.pending, total: controller.items.length, complete: receipt.complete,
    failures: receipt.failures.map(row => ({id: row.itemId, title: controller.items.find(item => item.id === row.itemId)!.title,
      reason: presentError({code: row.code}, 'detail', {operation: 'action'}).body}))} : undefined;
  return {
    rows: state.rows.map(row => ({id: row.id, title: row.name, size: row.count, canAdd: row.canAdd, alreadyIn: row.alreadyIn,
      covers: row.artwork.flatMap(entry => {const art = runtime.artwork(entry.posterUrl ?? entry.backdropUrl); return art ? [art] : [];})})),
    props: {count: controller.items.length, selectionLabel: t('next.selection.native.titles', {count: controller.items.length}),
      query: state.query, onQueryChange: (query: string) => run(() => controller.search(query)), showFind: true,
      hasNext: state.next, hasPrevious: state.previous, onNext: () => run(() => controller.page(state)), onPrevious: () => run(() => controller.page(state, true)),
      creating, onCreatingChange: setCreating, name, onNameChange: setName, onCreateAndAdd: (value: string) => run(() => controller.create(value)),
      onAdd: (id: string) => run(() => controller.choose(state, id)), onRetry: () => run(controller.retry),
      onRetryOperation: state.phase === 'uncertain' ? () => run(controller.retry) : undefined,
      onRetryError: state.phase === 'error' ? () => run(controller.retry) : undefined,
      loading: state.phase === 'idle' || state.phase === 'loading', pending: state.phase === 'adding', result,
      error: state.error ? t('next.selection.pickerFailed') : undefined}};
}
