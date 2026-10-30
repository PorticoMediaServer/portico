import {resolveQueryDelete} from './selection-delete-query';
import {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {DeleteMediaDialog} from '../../kit/selection';
import type {EntryActionPorts, EntryActionTarget} from '../../shell/entry-actions';
import type {LibraryEditorSession} from './SelectionEditorSurface';
import {LibraryDeleteProducer} from './selection-delete';

export type LibraryDeleteOptions = EntryActionPorts & Readonly<{signal: AbortSignal}>;
const t = defaultI18n.t;
export function LibraryDeleteSurface({session, options, onClose}: {session: LibraryEditorSession; options: LibraryDeleteOptions; onClose(): void}) {
  const anchor = useRef<HTMLElement | null>(session.anchor);
  const owner = useMemo(() => ({session, producer: new LibraryDeleteProducer()}), [session]);
  const producer = owner.producer;
  const targetCount = session.operation.target.mode === 'explicit' ? session.operation.target.items.length : session.operation.target.total;
  const tooMany = targetCount > 200;
  const [resolutionError, setResolutionError] = useState(false);
  const state = useSyncExternalStore(producer.subscribe, producer.getSnapshot, producer.getSnapshot);
  useEffect(() => {
    const session = owner.session;
    const target = session.operation.target;
    if (tooMany || !session.current()) return;
    setResolutionError(false);
    const signal = AbortSignal.any([session.lifetime.signal, options.signal]);
    const check = () => {if (signal.aborted || !session.current() || !options.isCurrent()) throw new DOMException('Selection retired', 'AbortError');};
    const local = new AbortController(), combined = AbortSignal.any([signal, local.signal]);
    const ports: EntryActionPorts = {...options, isCurrent: () => !combined.aborted && session.current() && options.isCurrent(), api: {
      request: async <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => {
        check();combined.throwIfAborted();const value = await options.api.request<T>(path, method, body, caller ? AbortSignal.any([combined, caller]) : combined);check();combined.throwIfAborted();return value;
      },
    }};
    void (async () => {
      const targets: readonly Pick<EntryActionTarget, 'itemId' | 'libraryId' | 'kind'>[] = target.mode === 'query'
        ? await resolveQueryDelete(target, ports, combined)
        : target.items.map(pick => {
          if (!('libraryId' in pick.item) || !pick.item.capabilities.delete) throw new Error('delete-not-offered');
          return {itemId: pick.item.id, libraryId: pick.item.libraryId, kind: pick.item.kind};
        });
      check();combined.throwIfAborted();producer.open(targets, ports);
    })().catch(() => {if (!combined.aborted && session.current()) setResolutionError(true);});
    return () => {local.abort();producer.close();};
  }, [owner, producer, options, tooMany]);
  const close = () => {session.lifetime.abort(); producer.close(); onClose();};
  const run = () => {void (state.phase === 'uncertain' ? producer.retry() : producer.confirm());};
  if (state.phase === 'uncertain' || state.phase === 'error') return <Dialog open onOpenChange={open => {if (!open) close();}}
    title={t('next.selection.delete')} returnFocusRef={anchor}
    footer={<Button onClick={() => {if (state.phase === 'uncertain') run(); else {close();}}}>{t(state.phase === 'uncertain' ? 'next.states.tryAgain' : 'next.selection.done')}</Button>}>
    <p role="alert">{t(state.phase === 'uncertain' ? 'next.selection.deleteUncertain' : 'next.selection.deleteFailed')}</p>
  </Dialog>;
  return <DeleteMediaDialog owner open onOpenChange={open => {if (!open && state.phase !== 'deleting' && state.phase !== 'confirming') close();}}
    returnFocusRef={anchor} selectionCount={targetCount}
    preview={state.preview ?? null} confirmation={state.confirmation} onConfirmationChange={value => producer.setConfirmation(value)}
    removeFiles={state.removeFiles} onRemoveFilesChange={value => producer.setRemoveFiles(value)} onDelete={run}
    busy={!resolutionError && !tooMany && (state.phase === 'idle' || state.phase === 'loading' || state.phase === 'confirming' || state.phase === 'deleting')}
    error={resolutionError ? t('next.selection.deleteFailed') : state.errorCode ? t('next.selection.deleteNotReady') : undefined} result={state.result} ownerTag={<span>{t('next.selection.owner')}</span>}/>;
}
