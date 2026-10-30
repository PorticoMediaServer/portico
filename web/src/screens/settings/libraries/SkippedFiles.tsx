import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {LibraryManagementService} from '@core/library-management.ts';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import {Dialog} from '../../../kit/overlays/Dialog';
import {skippedFiles, type SkippedPage} from './skipped-files';
import styles from './libraries.module.css';
const t = defaultI18n.t;

export function SkippedFiles({libraryId, api, service, isCurrent, label}: Readonly<{
  libraryId: string; api: LibraryContentApi; service: LibraryManagementService; isCurrent: () => boolean; label: string;
}>) {
  const [open, setOpen] = useState(false), [page, setPage] = useState<SkippedPage>(), [failed, setFailed] = useState(false), [busy, setBusy] = useState(false);
  const [libraryName, setLibraryName] = useState('');
  const lifetime = useRef<AbortController | undefined>(undefined), job = useRef<string | undefined>(undefined), running = useRef(false);
  const current = (capture: AbortController) => lifetime.current === capture && !capture.signal.aborted && isCurrent();
  const load = async (capture: AbortController, after = 0) => {
    if (!current(capture) || running.current || !job.current) return;
    running.current = true; setBusy(true); setFailed(false);
    try {
      const next = await skippedFiles(api, libraryId, job.current, after, capture.signal);
      if (current(capture)) setPage(before => after && before ? {...next, items: [...before.items, ...next.items]} : next);
    } catch { if (current(capture)) setFailed(true); }
    finally { if (current(capture)) {running.current = false; setBusy(false);} }
  };
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({current, load});
  useEffect(() => {setupPorts.current = {current, load};});
  useEffect(() => {
    const {current, load} = setupPorts.current;
    if (!open) return;
    const capture = new AbortController(); lifetime.current = capture; running.current = false; job.current = undefined;
    setPage(undefined); setLibraryName(''); setFailed(false); setBusy(true);
    void service.selectLibrary(libraryId).then(async () => {
      if (!current(capture)) return;
      const selected = service.getSnapshot().selected.data?.library;
      if (!selected || selected.id !== libraryId) throw new Error('library_changed');
      setLibraryName(selected.name);
      job.current = selected.lastScan?.id;
      if (job.current) await load(capture);
      else {setPage({items: [], count: 0, hasMore: false, next: 0}); setBusy(false);}
    }).catch(() => {if (current(capture)) {setFailed(true);setBusy(false);}});
    return () => {capture.abort(); if (lifetime.current === capture) lifetime.current = undefined;};
  }, [open, libraryId, api, service, isCurrent]);
  const scan = async () => {
    const capture = lifetime.current;
    if (!capture || !current(capture) || running.current) return;
    running.current = true;setBusy(true);setFailed(false);
    try {
      await service.selectLibrary(libraryId);
      if (!current(capture)) return;
      const selected = service.getSnapshot().selected;
      if (selected.phase !== 'ready' || selected.data?.library.id !== libraryId) throw new Error('library_changed');
      await service.scan();
      if (current(capture)) {
        const mutation = service.getSnapshot().mutation;
        if (mutation.phase === 'error' || mutation.phase === 'ambiguous') setFailed(true);
      }
    }
    catch {if (current(capture)) setFailed(true);}
    finally {if (current(capture)) {running.current = false;setBusy(false);}}
  };
  return <>
    <Button variant="glass" size="small" onClick={() => {if (isCurrent()) setOpen(true);}}>{label}</Button>
    <Dialog open={open} onOpenChange={setOpen} size="lg" width={640} title={t('next.ownerLibraries.filesSkippedTitle')}
      description={t('next.ownerLibraries.filesSkippedLede')} footer={<>
        {page && page.items.length > 0 && <span className={styles.skippedCount}>{t('next.ownerLibraries.filesCount', {count: page.count})}</span>}
        <Button onClick={() => setOpen(false)}>{t('next.overlays.close')}</Button>
        <Button variant="primary" icon="i-refresh" loading={busy} disabled={busy} onClick={() => {void scan();}}>{t('next.ownerLibraries.scanAgain')}</Button>
      </>}>
      {failed ? <p role="alert">{t('next.states.pageFailedBody')}</p> : null}
      {!page && busy ? <p role="status">{t('next.settings.loading')}</p> : null}
      {page && !page.items.length ? <div className={styles.skippedEmpty}>
        <span className={styles.skippedEmptyIcon}><Icon name="i-check" size={26}/></span>
        <b>{t('next.ownerLibraries.skippedEmptyTitle')}</b>
        <p>{t('next.ownerLibraries.skippedEmptyBody', {library: libraryName})}</p>
      </div> : null}
      {page && page.items.length > 0 && <ul className={styles.skippedList}>{page.items.map(file => <li key={file.sequence}><code>{file.path}</code><small>{t(file.reason)}</small></li>)}</ul>}
      {page?.hasMore && <Button disabled={busy} onClick={() => {const capture = lifetime.current;if (capture) void load(capture, page.next);}}>{t('next.ownerLibraries.showMore')}</Button>}
    </Dialog>
  </>;
}
