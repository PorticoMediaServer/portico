import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {BackupSummary} from '@core/administration.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {notMadeHere, passphraseRefused, passphraseTooLong, type BackupInspection, type RestoreTarget, type ServerBackupSource} from '@core/server-admin/server-backups.ts';
import {Button, Switch, TextField} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import {useRetained} from '../../../kit/overlays';
import {BackupConfirmationDialog, BackupList, ServerFolderPicker, SettingsAction} from '../../../kit/settings';
import {restoreReady, sentAsConfirmed} from '@core/server-admin/restore-gate.ts';
import styles from './server.module.css';

const t = defaultI18n.t;

export type BackupSurfaceProps = Readonly<{
  source: ServerBackupSource;
  subscribe: (listener: () => void) => () => void;
  isCurrent: () => boolean;
  scopeKey: string;
  reportError: (error: unknown) => void;
}>;

function useBackups(props: BackupSurfaceProps) {return useSyncExternalStore(props.subscribe, props.source.getSnapshot, props.source.getSnapshot);}
/** The server's own words for a refusal, or a general one. */
const message = (error: unknown) => {const text = (error as {message?: unknown} | null)?.message;return typeof text === 'string' && text ? text : t('error.unconfirmedChange');};

/** "Back up to": the server's backup folder, and the folder picker that changes it. */
export function StorageBackupFolder(props: BackupSurfaceProps & Readonly<{api: LibraryContentApi}>) {
  const state = useBackups(props), folder = state.backups?.folder;
  const [open, setOpen] = useState(false);
  return <div className={styles.backupFolder}>
    {folder ? <code className={styles.storagePath}>{folder}</code> : <span className={styles.storagePath}>{t('next.settings.loading')}</span>}
    <Button variant="glass" size="small" disabled={!folder || state.busy || !props.isCurrent()} onClick={() => setOpen(true)}>{t('next.settings.changeFolder')}</Button>
    <ServerFolderPicker open={open} onClose={() => setOpen(false)} api={props.api} initialPath={folder} purpose="folder"
      onChoose={paths => {const path = paths[0];if (path) void props.source.setFolder(path).catch(props.reportError);}} />
    {state.backups && !state.backups.folderWritable && <p className={styles.backupFailure} role="alert" style={{flexBasis: '100%'}}>{t('server.backups.folderUnwritable')}</p>}
  </div>;
}

/** "Back up now", and why the last scheduled backup failed until one succeeds. */
export function StorageBackupNow(props: BackupSurfaceProps) {
  const state = useBackups(props), backups = state.backups, running = !!backups?.running;
  const [failed, setFailed] = useState<string>();
  return <div className={styles.backupNow}>
    <Button variant="glass" size="small" disabled={!backups || state.busy || running || !!backups.pendingRestore || !props.isCurrent()}
      onClick={() => {setFailed(undefined);void props.source.backupNow().catch(error => {if (props.isCurrent()) setFailed(message(error));});}}>
      {t(running ? 'web.maintenance.backingUp' : 'web.maintenance.backUpNow')}
    </Button>
    {running && <span className={styles.backupProgress} role="progressbar" aria-label={t('web.maintenance.backingUp')}><i style={{width: '100%'}} /></span>}
    {!running && (failed || backups?.lastError) && <p className={styles.backupFailure} role="alert">{failed ?? t('server.backups.lastFailed', {reason: backups!.lastError!})}</p>}
  </div>;
}

/**
 * "Protect backups with a passphrase": how the server makes its next backups. Turning it on, or Change, asks for the
 * phrase twice; turning it off asks first. Backups already made keep their own protection, which the list shows.
 */
