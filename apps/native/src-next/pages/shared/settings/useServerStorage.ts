import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import type {BackupSummary} from '../../../../../../packages/client-core/src/administration';
import {notMadeHere, passphraseRefused, passphraseTooLong, scopeEnded, ServerBackupSource, type BackupInspection, type BackupSettings, type RestoreTarget} from '../../../../../../packages/client-core/src/server-admin/server-backups';
import {restoreReady, sentAsConfirmed} from '../../../../../../packages/client-core/src/server-admin/restore-gate';
import {when} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {SettingsCacheSource} from '../../../../../../packages/client-core/src/server-admin/cache-source';
import {readNoticesKeep, saveNoticesKeep, storageLineWords, type NoticesKeep} from '../../../../../../packages/client-core/src/server-admin/server-storage';
import {withGuardedRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import {settingsBinding, readSettingsBinding, settingsBindingPatch} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsRowIndex, settingsSpec} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeScreenLifetime, screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {useSettingsRequestScope} from '../../../screens/settings-live/use-settings-request-scope';
import {nativeStorageOwner, NativeStorageSpaceSource} from '../../../screens/settings-live/server/storage/space-source';

export type Translate = typeof defaultI18n.t;

/** What uses space on the server (GET /v1/admin/storage): where it keeps its own files, whether that's on a network
 * share, then each kind of file with its size and share of the total, in the words every app shows. */
export function useStorageSpace(context: NativeShellBootstrapContext, runtime: NativeShellRuntime) {
  const [source, setSource] = useState<NativeStorageSpaceSource>();
  const reportError = runtime.reportError, t = runtime.text.t as Translate;
  useLayoutEffect(() => {const next = new NativeStorageSpaceSource(context, reportError); setSource(next); return next.dispose;}, [context, reportError]);
  const state = useSyncExternalStore(source?.subscribe ?? noSubscribe, source?.getSnapshot ?? noSnapshot, source?.getSnapshot ?? noSnapshot);
  useEffect(() => {void source?.load();}, [source]);
  if (!source || !nativeStorageOwner(context) || !state) return undefined;
  const usage = state.usage;
  // A place the server could not measure has no size.
  const rows = usage?.lines.map(line => {const words = storageLineWords(line, t); return {id: line.id, name: words.name, bytes: line.bytes, help: words.help || undefined};}) ?? [];
  const total = rows.reduce((sum, row) => sum + (row.bytes ?? 0), 0);
  return {failed: state.phase === 'error', retry: () => {void source.load();}, usage, total, totalText: defaultI18n.bytes(total),
    rows: rows.map(row => ({...row, size: row.bytes === undefined ? t('server.space.sizeUnknown') : defaultI18n.bytes(row.bytes),
      share: total > 0 && row.bytes !== undefined ? Math.max(0, Math.min(1, row.bytes / total)) : undefined}))};
}
const noSubscribe = () => () => {};
const noSnapshot = () => undefined;

function createCache(context: NativeShellBootstrapContext, reportError: (error: unknown) => void) {
  const lifetime = new NativeScreenLifetime(context);
  const current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
    ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  const source = new SettingsCacheSource(api, current, error => {lifetime.dispose(); reportError(error);});
  return {context, source, current, dispose: () => {source.dispose(); lifetime.dispose();}};
}
type Cache = ReturnType<typeof createCache>;

/** Empty cache: asks first, and a submitted restart lock survives the page re-rendering. */
export function useEmptyCacheGroup(context: NativeShellBootstrapContext, runtime: NativeShellRuntime) {
  const [group, setGroup] = useState<Cache>();
  const reportError = runtime.reportError;
  useLayoutEffect(() => {const next = createCache(context, reportError); setGroup(next); return next.dispose;}, [context, reportError]);
  return group?.context === context ? group : undefined;
}
export function useEmptyCache(group: Cache, visible = true) {
  const {source} = group;
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [confirmation, setConfirmation] = useState<object>();
  const view = useMemo(() => ({source, active: true, visible: true, confirmation: undefined as object | undefined}), [source]);
  view.visible = visible;
  useLayoutEffect(() => {view.active = true; return () => {view.active = false; view.confirmation = undefined;};}, [view]);
  const current = () => view.active && view.visible && view.source === source && group.current();
  const held = confirmation && view.confirmation === confirmation && current() ? confirmation : undefined;
  const open = () => {if (!current() || source.getSnapshot().phase !== 'ready') return; const next = {}; view.confirmation = next; setConfirmation(next);};
  const close = () => {if (!held || view.confirmation !== held || source.getSnapshot().phase === 'busy') return; view.confirmation = undefined; setConfirmation(undefined);};
  const submit = async () => {
    const captured = held; if (!captured || !current() || view.confirmation !== captured || source.getSnapshot().phase !== 'ready') return;
    await source.empty(() => current() && view.confirmation === captured);
    if (current() && view.confirmation === captured) {view.confirmation = undefined; setConfirmation(undefined);}
  };
  return {state, held: !!held, open, close, submit, available: group.current() && state.phase === 'ready'};
}

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
export type BackupsGroup = ReturnType<typeof createBackups>;
/** The server's own words for a refusal, or a general one. */
const message = (error: unknown, t: Translate) => {const text = (error as {message?: unknown} | null)?.message; return typeof text === 'string' && text ? text : t('error.unconfirmedChange');};

