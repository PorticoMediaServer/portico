import {useState} from 'react';
import {defaultI18n} from '@i18n';
import {isJobTerminal} from '@core/bulk-jobs.ts';
import type {HomeSelectionPicker, HomeSelectionSnapshot} from '@core/home-selection/index.ts';
import {presentError} from '@core/presentation/errors.ts';
import {summarizeSelection} from '@core/selection-model/index.ts';
import {CoverMosaic} from '../kit/artwork';
import {CollectionPicker, PickerAddedToast, PlaylistPicker, pickerResultSucceeded, type PickerResult} from '../kit/selection';
import type {SelectionRendererSource} from './SelectionSurfaces';
import {retryPickerError, retryPickerOperation, retryPickerWindow, runSelectionCommand} from './selection-actions';
import {pickerResultView} from './selection-presentation';
import {useShellWidth} from './viewport';

const t = defaultI18n.t;
const reason = (code: string) => presentError({code}, 'home', {operation: 'action'}).body;

export function SelectionPickerSurface({source, snapshot}: {source: SelectionRendererSource; snapshot: HomeSelectionSnapshot}) {
  return snapshot.picker ? <PickerSessionSurface source={source} snapshot={snapshot} picker={snapshot.picker} /> : null;
}

/** The only local state is the unsubmitted name input; retained destination facts belong to the controller. */
function PickerSessionSurface({source, snapshot, picker}: {source: SelectionRendererSource; snapshot: HomeSelectionSnapshot; picker: HomeSelectionPicker}) {
  const [mode, setMode] = useState<'choose' | 'new'>('choose'), [name, setName] = useState('');
  const width = useShellWidth(), busy = picker.phase === 'adding', loading = picker.phase === 'loading';
  const {controller} = source;
  const run = (work: () => void | Promise<unknown>) => runSelectionCommand(work, source.reportError);
  const summary = summarizeSelection(snapshot.displayMembers.map(member => member.item));
  const selection = {count: summary.count, kind: summary.types.length === 1 ? summary.types[0].kind : undefined};
  const mosaic = (entries: HomeSelectionPicker['rows'][number]['artwork']) => <CoverMosaic alt="" covers={entries.flatMap((entry, index) => {
    const art = source.artwork.foreground(entry.posterUrl ?? entry.backdropUrl, 40 * (globalThis.devicePixelRatio || 1), 1, {group: 'home-destination-picker', order: index, priority: 'high'});
    return art ? [art] : [];
  })} />;
  const facts: NonNullable<PickerResult['jobFacts']>[number][] = [];
  if (picker.receipt) {
    const receipt = picker.receipt;
    if (receipt.failed > receipt.failures.length) facts.push({id: 'failed', text: t('next.home.picker.remainingFailures', {count: receipt.failed})});
    if (receipt.unknown > 0) facts.push({id: 'unknown', text: t('next.home.picker.unknown', {count: receipt.unknown})});
    const pending = receipt.jobs.filter(job => !isJobTerminal(job.state)).length;
    if (pending > 0) facts.push({id: 'pending', text: t('next.home.picker.pendingJobs', {count: pending})});
    if (receipt.unresolvedJobIds.length > 0) facts.push({id: 'unresolved', text: t('next.home.picker.unresolvedJobs', {count: receipt.unresolvedJobIds.length})});
  }
  const actualResult = pickerResultView(picker, reason, facts);
  const result = actualResult && {...actualResult, destination: {...actualResult.destination, artwork: mosaic(picker.destination!.artwork)}};
  const succeeded = picker.phase === 'added' && !!result && pickerResultSucceeded(result);
  const close = () => { if (!busy) controller.closePicker(); };
  const Picker = picker.kind === 'playlist' ? PlaylistPicker : CollectionPicker;
  return <>
    {!succeeded && <Picker open onOpenChange={open => { if (!open) close(); }} anchorRef={source.anchor} returnFocusRef={source.anchor}
      selection={selection} entries={picker.rows.map(row => ({id: row.id, name: row.name, size: row.count, canAdd: row.canAdd, alreadyIn: row.alreadyIn, artwork: mosaic(row.artwork)}))}
      query={picker.query} onQueryChange={query => run(() => controller.pickerSearch(query))}
      hasNext={picker.next} hasPrevious={picker.previous}
      onNext={picker.next && picker.phase === 'ready' ? () => run(() => controller.pickerNext()) : undefined}
      onPrevious={picker.previous && picker.phase === 'ready' ? () => run(() => controller.pickerPrevious()) : undefined}
      loading={loading} busy={busy} mode={mode} onModeChange={setMode} name={name} onNameChange={setName}
      onAdd={id => run(() => controller.chooseDestination(id))} onCreateAndAdd={value => run(() => controller.createDestination(value))}
      onRetry={ids => run(() => retryPickerWindow(controller, ids))}
      onRetryOperation={picker.phase === 'uncertain' ? () => run(() => retryPickerOperation(controller)) : undefined}
      onRetryError={picker.phase === 'error' ? () => run(() => retryPickerError(controller)) : undefined}
      result={result} error={picker.error ? reason(picker.error.code) : undefined} />}
    {result && <PickerAddedToast open={succeeded} onOpenChange={open => { if (!open && succeeded) { controller.closePicker(); controller.clear(); } }}
      selection={{count: summary.count}} destination={picker.kind} result={result}
      bottomInset={width >= 1024 ? source.dockVisible() ? 108 : 24 : source.dockVisible() ? 150 : 80}
      onView={id => run(() => { if (id !== controller.getSnapshot().picker?.destination?.id) throw new Error('stale-picker-destination'); return controller.viewDestination(); })} />}
  </>;
}
