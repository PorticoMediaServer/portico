import {useEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {Button, TextField} from '../controls';
import {Dialog} from '../overlays/Dialog';
import {usePresence} from '../overlays/shared';
import styles from './settings.module.css';

const t = defaultI18n.t;
export type BackupPassphraseDialogProps = Readonly<{
  open: boolean; mode: 'protect' | 'change' | 'restore'; onClose: () => void;
  scopeKey: string;
  isCurrent: () => boolean;
  subscribeAuthority: (listener: () => void) => () => void;
  /** `unopened` is the honest state when a protected restore refuses: the server cannot say
   *  whether the passphrase or the archive is wrong (v2 Root §1), so nothing is marked wrong. */
  submit: (passphrase: string, signal: AbortSignal) => Promise<'done' | 'wrong-passphrase' | 'unopened'>;
  onDone: () => void;
  /** No raw error or secret leaves this password surface. The parent shows its approved failure state. */
  onFailure: () => void;
  returnFocusRef?: RefObject<HTMLElement | null>;
}>;

/** Ruling §17's controlled password step; no persistence, logging or optimistic switch mutation. */
export function BackupPassphraseDialog(props: BackupPassphraseDialogProps) {
  const current = useSyncExternalStore(props.subscribeAuthority, props.isCurrent, props.isCurrent);
  const present = usePresence(props.open, 160);
  return current && present ? <Passphrase key={JSON.stringify([props.scopeKey, props.mode])} {...props} /> : null;
}

function Passphrase({open, mode, onClose, submit, onDone, onFailure, returnFocusRef, isCurrent}: BackupPassphraseDialogProps) {
  const [first, setFirst] = useState(''), [second, setSecond] = useState(''), [error, setError] = useState<'mismatch' | 'incorrect' | 'unopened'>(), [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null), request = useRef<AbortController | undefined>(undefined);
  const erase = () => {setFirst('');setSecond('');setError(undefined);};
  const close = () => {request.current?.abort();request.current = undefined;erase();setBusy(false);onClose();};
  useEffect(() => {
    if (!open) {request.current?.abort();request.current = undefined;erase();setBusy(false);}
    return () => {request.current?.abort();request.current = undefined;};
  }, [open, mode]);
  const bytes = new TextEncoder().encode(first).byteLength;
  const ready = bytes > 0 && bytes <= 1024 && (mode === 'restore' || bytes >= 12 && second.length > 0);
  const save = async () => {
    if (!isCurrent() || !open || !ready || request.current) return;
    if (mode !== 'restore' && first !== second) {setError('mismatch');return;}
    const controller = request.current = new AbortController(), passphrase = first;
    erase();setBusy(true);
    try {
      const result = await submit(passphrase, controller.signal);
      if (controller.signal.aborted || request.current !== controller || !isCurrent()) return;
      if (result === 'wrong-passphrase' && mode === 'restore') setError('incorrect');
      else if (result === 'unopened' && mode === 'restore') setError('unopened');
      else if (result === 'done') {onDone();onClose();}
      else {onClose();onFailure();}
    } catch {
      if (!controller.signal.aborted && request.current === controller && isCurrent()) {onClose();onFailure();}
    } finally {
      if (request.current === controller) {request.current = undefined;setBusy(false);}
    }
  };
  return <Dialog open={open} onOpenChange={value => {if (!value) close();}} initialFocusRef={input} returnFocusRef={returnFocusRef}
    title={t(mode === 'protect' ? 'next.settings.backup.protectTitle' : mode === 'change' ? 'next.settings.backup.changeTitle' : 'next.settings.backup.restoreTitle')}
    footer={<><Button size="small" onClick={close}>{t('next.settings.cancel')}</Button><Button size="small" variant="primary" loading={busy} disabled={!ready} onClick={() => {void save();}}>{t(mode === 'protect' ? 'next.settings.backup.protect' : mode === 'change' ? 'action.save' : 'next.settings.backup.restore')}</Button></>}>
    <form className={styles.passphraseFields} onSubmit={event => {event.preventDefault();void save();}}>
      <TextField ref={input} label={t('next.settings.backup.passphrase')} type="password" autoComplete="off" value={first} disabled={busy}
        onChange={event => {setFirst(event.currentTarget.value);setError(undefined);}} error={error === 'incorrect' ? t('next.settings.backup.incorrect') : undefined} />
      {mode !== 'restore' && <TextField label={t('next.settings.backup.repeat')} type="password" autoComplete="off" value={second} disabled={busy}
        onChange={event => {setSecond(event.currentTarget.value);setError(undefined);}} error={error === 'mismatch' ? t('next.settings.backup.mismatch') : undefined} />}
      {error === 'unopened' && <p className={styles.passphraseNotice} role="alert">{t('next.settings.backup.unopened')}</p>}
    </form>
  </Dialog>;
}