export function useBackupsGroup(ports: NativeSettingsLivePorts, runtime: NativeShellRuntime) {
  const [group, setGroup] = useState<BackupsGroup>(), context = ports.context, reportError = runtime.reportError;
  useLayoutEffect(() => {const next = createBackups(context, reportError); setGroup(next); return next.dispose;}, [context, reportError]);
  return group?.context === context ? group : undefined;
}

/** Storage › Backups on the rebuilt server: folder, Back up now, the list with Restore and Delete, a restore that waits for a
 * restart, restore from a file, and (under Advanced) artwork and the passphrase. The web's Backups.tsx draws the same. */
export function useBackups(group: BackupsGroup, t: Translate) {
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
  return {source, state, backups, running, busy, problem, act, menu, setMenu, deleting, setDeleting, typed, setTyped, picking, setPicking, restoring, setRestoring,
    when: (backup: BackupSummary) => when(Date.parse(backup.createdAt)), kind: (backup: BackupSummary) => backupKind(backup, t),
    size: (backup: BackupSummary) => defaultI18n.bytes(backup.bytes),
    chooseMenu: (backup: BackupSummary) => {if (group.current()) setMenu(backup);},
    restoreFromMenu: (backup: BackupSummary) => setRestoring({target: {id: backup.id}, when: defaultI18n.dateTime(backup.createdAt), protected: backup.encrypted === true}),
    deleteFromMenu: (backup: BackupSummary) => {setTyped(''); setDeleting(backup);},
    confirmDelete: () => {const backup = deleting; if (backup) act(() => source.remove(backup.id), () => setDeleting(undefined));},
    use: (paths: readonly {path: string}[]) => {
      const path = paths[0]?.path, purpose = picking; setPicking(undefined);
      if (!path || !group.current()) return;
      if (purpose === 'folder') act(() => source.setFolder(path)); else setRestoring({target: {path}});
    }};
}

function backupKind(backup: BackupSummary, t: Translate): string {
  return backup.kind === 'scheduled' ? t('web.maintenance.backupKindScheduled') : backup.kind === 'pre-restore' ? t('web.maintenance.backupKindPreRestore') : t('web.maintenance.backupKindManual');
}

/** One draft for the real days/hour/keep fields, saved through the page's existing backup source. */
export function useBackupSchedule(settings: BackupSettings, saving: boolean, onSave: (change: Pick<BackupSettings, 'days' | 'hour' | 'keep'>) => void) {
  const [draft, setDraft] = useState(settings), [chosen, setChosen] = useState(false), [valid, setValid] = useState(true);
  const sources = {forms: {backups: draft}};
  const read = (id: string) => readSettingsBinding(settingsBinding(id)!, sources, id)?.current;
  const mode = chosen ? 'custom' : read('storage.backupSchedule');
  const change = (id: string, value: unknown) => {
    if (saving) return;
    const patch = settingsBindingPatch(settingsBinding(id)!, value, sources);
    setDraft(current => ({...current, ...patch.values}));
    if (id === 'storage.backupSchedule') setChosen(value === 'custom');
  };
  const dirty = JSON.stringify([draft.days, draft.hour, draft.keep]) !== JSON.stringify([settings.days, settings.hour, settings.keep]);
  const ids = ['storage.backupKeep', 'storage.backupSchedule', ...(mode !== 'never' ? ['storage.backupTime'] : []), ...(mode === 'custom' ? ['storage.backupDays'] : [])];
  return {mode, dirty, valid, rows: ids.map(id => ({id, row: settingsRowIndex.get(id)!.row, value: id === 'storage.backupSchedule' ? mode : read(id),
    onCustom: id === 'storage.backupSchedule' ? () => change(id, 'custom') : undefined, onChange: (value: unknown) => change(id, value),
    onValidity: id === 'storage.backupKeep' ? setValid : undefined})),
    discard: () => {setDraft(settings); setChosen(false); setValid(true);},
    save: () => {if (!saving && valid) onSave({days: draft.days, hour: draft.hour, keep: draft.keep});}};
}

