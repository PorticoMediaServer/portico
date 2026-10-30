import {useEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {LibraryManagementService} from '@core/library-management.ts';
import {removeLibrary} from '@core/title-identity.ts';
import {Button, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import styles from './libraries.module.css';

const t = defaultI18n.t;

/** Remove library, said plainly: the owner types the library's own name before
 * the red button turns on (owner-libraries.html#7). The files on disk are never
 * touched by this command. */
export function RemoveLibrary({open, onOpenChange, libraryId, service, api, onRemoved, onError}: Readonly<{
  open: boolean; onOpenChange: (open: boolean) => void; libraryId: string; service: LibraryManagementService;
  api: LibraryContentApi; onRemoved: () => void; onError: (error: unknown) => void;
}>) {
  const [typed, setTyped] = useState(''), [name, setName] = useState(''), [revision, setRevision] = useState<number>();
  const [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const lifetime = useRef<{libraryId: string; running: boolean} | undefined>(undefined);
  useEffect(() => {
    if (!open) return;
    const capture = {libraryId, running: false}; lifetime.current = capture;
    const current = () => lifetime.current === capture;
    setTyped(''); setFailed(false); setBusy(false); setName(''); setRevision(undefined);
    void service.selectLibrary(libraryId).then(() => {
      if (!current()) return;
      const library = service.getSnapshot().selected.data?.library;
      if (library?.id === libraryId && service.getSnapshot().selected.phase === 'ready') { setName(library.name); setRevision(library.revision); }
    }).catch(error => { if (current()) onError(error); });
    return () => {if (current()) lifetime.current = undefined;};
  }, [open, libraryId, service, onError]);
  const confirm = async () => {
    const capture = lifetime.current;
    if (!open || !capture || capture.libraryId !== libraryId || capture.running || revision === undefined || !name || typed !== name) return;
    capture.running = true;
    const current = () => lifetime.current === capture;
    setBusy(true); setFailed(false);
    try { await removeLibrary(api, libraryId, typed, revision); if (current()) {onOpenChange(false); onRemoved();} }
    catch (error) { if (current()) {setFailed(true); onError(error);} }
    finally {if (current()) {capture.running = false;setBusy(false);}}
  };
  return <Dialog open={open} onOpenChange={value => {if (!lifetime.current?.running) onOpenChange(value);}} dismissOnBack={!busy} dismissOnOutsidePress={!busy} showCloseButton={!busy} size="sm" role="alertdialog"
    title={t('next.settingsSpec.server.libraries.panels.library.tabs.0.groups.2.rows.0.control.label')}
    description={t('next.settingsSpec.server.libraries.panels.library.tabs.0.groups.2.rows.0.control.confirm')}
    footer={<><Button disabled={busy} onClick={() => {if (!lifetime.current?.running) onOpenChange(false);}}>{t('next.settings.cancel')}</Button>
      <Button variant="primary" loading={busy} disabled={busy || !name || typed !== name} onClick={() => { void confirm(); }}>{t('next.settingsSpec.server.libraries.panels.library.tabs.0.groups.2.rows.0.control.label')}</Button></>}>
    <div className={styles.form}>
      <p className={styles.formNote}>{t('next.settingsSpec.server.libraries.panels.library.tabs.0.groups.2.rows.0.help')}</p>
      <TextField label={name} value={typed} onChange={event => setTyped(event.currentTarget.value)} />
      {failed && <p role="alert">{t('next.settings.libraries.removeFailed')}</p>}
    </div>
  </Dialog>;
}

/** The drawn action row: a plain red-label button that opens the confirmation. */
export function RemoveLibraryButton({libraryId, service, api, label, onRemoved, onError}: Readonly<{
  libraryId: string; service: LibraryManagementService; api: LibraryContentApi; label: string;
  onRemoved: () => void; onError: (error: unknown) => void;
}>) {
  const [open, setOpen] = useState(false);
  return <>
    <Button variant="text" size="small" className={styles.danger} onClick={() => setOpen(true)}>{label}</Button>
    <RemoveLibrary open={open} onOpenChange={setOpen} libraryId={libraryId} service={service} api={api} onRemoved={onRemoved} onError={onError} />
  </>;
}
