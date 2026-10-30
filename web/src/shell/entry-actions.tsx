import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../kit/controls';
import {Dialog} from '../kit/overlays';
import {CollectionPicker, DeleteMediaDialog, PlaylistPicker, type PickerEntry} from '../kit/selection';
import {entryActions, type EntryActionPorts, type EntryActionTarget, type EntryDestination} from './entry-actions';
import {deleteSurfaceFor, EntryDeleteProducer} from './entry-delete';
import {EntryPickerProducer} from './entry-picker';
import type {HomeSelectionPicker} from '@core/home-selection/types.ts';
import {pickerResultView} from './selection-presentation';

const t = defaultI18n.t;
function errorText(code: string | undefined): string | undefined {
  if (!code) return undefined;
  return t(code === 'delete_not_ready' ? 'next.selection.deleteNotReady'
    : code === 'delete_uncertain' ? 'next.selection.deleteUncertain'
      : 'next.selection.deleteFailed');
}

function EntryPickerSurface({destination, target, ports, anchor, canCreate, onClose}: {
  destination: EntryDestination; target: EntryActionTarget; ports: EntryActionPorts; anchor: HTMLElement | null; canCreate: boolean; onClose: () => void;
}) {
  const producer = useRef<EntryPickerProducer | null>(null), anchorRef = useRef<HTMLElement | null>(anchor);
  const [picker, setPicker] = useState<HomeSelectionPicker>(), [error, setError] = useState<string>();
  const [mode, setMode] = useState<'choose' | 'new'>('choose'), [name, setName] = useState('');
  useEffect(() => {
    setPicker(undefined); setError(undefined); setMode('choose'); setName('');
    const owned = new EntryPickerProducer(target, destination, ports, canCreate, value => {if (producer.current === owned) setPicker(value);});
    producer.current = owned;
    void owned.open().catch(() => {if (producer.current === owned && ports.isCurrent()) setError(t('next.selection.pickerFailed'));});
    return () => {owned.close(); if (producer.current === owned) producer.current = null;};
  }, [target, destination, ports, canCreate]);
  const close = () => {producer.current?.close(); producer.current = null; onClose();};
  const run = (work: (value: EntryPickerProducer) => Promise<void>) => {
    const owned = producer.current; if (!owned || !ports.isCurrent()) return;
    setError(undefined);
    void work(owned).catch(() => {if (producer.current === owned && ports.isCurrent()) setError(t('next.selection.pickerFailed'));});
  };
  const entries: readonly PickerEntry[] = picker?.rows.map(row => ({id: row.id, name: row.name, size: row.count, canAdd: row.canAdd, alreadyIn: row.alreadyIn})) ?? [];
  const result = picker && pickerResultView(picker, () => t('next.selection.pickerFailed'));
  const Picker = destination === 'playlist' ? PlaylistPicker : CollectionPicker;
  return <Picker open onOpenChange={value => {if (!value) close();}} selection={{count: 1}} anchorRef={anchorRef} returnFocusRef={anchorRef}
    entries={entries} query={picker?.query ?? ''} onQueryChange={value => run(owned => owned.search(value))}
    loading={!picker || picker.phase === 'loading'} busy={picker?.phase === 'adding'}
    hasNext={picker?.next} hasPrevious={picker?.previous} onNext={picker?.next ? () => run(owned => owned.page()) : undefined}
    onPrevious={picker?.previous ? () => run(owned => owned.page(true)) : undefined}
    mode={mode} onModeChange={next => {if (next === 'new' && destination === 'collection' && !canCreate) {setError(t('next.selection.collectionOwnerOnly')); return;} setMode(next);}}
    name={name} onNameChange={setName} onAdd={id => run(owned => owned.choose(id))} onCreateAndAdd={value => run(owned => owned.create(value))}
    onRetry={ids => {const failures = picker?.receipt?.failures.map(row => row.itemId) ?? []; if (picker?.phase !== 'partial' || ids.length !== failures.length || ids.some((id, index) => id !== failures[index])) return; run(owned => owned.retry());}}
    onRetryOperation={picker?.phase === 'uncertain' ? () => run(owned => owned.retry()) : undefined}
    onRetryError={picker?.phase === 'error' || error ? () => run(owned => owned.retry()) : undefined}
    result={result} error={error ?? (picker?.error ? t('next.selection.pickerFailed') : undefined)} />;
}

/** Mounted once by the shell. It renders nothing until a screen effect opens a surface. */
export function EntryActionHost() {
  const open = useSyncExternalStore(entryActions.subscribe, entryActions.getSnapshot, () => null);
  const producer = useRef(new EntryDeleteProducer());
  const state = useSyncExternalStore(producer.current.subscribe, producer.current.getSnapshot, producer.current.getSnapshot);
  const anchor = useRef<HTMLElement | null>(null);
  const stale = !!open && !open.ports.isCurrent();
  useEffect(() => {
    if (stale) { entryActions.close(); return; }
    const current = producer.current;
    if (!open || open.kind !== 'delete') { current.close(); return; }
    anchor.current = open.anchor;
    current.open(open.target, open.ports);
    return () => current.close();
  }, [open, stale]);
  if (!open || stale) return null;
  if (open.kind === 'picker') return <EntryPickerSurface destination={open.destination} target={open.target} ports={open.ports} anchor={open.anchor} canCreate={open.canCreate} onClose={() => entryActions.close()} />;
  const deleteNow = () => { void (state.phase === 'uncertain' ? producer.current.retry() : producer.current.confirm()); };
  const surface = deleteSurfaceFor(state.phase);
  if (surface === 'uncertain') {
    return <Dialog open onOpenChange={value => { if (!value) entryActions.close(); }} title={t('next.selection.delete')}
      returnFocusRef={anchor} footer={<Button variant="primary" onClick={deleteNow}>{t('next.states.tryAgain')}</Button>}>
      <p role="alert">{errorText(state.errorCode)}</p>
    </Dialog>;
  }
  return <DeleteMediaDialog owner open onOpenChange={value => { if (!value && state.phase !== 'deleting' && state.phase !== 'confirming') entryActions.close(); }}
    returnFocusRef={anchor} selectionCount={1} preview={state.preview ?? null}
    confirmation={state.confirmation} onConfirmationChange={value => producer.current.setConfirmation(value)}
    removeFiles={state.removeFiles} onRemoveFilesChange={value => producer.current.setRemoveFiles(value)}
    onDelete={deleteNow} busy={state.phase === 'loading' || state.phase === 'confirming' || state.phase === 'deleting'}
    error={errorText(state.errorCode)} result={state.result}
    ownerTag={<span>{t('next.selection.owner')}</span>} />;
}
