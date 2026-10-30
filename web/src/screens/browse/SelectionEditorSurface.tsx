import {Artwork} from '../../kit/artwork';
import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {BulkMetadataPreview} from '@core/home-selection/index.ts';
import type {BulkReceipt} from '@core/metadata-bulk.ts';
import {selectionKindHasBulkFields} from '@core/selection-model/index.ts';
import type {BrowseSelectionContext, SelectionOperation, SelectionKind} from '@core/selection-model/index.ts';
import {BulkEditor, MixedEditExplainer, type BulkEditorDraft, type SelectionItemDisplay} from '../../kit/selection';
import {Dialog} from '../../kit/overlays';
import {Button} from '../../kit/controls';
import {OwnerTag} from '../../kit/shell';
import {editorResultView, editorUiDraft, editorValues} from '../../shell/selection-presentation';
import {createSelectionEditor, type SelectionEditorOptions} from './selection-editor';

export type LibraryEditorSession = Readonly<{operation: SelectionOperation<BrowseSelectionContext>; anchor: HTMLElement; lifetime: AbortController; current: () => boolean}>;
export function SelectionEditorSurface({session, options, onKeepOnly, onClose}: {session: LibraryEditorSession; options: SelectionEditorOptions; onKeepOnly: (kind: SelectionKind) => void; onClose: () => void}) {
  const anchor = useRef<HTMLElement | null>(session.anchor);
  const producer = useMemo(() => createSelectionEditor({...options, signal: session.lifetime.signal, isCurrent: session.current}), [options, session]);
  const [preview, setPreview] = useState<BulkMetadataPreview>(), [targets, setTargets] = useState<readonly SelectionItemDisplay[]>([]);
  const [draft, setDraft] = useState<BulkEditorDraft>(() => editorUiDraft({lockEdited: true}));
  const [receipts, setReceipts] = useState<readonly BulkReceipt[]>([]), [busy, setBusy] = useState(true), [error, setError] = useState(false);
  const types = session.operation.target.mode === 'explicit' ? [...session.operation.target.items.reduce((counts, pick) => counts.set(pick.item.kind, (counts.get(pick.item.kind) ?? 0) + 1), new Map<SelectionKind, number>())].map(([kind, count]) => ({kind, count})) : session.operation.target.summary.types;
  const mixed = types.length > 1, noBulkKind = types.length === 1 && !selectionKindHasBulkFields(types[0].kind);
  const flight = useRef(false), operation = useRef(session.operation);
  const load = useCallback(async () => {
    if (mixed || noBulkKind || flight.current || !session.current()) return;
    flight.current = true; setBusy(true); setError(false);
    try {
      const next = await producer.preview(operation.current), display = await producer.display(operation.current);
      if (session.current()) {
        const aspect = next.contentKind === 'episode' ? 16 / 9 : ['song', 'album', 'artist'].includes(next.contentKind) ? 1 : 2 / 3;
        setTargets(display.map(target => ({id: target.id, title: target.title,
          ...(options.artwork ? {artwork: <Artwork source={options.artwork.foreground(target.posterUrl, 120, aspect)} alt=""/>} : {})})));
        setPreview(next);
      }
    } catch {if (session.current()) setError(true);}
    finally {flight.current = false; if (session.current()) setBusy(false);}
  }, [mixed, noBulkKind, producer, session, options.artwork]);
  useEffect(() => {void load(); return () => session.lifetime.abort();}, [load, session]);
  const close = () => {session.lifetime.abort(); onClose();};
  const save = async (value: BulkEditorDraft) => {
    if (!preview || flight.current || !session.current()) return;
    flight.current = true; setBusy(true); setError(false);
    try {const result = await producer.save(operation.current, preview, value, session.lifetime.signal); if (session.current()) setReceipts(result);}
    catch {if (session.current()) setError(true);}
    finally {flight.current = false; if (session.current()) setBusy(false);}
  };
  if (mixed || noBulkKind) return <MixedEditExplainer open reason={noBulkKind ? 'no-bulk-fields' : 'mixed-types'} onOpenChange={open => {if (!open) close();}} anchorRef={anchor} returnFocusRef={anchor}
    types={types} onKeepOnly={kind => {if (session.current()) onKeepOnly(kind);}}/>;
  if (!preview) return <Dialog open onOpenChange={open => {if (!open) close();}} title={defaultI18n.t('next.selection.edit')} returnFocusRef={anchor}>
    <p role={error ? 'alert' : 'status'}>{defaultI18n.t(error ? 'next.states.crashed' : 'next.states.loading')}</p>
    {error && <Button onClick={() => {void load();}}>{defaultI18n.t('next.states.tryAgain')}</Button>}
  </Dialog>;
  if (!preview.schema.some(field => field.bulk)) return <MixedEditExplainer open reason="no-bulk-fields" onOpenChange={open => {if (!open) close();}}
    anchorRef={anchor} returnFocusRef={anchor} types={types} onKeepOnly={() => {}}/>;
  const retry = async (ids: readonly string[]) => {
    if (flight.current || !session.current() || operation.current.target.mode !== 'explicit') return;
    const failed = new Set(receipts.flatMap(receipt => receipt.results.filter(row => !row.ok).map(row => row.id)));
    if (!ids.length || new Set(ids).size !== ids.length || ids.some(id => !failed.has(id))) return;
    const target = operation.current.target;
    const items = target.items.filter(pick => 'id' in pick.item && ids.includes(pick.item.id));
    const displayItems = target.displayItems.filter(pick => items.some(value => value.key === pick.key));
    if (items.length !== ids.length) return;
    operation.current = Object.freeze({target: Object.freeze({...target, items, displayItems})});
    setPreview(undefined); setReceipts([]); await load();
  };
  const target = operation.current.target;
  const kind = target.mode === 'explicit' ? target.items[0]?.item.kind : undefined;
  if (!kind) return null;
  return <BulkEditor open onOpenChange={open => {if (!open && !busy) close();}} returnFocusRef={anchor}
    selection={{count: preview.total, kind}} schema={preview.schema} singleItemFields={preview.singleItemFields} values={editorValues(preview)}
    draft={draft} onDraftChange={value => {if (!busy && !error && !receipts.length) setDraft(value);}} onSave={value => {void save(value);}}
    onRetry={ids => {void retry(ids);}} targets={targets} result={editorResultView(receipts)} busy={busy}
    error={error ? defaultI18n.t('next.selection.editorUncertain') : undefined} ownerTag={<OwnerTag/>}/>;
}
