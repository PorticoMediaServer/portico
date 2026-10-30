import {useEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import type {BackupSummary} from '@core/administration.ts';
import {Button, TextField} from '../controls';
import {Dialog} from '../overlays/Dialog';
import {usePresence} from '../overlays/shared';
import {SettingsAction} from './SettingsAction';
import styles from './settings.module.css';

export type BackupConfirmationDialogProps = Readonly<{
  open: boolean;
  mode: 'restore' | 'delete';
  backup: BackupSummary;
  scopeKey: string;
  /** The real domain guards the captured viewer and current target before every command. */
  isCurrent: () => boolean;
  subscribeAuthority: (listener: () => void) => () => void;
  submit: (backup: BackupSummary, signal: AbortSignal) => Promise<void>;
  onClose: () => void;
  onFailure: (error: unknown) => void;
  format: Pick<typeof defaultI18n, 'dateTime' | 't'>;
  returnFocusRef?: RefObject<HTMLElement | null>;
}>;

/** Only the domain callback stages or deletes; the visual never guesses success from polling. */
export function BackupConfirmationDialog(props: BackupConfirmationDialogProps) {
  const current = useSyncExternalStore(props.subscribeAuthority, props.isCurrent, props.isCurrent);
  const present = usePresence(props.open, 160);
  return current && present ? <Confirmation key={JSON.stringify([props.scopeKey, props.mode, props.backup.id])} {...props} /> : null;
}

function Confirmation({open, mode, backup, submit, onClose, onFailure, isCurrent, format, returnFocusRef}: BackupConfirmationDialogProps) {
  const [typed, setTyped] = useState(''), [busy, setBusy] = useState(false);
  const request = useRef<AbortController | undefined>(undefined), cancel = useRef<HTMLButtonElement>(null);
  const {t} = format, word = t('server.confirm.word.delete');
  useEffect(() => () => {request.current?.abort();request.current = undefined;}, []);
  const close = () => {request.current?.abort();request.current = undefined;setTyped('');onClose();};
  const confirm = async () => {
    if (!isCurrent() || request.current || mode === 'delete' && typed !== word) return;
    const controller = request.current = new AbortController();setBusy(true);setTyped('');
    try {
      await submit(backup, controller.signal);
      if (!controller.signal.aborted && request.current === controller && isCurrent()) onClose();
    } catch (error) {
      if (!controller.signal.aborted && request.current === controller && isCurrent()) onFailure(error);
    } finally {
      if (request.current === controller) {request.current = undefined;setBusy(false);}
    }
  };
  const when = <b>{t('server.backups.restoreWhich', {when: format.dateTime(backup.createdAt)})}</b>;
  return <Dialog open={open} onOpenChange={open => {if (!open) close();}} title={t(mode === 'restore' ? 'web.maintenance.restoreTitle' : 'web.maintenance.deleteBackupTitle')}
    initialFocusRef={cancel} returnFocusRef={returnFocusRef} bodyClassName={styles.backupConfirmation}
    footer={<><Button ref={cancel} size="small" onClick={close}>{t('next.settings.cancel')}</Button><SettingsAction size="small" appearance={mode === 'delete' ? 'danger' : 'primary'} loading={busy} disabled={busy || mode === 'delete' && typed !== word} onClick={() => {void confirm();}}>{t(mode === 'restore' ? 'web.maintenance.restoreConfirm' : 'web.maintenance.deleteBackupConfirm')}</SettingsAction></>}>
    {mode === 'restore' ? <>{when}<p>{t('web.maintenance.restoreBody')}</p></> : <><p>{t('server.backups.deleteBody')}</p>{when}<TextField label={t('web.deleteAccount.typeLabel', {value: word})} value={typed} disabled={busy} autoComplete="off" onChange={event => setTyped(event.currentTarget.value)} /></>}
  </Dialog>;
}
