import {useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {backupProgressFraction, type BackupSummary} from '@core/administration.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays/Dialog';
import {useRetained} from '../../../kit/overlays';
import {BackupConfirmationDialog, BackupList, BackupPassphraseDialog, ServerFolderPicker, SettingsAction} from '../../../kit/settings';
import type {SettingsBackupSource, SettingsRestoreSelection} from '../backup-source';
import {isAuthenticationFailure} from '@core/backups.ts';
import {RestoreStatus} from './RestoreStatus';
import {protectedRestoreResult} from './restore';
import styles from './server.module.css';

const t = defaultI18n.t;

export type BackupSurfaceProps = Readonly<{
  source: SettingsBackupSource;
  subscribe: (listener: () => void) => () => void;
  isCurrent: () => boolean;
  scopeKey: string;
  reportError: (error: unknown) => void;
}>;

function useBackups(props: BackupSurfaceProps) {return useSyncExternalStore(props.subscribe, props.source.getSnapshot, props.source.getSnapshot);}

/** "Back up to": the server's own backup folder, and the drawn folder picker that changes it. */
export function StorageBackupFolder(props: BackupSurfaceProps & Readonly<{api: LibraryContentApi}>) {
  const state = useBackups(props), folder = state.settings?.backupFolder;
  const [open, setOpen] = useState(false);
  return <div className={styles.backupFolder}>
    {folder ? <code className={styles.storagePath}>{folder}</code> : <span className={styles.storagePath}>{t('next.settings.loading')}</span>}
    <Button variant="glass" size="small" disabled={!folder || state.busy || !props.isCurrent()} onClick={() => setOpen(true)}>{t('next.settings.changeFolder')}</Button>
    <ServerFolderPicker open={open} onClose={() => setOpen(false)} api={props.api} initialPath={folder} purpose="folder"
      onChoose={paths => {const path = paths[0];if (!path) return;void props.source.save({backupFolder: path}).catch(props.reportError);}} />
  </div>;
}

/** "Back up now" and the real fraction the server reports while it runs. */
export function StorageBackupNow(props: BackupSurfaceProps) {
  const state = useBackups(props), document = state.document, running = document?.running;
  const fraction = running ? backupProgressFraction(running) : undefined;
  // Restore and delete stay disabled until a running backup finishes, so one is offered at a time.
  const blocked = !document || state.busy || !!running || !!document.restore;
  return <div className={styles.backupNow}>
    <Button variant="glass" size="small" disabled={blocked || !props.isCurrent()} onClick={() => {void props.source.backupNow().catch(props.reportError);}}>
      {t(running ? 'web.maintenance.backingUp' : 'web.maintenance.backUpNow')}
    </Button>
    {running && <span className={styles.backupProgress} role="progressbar" aria-label={t('web.maintenance.backingUp')} aria-valuemin={0} aria-valuemax={100}
      aria-valuenow={fraction === undefined ? undefined : Math.round(fraction * 100)}><i style={{width: `${((fraction ?? 0) * 100).toFixed(1)}%`}} /></span>}
    {running?.kind === 'manual' && <p className={styles.storageNote} style={{flexBasis: '100%'}}>{t('web.maintenance.backupMayPause')}</p>}
    {!running && document?.lastBackup?.outcome === 'failed' && <p className={styles.backupFailure} role="alert">{t(document.lastBackup.errorCode === 'backup_busy' ? 'web.maintenance.backupBusy' : document.lastBackup.errorCode === 'insufficient_disk' ? 'web.maintenance.backupNoSpace' : 'web.console.jobFailed')}</p>}
  </div>;
}

type ProtectedSelection = Readonly<{selection: SettingsRestoreSelection; controller: AbortController}>;
type RestoreView = {active: boolean; confirmation?: object; picker?: object; choice?: object; request?: AbortController; protected?: ProtectedSelection};
function useRestoreView(props: BackupSurfaceProps) {
  const source = props.source;
  const view = useMemo<RestoreView>(() => { /* Preserve the original ownership/invalidation keys. */ void source; void props.scopeKey; return ({active: true}); }, [source, props.scopeKey]);
  useLayoutEffect(() => {
    view.active = true;
    return () => {view.active = false;view.confirmation = undefined;view.picker = undefined;view.choice = undefined;view.request?.abort();view.protected?.controller.abort();if (view.protected) source.cancelRestoreSelection(view.protected.selection);view.protected = undefined;};
  }, [source, view]);
  return view;
}
function handoff(props: BackupSurfaceProps, view: RestoreView, selection: SettingsRestoreSelection): ProtectedSelection {
  const controller = new AbortController();
  if (!view.active || !props.isCurrent()) {controller.abort();throw new DOMException('The restore view ended.', 'AbortError');}
  props.source.handoffRestoreSelection(selection, controller.signal);
  const held = {selection, controller};view.protected = held;return held;
}

/** Restore/delete operate only on the exact current confirmation and listed backup. */
export function StorageBackups(props: BackupSurfaceProps) {
  const state = useBackups(props), document = state.document, view = useRestoreView(props);
  const [confirm, setConfirm] = useState<Readonly<{mode: 'restore' | 'delete'; backup: BackupSummary; view: RestoreView}>>();
  const [protectedBackup, setProtectedBackup] = useState<ProtectedSelection>();
  const confirmation = confirm?.view === view && view.active && view.confirmation === confirm ? confirm : undefined;
  const protectedCurrent = protectedBackup && view.active && view.protected === protectedBackup ? protectedBackup : undefined;
  const retainedConfirmation = useRetained(confirmation);
  const closeConfirmation = () => {if (view.confirmation === confirm) view.confirmation = undefined;setConfirm(undefined);};
  const closeProtected = () => {if (protectedCurrent) {protectedCurrent.controller.abort();props.source.cancelRestoreSelection(protectedCurrent.selection);view.protected = undefined;}setProtectedBackup(undefined);};
  const submit = async (backup: BackupSummary, signal: AbortSignal) => {
    if (!confirmation || view.confirmation !== confirmation || !view.active || !props.isCurrent() || signal.aborted || backup !== confirmation.backup || !props.source.getSnapshot().document?.backups.includes(backup)) throw new DOMException('The backup confirmation ended.', 'AbortError');
    if (confirmation.mode === 'delete') {await props.source.remove(backup.id, signal);return;}
    const selection = await props.source.prepareRestore({backupId: backup.id}, signal);
    if (!view.active || view.confirmation !== confirmation || signal.aborted || !selection.isCurrent()) {props.source.cancelRestoreSelection(selection);throw new DOMException('The backup confirmation ended.', 'AbortError');}
    if (selection.encrypted) {setProtectedBackup(handoff(props, view, selection));return;}
    await props.source.restore(selection, undefined, signal);
  };
  const openPassphrase = async (passphrase: string, signal: AbortSignal) => {
    const held = protectedCurrent;
    if (!held || !view.active || view.protected !== held || held.controller.signal.aborted || signal.aborted) throw new DOMException('The passphrase step ended.', 'AbortError');
    try {const result = await props.source.restore(held.selection, passphrase, signal);if (!result?.staged) throw new Error('restore_not_confirmed');if (view.active && view.protected === held) closeProtected();return 'done' as const;}
    catch (error) {if (view.active && view.protected === held && !signal.aborted && protectedRestoreResult(error) === 'unopened') return 'unopened' as const;throw error;}
  };
  if (!document) return <p role="status">{t('next.settings.loading')}</p>;
  const open = (mode: 'restore' | 'delete', backup: BackupSummary) => {
    if (!view.active || !props.isCurrent() || !props.source.getSnapshot().document?.backups.includes(backup)) return;
    const next = {mode, backup, view};view.confirmation = next;setConfirm(next);
  };
  return <>
    <RestoreStatus {...props}/>
    {!document.backups.length && <p className={styles.backupEmpty}>{t('web.maintenance.noBackups')}</p>}
    <BackupList backups={document.backups} busy={state.busy || !!document.running || !!document.restore && !document.restore.errorCode} scopeKey={props.scopeKey}
      isCurrent={props.isCurrent} subscribeAuthority={props.subscribe} format={defaultI18n}
      requestRestore={backup => open('restore', backup)} requestDelete={backup => open('delete', backup)} />
    {retainedConfirmation && <BackupConfirmationDialog open={!!confirmation} mode={retainedConfirmation.mode} backup={retainedConfirmation.backup} scopeKey={props.scopeKey} isCurrent={props.isCurrent}
      subscribeAuthority={props.subscribe} format={defaultI18n} submit={submit} onClose={closeConfirmation} onFailure={error => {closeConfirmation();if (isAuthenticationFailure(error)) props.reportError(error);}} />}
    <BackupPassphraseDialog open={!!protectedCurrent} mode="restore" scopeKey={props.scopeKey} isCurrent={props.isCurrent} subscribeAuthority={props.subscribe}
      submit={openPassphrase} onDone={closeProtected} onFailure={() => {if (!props.source.getSnapshot().settings) props.reportError(new Error('backup_scope_ended'));}} onClose={closeProtected} />
  </>;
}

/** File selection, confirmation and the passphrase step each own their exact lifetime. */
export function StorageRestoreFile(props: BackupSurfaceProps & Readonly<{api: LibraryContentApi}>) {
  const state = useBackups(props), busyNow = state.busy || !!state.document?.running || !!state.document?.restore && !state.document.restore.errorCode, view = useRestoreView(props);
  const [picker, setPicker] = useState<object>(), [chosen, setChosen] = useState<{path: string; view: RestoreView}>(), [busy, setBusy] = useState(false);
  const [protectedSelection, setProtectedSelection] = useState<ProtectedSelection>();
  const cancelButton = useRef<HTMLButtonElement>(null), opener = useRef<HTMLButtonElement>(null);
  const choice = chosen?.view === view && view.active && view.choice === chosen ? chosen : undefined;
  const protectedCurrent = protectedSelection && view.active && view.protected === protectedSelection ? protectedSelection : undefined;
  const closePicker = () => {view.picker = undefined;setPicker(undefined);};
  const close = () => {view.request?.abort();view.request = undefined;view.choice = undefined;setChosen(undefined);setBusy(false);};
  const closeProtected = () => {if (protectedCurrent) {protectedCurrent.controller.abort();props.source.cancelRestoreSelection(protectedCurrent.selection);view.protected = undefined;}setProtectedSelection(undefined);};
  const restore = async () => {
    const captured = choice;
    if (!captured || !view.active || view.choice !== captured || view.request || !props.isCurrent()) return;
    const controller = new AbortController();view.request = controller;setBusy(true);
    const current = () => view.active && view.choice === captured && view.request === controller && !controller.signal.aborted && props.isCurrent();
    try {
      const selection = await props.source.prepareRestore({path: captured.path}, controller.signal);
      if (!current() || !selection.isCurrent()) {props.source.cancelRestoreSelection(selection);return;}
      if (selection.encrypted) {setProtectedSelection(handoff(props, view, selection));view.choice = undefined;setChosen(undefined);return;}
      await props.source.restore(selection, undefined, controller.signal);
      if (current()) close();
    } catch (error) {if (current()) {close();if (isAuthenticationFailure(error)) props.reportError(error);}}
    finally {if (view.request === controller) {view.request = undefined;if (view.active) setBusy(false);}}
  };
  return <>
    <Button ref={opener} variant="glass" size="small" disabled={busyNow || !props.isCurrent()} onClick={() => {if (!view.active || !props.isCurrent()) return;const next = {};view.picker = next;setPicker(next);}}>{t('next.settingsSpec.server.storage.groups.1.rows.5.control.label')}</Button>
    <ServerFolderPicker open={!!picker && view.active && view.picker === picker} onClose={closePicker} api={props.api} purpose="backup" returnFocusRef={opener}
      onChoose={paths => {if (!view.active || !picker || view.picker !== picker || !props.isCurrent()) return;const path = paths[0];if (path) {const next = {path, view};view.choice = next;setChosen(next);}closePicker();}} />
    <Dialog open={!!choice} onOpenChange={open => {if (!open) close();}} title={t('web.maintenance.restoreFileTitle')} initialFocusRef={cancelButton} returnFocusRef={opener}
      footer={<><Button ref={cancelButton} size="small" onClick={close}>{t('next.settings.cancel')}</Button><SettingsAction size="small" appearance="primary" loading={busy} disabled={busy} onClick={() => {void restore();}}>{t('web.maintenance.restoreConfirm')}</SettingsAction></>}>
      <p>{t('web.maintenance.restoreBody')}</p>
    </Dialog>
    <BackupPassphraseDialog open={!!protectedCurrent} mode="restore" scopeKey={props.scopeKey} isCurrent={props.isCurrent} subscribeAuthority={props.subscribe}
      submit={async (passphrase, signal) => {
        const held = protectedCurrent;
        if (!held || !view.active || view.protected !== held || held.controller.signal.aborted || signal.aborted) throw new DOMException('The passphrase step ended.', 'AbortError');
        try {const result = await props.source.restore(held.selection, passphrase, signal);if (!result?.staged) throw new Error('restore_not_confirmed');if (view.active && view.protected === held) closeProtected();return 'done' as const;}
        catch (error) {if (view.active && view.protected === held && !signal.aborted && protectedRestoreResult(error) === 'unopened') return 'unopened' as const;throw error;}
      }}
      onDone={closeProtected} onFailure={() => {if (!props.source.getSnapshot().settings) props.reportError(new Error('backup_scope_ended'));}} onClose={closeProtected} />
  </>;
}