export function StorageBackupPassphrase(props: BackupSurfaceProps & Readonly<{label: string}>) {
  const state = useBackups(props), on = state.backups?.protected === true;
  const [editing, setEditing] = useState<'set' | 'change'>(), [stopping, setStopping] = useState(false);
  const disabled = !state.backups || state.busy || !!state.backups.running || !props.isCurrent();
  return <div className={styles.backupPassphrase}>
    {/* "Change…" as on Back up to, so the row's own label keeps its room at phone width. */}
    {on && <Button variant="glass" size="small" disabled={disabled} aria-label={t('next.settings.backup.changeTitle')} onClick={() => setEditing('change')}>{t('next.settings.changeFolder')}</Button>}
    <Switch aria-label={props.label} checked={on} disabled={disabled} onCheckedChange={next => {if (next) setEditing('set'); else setStopping(true);}} />
    <PassphraseDialog {...props} mode={editing} onClose={() => setEditing(undefined)} />
    <StopProtectingDialog {...props} open={stopping} onClose={() => setStopping(false)} />
  </div>;
}

/** The phrase twice. It lives only in these two fields until Save sends it, and the fields empty as it is sent. */
function PassphraseDialog(props: BackupSurfaceProps & Readonly<{mode?: 'set' | 'change'; onClose: () => void}>) {
  const {mode, onClose} = props;
  const [phrase, setPhrase] = useState(''), [repeat, setRepeat] = useState(''), [refused, setRefused] = useState<string>(), [busy, setBusy] = useState(false);
  const request = useRef<AbortController>(undefined), cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => {setPhrase('');setRepeat('');setRefused(undefined);}, [mode]);
  useEffect(() => () => request.current?.abort(), []);
  const tooLong = passphraseTooLong(phrase), mismatch = repeat !== '' && repeat !== phrase;
  const ready = !busy && phrase !== '' && phrase === repeat && !tooLong;
  const close = () => {request.current?.abort();setPhrase('');setRepeat('');onClose();};
  const save = async () => {
    if (!ready || !props.isCurrent()) return;
    const sent = phrase, controller = request.current = new AbortController();
    setPhrase('');setRepeat('');setRefused(undefined);setBusy(true);
    try {await props.source.setPassphrase(sent, controller.signal);if (!controller.signal.aborted) onClose();}
    catch (error) {if (!controller.signal.aborted) setRefused(passphraseRefused(error) ? t('server.backups.passphraseTooLong') : message(error));}
    finally {if (request.current === controller) {request.current = undefined;setBusy(false);}}
  };
  return <Dialog footerDismisses open={!!mode} onOpenChange={open => {if (!open) close();}} initialFocusRef={cancel}
    title={t(mode === 'change' ? 'next.settings.backup.changeTitle' : 'next.settings.backup.protectTitle')}
    footer={<><Button ref={cancel} size="small" onClick={close}>{t('next.settings.cancel')}</Button>
      <SettingsAction size="small" appearance="primary" loading={busy} disabled={!ready} onClick={() => {void save();}}>{t(mode === 'change' ? 'server.backups.savePassphrase' : 'next.settings.backup.protect')}</SettingsAction></>}>
    <p className={styles.storageNote}>{t('server.backups.passphraseFuture')}</p>
    <TextField label={t('next.settings.backup.passphrase')} type="password" value={phrase} disabled={busy} autoComplete="new-password"
      error={tooLong ? t('server.backups.passphraseTooLong') : undefined} onChange={event => setPhrase(event.currentTarget.value)} />
    <TextField label={t('next.settings.backup.repeat')} type="password" value={repeat} disabled={busy} autoComplete="new-password"
      error={mismatch ? t('next.settings.backup.mismatch') : undefined} onChange={event => setRepeat(event.currentTarget.value)} />
    {refused && <p role="alert" className={styles.backupFailure}>{refused}</p>}
  </Dialog>;
}

/** Turning protection off for the next backups, asked first. */
function StopProtectingDialog(props: BackupSurfaceProps & Readonly<{open: boolean; onClose: () => void}>) {
  const {open, onClose} = props;
  const [refused, setRefused] = useState<string>(), [busy, setBusy] = useState(false), request = useRef<AbortController>(undefined), cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => {setRefused(undefined);}, [open]);
  useEffect(() => () => request.current?.abort(), []);
  const close = () => {request.current?.abort();onClose();};
  const stop = async () => {
    if (busy || !props.isCurrent()) return;
    const controller = request.current = new AbortController();setRefused(undefined);setBusy(true);
    try {await props.source.setPassphrase('', controller.signal);if (!controller.signal.aborted) onClose();}
    catch (error) {if (!controller.signal.aborted) setRefused(message(error));}
    finally {if (request.current === controller) {request.current = undefined;setBusy(false);}}
  };
  return <Dialog footerDismisses open={open} onOpenChange={next => {if (!next) close();}} initialFocusRef={cancel} title={t('server.backups.passphraseOffTitle')}
    footer={<><Button ref={cancel} size="small" onClick={close}>{t('next.settings.cancel')}</Button>
      <SettingsAction size="small" appearance="danger" loading={busy} disabled={busy} onClick={() => {void stop();}}>{t('server.backups.passphraseOff')}</SettingsAction></>}>
    <p>{t('server.backups.passphraseOffBody')}</p>
    {refused && <p role="alert" className={styles.backupFailure}>{refused}</p>}
  </Dialog>;
}

