import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {BackupSummary} from '../../../../../../packages/client-core/src/administration';
import {notMadeHere, passphraseRefused, passphraseTooLong, scopeEnded, ServerBackupSource, type BackupInspection, type RestoreTarget} from '../../../../../../packages/client-core/src/server-admin/server-backups';
import {restoreReady, sentAsConfirmed} from '../../../../../../packages/client-core/src/server-admin/restore-gate';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {nativeStorageOwner} from '../../../screens/settings-live/server/storage/space-source';

type Translate = typeof defaultI18n.t;

/** One owner-scoped reader of `GET /v1/admin/backups` per page: the same `ServerBackupSource` the web's Storage page uses. */
function createBackups(context: NativeShellBootstrapContext, reportError: NativeShellRuntime['reportError']) {
  const listeners = new Set<() => void>(), current = () => nativeStorageOwner(context);
  const source = new ServerBackupSource(context.api, current, () => {for (const listener of listeners) listener();});
  return {context, source, current, api: context.api,
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    // Signed out or no longer the owner: the source has ended itself, and the shell opens sign-in.
    failure: (error: unknown) => {if (scopeEnded(error)) reportError(error);},
    dispose: () => {source.dispose(); listeners.clear();}};
}
export type BackupGroup = ReturnType<typeof createBackups>;
/** The server's own words for a refusal, or a general one. */
const message = (error: unknown, t: Translate) => {const text = (error as {message?: unknown} | null)?.message; return typeof text === 'string' && text ? text : t('error.unconfirmedChange');};

/** Storage › Backups on the rebuilt server: folder, Back up now, the list with Restore and Delete, a restore that waits for a
 * restart, restore from a file, and (under Advanced) artwork and the passphrase. The web's Backups.tsx draws the same. */
