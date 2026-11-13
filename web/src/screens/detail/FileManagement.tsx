import {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {useViewerQueries} from '../../platform/freshness';
import {deletionAllowed, type VersionDeletePreview} from '@core/administration.ts';
import type {DetailProjection} from '@core/detail.ts';
import type {ContentEntry} from '@core/library-content.ts';
import {canMergeTitles, mergeExplanation} from '@core/title-identity.ts';
import {versionModels} from '@core/presentation/kit-model';
import {Button, TextField, Segmented} from '../../kit/controls';
import {Banner, Dialog} from '../../kit/overlays';
import {ApiError} from '@core/http.ts';
import {TitleProse} from '../../kit/title/TitleParts';
import {createSearchBindings} from '../search/bindings';
import type {DetailBindings} from './bindings';
import type {FileInfoManageContext} from './TitleFileInfo';
import {t, environment} from './text';
import {ScreenFailure} from './Detail';
import styles from './detail.module.css';

type Props = {binding: DetailBindings; data: DetailProjection; manage: FileInfoManageContext; onClose: () => void; returnFocusRef: RefObject<HTMLButtonElement | null>};

/** Why the server refused a manage command, in one line: its own reason when it gave one, else what did not happen.
 * Every manage dialog draws it the same way, with Try again, and keeps the owner's choice in place. */
export function ManageRefusal({error, fallback, retry}: {error: unknown; fallback: Parameters<typeof t>[0]; retry: () => void}) {
  // A refusal without a reason carries the transport's generic "Request failed (status)." text, which says nothing.
  const reason = error instanceof ApiError && error.message && error.message !== `Request failed (${error.status}).` ? error.message : t(fallback);
  return <Banner icon="i-warn" action={{label: t('action.tryAgain'), onAction: retry}}>{reason}</Banner>;
}

/** Retained Split and Delete dialogs. The binding rechecks permission at each command. */
export function FileManagement(props: Props) {
  const action = props.manage.action.action;
  return action === 'delete-version' ? <DeleteVersion {...props} /> : action === 'split-versions' ? <SplitVersion {...props} /> : <MergeTitle {...props} />;
}

function DeleteVersion({binding, data, manage, onClose, returnFocusRef}: Props) {
  const file = manage.action.fileId ? manage.files.get(manage.action.fileId) : undefined;
  const [preview, setPreview] = useState<VersionDeletePreview>(), [typed, setTyped] = useState(''), [failed, setFailed] = useState(false), [refused, setRefused] = useState<unknown>(), [busy, setBusy] = useState(false), [kept, setKept] = useState<'file_changed' | 'file_not_removed'>();
  const request = useRef<AbortController | undefined>(undefined), [operationId] = useState(() => crypto.randomUUID());
  const read = useCallback(() => {
    request.current?.abort(); const controller = request.current = new AbortController();
    setPreview(undefined); setFailed(false); setTyped('');
    if (file) void binding.previewDelete(data, file, controller.signal).then(value => { if (!controller.signal.aborted) setPreview(value); }, error => { if (!controller.signal.aborted) { setFailed(true); binding.context.effects.reportError(error); } });
  }, [binding, data, file]);
  useEffect(() => { read(); return () => request.current?.abort(); }, [read]);
  const close = () => { if (!busy) { request.current?.abort(); onClose(); } };
  const confirm = async () => {
    if (!preview || !deletionAllowed(preview) || typed.trim() !== preview.confirmation || busy) return;
    const controller = request.current = new AbortController(); setBusy(true); setRefused(undefined); setKept(undefined);
    try {
      const result = await binding.deleteVersion(data, preview, typed.trim(), operationId, controller.signal);
      if (controller.signal.aborted) return;
      const retained = result.receipts.find(value => !value.removed);
      if (retained) setKept(retained.code === 'file_changed' ? 'file_changed' : 'file_not_removed');
      else { onClose(); await binding.service.refresh(); }
    } catch (error) { if (!controller.signal.aborted) { setRefused(error ?? true); binding.context.effects.reportError(error); } }
    finally { if (!controller.signal.aborted) setBusy(false); }
  };
  return <Dialog footerDismisses open onOpenChange={value => { if (!value) close(); }} title={t('title.deleteVersion.title')} description={data.item.title} width={500} returnFocusRef={returnFocusRef}
    dismissOnOutsidePress={!busy} dismissOnBack={!busy} showCloseButton={!busy} footer={<><Button variant="ghost" size="compact" disabled={busy} onClick={close}>{t('action.cancel')}</Button><Button size="compact" disabled={!preview || !deletionAllowed(preview) || typed.trim() !== preview.confirmation} loading={busy} onClick={() => { void confirm(); }}>{t('action.delete')}</Button></>}>
    {failed && <ScreenFailure retry={read} />}{refused !== undefined && <ManageRefusal error={refused} fallback="web.manage.deleteRefused" retry={() => { void confirm(); }} />}
    {kept && <p role="alert">{t(kept === 'file_changed' ? 'deleteMedia.fileChanged' : 'deleteMedia.fileNotRemoved')}</p>}
    {!preview && !failed && <p role="status">{t('deleteMedia.checking')}</p>}
    {preview && <div className={styles.manageBody}><TitleProse>{t('title.deleteVersion.body', {size: preview.totalBytesText})}</TitleProse>{file?.path && <p className={styles.path}>{file.path}</p>}
      {deletionAllowed(preview) ? <TextField label={preview.confirmationKind === 'count' ? t('deleteMedia.typeCount', {count: preview.confirmation}) : t('deleteMedia.typeTitle')} description={preview.confirmationKind === 'title' ? preview.confirmation : undefined} value={typed} onChange={event => setTyped(event.currentTarget.value)} autoComplete="off" disabled={busy} /> : <p>{t('deleteMedia.disabled')}</p>}
    </div>}
  </Dialog>;
}

function SplitVersion({binding, data, manage, onClose, returnFocusRef}: Props) {
  const [fileId, setFileId] = useState<string>(), [busy, setBusy] = useState(false), [notFound, setNotFound] = useState(false), [refused, setRefused] = useState<unknown>();
  const request = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => request.current?.abort(), []);
  // Only a version that is one file on its own (or a flat file) can become its own title.
  const occurrenceOf = (id: string) => manage.splitCandidates.find(candidate => candidate.id === id)?.occurrence;
  const models = versionModels(manage.splitCandidates.map(candidate => candidate.facts), environment);
  const close = () => { request.current?.abort(); onClose(); };
  const confirm = async () => {
    if (!fileId || busy) return;
    const controller = request.current = new AbortController(); setBusy(true); setRefused(undefined); setNotFound(false);
    try {
      const occurrence = occurrenceOf(fileId);
      if (!occurrence) throw new DOMException('Version changed', 'AbortError');
      const entry = await binding.splitVersion(data, occurrence, controller.signal);
      if (controller.signal.aborted) return;
      onClose(); binding.context.effects.openEntry(entry);
    } catch (error) {
      if (controller.signal.aborted) return;
      // The file is no longer where the owner saw it: say so, rather than a generic failure.
      if ((error as {code?: string})?.code === 'not_found' || (error as {code?: string})?.code === 'stale_continuation') setNotFound(true);
      else { setRefused(error ?? true); binding.context.effects.reportError(error); }
    }
    finally { if (!controller.signal.aborted) setBusy(false); }
  };
  return <Dialog footerDismisses open onOpenChange={value => { if (!value) close(); }} title={t('title.split.title')} description={data.item.title} width={480} returnFocusRef={returnFocusRef}
    footer={<><Button variant="ghost" size="compact" onClick={close}>{t('action.cancel')}</Button><Button variant="primary" size="compact" loading={busy} disabled={!fileId} onClick={() => { void confirm(); }}>{t('title.split.confirm')}</Button></>}>
    <div className={styles.manageBody}><TitleProse>{t('title.split.body')}</TitleProse><Segmented aria-label={t('next.kit.splitVersions')} value={fileId ?? ''} onValueChange={setFileId} disabled={busy} items={models.map(model => ({value: model.id, label: <span className={styles.versionName}>{[...model.name, ...model.detail].map(part => <span key={part.id}>{part.text}</span>)}</span>}))} />
      {busy && <p role="status">{t('title.split.working')}</p>}{notFound && <p role="alert">{t('title.split.notFound')}</p>}{refused !== undefined && <ManageRefusal error={refused} fallback="web.manage.splitRefused" retry={() => { void confirm(); }} />}
    </div>
  </Dialog>;
}