/** A restore that waits for a restart: Restart now (asking first, with how many are playing) or Cancel. */
function PendingRestore(props: BackupSurfaceProps) {
  const state = useBackups(props), pending = state.backups?.pendingRestore;
  // What a restart would do is asked of the server, never taken from what this page saw when it staged the
  // restore: another tab, another device or a reload staged nothing here (rule 64).
  const [asks, setAsks] = useState<Readonly<{playing: number; canRestart: boolean}>>();
  const [confirm, setConfirm] = useState(false), [error, setError] = useState<string>();
  const ask = async () => {const now = await props.source.restartAsks();if (props.isCurrent()) setAsks(now);return now;};
  useEffect(() => {setAsks(undefined);if (pending) void ask().catch(() => undefined);
    // Asked again for each restore that waits.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pending?.backup, pending?.stagedAt]);
  if (state.restarting) return <p role="status" className={styles.storageNote}>{t('server.backups.restarting')}</p>;
  if (!pending) return null;
  const act = (work: () => Promise<unknown>) => {setError(undefined);void work().catch(e => {if (props.isCurrent()) setError(message(e));});};
  return <div className={styles.restoreStatus} role="status">
    <b>{t('server.backups.pending', {name: pending.backup})}</b>
    {asks && !asks.canRestart && <p className={styles.storageNote}>{t('server.backups.restartByHand')}</p>}
    <div>
      {asks?.canRestart && <SettingsAction size="small" appearance="primary" disabled={state.busy} onClick={() => act(async () => {if ((await ask()).canRestart && props.isCurrent()) setConfirm(true);})}>{t('server.backups.restartNow')}</SettingsAction>}
      <Button size="small" variant="glass" disabled={state.busy} onClick={() => act(() => props.source.cancelRestore())}>{t('server.backups.cancelRestore')}</Button>
    </div>
    {error && <p role="alert" className={styles.backupFailure}>{error}</p>}
    <Dialog footerDismisses open={confirm && !!asks} onOpenChange={open => {if (!open) setConfirm(false);}} title={t('server.backups.restartTitle')}
      footer={<><Button size="small" onClick={() => setConfirm(false)}>{t('next.settings.cancel')}</Button>
        <SettingsAction size="small" appearance="primary" disabled={state.busy} onClick={() => {setConfirm(false);act(async () => {if (!await props.source.restart() && props.isCurrent()) setAsks({playing: asks?.playing ?? 0, canRestart: false});});}}>{t('server.backups.restartNow')}</SettingsAction></>}>
      <p>{t('server.backups.restartBody', {count: asks?.playing ?? 0})}</p>
    </Dialog>
  </div>;
}

/**
 * Restoring one backup: the server checks it whole and says what restoring does; the owner gives their
 * password (and two-step code), and types the word for a backup this server did not make. The restore is
 * then made at the next start. A protected backup is opened with its passphrase first: a listed one says so
 * itself, a chosen file says so when the check is refused for it. The phrase stays in its masked field only
 * until the restore sends it.
 */
function RestoreDialog(props: BackupSurfaceProps & Readonly<{target?: RestoreTarget; when?: string; protected?: boolean; onClose: () => void}>) {
  const {target, onClose} = props;
  const [inspection, setInspection] = useState<BackupInspection>(), [refused, setRefused] = useState<string>();
  const [password, setPassword] = useState(''), [code, setCode] = useState(''), [typed, setTyped] = useState(''), [warning, setWarning] = useState<string>();
  const [locked, setLocked] = useState(false), [phrase, setPhrase] = useState('');
  const [busy, setBusy] = useState(false), request = useRef<AbortController>(undefined), cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    setInspection(undefined);setRefused(undefined);setPassword('');setCode('');setTyped('');setWarning(undefined);setPhrase('');setLocked(!!props.protected);
    if (!target || props.protected) return;
    const controller = new AbortController();
    props.source.inspect(target, controller.signal).then(found => {if (!controller.signal.aborted) setInspection(found);},
      error => {if (controller.signal.aborted) return;if (passphraseRefused(error)) setLocked(true); else setRefused(message(error));});
    return () => controller.abort();
  }, [target, props.protected, props.source]);
  useEffect(() => () => {request.current?.abort();}, []);
  // Wrong phrase and damage in the backup's first part look the same to the server, so the words name both.
  const unopened = () => {setInspection(undefined);setPhrase('');setRefused(t('next.settings.backup.unopened'));};
  const open = async () => {
    if (!target || !phrase || busy || !props.isCurrent()) return;
    const controller = request.current = new AbortController();setBusy(true);setRefused(undefined);
    try {const found = await props.source.inspect(target, controller.signal, phrase);if (!controller.signal.aborted) setInspection(found);}
    catch (error) {if (!controller.signal.aborted) {if (passphraseRefused(error)) unopened(); else setRefused(message(error));}}
    finally {if (request.current === controller) {request.current = undefined;setBusy(false);}}
  };
  const word = t('server.backups.restoreWord');
  // A backup not made here also shows the warning (the check's, or the server's on the first press).
  const elsewhere = !!inspection && sentAsConfirmed({madeHere: inspection.madeHere, warned: warning !== undefined});
  const ready = restoreReady({checked: !!inspection, busy, password, typed, word}) && (!locked || phrase !== '');
  const restore = async () => {
    if (!target || !ready || !props.isCurrent()) return;
    const sent = locked ? phrase : undefined;
    const controller = request.current = new AbortController();setBusy(true);setRefused(undefined);
    try {
      await props.source.restore(target, {password, code: code.trim() || undefined, notMadeHereConfirmed: elsewhere, passphrase: sent}, controller.signal);
      if (!controller.signal.aborted) {setPassword('');setPhrase('');onClose();}
    } catch (error) {
      if (controller.signal.aborted) return;
      const warned = notMadeHere(error);
      // The phrase was sent, so a refused restore asks for it again rather than keeping it.
      if (passphraseRefused(error)) unopened(); else if (warned !== undefined) setWarning(warned); else setRefused(message(error));
      setPassword('');if (sent !== undefined) setPhrase('');
    } finally {if (request.current === controller) {request.current = undefined;setBusy(false);}}
  };
  return <Dialog footerDismisses open={!!target} onOpenChange={open => {if (!open) {request.current?.abort();setPhrase('');onClose();}}} title={t(target && 'path' in target ? 'web.maintenance.restoreFileTitle' : 'web.maintenance.restoreTitle')}
    initialFocusRef={cancel}
    footer={<><Button ref={cancel} size="small" onClick={() => {request.current?.abort();setPhrase('');onClose();}}>{t('next.settings.cancel')}</Button>
      <SettingsAction size="small" appearance="primary" loading={busy} disabled={!ready} onClick={() => {void restore();}}>{t('web.maintenance.restoreConfirm')}</SettingsAction></>}>
    {props.when && <b>{t('server.backups.restoreWhich', {when: props.when})}</b>}
    {!inspection && !refused && !locked && <p role="status">{t('server.backups.checking')}</p>}
    {locked && !inspection && <>
      <p className={styles.storageNote}><b>{t('next.settings.backup.restoreTitle')}</b> {t('server.backups.passphraseNeeded')}</p>
      <div className={styles.backupOpen}>
        <TextField label={t('next.settings.backup.passphrase')} type="password" value={phrase} disabled={busy} autoComplete="off"
          onChange={event => setPhrase(event.currentTarget.value)} onKeyDown={event => {if (event.key === 'Enter') {event.preventDefault();void open();}}} />
        <SettingsAction size="small" loading={busy} disabled={busy || !phrase} onClick={() => {void open();}}>{t('server.backups.openBackup')}</SettingsAction>
      </div>
    </>}
    {inspection && <ul className={styles.storageNote}>{inspection.restoring.map(line => <li key={line}>{t(`server.backups.restoring.${line}` as MessageId)}</li>)}</ul>}
    {elsewhere && <p className={styles.backupFailure}>{warning || t('server.backups.notMadeHere')}</p>}
    {inspection && <>
      <TextField label={t('web.deleteAccount.typeLabel', {value: word})} value={typed} disabled={busy} autoComplete="off" onChange={event => setTyped(event.currentTarget.value)} />
      {locked && <TextField label={t('next.settings.backup.passphrase')} type="password" value={phrase} disabled={busy} autoComplete="off" onChange={event => setPhrase(event.currentTarget.value)} />}
      <TextField label={t('server.backups.password')} type="password" value={password} disabled={busy} autoComplete="current-password" onChange={event => setPassword(event.currentTarget.value)} />
      <TextField label={t('server.backups.code')} value={code} disabled={busy} autoComplete="one-time-code" inputMode="numeric" onChange={event => setCode(event.currentTarget.value)} />
    </>}
    {refused && <p role="alert" className={styles.backupFailure}>{refused}</p>}
  </Dialog>;
}