export function useBackupGroup({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const [group, setGroup] = useState<BackupGroup>(), context = ports.context, reportError = runtime.reportError;
  useLayoutEffect(() => {const next = createBackups(context, reportError); setGroup(next); return next.dispose;}, [context, reportError]);
  return group?.context === context ? group : undefined;
}


export function useStorageBackups({group, t}: {group: BackupGroup; t: Translate}) {
  const {source} = group, state = useSyncExternalStore(group.subscribe, source.getSnapshot, source.getSnapshot), backups = state.backups;
  const [problem, setProblem] = useState<string>(), [menu, setMenu] = useState<BackupSummary>(), [deleting, setDeleting] = useState<BackupSummary>(), [typed, setTyped] = useState('');
  const [picking, setPicking] = useState<'folder' | 'restore'>(), [restoring, setRestoring] = useState<Readonly<{target: RestoreTarget; when?: string; protected?: boolean}>>();
  useEffect(() => {void source.load().catch(error => {group.failure(error); if (group.current()) setProblem(message(error, t));});}, [group, source, t]);
  const act = (work: () => Promise<unknown>, after?: () => void) => {
    if (!group.current()) return;
    setProblem(undefined);
    void work().then(() => {if (group.current()) after?.();}, error => {group.failure(error); if (group.current()) setProblem(message(error, t));});
  };
  const running = !!backups?.running, waiting = !!backups?.pendingRestore, busy = state.busy || running || waiting;
  const current = (backup: BackupSummary) => group.current() && source.getSnapshot().list.includes(backup);
  return {
    state, backups, running, waiting, busy, problem, menu, deleting, typed, picking, restoring,
    load: () => act(() => source.load()),
    choose: (backup: BackupSummary) => {if (current(backup)) setMenu(backup);},
    closeMenu: () => setMenu(undefined),
    openDelete: () => {
      if (!menu || !current(menu)) return;
      setTyped(''); setDeleting(menu);
    },
    openRestore: () => {
      if (menu && current(menu)) setRestoring({target: {id: menu.id}, when: defaultI18n.dateTime(menu.createdAt), protected: menu.encrypted === true});
    },
    cancelDelete: () => setDeleting(undefined), setTyped,
    remove: () => {if (deleting && current(deleting) && typed.trim() === 'delete') act(() => source.remove(deleting.id), () => setDeleting(undefined));},
    setPicking,
    usePath: (path?: string) => {
      const purpose = picking; setPicking(undefined);
      if (!path || !group.current()) return;
      if (purpose === 'folder') act(() => source.setFolder(path));
      else if (purpose === 'restore') setRestoring({target: {path}});
    },
    closeRestore: () => setRestoring(undefined),
    backupNow: () => act(() => source.backupNow()),
    saveSettings: (change: Parameters<typeof source.saveSettings>[0]) => act(() => source.saveSettings(change)),
  };
}

export function useBackupPassphrase({group, t}: {group: BackupGroup; t: Translate}) {
  const {source} = group, state = useSyncExternalStore(group.subscribe, source.getSnapshot, source.getSnapshot), on = state.backups?.protected === true;
  const [editing, setEditing] = useState<'set' | 'change'>(), [stopping, setStopping] = useState(false), [phrase, setPhrase] = useState(''), [repeat, setRepeat] = useState('');
  const [refused, setRefused] = useState<string>(), [busy, setBusy] = useState(false), request = useRef<AbortController>(undefined);
  useEffect(() => () => request.current?.abort(), []);
  const disabled = !state.backups || state.busy || !!state.backups.running;
  const close = () => {request.current?.abort(); setPhrase(''); setRepeat(''); setRefused(undefined); setEditing(undefined); setStopping(false);};
  const send = async (sent: string) => {
    const controller = request.current = new AbortController();
    setPhrase(''); setRepeat(''); setRefused(undefined); setBusy(true);
    try {await source.setPassphrase(sent, controller.signal); if (!controller.signal.aborted) close();}
    catch (error) {group.failure(error); if (!controller.signal.aborted) setRefused(passphraseRefused(error) ? t('server.backups.passphraseTooLong') : message(error, t));}
    finally {if (request.current === controller) {request.current = undefined; setBusy(false);}}
  };
  const tooLong = passphraseTooLong(phrase), mismatch = repeat !== '' && repeat !== phrase, ready = !busy && phrase !== '' && phrase === repeat && !tooLong;
  return {
    on, disabled, editing, stopping, phrase, repeat, tooLong, mismatch, ready, busy, refused, setEditing, setStopping, setPhrase, setRepeat, close,
    save: () => {if (ready && group.current()) void send(phrase);},
    turnOff: () => {if (group.current() && !busy) void send('');},
  };
}

export function usePendingRestore({group, t}: {group: BackupGroup; t: Translate}) {
  const {source} = group, state = useSyncExternalStore(group.subscribe, source.getSnapshot, source.getSnapshot), pending = state.backups?.pendingRestore;
  const [asks, setAsks] = useState<Readonly<{playing: number; canRestart: boolean}>>(), [confirm, setConfirm] = useState(false), [error, setError] = useState<string>();
  const ask = async () => {const now = await source.restartAsks(); if (group.current()) setAsks(now); return now;};
  const key = pending ? `${pending.backup}|${pending.stagedAt}` : '';
  useEffect(() => {setAsks(undefined); if (key) void ask().catch(() => undefined);
    // Asked again for each restore that waits.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  const act = (work: () => Promise<unknown>) => {setError(undefined); void work().catch(e => {group.failure(e); if (group.current()) setError(message(e, t));});};
  return {
    state, pending, asks, confirm, setConfirm, error,
    askRestart: () => act(async () => {if ((await ask()).canRestart && group.current()) setConfirm(true);}),
    cancelRestore: () => act(() => source.cancelRestore()),
    restart: () => {
      setConfirm(false);
      act(async () => {if (!await source.restart() && group.current()) setAsks({playing: asks?.playing ?? 0, canRestart: false});});
    },
  };
}

export function useBackupRestore({group, t, target, locked: listedLocked, onClose}: {group: BackupGroup; t: Translate; target?: RestoreTarget; locked?: boolean; onClose: () => void}) {
  const {source} = group;
  const [inspection, setInspection] = useState<BackupInspection>(), [refused, setRefused] = useState<string>(), [warning, setWarning] = useState<string>();
  const [password, setPassword] = useState(''), [code, setCode] = useState(''), [typed, setTyped] = useState(''), [locked, setLocked] = useState(false), [phrase, setPhrase] = useState('');
  const [busy, setBusy] = useState(false), request = useRef<AbortController>(undefined);
  useEffect(() => {
    setInspection(undefined); setRefused(undefined); setWarning(undefined); setPassword(''); setCode(''); setTyped(''); setPhrase(''); setLocked(!!listedLocked);
    if (!target || listedLocked) return;
    const controller = new AbortController();
    source.inspect(target, controller.signal).then(found => {if (!controller.signal.aborted) setInspection(found);},
      error => {if (controller.signal.aborted) return; group.failure(error); if (passphraseRefused(error)) setLocked(true); else setRefused(message(error, t));});
    return () => controller.abort();
  }, [target, listedLocked, source, group, t]);
  useEffect(() => () => request.current?.abort(), []);
  const close = () => {request.current?.abort(); setPhrase(''); setPassword(''); onClose();};
  // A wrong phrase and damage in the backup's first part look the same to the server, so the words name both.
  const unopened = () => {setInspection(undefined); setPhrase(''); setRefused(t('next.settings.backup.unopened'));};
  const run = async (work: (signal: AbortSignal) => Promise<void>, onError: (error: unknown) => void) => {
    if (busy || !group.current()) return;
    const controller = request.current = new AbortController(); setBusy(true); setRefused(undefined);
    try {await work(controller.signal);} catch (error) {group.failure(error); if (!controller.signal.aborted) onError(error);}
    finally {if (request.current === controller) {request.current = undefined; setBusy(false);}}
  };
  const open = () => {if (target && phrase) void run(async signal => {const found = await source.inspect(target, signal, phrase); if (!signal.aborted) setInspection(found);},
    error => {if (passphraseRefused(error)) unopened(); else setRefused(message(error, t));});};
  const word = t('server.backups.restoreWord');
  const elsewhere = !!inspection && sentAsConfirmed({madeHere: inspection.madeHere, warned: warning !== undefined});
  const ready = restoreReady({checked: !!inspection, busy, password, typed, word}) && (!locked || phrase !== '');
  const restore = () => {
    if (!target || !ready) return;
    const sent = locked ? phrase : undefined;
    void run(async signal => {
      await source.restore(target, {password, code: code.trim() || undefined, notMadeHereConfirmed: elsewhere, passphrase: sent}, signal);
      if (!signal.aborted) close();
    }, error => {
      const warned = notMadeHere(error);
      // The phrase was sent, so a refused restore asks for it again rather than keeping it.
      if (passphraseRefused(error)) unopened(); else if (warned !== undefined) setWarning(warned); else setRefused(message(error, t));
      setPassword(''); if (sent !== undefined) setPhrase('');
    });
  };
  return {inspection, refused, warning, password, code, typed, locked, phrase, busy, close, open, word, elsewhere, ready, restore,
    setPassword, setCode, setTyped, setPhrase};
}
