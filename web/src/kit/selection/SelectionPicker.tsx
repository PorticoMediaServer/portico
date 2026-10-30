import {useEffect, useRef, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Button, TextField} from '../controls';
import {BottomSheet, Popover, ToastHost, type OverlayChange, type ToastData} from '../overlays';
import {focusable, usePhone} from '../overlays/shared';
import {Icon} from '../Icon';
import {selectionPickerTitleParts, selectionText as text} from './copy';
import {missingPickerIds, pickerResultSucceeded, retryLabel, selectionCountText} from './presentation';
import type {PickerEntry, PickerResult, SelectionCount, SelectionPickerProps} from './types';
import styles from './selection.module.css';

type Destination = 'playlist' | 'collection';
type PickerRowDisplay = PickerResult['destination'] & Partial<Pick<PickerEntry, 'size' | 'alreadyIn'>>;
function PickerRow({entry, onSelect, busy, end}: {entry: PickerRowDisplay; onSelect?: () => void; busy: boolean; end?: ReactNode}) {
  const content = <><span className={styles.pickerArtwork}>{entry.artwork}</span><span className={styles.pickerText}><b>{entry.name}</b>{entry.size !== undefined && <small>{text('next.selection.titles', {count: entry.size})}</small>}</span>
    {end ?? (entry.alreadyIn !== undefined && entry.alreadyIn > 0 ? <span className={styles.pickerEnd}>{text('next.selection.alreadyIn', {count: entry.alreadyIn})}</span> : undefined)}</>;
  return onSelect ? <Button variant="text" size="small" className={styles.pickerRow} disabled={busy} onClick={onSelect}>{content}</Button> : <div className={styles.pickerRow}><span>{content}</span></div>;
}