/** The listed backups with Restore and Delete, and a restore waiting for a restart. */
export function StorageBackups(props: BackupSurfaceProps) {
  const state = useBackups(props), backups = state.backups;
  const [deleting, setDeleting] = useState<BackupSummary>(), [restoring, setRestoring] = useState<Readonly<{target: RestoreTarget; when: string; protected: boolean}>>();
  const retained = useRetained(deleting);
  if (!backups) return <p role="status">{t('next.settings.loading')}</p>;
  const busy = state.busy || !!backups.running || !!backups.pendingRestore;
  return <>
    <PendingRestore {...props} />
    {!state.list.length && <p className={styles.backupEmpty}>{t('web.maintenance.noBackups')}</p>}
    <BackupList backups={state.list} busy={busy} scopeKey={props.scopeKey} isCurrent={props.isCurrent} subscribeAuthority={props.subscribe} format={defaultI18n}
      requestRestore={backup => setRestoring({target: {id: backup.id}, when: defaultI18n.dateTime(backup.createdAt), protected: backup.encrypted === true})} requestDelete={setDeleting} />
    {retained && <BackupConfirmationDialog open={!!deleting} mode="delete" backup={retained} scopeKey={props.scopeKey} isCurrent={props.isCurrent}
      subscribeAuthority={props.subscribe} format={defaultI18n} submit={(backup, signal) => props.source.remove(backup.id, signal)}
      onClose={() => setDeleting(undefined)} onFailure={error => {setDeleting(undefined);props.reportError(error);}} />}
    <RestoreDialog {...props} target={restoring?.target} when={restoring?.when} protected={restoring?.protected} onClose={() => setRestoring(undefined)} />
  </>;
}

/** Restore from a backup file chosen in the folder explorer. */
export function StorageRestoreFile(props: BackupSurfaceProps & Readonly<{api: LibraryContentApi}>) {
  const state = useBackups(props), busy = state.busy || !!state.backups?.running || !!state.backups?.pendingRestore;
  const [picking, setPicking] = useState(false), [target, setTarget] = useState<RestoreTarget>(), opener = useRef<HTMLButtonElement>(null);
  return <>
    <Button ref={opener} variant="glass" size="small" disabled={busy || !props.isCurrent()} onClick={() => setPicking(true)}>{t('next.settingsSpec.server.storage.groups.1.rows.5.control.label')}</Button>
    <ServerFolderPicker open={picking} onClose={() => setPicking(false)} api={props.api} purpose="backup" returnFocusRef={opener}
      onChoose={paths => {setPicking(false);if (paths[0]) setTarget({path: paths[0]});}} />
    <RestoreDialog {...props} target={target} onClose={() => setTarget(undefined)} />
  </>;
}
