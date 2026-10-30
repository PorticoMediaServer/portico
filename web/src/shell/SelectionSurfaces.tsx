import {useEffect, useMemo, useRef, useSyncExternalStore, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import type {BrowserQueryActionHost} from '../screens/browse/selection-action-host';
import type {SelectionContext} from '@core/selection-model/types.ts';
import type {HomeSelectionController, HomeSelectionSnapshot} from '@core/home-selection/index.ts';
import {summarizeSelection} from '@core/selection-model/index.ts';
import {presentError} from '@core/presentation/errors.ts';
import type {EntryActionId} from '@core/presentation/entry-actions.ts';
import {homeEntryCapabilities} from '@core/home-selection.ts';
import {Artwork} from '../kit/artwork';
import {Button, Select} from '../kit/controls';
import {Banner, Dialog, Menu, type MenuEntry} from '../kit/overlays';
import {BulkEditor, DeleteMediaDialog, MixedEditExplainer, PhoneActionBar, SelectionBar} from '../kit/selection';
import {OwnerTag, ServerAudienceTag, ServerMark, type EditorAudience} from '../kit/shell';
import type {IconName} from '../kit/Icon';
import type {ShellArtwork} from './artwork';
import {retryEditorRead, runSelectionCommand, selectionCallbacks} from './selection-actions';
import {editorCoreDraft, editorResultView, editorUiDraft, editorValues, selectionMemberDisplay} from './selection-presentation';
import {SelectionPickerSurface} from './SelectionPickerSurface';
import {useShellWidth} from './viewport';
import {MetadataEditor} from '../screens/metadata/MetadataEditor';
import metadataStyles from '../screens/metadata/metadata.module.css';
import styles from './selection.module.css';

export type SelectionRendererSource = Readonly<{
  controller: HomeSelectionController;
  queryActions?: BrowserQueryActionHost<SelectionContext>;
  metadata?: Readonly<{api: Parameters<typeof MetadataEditor>[0]['api']; scope: Parameters<typeof MetadataEditor>[0]['scope']; current: (snapshot: HomeSelectionSnapshot) => boolean}>;
  anchor: RefObject<HTMLElement | null>;
  artwork: ShellArtwork;
  owner: boolean;
  editorAudience: EditorAudience;
  dockVisible: () => boolean;
  reportError: (error: unknown) => void;
}>;
const t = defaultI18n.t;
const menuIcons: Record<EntryActionId, IconName> = {
  play: 'i-play', resume: 'i-play', playFromStart: 'i-restart', playNext: 'i-queue', addToQueue: 'i-queue',
  watchWithGroup: 'i-together', addToGroupQueue: 'i-queue', watchlist: 'i-bookmark', favorite: 'i-heart',
  watched: 'i-check', removeFromContinueWatching: 'i-x', notInterested: 'i-eyeoff', rate: 'i-star',
  addToPlaylist: 'i-listadd', addToCollection: 'i-layers', download: 'i-download', details: 'i-info',
  editMetadata: 'i-edit', refreshMetadata: 'i-refresh', separateArtist: 'i-link', delete: 'i-trash',
};
const errorText = (error: unknown) => error ? presentError(error, 'home', {operation: 'action'}).body : undefined;
function run(source: SelectionRendererSource, action: () => void | Promise<unknown>) {
  runSelectionCommand(action, source.reportError);
}
function actionsFor(source: SelectionRendererSource, snapshot: HomeSelectionSnapshot) {
  const target = snapshot.selection.target;
  const query = target?.mode === 'query' ? {onDelete: source.queryActions?.offered(target, 'delete') ? () => run(source, source.queryActions!.openDelete) : undefined,
    onDownload: source.queryActions?.offered(target, 'download') ? () => run(source, source.queryActions!.openDownload) : undefined} : {};
  return {...selectionCallbacks(snapshot, source.controller, element => { source.anchor.current = element; }, source.reportError),
    ...(snapshot.actions.delete.state === 'disabled' && snapshot.actions.delete.reason === 'one-library-required' ? {deleteDisabledReason: t('next.selection.oneLibrary')} : {}),
    ...query, ownerMark: <ServerMark size={22} />};
}

export function SelectionPhoneBar({source}: {source: SelectionRendererSource}) {
  const snapshot = useSyncExternalStore(source.controller.subscribe, source.controller.getSnapshot, source.controller.getSnapshot);
  const {summary} = snapshot.selection;
  return <PhoneActionBar selection={{count: summary.count, kind: summary.types.length === 1 ? summary.types[0].kind : undefined}}
    actions={actionsFor(source, snapshot)} episodeSelection={summary.types.length === 1 && summary.types[0].kind === 'episode'} />;
}

/** The canonical controller owns membership, grants, commands and all operation identities. */
export function SelectionSurfaces({source}: {source: SelectionRendererSource}) {
  const {controller} = source;
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const width = useShellWidth(), summary = snapshot.selection.summary;
  useEffect(() => {
    const key = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || event.altKey || !controller.getSnapshot().selection.active) return;
      const target = event.target;
      if (target instanceof Element && target.closest('input,textarea,select,[contenteditable="true"],[role="dialog"],[role="menu"]')) return;
      const state = controller.getSnapshot();
      if (state.menu || state.picker || state.editor || state.deletion || state.rating || state.mixedEdit) return;
      if (event.key === 'Escape') { event.preventDefault(); controller.clear(); }
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'a') {
        event.preventDefault(); run(source, () => { const result = controller.selectRow(); if (!result.ok) throw Object.assign(new Error(result.reason), {code: result.reason}); });
      }
    };
    document.addEventListener('keydown', key);
    return () => document.removeEventListener('keydown', key);
  }, [controller, source]);
  const menu = snapshot.menu;
  const rating = menu?.member.entry && homeEntryCapabilities(menu.member.entry, {platform: 'web', playbackAvailable: true, queueAvailable: true}).rating;
  const entries: MenuEntry[] = menu?.groups.flatMap(group => [
    ...(group.title ? [{id: 'group:' + group.id, section: true as const, label: t(group.title), ownerOnly: group.id === 'manage'}] : group.id === 'play' ? [] : [{id: 'group:' + group.id, separator: true as const}]),
    ...group.actions.map(action => ({id: action.id, label: action.id === 'rate' && rating != null ? t(action.label, {stars: defaultI18n.number(rating / 2)}) : t(action.label), icon: menuIcons[action.id],
      selected: action.selected, destructive: action.destructive, onSelect: () => run(source, () => controller.executeMenu(action.id))})),
  ]) ?? [];
  const dialogSummary = summarizeSelection(snapshot.displayMembers.map(member => member.item));
  const editor = snapshot.editor, deletion = snapshot.deletion;
  const editorResult = editor && editorResultView(editor.receipts);
  const targets = snapshot.displayMembers.map(member => {
    const display = selectionMemberDisplay(member);
    return {...display, artwork: <Artwork alt="" source={source.artwork.foreground(display.posterUrl, 100 * (globalThis.devicePixelRatio || 1), 2 / 3, {group: 'home-selection', priority: 'high'})} />};
  });
  const editorBusy = editor?.phase === 'loading' || editor?.phase === 'saving';
  const singleEditor = source.metadata && editor?.phase === 'ready' && editor.preview?.total === 1 && editor.preview.targets.length === 1 && snapshot.displayMembers.length === 1 && source.metadata.current(snapshot);
  return <>
    {source.queryActions?.surface}
    {snapshot.selection.active && <div className={styles.bar}><SelectionBar selection={{count: summary.count, kind: summary.types.length === 1 ? summary.types[0].kind : undefined}}
      actions={actionsFor(source, snapshot)} typeCount={summary.types.length} selectAll={{kind: 'row'}}
      onSelectAll={() => run(source, () => { const result = controller.selectRow(); if (!result.ok) throw Object.assign(new Error(result.reason), {code: result.reason}); })}
      onExit={() => controller.clear()} /></div>}
    <Menu open={!!menu} onOpenChange={open => { if (!open) controller.closeMenu(); }} title={t('next.home.moreFor', {title: menu ? selectionMemberDisplay(menu.member).title : ''})}
      anchorRef={source.anchor} items={entries} width={300} />
    {snapshot.mixedEdit && <MixedEditExplainer open onOpenChange={open => { if (!open) controller.closeEditor(); }} anchorRef={source.anchor}
      types={dialogSummary.types} reason={snapshot.editExplanation === 'no-bulk-fields' ? 'no-bulk-fields' : 'mixed-types'} onKeepOnly={kind => { controller.keepOnly(kind); }} />}
    {singleEditor ? <SingleMetadataEditor source={source} snapshot={snapshot}/> : editor?.preview && editor.phase !== 'uncertain' && editor.phase !== 'error' && dialogSummary.types.length === 1 ? <BulkEditor open onOpenChange={open => { if (!open && !editorBusy) controller.closeEditor(); }} returnFocusRef={source.anchor}
      selection={{count: dialogSummary.count, kind: dialogSummary.types[0].kind}} schema={editor.preview.schema} singleItemFields={editor.preview.singleItemFields} values={editorValues(editor.preview)} draft={editorUiDraft(editor.draft)}
      onDraftChange={draft => controller.setEditorDraft(editorCoreDraft(draft))}
      onSave={draft => run(source, async () => { controller.setEditorDraft(editorCoreDraft(draft)); await controller.saveEditor(); })}
      onRetry={(failedIds, draft) => run(source, () => controller.retryEditor(editorCoreDraft(draft), failedIds))}
      targets={targets} result={editorResult ?? undefined} busy={editorBusy} error={errorText(editor.error)}
      ownerTag={<ServerAudienceTag audience={source.editorAudience} layout={width < 600 ? 'phone' : 'desktop'} />} /> : editor && <Dialog open
        onOpenChange={open => { if (!open && !editorBusy) controller.closeEditor(); }} title={t('next.home.editSelection')} returnFocusRef={source.anchor} placement="right" width={600}
        dismissOnOutsidePress={!editorBusy} dismissOnBack={!editorBusy}
        footer={editor.error && <Button onClick={() => run(source, () => retryEditorRead(controller, editor.phase))}>{t('next.states.tryAgain')}</Button>}>
        <p role={editor.error ? 'alert' : 'status'}>{errorText(editor.error) ?? t('next.selection.checking')}</p>
        {editorResult && <p role="status">{t('next.selection.bulkResult', {updated: editorResult.updated, failed: editorResult.failed})}</p>}
      </Dialog>}
    {deletion && deletion.phase !== 'uncertain' && <DeleteMediaDialog open onOpenChange={open => { if (!open && deletion.phase !== 'deleting') controller.closeDelete(); }} returnFocusRef={source.anchor}
      owner={source.owner} selectionCount={dialogSummary.count} preview={deletion.preview} confirmation={deletion.confirmation}
      onConfirmationChange={value => controller.setDeleteConfirmation(value)} removeFiles={deletion.removeFiles} onRemoveFilesChange={value => controller.setDeleteRemoveFiles(value)}
      onDelete={() => run(source, () => controller.confirmDelete())} busy={deletion.phase === 'loading' || deletion.phase === 'deleting'} result={deletion.result ?? undefined}
      error={errorText(deletion.error)} ownerTag={<OwnerTag />} artwork={Object.fromEntries(targets.map(target => [target.id, target.artwork]))} />}
    {deletion?.phase === 'uncertain' && <Dialog open onOpenChange={open => { if (!open) controller.closeDelete(); }} title={t('next.selection.delete')} returnFocusRef={source.anchor}
      footer={<Button variant="primary" onClick={() => run(source, () => controller.confirmDelete())}>{t('next.states.tryAgain')}</Button>}><p role="alert">{errorText(deletion.error)}</p></Dialog>}
    <SelectionPickerSurface source={source} snapshot={snapshot} />
    {snapshot.rating && <RatingSurface source={source} snapshot={snapshot} />}
    {!editor && !deletion && !snapshot.picker && !snapshot.rating && (snapshot.action.error || snapshot.action.phase === 'partial') && <Banner className={styles.status} icon="i-warn"
      action={snapshot.action.error?.retryable || snapshot.action.phase === 'partial' ? {label: t('next.states.tryAgain'), onAction: () => run(source, () => controller.retryAction())} : undefined}>
      {errorText(snapshot.action.error) ?? t('next.home.someActionsFailed')}
    </Banner>}
  </>;
}

