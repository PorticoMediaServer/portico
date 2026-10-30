import React, {useEffect, useState, useSyncExternalStore} from 'react';
import {summarizeSelection} from '../../../../packages/client-core/src/selection-model/index';
import type {HomeSelectionController} from '../../../../packages/client-core/src/home-selection/index';
import type {ContentEntry} from '../../../../packages/client-core/src/library-content';
import {presentError} from '../../../../packages/client-core/src/presentation/errors';
import {CollectionPicker, PlaylistPicker, type PickerResult} from '../kit/selection';
import {CoverMosaic} from '../kit/artwork';
import {useTheme} from '../kit/theme';
import {homeCardText} from '../screens/home/model';
import {nativePickerCounts} from './selection-picker';
import {selectedItemsLabel} from './selection-display';
import type {NativeShellRuntime} from './runtime';
import {tvFocusIds} from './tv-focus';

/** The shared controller owns page cursors, creation, job ids and durable outcomes. */
export function HomeDestinationPicker({controller, runtime, reportError}: {
  controller: HomeSelectionController; runtime: NativeShellRuntime; reportError: (error: unknown) => void;
}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), theme = useTheme(), picker = state.picker;
  const [creating, setCreating] = useState(false), [name, setName] = useState('');
  useEffect(() => {setCreating(false); setName('');}, [picker?.kind]);
  if (!picker) return null;
  const run = (work: Promise<unknown>) => {void work.catch(reportError);};
  const art = (entries: readonly ContentEntry[]) => <CoverMosaic shape="square" width={theme.isTV ? 76 : 48}
    covers={entries.flatMap(entry => {const source = runtime.artwork(entry.posterUrl); return source ? [source] : [];})}/>;
  const error = picker.error ? presentError(picker.error, 'home', {operation: 'action'}).body : undefined;
  const receipt = picker.receipt, destination = picker.destination;
  const failures = (receipt?.failures ?? []).map((row, index) => {
    const entry = receipt!.failureEntries[index];
    if (!entry || entry.id !== row.itemId) throw new Error('picker_failure_entry_required');
    return {id: row.itemId, title: homeCardText(entry, false, runtime.text).title, reason: presentError({code: row.code}, 'home', {operation: 'action'}).body};
  });
  const counts = nativePickerCounts(picker);
  const result: PickerResult | undefined = receipt && destination ? {destination: {id: destination.id, title: destination.name, size: destination.count, artwork: art(destination.artwork)},
    ...counts!, failures} : undefined;
  const Picker = picker.kind === 'playlist' ? PlaylistPicker : CollectionPicker;
  return <Picker testID={theme.isTV ? tvFocusIds(runtime.getSnapshot().authority).surface(`picker.${picker.kind}`) : undefined}
    open onOpenChange={open => {if (!open) controller.closePicker();}} count={state.displayMembers.length}
    selectionLabel={selectedItemsLabel(summarizeSelection(state.displayMembers.map(member => member.item)))}
    entries={picker.rows.map(row => ({id: row.id, title: row.name, size: row.count, canAdd: row.canAdd,
      ...(row.alreadyIn === undefined ? {} : {alreadyIn: row.alreadyIn}), artwork: art(row.artwork)}))}
    query={picker.query} onQueryChange={query => run(controller.pickerSearch(query))} showFind
    hasNext={picker.next} hasPrevious={picker.previous}
    onNext={picker.next ? () => run(controller.pickerNext()) : undefined} onPrevious={picker.previous ? () => run(controller.pickerPrevious()) : undefined}
    loading={picker.phase === 'loading'} pending={picker.phase === 'adding'} error={error}
    creating={creating} onCreatingChange={setCreating} name={name} onNameChange={setName}
    onCreateAndAdd={value => {setCreating(false); run(controller.createDestination(value));}} onAdd={id => run(controller.chooseDestination(id))}
    onRetry={ids => {
      const expected = receipt?.failures.map(row => row.itemId) ?? [];
      if (ids.length !== expected.length || new Set(ids).size !== expected.length || ids.some(id => !expected.includes(id))) {reportError(new Error('invalid_picker_retry_targets')); return;}
      run(controller.retryPicker());
    }} onRetryError={picker.phase === 'error' ? () => run(controller.retryPicker()) : undefined}
    onRetryOperation={picker.phase === 'uncertain' ? () => run(controller.retryPicker()) : undefined}
    result={result} onViewDestination={id => {if (controller.getSnapshot().picker?.destination?.id === id) run(controller.viewDestination());}}
    onResultDismiss={() => controller.closePicker()}/>;
}
