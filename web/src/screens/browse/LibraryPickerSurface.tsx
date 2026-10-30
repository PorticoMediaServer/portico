import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {HomeSelectionPicker} from '@core/home-selection/types.ts';
import type {EntryActionPorts, EntryDestination} from '../../shell/entry-actions';
import {pickerResultView} from '../../shell/selection-presentation';
import type {ShellArtwork} from '../../shell/artwork';
import {CoverMosaic} from '../../kit/artwork';
import {CollectionPicker, PickerAddedToast, PlaylistPicker, pickerResultSucceeded, type PickerEntry} from '../../kit/selection';
import {LibraryPickerProducer, type LibraryPickerTarget} from './selection-picker';
export type LibraryPickerOptions = EntryActionPorts & Readonly<{signal: AbortSignal; canCreateCollection: boolean;
  /** The screen's own artwork, for the picker's 2 by 2 mosaics (select.html 18). */
  artwork?: ShellArtwork;
  /** The destination page this surface can really open; absent leaves the toast without View. */
  onView?: (destination: EntryDestination, id: string) => void}>;
const t = defaultI18n.t;
export function LibraryPickerSurface({destination, target, ports, artwork, onView, anchor, canCreate, onClose}: {
  destination: EntryDestination; target: LibraryPickerTarget; ports: EntryActionPorts; artwork?: ShellArtwork; onView?: (destination: EntryDestination, id: string) => void;
  anchor: HTMLElement | null; canCreate: boolean; onClose: () => void;
}) {
  const producer = useRef<LibraryPickerProducer | null>(null), anchorRef = useRef<HTMLElement | null>(anchor);
  const [picker, setPicker] = useState<HomeSelectionPicker>(), [error, setError] = useState<string>();
  const [mode, setMode] = useState<'choose' | 'new'>('choose'), [name, setName] = useState('');
  useEffect(() => {
    setPicker(undefined); setError(undefined); setMode('choose'); setName('');
    const owned = new LibraryPickerProducer(target, destination, ports, canCreate, value => {if (producer.current === owned) setPicker(value);});
    producer.current = owned;
    void owned.open().catch(() => {if (producer.current === owned && ports.isCurrent()) setError(t('next.selection.pickerFailed'));});
    return () => {owned.close(); if (producer.current === owned) producer.current = null;};
  }, [target, destination, ports, canCreate]);
  const close = () => {producer.current?.close(); producer.current = null; onClose();};
  const run = (work: (value: LibraryPickerProducer) => Promise<void>) => {
    const owned = producer.current; if (!owned || !ports.isCurrent()) return;
    setError(undefined);
    void work(owned).catch(() => {if (producer.current === owned && ports.isCurrent()) setError(t('next.selection.pickerFailed'));});
  };
  // select.html 18: each row and the result carry the destination's first four
  // covers as a 2 by 2 mosaic, from the screen's own artwork source only.
  const mosaic = (rows: HomeSelectionPicker['rows'][number]['artwork'] | undefined) => {
    const source = artwork; if (!source || !rows) return undefined;
    const covers = rows.flatMap((row, index) => {
      const art = source.foreground(row.posterUrl ?? row.backdropUrl, 40 * (globalThis.devicePixelRatio || 1), 1, {group: 'library-destination-picker', order: index, priority: 'high'});
      return art ? [art] : [];
    });
    return covers.length ? <CoverMosaic alt="" covers={covers} /> : undefined;
  };
  const entries: readonly PickerEntry[] = picker?.rows.map(row => ({id: row.id, name: row.name, size: row.count, canAdd: row.canAdd, alreadyIn: row.alreadyIn, artwork: mosaic(row.artwork)})) ?? [];
  const resultView = picker && pickerResultView(picker, () => t('next.selection.pickerFailed'));
  const result = resultView && picker ? {...resultView, destination: {...resultView.destination, artwork: mosaic(picker.destination?.artwork)}} : undefined;
  // select.html 18: a fully successful add closes the sheet, ends the selection and
  // leaves one toast with View; a partial result keeps the sheet open (frame 19).
  const succeeded = picker?.phase === 'added' && !!result && pickerResultSucceeded(result);
  const selectionCount = {count: 'mode' in target ? target.total : target.length};
  const Picker = destination === 'playlist' ? PlaylistPicker : CollectionPicker;
  return <><Picker open={!succeeded} onOpenChange={value => {if (!value) close();}} selection={selectionCount} anchorRef={anchorRef} returnFocusRef={anchorRef}
    entries={entries} query={picker?.query ?? ''} onQueryChange={value => run(owned => owned.search(value))}
    loading={!picker || picker.phase === 'loading'} busy={picker?.phase === 'adding'}
    hasNext={picker?.next} hasPrevious={picker?.previous} onNext={picker?.next ? () => run(owned => owned.page()) : undefined}
    onPrevious={picker?.previous ? () => run(owned => owned.page(true)) : undefined}
    mode={mode} onModeChange={next => {if (next === 'new' && destination === 'collection' && !canCreate) {setError(t('next.selection.collectionOwnerOnly')); return;} setMode(next);}}
    name={name} onNameChange={setName} onAdd={id => run(owned => owned.choose(id))} onCreateAndAdd={value => run(owned => owned.create(value))}
    onRetry={'mode' in target ? undefined : ids => {const failures = picker?.receipt?.failures.map(row => row.itemId) ?? []; if (picker?.phase !== 'partial' || ids.length !== failures.length || ids.some((id, index) => id !== failures[index])) return; run(owned => owned.retry());}}
    onRetryOperation={picker?.phase === 'uncertain' ? () => run(owned => owned.retry()) : undefined}
    onRetryError={picker?.phase === 'error' || error ? () => run(owned => owned.retry()) : undefined}
    result={result} error={error ?? (picker?.error ? t('next.selection.pickerFailed') : undefined)} />
    {result && <PickerAddedToast open={succeeded} onOpenChange={value => {if (!value && succeeded) close();}} selection={selectionCount} destination={destination} result={result}
      {...(onView ? {onView: (id: string) => onView(destination, id)} : {})} />}
  </>;
}