function RatingSurface({source, snapshot}: {source: SelectionRendererSource; snapshot: HomeSelectionSnapshot}) {
  const rating = snapshot.rating!;
  const busy = rating.phase === 'loading' || rating.phase === 'saving', ready = rating.phase === 'ready';
  const title = snapshot.displayMembers[0] && selectionMemberDisplay(snapshot.displayMembers[0]).title;
  return <Dialog open onOpenChange={open => { if (!open && !busy) source.controller.closeRating(); }} title={t('title.rate', {title: title ?? ''})}
    returnFocusRef={source.anchor} dismissOnOutsidePress={!busy} dismissOnBack={!busy} footer={<>
      <Button onClick={() => source.controller.closeRating()} disabled={busy}>{t('action.close')}</Button>
      {ready && <Button variant="primary" onClick={() => run(source, () => source.controller.submitRating())}>{t('action.save')}</Button>}
      {(rating.phase === 'uncertain' || rating.phase === 'error') && <Button variant="primary" onClick={() => run(source, () => source.controller.retryRating())}>{t('next.states.tryAgain')}</Button>}
    </>}>
    {rating.value !== undefined && <Select label={t('next.home.yourRating')} aria-label={t('next.home.yourRating')} value={rating.value === null ? 'none' : String(rating.value)}
      options={[{value: 'none', label: t('next.home.noRating')}, ...Array.from({length: 11}, (_, value) => ({value: String(value), label: t('next.home.ratingValue', {value})}))]}
      disabled={!ready} onValueChange={value => source.controller.setRatingValue(value === 'none' ? null : Number(value))} />}
    {busy && <p role="status">{t('next.selection.checking')}</p>}
    {rating.phase === 'result' && <p role="status">{t('next.selection.saved')}</p>}
    {rating.error && <p role="alert">{errorText(rating.error)}</p>}
  </Dialog>;
}