/** Every action delegates to the supplied operation; entries and outcomes are bounded real data. */
function SelectionPicker({destination, ...props}: SelectionPickerProps & {destination: Destination}) {
  const {selection, entries, query, onQueryChange, hasNext, hasPrevious, onNext, onPrevious, loading = false, mode, onModeChange, name, onNameChange, onAdd, onCreateAndAdd, onRetry, onRetryOperation, onRetryError, result, busy = false, error, anchorRef, ...surface} = props;
  const phone = usePhone(), nameRef = useRef<HTMLInputElement | null>(null), retryRef = useRef<HTMLButtonElement | null>(null), listRef = useRef<HTMLDivElement | null>(null);
  const retryRecovery = onRetryError ?? onRetryOperation, recovering = !!retryRecovery;
  const windowKey = JSON.stringify([query, entries.map(entry => entry.id)]);
  const page = useRef<{direction: 'next' | 'previous'; key: string; focus: boolean; loading: boolean} | null>(null);
  const scrollTop = useRef(0), lastQuery = useRef(query);
  useEffect(() => {
    const list = listRef.current, pending = page.current;
    if (pending && loading) pending.loading = true;
    if (list && pending && !loading && (pending.key !== windowKey || pending.loading)) {
      list.scrollTop = pending.direction === 'previous' ? list.scrollHeight : 0;
      scrollTop.current = list.scrollTop;
      if (pending.focus) { const rows = focusable(list); (pending.direction === 'previous' ? rows.at(-1) : rows[0])?.focus({preventScroll: true}); }
      page.current = null;
    }
    if (lastQuery.current !== query) { if (list) list.scrollTop = 0; scrollTop.current = 0; page.current = null; lastQuery.current = query; }
    if (error) page.current = null;
  }, [windowKey, loading, query, error]);
  const turnPage = (direction: 'next' | 'previous') => {
    const callback = direction === 'next' ? hasNext && onNext : hasPrevious && onPrevious;
    if (!callback || loading || busy || page.current) return false;
    page.current = {direction, key: windowKey, focus: !!listRef.current?.contains(document.activeElement), loading: false};
    callback(); return true;
  };
  const newName = text(destination === 'playlist' ? 'next.selection.newPlaylistName' : 'next.selection.newCollectionName');
  const titleKey = mode === 'new' ? 'next.selection.newPickerTitle' : 'next.selection.pickerTitle';
  const titleValues = {items: selectionCountText(selection), destination: text(destination === 'playlist' ? 'next.selection.playlistWord' : 'next.selection.collectionWord')};
  const title = text(titleKey, titleValues);
  const titleParts = selectionPickerTitleParts(titleKey, titleValues);
  const failures = result?.failures ?? [];
  const retryFailed = !!onRetry && failures.length > 0 && !!result?.complete && !recovering && !loading;
  const retryAvailable = recovering || retryFailed;
  useEffect(() => {if (surface.open && retryAvailable) retryRef.current?.focus({preventScroll: true});}, [surface.open, retryAvailable]);
  const resultSummary = result?.inserted === undefined ? undefined : result.total === undefined ? text('next.selection.added', {count: result.inserted}) : text('next.selection.addedPartial', {added: result.inserted, total: result.total, name: result.destination.name});
  const content = <div className={styles.pickerContent} aria-busy={busy || loading || result && !result.complete || undefined}>
    {!phone && <div className={styles.pickerHeading}>{titleParts.map((part, index) => part.argument === 'items' ? <b key={index}>{part.text}</b> : part.text)}</div>}
    {result ? <>
      {(resultSummary || failures.length > 0 || result.jobFacts?.length || !result.complete && result.processed !== undefined && result.total !== undefined) && <div className={styles.pickerFailure} role="status">
        {resultSummary && <b><Icon name={failures.length || recovering ? 'i-warn' : !loading && pickerResultSucceeded(result) ? 'i-check' : 'i-info'} size={18} />{resultSummary}</b>}
        {!result.complete && result.processed !== undefined && result.total !== undefined && <p>{text('next.selection.countOf', {count: result.processed, total: result.total})}</p>}
        {result.jobFacts?.map(fact => <p key={fact.id}>{fact.text}</p>)}
        {failures.map(failure => <p key={failure.id}>{text('next.selection.couldNotAdd', {title: failure.title, reason: failure.reason})}</p>)}
      </div>}
      <PickerRow entry={{...result.destination, alreadyIn: result.alreadyIn}} busy={busy} end={result.inserted === undefined ? undefined : <span className={styles.pickerEnd}>{text('next.selection.added', {count: result.inserted})}</span>} />
      <div className={styles.pickerButtons}><Button size="small" disabled={busy} onClick={() => surface.onOpenChange(false, 'action')}>{text('next.selection.done')}</Button>{retryFailed && <Button ref={retryRef} size="small" variant="primary" loading={busy} onClick={() => {if (!busy && !loading) onRetry?.(missingPickerIds(failures));}}>{retryLabel(failures)}</Button>}{retryRecovery && <Button ref={retryRef} size="small" variant="primary" loading={busy} onClick={() => {if (!busy) retryRecovery();}}>{text('next.selection.retry')}</Button>}</div>
    </> : retryRecovery ? <div className={styles.pickerButtons}><Button size="small" disabled={busy} onClick={() => surface.onOpenChange(false, 'action')}>{text('next.selection.done')}</Button><Button ref={retryRef} size="small" variant="primary" loading={busy} onClick={() => {if (!busy) retryRecovery();}}>{text('next.selection.retry')}</Button></div> : mode === 'new' ? <form className={styles.pickerNew} onSubmit={event => { event.preventDefault(); if (!busy && !loading) onCreateAndAdd(name.trim() || newName); }}>
      <TextField ref={nameRef} autoFocus label={text('next.selection.name')} aria-label={text('next.selection.name')} value={name} onChange={event => onNameChange(event.currentTarget.value)} placeholder={newName} disabled={busy} />
      {/* select.html 19: the new-name sheet carries one button; dismissing it is the cancel. */}
      <div className={styles.pickerButtons}><Button size="small" type="submit" variant="primary" loading={busy} disabled={loading}>{text('next.selection.createAndAdd', {count: selection.count})}</Button></div>
    </form> : <>
      {onQueryChange && <TextField variant="search" leadingIcon="i-search" aria-label={text(destination === 'playlist' ? 'next.selection.findPlaylist' : 'next.selection.findCollection')} placeholder={text(destination === 'playlist' ? 'next.selection.findPlaylist' : 'next.selection.findCollection')} value={query ?? ''} onChange={event => onQueryChange(event.currentTarget.value)} disabled={busy} containerClassName={styles.pickerSearch} />}
      <Button variant="text" size="small" className={styles.pickerRow} disabled={busy || loading} onClick={() => {if (!busy && !loading) onModeChange('new');}}><span className={styles.newPickerArtwork}><Icon name="i-plus" size={20} /></span><span className={styles.pickerText}><b>{text(destination === 'playlist' ? 'next.selection.newPlaylist' : 'next.selection.newCollection')}</b></span></Button>
      <div className={styles.pickerSeparator} />
      <div className={styles.pickerEntries} ref={listRef} aria-busy={loading || undefined}
        onWheel={event => {const list = event.currentTarget; if (event.deltaY > 0 && list.scrollTop + list.clientHeight >= list.scrollHeight - 1) turnPage('next'); else if (event.deltaY < 0 && list.scrollTop <= 1) turnPage('previous');}}
        onScroll={event => {const list = event.currentTarget, previous = scrollTop.current; scrollTop.current = list.scrollTop; if (list.scrollTop > previous && list.scrollTop + list.clientHeight >= list.scrollHeight - 1) turnPage('next'); else if (list.scrollTop < previous && list.scrollTop <= 1) turnPage('previous');}}
        onKeyDown={event => {const rows = focusable(event.currentTarget); if ((event.key === 'PageDown' && event.currentTarget.scrollTop + event.currentTarget.clientHeight >= event.currentTarget.scrollHeight - 1 || event.key === 'ArrowDown' && document.activeElement === rows.at(-1)) && turnPage('next')) event.preventDefault(); else if ((event.key === 'PageUp' && event.currentTarget.scrollTop <= 1 || event.key === 'ArrowUp' && document.activeElement === rows[0]) && turnPage('previous')) event.preventDefault();}}>
        {entries.map(entry => <PickerRow key={entry.id} entry={entry} busy={busy || loading || !entry.canAdd} onSelect={() => {if (entry.canAdd && !busy && !loading) onAdd(entry.id);}} />)}
      </div>
    </>}
    {error && <div className={styles.pickerFailure} role="alert"><Icon name="i-warn" size={18} /><p>{error}</p></div>}
  </div>;
  const initialFocusRef = retryAvailable ? retryRef : mode === 'new' ? nameRef : surface.initialFocusRef;
  const shared = {...surface, onOpenChange: (next: boolean, reason: Parameters<OverlayChange>[1]) => {if (next || !busy) surface.onOpenChange(next, reason);}, returnFocusRef: surface.returnFocusRef ?? anchorRef, title: phone && mode === 'new' ? newName : title, initialFocusRef, className: styles.picker, dismissOnOutsidePress: !busy, dismissOnBack: !busy, showCloseButton: !phone && !busy};
  // Frame 12's chooser is 360px; frame 13's new/result/collection states occupy 410px cells.
  const width = mode === 'new' || result || destination === 'collection' ? 410 : 360;
  return phone ? <BottomSheet {...shared} fitContent>{content}</BottomSheet> : <Popover {...shared} anchorRef={anchorRef} title={title} width={width} placement="bottom-start">{content}</Popover>;
}

