import {useEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {ReadingCollection} from '@core/reading-collection.ts';
import {presentError} from '@core/presentation/errors.ts';
import {Button} from '../../kit/controls';
import {CoverMosaic} from '../../kit/artwork';
import {CollectionPicker, type PickerResult} from '../../kit/selection';
import {screenLifetime, type ScreenContext} from '../detail/context';
import type {ReadingLibraryItem} from './library-collection';
import {readingSelectionAuthority} from './reading-selection-authority';
import styles from './reading-selection.module.css';

const {t} = defaultI18n;
type Props = Readonly<{context: ScreenContext; selection: ReadonlyMap<string, ReadingLibraryItem>; kind: 'ebook' | 'comic';
  current: (item: ReadingLibraryItem) => boolean; onEdit: (anchor: HTMLElement) => void; onExit: () => void}>;

/** Reading's drawn Collection action stays visible beside Edit details (desktop-read 2b,
 * phone-read 2). It captures this exact selection, never a successor list or query. */
export function ReadSelectionBar(props: Props) {
  const latest = useRef(props); latest.current = props;
  const [opening, setOpening] = useState<Readonly<{controller: ReadingCollection; close: () => void}>>();
  const active = useRef(opening); active.current = opening;
  const anchor = useRef<HTMLElement | null>(null);
  useEffect(() => () => active.current?.close(), []);
  useEffect(() => {const previous = active.current; if (previous) {previous.close(); active.current = undefined; setOpening(undefined);}}, [props.selection, props.context]);
  const authority = readingSelectionAuthority(props.context.identity?.viewer);
  const close = () => {active.current?.close(); active.current = undefined; setOpening(undefined);};
  const open = (element: HTMLElement) => {
    if (latest.current.selection !== props.selection || latest.current.context !== props.context || active.current || !props.selection.size || !props.context.isCurrent() || ![...props.selection.values()].every(props.current)) return;
    const captured = props, items = [...captured.selection.values()], life = screenLifetime(captured.context);
    const current = () => life.current() && latest.current.selection === captured.selection && latest.current.context === captured.context
      && items.every(item => latest.current.current(item));
    const controller = new ReadingCollection({api: life.api, scope: captured.context.scope, requestId: async () => crypto.randomUUID(),
      signal: life.signal(), isCurrent: current, canCreate: authority.create}, items.map(item => ({id: item.id, libraryId: item.libraryId, title: item.title, kind: captured.kind})));
    anchor.current = element;
    const held = {controller, close: () => {life.dispose(); controller.dispose();}};
    active.current = held; setOpening(held);
    void controller.load().catch(error => {if (current()) captured.context.effects.reportError(error);});
  };
  const disabled = !props.selection.size || !!opening;
  return <>
    <div className={styles.bar} role="toolbar" aria-label={t('next.selection.selection')} onKeyDown={event => {if (event.key === 'Escape') {event.preventDefault(); close(); props.onExit();}}}>
      <Button variant="text" className={styles.cancel} onClick={() => {close(); props.onExit();}}>{t('next.selection.cancel')}</Button>
      <b>{t('next.selection.selected', {count: props.selection.size})}<small>{t('next.reader.bulkLimit', {count: 200})}</small></b>
      <div className={styles.actions}>
        {authority.edit && <Button variant="text" icon="i-edit" className={styles.edit} disabled={disabled} onClick={event => {if (latest.current.selection === props.selection && props.context.isCurrent() && [...props.selection.values()].every(props.current)) props.onEdit(event.currentTarget);}}>{t('next.reader.editDetails')}</Button>}
        <Button variant="text" icon="i-layers" disabled={disabled} aria-haspopup="dialog" onClick={event => open(event.currentTarget)}>
          <span className={styles.desktop}>{t('next.reader.addToCollection')}</span><span className={styles.phone}>{t('next.home.collection')}</span>
        </Button>
      </div>
      <Button variant="text" className={styles.done} onClick={() => {close(); props.onExit();}}>{t('next.selection.done')}</Button>
    </div>
    {opening && <ReadCollectionPicker controller={opening.controller} context={props.context} anchor={anchor} onClose={close}/>}
  </>;
}

export function ReadCollectionPicker({controller, context, anchor, onClose}: {controller: ReadingCollection; context: ScreenContext; anchor: RefObject<HTMLElement | null>; onClose: () => void}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), [mode, setMode] = useState<'choose' | 'new'>('choose'), [name, setName] = useState('');
  const run = (work: () => Promise<void>) => {void work().catch(error => {if (context.isCurrent()) context.effects.reportError(error);});};
  const receipt = state.receipt, destination = state.destination;
  const result: PickerResult | undefined = receipt && destination ? {destination: {id: destination.id, name: destination.name, size: destination.entryCount},
    inserted: receipt.added, processed: receipt.added + receipt.alreadyPresent + receipt.failed, alreadyIn: receipt.alreadyPresent,
    failed: receipt.failed, unknown: receipt.unknown, pending: receipt.pending, total: controller.items.length, complete: receipt.complete,
    failures: receipt.failures.map(row => ({id: row.itemId, title: controller.items.find(item => item.id === row.itemId)!.title,
      reason: presentError({code: row.code}, 'detail', {operation: 'action'}).body}))} : undefined;
  const artwork = (rows: readonly {posterUrl?: string; backdropUrl?: string}[]) => {
    const covers = rows.flatMap(row => {const art = context.artwork.foreground(row.posterUrl ?? row.backdropUrl, 80, 1); return art ? [art] : [];});
    return covers.length ? <CoverMosaic alt="" covers={covers}/> : undefined;
  };
  return <CollectionPicker open onOpenChange={open => {if (!open) onClose();}} selection={{count: controller.items.length}} anchorRef={anchor} returnFocusRef={anchor}
    entries={state.rows.map(row => ({id: row.id, name: row.name, size: row.count, canAdd: row.canAdd, alreadyIn: row.alreadyIn, artwork: artwork(row.artwork)}))}
    query={state.query} onQueryChange={query => run(() => controller.search(query))} hasNext={state.next} hasPrevious={state.previous}
    onNext={() => run(() => controller.page(state))} onPrevious={() => run(() => controller.page(state, true))}
    mode={mode} onModeChange={setMode} name={name} onNameChange={setName} onCreateAndAdd={value => run(() => controller.create(value))}
    onAdd={id => run(() => controller.choose(state, id))} onRetry={() => run(controller.retry)}
    onRetryOperation={state.phase === 'uncertain' ? () => run(controller.retry) : undefined}
    onRetryError={state.phase === 'error' ? () => run(controller.retry) : undefined}
    loading={state.phase === 'idle' || state.phase === 'loading'} busy={state.phase === 'adding'} result={result}
    error={state.error ? t('next.selection.pickerFailed') : undefined}/>;
}
