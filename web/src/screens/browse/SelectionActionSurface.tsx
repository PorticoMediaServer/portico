import {useRef, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {canonicalMusicQualityLadder, canonicalQualityLadder} from '../../../../packages/contracts/src/types.ts';
import {musicQualityPresentation, qualityPresentation} from '@core/playback-v1/quality-presentation.ts';
import {presentError} from '@core/presentation/errors.ts';
import {Button, Select, Switch, TextField} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {ProgressRing} from '../../kit/shell';
import type {BrowserSelectionActionModel} from './selection-action-model.ts';
import styles from '../../kit/selection/selection.module.css';

const t = defaultI18n.t;
/** Aggregate presentation is separate from the explicit <=200 DeletePreview gate. */
export function SelectionActionSurface({model, onClose, onOptionsChange}: Readonly<{
  model: BrowserSelectionActionModel; onClose: () => void;
  onOptionsChange: (options: Readonly<{deleteFiles: boolean}> | Readonly<{quality: string}>) => void;
}>) {
  const state = useSyncExternalStore(model.controller.subscribe, model.controller.getSnapshot, model.controller.getSnapshot);
  const transfers = useSyncExternalStore(model.subscribeTransfers, model.getTransfers, model.getTransfers);
  const input = useRef<HTMLInputElement | null>(null);
  if (!state.input || state.phase === 'idle' || state.phase === 'retired') return null;
  const deleting = state.input.action === 'delete', preview = state.preparation?.kind === 'selection-action-preview' ? state.preparation : undefined;
  const busy = ['preparing', 'confirming', 'submitting', 'running', 'cancelling'].includes(state.phase);
  const job = state.job, count = preview?.total ?? job?.total;
  const title = !deleting ? t('next.detail.download') : count === 1 && preview ? t('next.selection.deleteTitle', {title: preview.confirmation})
    : count !== undefined ? t('next.selection.deleteTitles', {count}) : t('next.selection.delete');
  const canConfirm = state.phase === 'ready' && !!preview && state.confirmation === preview.confirmation && Date.parse(preview.expiresAt) > Date.now();
  const firstTransfer = transfers.items[0];
  const music = state.input.selector.query.pivot === 'songs';
  const qualities = state.input.selector.query.pivot === 'parts' ? [qualityPresentation('original', false, defaultI18n)]
    : music ? canonicalMusicQualityLadder.map(row => musicQualityPresentation(row.id, false, defaultI18n))
      : canonicalQualityLadder.map(row => qualityPresentation(row.id, false, defaultI18n));
  const shownError = state.errorCode ? presentError({code: state.errorCode}, deleting ? 'library' : 'downloads', {operation: 'action'}).body : '';
  return <Dialog open onOpenChange={open => {if (!open) onClose();}} title={title} accessibilityLabel={title}
    width={520} className={styles.deleteDialog} bodyClassName={styles.deleteBody} footerClassName={styles.deleteFooter}
    initialFocusRef={input} footer={<>
      <Button size="compact" variant="glass" onClick={onClose}>{t('next.states.close')}</Button>
      {(busy || state.phase === 'uncertain') && <Button size="compact" disabled={state.phase === 'cancelling'} onClick={() => {void model.controller.cancel();}}>{t('next.selection.cancel')}</Button>}
      {(state.phase === 'uncertain' || state.phase === 'error' && !job) && <Button size="compact" onClick={() => {void model.controller.retry();}}>{t('next.states.tryAgain')}</Button>}
      {!job && state.phase !== 'uncertain' && <Button size="compact" disabled={!canConfirm} onClick={() => {void model.controller.confirm();}}>{t(deleting ? 'next.selection.delete' : 'next.detail.download')}</Button>}
    </>}>
    {deleting && state.input.action === 'delete' ? <label className={styles.removeFilesRow}>
      <span>{t('next.selection.removeFiles')}</span><Switch checked={state.input.options.deleteFiles} disabled={!!job || busy || state.phase === 'uncertain'}
        onCheckedChange={deleteFiles => onOptionsChange({deleteFiles})} aria-label={t('next.selection.removeFiles')}/>
    </label> : state.input.action === 'download' && <Select label={t('next.detail.downloadQuality')} value={state.input.options.quality}
      options={qualities.map(row => ({value: row.id, label: row.label}))}
      disabled={!!job || busy || state.phase === 'uncertain'} onValueChange={quality => onOptionsChange({quality})}/>}
    {state.phase === 'preparing' && <p role="status">{t(deleting ? 'next.selection.checking' : 'web.downloads.requestPreparing')}</p>}
    {preview && !job && <>
      {preview.bytesKnown && <p><b>{t('next.selection.files', {count: preview.files, size: defaultI18n.bytes(preview.bytes)})}</b></p>}
      {deleting && state.input.action === 'delete' && <p className={styles.deleteSmall}>{t(state.input.options.deleteFiles ? 'next.selection.filesDeleted' : 'next.selection.recordOnly')}</p>}
      <TextField ref={input} value={state.confirmation} onChange={event => model.controller.setConfirmation(event.currentTarget.value)}
        label={t(preview.confirmationKind === 'title' ? 'next.selection.typeTitle' : 'next.selection.confirmCount', {count: preview.confirmation})}
        description={preview.confirmationKind === 'title' ? preview.confirmation : undefined}
        disabled={state.phase !== 'ready'} inputMode={preview.confirmationKind === 'count' ? 'numeric' : 'text'} autoComplete="off" autoCapitalize="none" spellCheck={false}/>
    </>}
    {job && <div role="status">
      <p>{deleting ? t('next.selection.deleteResult', {removed: job.done, failed: job.failed}) : t('next.selection.jobProgress', {done: job.done + job.failed, total: job.total})}</p>
      {deleting && <p>{t('next.selection.jobProgress', {done: job.done + job.failed, total: job.total})}</p>}
      {job.failed > 0 && !deleting && <p>{t('web.downloads.requestFailed', {failed: job.failed})}</p>}
      {['capturing', 'sealed'].includes(job.state) && <p>{t('web.selection.preparing')}</p>}
      {(job.state === 'cancelled' || job.errorCode === 'cancelled') && <p>{t('web.dvr.state.cancelled')}</p>}
      {deleting && (job.filesDeleted > 0 || job.bytesDeleted > 0) && <p>{t('next.selection.files', {count: job.filesDeleted, size: defaultI18n.bytes(job.bytesDeleted)})} {t('next.selection.filesDeleted')}</p>}
      {job.errorCode && job.errorCode !== 'cancelled' && <p role="alert">{presentError({code: job.errorCode}, deleting ? 'library' : 'downloads', {operation: 'action'}).body}</p>}
      {busy && <ProgressRing icon={deleting ? 'i-trash' : 'i-download'} fraction={job.state === 'capturing' ? job.admitted / job.total : (job.done + job.failed) / job.total}/>}
    </div>}
    {shownError && <p role="alert">{state.phase === 'uncertain' && deleting ? t('next.selection.deleteUncertain') : shownError}</p>}
    {!deleting && firstTransfer && <div>
      <p>{firstTransfer.title.title}</p>
      <Button disabled={transfers.phase === 'handing-off'} onClick={() => {void model.transferNext();}}>{t('web.downloads.save')}</Button>
      <Button disabled={transfers.phase === 'handing-off'} onClick={() => model.skipNext()}>{t('web.dvr.skipThis')}</Button>
    </div>}
    {!deleting && !firstTransfer && transfers.nextCursor && <Button disabled={transfers.phase === 'loading'} onClick={() => {void model.nextPage();}}>{t('web.pager.next')}</Button>}
    {!deleting && transfers.phase === 'waiting' && <p role="status">{t('web.downloads.requestPreparing')}</p>}
    {!deleting && transfers.errorCode && <p role="alert">{t('error.unavailable')} <Button onClick={() => {void model.retryInventory();}}>{t('next.states.tryAgain')}</Button></p>}
  </Dialog>;
}
