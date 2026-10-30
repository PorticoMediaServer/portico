import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {BulkMetadataPreview} from '@core/home-selection/index.ts';
import type {HomeSelectionProgress} from '@core/home-selection/types.ts';
import {BulkEditor, MixedEditExplainer, type BulkEditorDraft} from '../../kit/selection';
import {Dialog} from '../../kit/overlays';
import {Button} from '../../kit/controls';
import {OwnerTag} from '../../kit/shell';
import {editorUiDraft, editorValues} from '../../shell/selection-presentation';
import type {LibraryEditorSession} from './SelectionEditorSurface';
import type {SelectionEditorOptions} from './selection-editor';
import {createQuerySelectionEditor, type QueryEditorResult} from './selection-query-editor';
const t = defaultI18n.t;
export function QuerySelectionEditorSurface({session, options, onClose}: {session: LibraryEditorSession; options: SelectionEditorOptions; onClose(): void}) {
  const anchor = useRef<HTMLElement | null>(session.anchor), flight = useRef(false);
  const producer = useMemo(() => createQuerySelectionEditor({...options, signal: session.lifetime.signal, isCurrent: session.current}), [session, options]);
  const [preview, setPreview] = useState<BulkMetadataPreview>(), [draft, setDraft] = useState<BulkEditorDraft>(() => editorUiDraft({lockEdited: true}));
  const [busy, setBusy] = useState(true), [error, setError] = useState(false), [progress, setProgress] = useState<HomeSelectionProgress>();
  const [result, setResult] = useState<QueryEditorResult>();
  const load = useCallback(async () => {
    if (flight.current || !session.current()) return;
    flight.current = true; setBusy(true); setError(false);
    try {const value = await producer.preview(session.operation); if (session.current()) setPreview(value);}
    catch {if (session.current()) setError(true);}
    finally {flight.current = false; if (session.current()) setBusy(false);}
  }, [producer, session]);
  useEffect(() => {void load(); return () => session.lifetime.abort();}, [load, session]);
  const close = () => {session.lifetime.abort(); onClose();};
  const save = async (value: BulkEditorDraft) => {
    if (!preview || flight.current || !session.current()) return;
    flight.current = true; setBusy(true); setError(false);
    try {const valueResult = await producer.save(session.operation, preview, value, valueProgress => {if (session.current()) setProgress(valueProgress);}); if (session.current()) setResult(valueResult);}
    catch {if (session.current()) setError(true);}
    finally {flight.current = false; if (session.current()) setBusy(false);}
  };
  const target = session.operation.target;
  if (target.mode !== 'query' || target.summary.types.length !== 1) return null;
  if (result) return <Dialog open onOpenChange={open => {if (!open) close();}} title={t('next.selection.edit')} returnFocusRef={anchor}
    footer={<Button onClick={close}>{t('next.selection.done')}</Button>}>
    <p role="status">{t('next.selection.bulkResult', {updated: result.updated, failed: result.failed})}</p>
  </Dialog>;
  if (!preview) return <Dialog open onOpenChange={open => {if (!open) close();}} title={t('next.selection.edit')} returnFocusRef={anchor}>
    <p role={error ? 'alert' : 'status'}>{t(error ? 'next.states.crashed' : 'next.states.loading')}</p>
    {error && <Button onClick={() => {void load();}}>{t('next.states.tryAgain')}</Button>}
  </Dialog>;
  if (!preview.schema.some(field => field.bulk)) return <MixedEditExplainer open reason="no-bulk-fields" types={target.summary.types}
    anchorRef={anchor} returnFocusRef={anchor} onOpenChange={open => {if (!open) close();}} onKeepOnly={close}/>;
  return <BulkEditor open onOpenChange={open => {if (!open && !busy) close();}} returnFocusRef={anchor}
    selection={{count: preview.total, kind: target.summary.types[0].kind}} schema={preview.schema} singleItemFields={preview.singleItemFields}
    values={editorValues(preview)} draft={draft} onDraftChange={value => {if (!busy && !error) setDraft(value);}}
    onSave={value => {void save(value);}} onRetry={() => {void save(draft);}} targets={[]} busy={busy} ownerTag={<OwnerTag/>}
    description={progress ? t(progress.totalKnown ? 'next.selection.jobProgress' : 'next.selection.jobPreparing', {done: progress.done, total: progress.total}) : undefined}
    error={error ? t('next.selection.editorUncertain') : undefined}/>;
}