/** Open only after the controller has validated the concrete target and owner preview. */
function SingleMetadataEditor({source, snapshot}: {source: SelectionRendererSource; snapshot: HomeSelectionSnapshot}) {
  const grant = useRef(snapshot).current;
  const metadata = source.metadata!, preview = grant.editor!.preview!, target = preview.targets[0];
  const api = useMemo(() => ({async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    const assert = () => {if (!metadata.current(grant) || source.controller.getSnapshot().editor?.preview !== preview) throw new DOMException('Metadata authority changed', 'AbortError');};
    assert();
    const value = await metadata.api.request<T>(path, method, body, signal);
    assert();
    return value;
  }, ...(metadata.api.requestMetadataUpload ? {async requestMetadataUpload<T>(path: string, body: FormData, signal?: AbortSignal): Promise<T> {
    const assert = () => {if (!metadata.current(grant) || source.controller.getSnapshot().editor?.preview !== preview) throw new DOMException('Metadata authority changed', 'AbortError');};
    assert();
    const value = await metadata.api.requestMetadataUpload!<T>(path, body, signal);
    assert();
    return value;
  }} : {})}), [metadata, source.controller, grant, preview]);
  return <Dialog open onOpenChange={open => {if (!open) source.controller.closeEditor();}} title={t('next.home.editSelection')}
    returnFocusRef={source.anchor} responsive={false} placement="right" width={720} showCloseButton={false} headerClassName={metadataStyles.hostTitle} bodyClassName={metadataStyles.hostBody}>
    <MetadataEditor artwork={source.artwork} api={api} scope={metadata.scope} target={target} onClose={() => source.controller.closeEditor()}/>
  </Dialog>;
}
