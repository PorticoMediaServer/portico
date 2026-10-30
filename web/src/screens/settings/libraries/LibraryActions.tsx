import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {router} from '../../../router';
import {Button} from '../../../kit/controls';
import {LibraryManagementService} from '@core/library-management.ts';
import {libraryScanAction} from './model';

/** Runs one library scan through the bounded management service, which owns the
 * current library selection, the command and its reconciliation. */
export function LibraryScanAction({rowId, label, libraryId, service, onError}: Readonly<{
  rowId: string; label: string; libraryId: string; service: LibraryManagementService; onError: (error: unknown) => void;
}>) {
  const snapshot = useSyncExternalStore(service.subscribe, service.getSnapshot, service.getSnapshot);
  const [busy, setBusy] = useState(false);
  const lifetime = useRef<{libraryId: string; running: boolean} | undefined>(undefined);
  useEffect(() => {
    const capture = {libraryId, running: false}; lifetime.current = capture; setBusy(false);
    return () => {if (lifetime.current === capture) lifetime.current = undefined;};
  }, [libraryId, service]);
  useEffect(() => {
    if (rowId === 'libraries.scanNow' && service.getSnapshot().selectedLibraryId !== libraryId) void service.selectLibrary(libraryId).catch(onError);
  }, [service, libraryId, rowId, onError]);
  const selectedLibrary = snapshot.selected.data?.library;
  const lastScan = selectedLibrary?.id === libraryId ? selectedLibrary.lastScan : undefined;
  const running = lastScan && (lastScan.status === 'running' || lastScan.status === 'queued' || lastScan.status === 'paused');
  const mode = libraryScanAction(rowId);
  if (!mode) return null;
  const run = async () => {
    const capture = lifetime.current;
    if (!capture || capture.libraryId !== libraryId || capture.running || service.getSnapshot().mutation.phase === 'pending' || service.getSnapshot().selected.phase === 'loading') return;
    capture.running = true;
    const current = () => lifetime.current === capture;
    setBusy(true);
    try {
      await service.selectLibrary(libraryId);
      if (!current()) return;
      const selected = service.getSnapshot().selected;
      if (selected.phase !== 'ready' || selected.data?.library.id !== libraryId) throw new Error('library_changed');
      if (mode === 'full') await service.scan('full');
      else await service.scan();
      if (current() && service.getSnapshot().mutation.phase !== 'complete') throw new Error('library_scan_failed');
    } catch (error) {if (current()) onError(error);}
    finally {if (current()) {capture.running = false;setBusy(false);}}
  };
  const cancel = async () => {
    const capture = lifetime.current;
    if (!capture || capture.running || !lastScan?.actions.includes('cancel')) return;
    capture.running = true; setBusy(true);
    try {
      await service.cancelJob(lastScan.id);
      if (lifetime.current === capture && service.getSnapshot().mutation.phase !== 'complete') throw new Error('library_cancel_failed');
    } catch (error) {if (lifetime.current === capture) onError(error);}
    finally {if (lifetime.current === capture) {capture.running = false;setBusy(false);}}
  };
  if (running) return <>
    <span role="status">{defaultI18n.t('next.settings.libraries.scanning')}</span>
    {mode === 'incremental' && <>
      <p>{defaultI18n.t('next.ownerLibraries.scanningNote')}</p>
      <Button variant="glass" size="small" onClick={() => {void router.navigate({to: '/dashboard'}).catch(onError);}}>{defaultI18n.t('next.ownerLibraries.openDashboard')}</Button>
      {lastScan.actions.includes('cancel') && <Button variant="glass" size="small" disabled={busy} loading={busy} onClick={() => {void cancel();}}>{defaultI18n.t('web.libraries.cancelScan')}</Button>}
    </>}
  </>;
  return <Button variant="glass" size="small" loading={busy} disabled={busy || snapshot.mutation.phase === 'pending' || snapshot.selected.phase === 'loading'} onClick={() => { void run(); }}>{label}</Button>;
}