export function PlaylistPicker(props: SelectionPickerProps) { return <SelectionPicker {...props} destination="playlist" />; }
export function CollectionPicker(props: SelectionPickerProps) { return <SelectionPicker {...props} destination="collection" />; }

export type PickerAddedToastProps = Readonly<{open: boolean; onOpenChange: OverlayChange; selection: SelectionCount; destination: Destination; result: PickerResult;
  /** Absent where the surface has no real destination page to open. */
  onView?: (id: string) => void; bottomInset?: number}>;
function PickerToastViewLabel({destination}: {destination: Destination}) {
  const phone = usePhone();
  return phone ? defaultI18n.t('action.view') : text(destination === 'playlist' ? 'next.selection.viewPlaylist' : 'next.selection.viewCollection');
}
/** Owns the supported host, exact result copy, positioning, lifetime and dismissal. */
export function PickerAddedToast({open, onOpenChange, selection, destination, result, onView, bottomInset}: PickerAddedToastProps) {
  const toast: ToastData | null = open && pickerResultSucceeded(result) ? {
    id: `picker-${destination}-${result.destination.id}`,
    message: <>{text('next.selection.addedToast', {items: selectionCountText({count: result.inserted!, kind: selection.kind}), name: result.destination.name})}{result.alreadyIn !== undefined && result.alreadyIn > 0 && <> {text('next.selection.duplicatesToast', {count: result.alreadyIn})}</>}</>,
    ...(onView ? {action: {variant: 'text' as const, label: <PickerToastViewLabel destination={destination} />, accessibilityLabel: text(destination === 'playlist' ? 'next.selection.viewPlaylist' : 'next.selection.viewCollection'), onAction: () => onView(result.destination.id)}} : {}),
  } : null;
  return <ToastHost toast={toast} bottomInset={bottomInset} className={styles.pickerToastHost} toastClassName={styles.pickerToast} onDismiss={(_id, reason) => onOpenChange(false, reason)} />;
}
