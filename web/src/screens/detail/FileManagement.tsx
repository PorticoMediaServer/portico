import {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {useViewerQueries} from '../../platform/freshness';
import {VersionDeletionOpening, type VersionDeletionState} from './version-deletion';
import {bytesText} from '@core/presentation/kit-model/technical.ts';
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

/** The press of Delete this version… starts its preview, once (version-deletion.ts); any other manage action is as it was. */
export function withVersionDeletion(binding: DetailBindings, data: DetailProjection, context: FileInfoManageContext): FileInfoManageContext {
  const file = context.action.action === 'delete-version' && context.action.fileId ? context.files.get(context.action.fileId) : undefined;
  return file ? {...context, deletion: new VersionDeletionOpening(signal => binding.previewDelete(data, file, signal), binding.context.effects.reportError)} : context;
}

/** Delete this version… (owner only): what it takes, from the server's preview of this exact version (asked once, by the
 * press that opened this), then the delete. The owner types the title to confirm, and Delete is the destructive button,
 * never the default action. */
export function DeleteVersion({binding, data, manage, onClose, returnFocusRef}: Props) {
  const file = manage.action.fileId ? manage.files.get(manage.action.fileId) : undefined, opening = manage.deletion;
  const state = useSyncExternalStore(opening?.subscribe ?? noSubscription, opening?.getSnapshot ?? missing, opening?.getSnapshot ?? missing);
  const preview = state.phase === 'ready' ? state.preview : undefined;
  const [typed, setTyped] = useState(''), [refused, setRefused] = useState<unknown>(), [busy, setBusy] = useState(false), [kept, setKept] = useState(false);
  const request = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => request.current?.abort(), []);
  const close = () => { if (!busy) { request.current?.abort(); onClose(); } };
  const confirmed = !!preview && typed.trim() === preview.title;
  const confirm = async () => {
    if (!preview || !confirmed || busy) return;
    const controller = request.current = new AbortController(); setBusy(true); setRefused(undefined); setKept(false);
    try {
      const result = await binding.deleteVersion(data, preview, controller.signal);
      if (controller.signal.aborted) return;
      if (result.filesRetained) setKept(true);
      else { onClose(); await binding.service.refresh(); }
    } catch (error) { if (!controller.signal.aborted) { setRefused(error ?? true); binding.context.effects.reportError(error); } }
    finally { if (!controller.signal.aborted) setBusy(false); }
  };
  const going = preview ? [...(preview.titles > preview.alsoRemoves.length ? [preview.title] : []), ...preview.alsoRemoves.map(title => title.title)] : [];
  return <Dialog footerDismisses open onOpenChange={value => { if (!value) close(); }} title={t('title.deleteVersion.title')} description={data.item.title} width={500} returnFocusRef={returnFocusRef}
    dismissOnOutsidePress={!busy} dismissOnBack={!busy} showCloseButton={!busy} footer={<><Button variant="ghost" size="compact" disabled={busy} onClick={close}>{t('action.cancel')}</Button><Button variant="destructive" size="compact" disabled={!confirmed || kept} loading={busy} onClick={() => { void confirm(); }}>{t('action.delete')}</Button></>}>
    {state.phase === 'failed' && <ManageRefusal error={state.error} fallback="web.manage.deletePreviewFailed" retry={() => opening?.retry()} />}
    {refused !== undefined && <ManageRefusal error={refused} fallback="web.manage.deleteRefused" retry={() => { void confirm(); }} />}
    {kept && <p role="alert">{t('web.deleteVersion.kept')}</p>}
    {state.phase === 'checking' && <p role="status" aria-busy="true" className={styles.checking}>{t('deleteMedia.checking')}</p>}
    {preview && <div className={styles.manageBody}>
      <TitleProse><b>{t('web.deleteVersion.files', {count: preview.files, size: bytesText(preview.bytes, environment.t) ?? ''})}</b> {t('web.deleteVersion.disk')}</TitleProse>
      {file?.path && <p className={styles.path}>{file.path}</p>}
      {going.length ? <TitleProse>{t('web.deleteVersion.titlesGo', {count: going.length})} {going.join(', ')}</TitleProse> : <TitleProse>{t('web.deleteVersion.titleStays', {title: preview.title})}</TitleProse>}
      {preview.listEntries > 0 && <TitleProse>{t('web.deleteVersion.listEntries', {count: preview.listEntries})}</TitleProse>}
      {preview.otherPeople > 0 && <TitleProse>{t('web.deleteVersion.otherPeople', {count: preview.otherPeople})}</TitleProse>}
      <TextField label={t('deleteMedia.typeTitle')} description={preview.title} value={typed} onChange={event => setTyped(event.currentTarget.value)} autoComplete="off" disabled={busy || kept} />
    </div>}
  </Dialog>;
}
const noSubscription = () => () => {};
const missing = (): VersionDeletionState => ({phase: 'failed', error: undefined});

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