function MergeTitle({binding, data, manage, onClose, returnFocusRef}: Props) {
  const queries = useViewerQueries();
  const search = useMemo(() => createSearchBindings(binding.context, queries), [binding, queries]);
  useEffect(() => () => search.dispose(), [search]);
  const snapshot = useSyncExternalStore(search.service.subscribe, search.service.getSnapshot, search.service.getSnapshot);
  const [query, setQuery] = useState(''), [candidate, setCandidate] = useState<ContentEntry>(), [keepId, setKeepId] = useState(data.item.id), [busy, setBusy] = useState(false), [refused, setRefused] = useState<unknown>();
  const request = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => request.current?.abort(), []);
  useEffect(() => {
    const timer = setTimeout(() => { void search.select(query, {libraryIds: [data.scope.libraryId]}).catch(binding.context.effects.reportError); }, 260);
    return () => { clearTimeout(timer); search.service.cancel(); };
  }, [search, query, binding, data.scope.libraryId]);
  const entries = snapshot.phase === 'ready' ? snapshot.groups.flatMap(group => group.items).filter((entry): entry is ContentEntry => 'libraryId' in entry && entry.libraryId === data.scope.libraryId && canMergeTitles(data.item, entry)) : [];
  const close = () => { if (!busy) { request.current?.abort(); onClose(); } };
  const confirm = async () => {
    if (!candidate || busy) return;
    const controller = request.current = new AbortController(); setBusy(true); setRefused(undefined);
    try {
      const entry = await binding.mergeTitle(data, candidate, keepId, controller.signal);
      if (!controller.signal.aborted) { onClose(); binding.context.effects.openEntry(entry); }
    } catch (error) { if (!controller.signal.aborted) { setRefused(error ?? true); binding.context.effects.reportError(error); } }
    finally { if (!controller.signal.aborted) setBusy(false); }
  };
  return <Dialog footerDismisses open onOpenChange={value => { if (!value) close(); }} title={t(candidate ? 'web.merge.title' : 'title.merge.action')} width={480} returnFocusRef={returnFocusRef} dismissOnOutsidePress={!busy} dismissOnBack={!busy} showCloseButton={!busy}
    footer={<><Button size="compact" variant="ghost" onClick={close} disabled={busy}>{t('action.cancel')}</Button>{candidate && <Button size="compact" variant="primary" loading={busy} onClick={() => { void confirm(); }}>{t('web.selection.merge')}</Button>}</>}>
    <div className={styles.manageBody}>{!candidate ? <><TextField variant="search" aria-label={t('web.search.title')} value={query} onChange={event => setQuery(event.currentTarget.value)} />{entries.map(entry => <Button key={entry.id} size="compact" variant="text" onClick={() => setCandidate(entry)}>{entry.displayTitle ?? entry.title}{entry.year && <span>{entry.year}</span>}</Button>)}</> : <>
      <Segmented aria-label={t('web.merge.keep')} value={keepId} onValueChange={setKeepId} disabled={busy} items={[{value: data.item.id, label: data.item.title}, {value: candidate.id, label: candidate.displayTitle ?? candidate.title}]} />
      <TitleProse>{t(mergeExplanation(data.item.kind), {title: keepId === data.item.id ? candidate.displayTitle ?? candidate.title : data.item.title})}</TitleProse>
    </>}{!candidate && query.trim() && snapshot.phase === 'ready' && entries.length === 0 && <p role="status" className={styles.noMatch}>{t('title.merge.none')}</p>}
    {refused !== undefined && <ManageRefusal error={refused} fallback="web.manage.mergeRefused" retry={() => { void confirm(); }} />}{snapshot.phase === 'error' && <ScreenFailure retry={() => { void search.service.retry(); }} />}</div>
  </Dialog>;
}
