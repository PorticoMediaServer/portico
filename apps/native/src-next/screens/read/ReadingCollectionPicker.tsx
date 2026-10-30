import React, {useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ReadingCollection} from '../../../../../packages/client-core/src/reading-collection';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {CollectionPicker, type PickerResult} from '../../kit/selection';
import {CoverMosaic} from '../../kit/artwork';
import type {NativeShellRuntime} from '../../shell/runtime';

export function ReadingCollectionPicker({controller, runtime, onClose, onView}: {controller: ReadingCollection; runtime: NativeShellRuntime; onClose: () => void; onView: () => void}) {
  const {t} = defaultI18n, state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [creating, setCreating] = useState(false), [name, setName] = useState('');
  const run = (work: () => Promise<void>) => {void work().catch(runtime.reportError);};
  const receipt = state.receipt, destination = state.destination;
  const result: PickerResult | undefined = receipt && destination ? {destination: {id: destination.id, title: destination.name, size: destination.entryCount},
    inserted: receipt.added, processed: receipt.added + receipt.alreadyPresent + receipt.failed, alreadyIn: receipt.alreadyPresent,
    failed: receipt.failed, unknown: receipt.unknown, pending: receipt.pending, total: controller.items.length, complete: receipt.complete,
    failures: receipt.failures.map(row => ({id: row.itemId, title: controller.items.find(item => item.id === row.itemId)!.title,
      reason: presentError({code: row.code}, 'detail', {operation: 'action'}).body}))} : undefined;
  return <CollectionPicker open onOpenChange={open => {if (!open) onClose();}} count={controller.items.length}
    selectionLabel={t('next.selection.native.titles', {count: controller.items.length})}
    entries={state.rows.map(row => ({id: row.id, title: row.name, size: row.count, canAdd: row.canAdd, alreadyIn: row.alreadyIn,
      artwork: <CoverMosaic shape="square" covers={row.artwork.flatMap(entry => {const art = runtime.artwork(entry.posterUrl ?? entry.backdropUrl); return art ? [art] : [];})} width={48} height={48}/>}))}
    query={state.query} onQueryChange={query => run(() => controller.search(query))} showFind
    hasNext={state.next} hasPrevious={state.previous} onNext={() => run(() => controller.page(state))} onPrevious={() => run(() => controller.page(state, true))}
    creating={creating} onCreatingChange={setCreating} name={name} onNameChange={setName} onCreateAndAdd={value => run(() => controller.create(value))}
    onAdd={id => run(() => controller.choose(state, id))} onRetry={() => run(controller.retry)}
    onRetryOperation={state.phase === 'uncertain' ? () => run(controller.retry) : undefined}
    onRetryError={state.phase === 'error' ? () => run(controller.retry) : undefined}
    loading={state.phase === 'idle' || state.phase === 'loading'} pending={state.phase === 'adding'} result={result}
    error={state.error ? t('next.selection.pickerFailed') : undefined} onViewDestination={onView} onResultDismiss={onClose} testID="reading-collection-picker"/>;
}