/**
 * "Protect backups with a passphrase": how the next backups are made. On, or Change, asks for the phrase twice; Off asks
 * first. The phrase lives only in these two masked fields until Save sends it, and they empty as it is sent.
 */
export function usePassphrase(group: BackupsGroup, t: Translate) {
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
  return {on, editing, stopping, phrase, setPhrase, repeat, setRepeat, refused, busy, disabled, close, tooLong, mismatch, ready,
    toggle: (next: boolean) => {if (next) setEditing('set'); else setStopping(true);}, change: () => setEditing('change'),
    save: () => {if (ready && group.current()) void send(phrase);}, turnOff: () => {if (group.current()) void send('');}};
}

/** A restore that waits for a restart: Restart now (asking first, with how many are playing, as the server says now) or Cancel. */
export function usePendingRestore(group: BackupsGroup, t: Translate) {
  const {source} = group, state = useSyncExternalStore(group.subscribe, source.getSnapshot, source.getSnapshot), pending = state.backups?.pendingRestore;
  const [asks, setAsks] = useState<Readonly<{playing: number; canRestart: boolean}>>(), [confirm, setConfirm] = useState(false), [error, setError] = useState<string>();
  const ask = async () => {const now = await source.restartAsks(); if (group.current()) setAsks(now); return now;};
  const key = pending ? `${pending.backup}|${pending.stagedAt}` : '';
  useEffect(() => {setAsks(undefined); if (key) void ask().catch(() => undefined);
    // Asked again for each restore that waits.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  const act = (work: () => Promise<unknown>) => {setError(undefined); void work().catch(e => {group.failure(e); if (group.current()) setError(message(e, t));});};
  return {state, pending, asks, confirm, setConfirm, error,
    restartNow: () => act(async () => {if ((await ask()).canRestart && group.current()) setConfirm(true);}),
    cancel: () => act(() => source.cancelRestore()),
    confirmRestart: () => {setConfirm(false); act(async () => {if (!await source.restart() && group.current()) setAsks({playing: asks?.playing ?? 0, canRestart: false});});}};
}

/**
 * Restoring one backup, as the web's RestoreDialog: the server checks it whole and says what restoring does; the owner
 * gives their password (and two-step code) and types the word; a backup this server did not make shows its warning first.
 * A protected backup is opened with its passphrase first: a listed one says so itself, a chosen file when the check is
 * refused for it. The phrase stays in its masked field only until the request that needs it is sent.
 */
export function useRestore({group, t, target, locked: listedLocked, onClose}: {group: BackupsGroup; t: Translate; target?: RestoreTarget; locked?: boolean; onClose: () => void}) {
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
  return {inspection, refused, warning, password, setPassword, code, setCode, typed, setTyped, locked, phrase, setPhrase, busy, close, open, word, elsewhere, ready, restore};
}

const keepRow = (id: string) => settingsRowIndex.get(id)!.row;

/** Storage & backups › Advanced: notification retention and the Messages settings link. */
export function useStorageKeep(context: NativeShellBootstrapContext, t: Translate) {
  const {api, lifetime} = useSettingsRequestScope(context);
  const documents = useMemo(() => withGuardedRaw(api, context.api, () => {if (!lifetime.current()) throw screenScopeEnded();}), [api, context, lifetime]);
  const [notices, setNotices] = useState<NoticesKeep>(), [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const load = useCallback(() => {
    void readNoticesKeep(api).then(value => {if (lifetime.current()) setNotices(value);}, () => {if (lifetime.current()) setFailed(true);});
  }, [api, lifetime]);
  useEffect(load, [load]);
  const run = async (work: () => Promise<unknown>) => {
    if (busy || !lifetime.current()) return;
    setBusy(true); setFailed(false);
    try {await work();} catch {if (lifetime.current()) setFailed(true);}
    finally {if (lifetime.current()) {setBusy(false); load();}}
  };
  const notification = keepRow('storage.keepNotifications');
  return {notices, busy, failed, notification: {label: t(notification.label), help: notification.help ? t(notification.help) : undefined,
    options: (notification.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label)}))},
    logsLabel: t(keepRow('storage.keepLogsLink').label),
    setDays: (value: string) => {if (notices) void run(() => saveNoticesKeep(documents, notices, Number(value)));}};
}

/** The page's tabs as the shared settings list draws them (owner-server 8): Space, Limits and retention, Backups. */
export function useStorageTabs(t: Translate) {
  const tabs = settingsSpec.find(section => section.scope === 'server' && section.id === 'storage')?.tabs ?? [];
  const [tab, setTab] = useState<string>(tabs[0]?.id ?? 'space');
  return {tab, choose: setTab, items: tabs.map(value => ({value: value.id, label: t(value.title)}))};
}
