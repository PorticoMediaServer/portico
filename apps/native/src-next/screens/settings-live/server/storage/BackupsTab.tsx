import React, {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import type {BackupSummary} from '../../../../../../../packages/client-core/src/administration';
import {notMadeHere, passphraseRefused, passphraseTooLong, scopeEnded, ServerBackupSource, type BackupInspection, type RestoreTarget} from '../../../../../../../packages/client-core/src/server-admin/server-backups';
import {restoreReady, sentAsConfirmed} from '../../../../../../../packages/client-core/src/server-admin/restore-gate';
import {when} from '../../../../../../../packages/client-core/src/server-admin/console-words';
import {defaultI18n, type MessageId} from '../../../../../../../packages/i18n/src';
import {Button, Focusable, Switch, TextField} from '../../../../kit/controls';
import {BottomSheet, Menu} from '../../../../kit/overlays';
import {FolderExplorer} from '../../owner-libraries/FolderExplorer';
import {NativeAdvancedSection} from '../AdvancedSection';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeSettingsLivePorts} from '../../ports';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import {nativeStorageOwner} from './space-source';

type Translate = typeof defaultI18n.t;
type LayoutProps = Readonly<{children?: React.ReactNode; advancedContent?: React.ReactNode; advancedSummary?: string; advancedOpen?: boolean; onAdvancedOpenChange?: (open: boolean) => void}>;

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
type Group = ReturnType<typeof createBackups>;
/** The server's own words for a refusal, or a general one. */
const message = (error: unknown, t: Translate) => {const text = (error as {message?: unknown} | null)?.message; return typeof text === 'string' && text ? text : t('error.unconfirmedChange');};

/** Storage › Backups on the rebuilt server: folder, Back up now, the list with Restore and Delete, a restore that waits for a
 * restart, restore from a file, and (under Advanced) artwork and the passphrase. The web's Backups.tsx draws the same. */
export function BackupsTab({ports, runtime, t, ...layout}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; t: Translate} & LayoutProps) {
  const [group, setGroup] = useState<Group>(), context = ports.context, reportError = runtime.reportError;
  useLayoutEffect(() => {const next = createBackups(context, reportError); setGroup(next); return next.dispose;}, [context, reportError]);
  return group?.context === context ? <Backups group={group} t={t} {...layout}/> : null;
}

function Backups({group, t, children, advancedContent, advancedSummary, advancedOpen, onAdvancedOpenChange}: {group: Group; t: Translate} & LayoutProps) {
  const theme = useTheme(), {source} = group, state = useSyncExternalStore(group.subscribe, source.getSnapshot, source.getSnapshot), backups = state.backups;
  const [problem, setProblem] = useState<string>(), [menu, setMenu] = useState<BackupSummary>(), [deleting, setDeleting] = useState<BackupSummary>(), [typed, setTyped] = useState('');
  const [picking, setPicking] = useState<'folder' | 'restore'>(), [restoring, setRestoring] = useState<Readonly<{target: RestoreTarget; when?: string; protected?: boolean}>>();
  useEffect(() => {void source.load().catch(error => {group.failure(error); if (group.current()) setProblem(message(error, t));});}, [group, source, t]);
  const act = (work: () => Promise<unknown>, after?: () => void) => {
    if (!group.current()) return;
    setProblem(undefined);
    void work().then(() => {if (group.current()) after?.();}, error => {group.failure(error); if (group.current()) setProblem(message(error, t));});
  };
  const running = !!backups?.running, waiting = !!backups?.pendingRestore, busy = state.busy || running || waiting;
  const muted = {color: theme.colors.textSecondary};
  return <View style={{marginTop: 22, gap: 12}} testID="server-storage-backups">
    <Text style={[shellType(17, 800), {color: theme.colors.text}]}>{t('web.maintenance.backups')}</Text>
    <View style={{gap: 8}}>
      <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{t('next.settingsSpec.server.storage.groups.1.rows.0.label')}</Text>
      <Text selectable style={[muted, {fontFamily: 'monospace'}]}>{backups?.folder ?? t('next.settings.loading')}</Text>
      {backups && !backups.folderWritable && <Text accessibilityRole="alert" style={{color: theme.colors.record}}>{t('server.backups.folderUnwritable')}</Text>}
      <View style={{flexDirection: 'row'}}><Button size="compact" variant="glass" disabled={!backups || state.busy} onPress={() => setPicking('folder')}>{t('next.settings.changeFolder')}</Button></View>
    </View>
    {children}
    <View style={{flexDirection: 'row'}}><Button testID="server-backup-now" size="compact" loading={running} disabled={!backups || busy} onPress={() => act(() => source.backupNow())}>{t(running ? 'web.maintenance.backingUp' : 'web.maintenance.backUpNow')}</Button></View>
    {!running && backups?.lastError && <Text accessibilityRole="alert" style={{color: theme.colors.record}}>{t('server.backups.lastFailed', {reason: backups.lastError})}</Text>}
    {problem && <Text accessibilityRole="alert" style={{color: theme.colors.record}}>{problem}</Text>}
    <PendingRestore group={group} t={t}/>
    {backups && !state.list.length && <Text style={muted}>{t('web.maintenance.noBackups')}</Text>}
    {state.list.map(backup => <Focusable key={backup.id} testID={`server-backup-${backup.id}`} accessibilityRole="button" accessibilityLabel={`${when(Date.parse(backup.createdAt))}, ${backupKind(backup, t)}`} disabled={busy}
      onPress={() => {if (group.current()) setMenu(backup);}} focusKind="row" focusRadius={12} style={{padding: 12, gap: 6, borderRadius: 12, backgroundColor: theme.colors.raised}}>
      <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{when(Date.parse(backup.createdAt))}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 12}}>
        <Text style={muted}>{backupKind(backup, t)}</Text><Text style={muted}>{defaultI18n.bytes(backup.bytes)}</Text>
        {backup.serverVersion ? <Text style={muted}>{t('server.backups.madeBy', {version: backup.serverVersion})}</Text> : null}
        {backup.encrypted && <Text style={muted}>{t('server.backups.protected')}</Text>}
      </View>
    </Focusable>)}
    <Menu testID="server-backup-actions" open={!!menu} onOpenChange={value => {if (!value) setMenu(undefined);}} presentation="sheet" title={menu ? when(Date.parse(menu.createdAt)) : undefined} items={menu ? [
      {id: 'restore', label: t('web.maintenance.restoreBackup'), onSelect: () => setRestoring({target: {id: menu.id}, when: defaultI18n.dateTime(menu.createdAt), protected: menu.encrypted === true})},
      {id: 'delete', label: t('action.delete'), destructive: true, onSelect: () => {setTyped(''); setDeleting(menu);}},
    ] : []}/>
    <BottomSheet open={!!deleting} onOpenChange={value => {if (!value) setDeleting(undefined);}} title={t('web.maintenance.deleteBackupTitle')} description={t('web.maintenance.deleteBackupBody', {id: 'delete'})}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" onPress={() => setDeleting(undefined)}>{t('action.cancel')}</Button>
        <Button variant="danger" loading={state.busy} disabled={state.busy || typed.trim() !== 'delete'} style={{flex: 1}} onPress={() => {const backup = deleting; if (backup) act(() => source.remove(backup.id), () => setDeleting(undefined));}}>{t('action.delete')}</Button></View>}>
      <TextField value={typed} onChangeText={setTyped} autoCapitalize="none" autoCorrect={false} accessibilityLabel={t('web.deleteAccount.typeLabel', {value: 'delete'})} placeholder={t('web.deleteAccount.typeLabel', {value: 'delete'})}/>
    </BottomSheet>
    <View style={{flexDirection: 'row'}}><Button variant="glass" size="compact" disabled={!backups || busy} onPress={() => setPicking('restore')}>{t('web.maintenance.restoreFromFile')}</Button></View>
    {picking && <FolderExplorer open testID={picking === 'restore' ? 'server-restore-picker' : 'server-backup-folder-picker'} api={group.api} multiple={false}
      title={t(picking === 'restore' ? 'web.maintenance.restoreFromFile' : 'next.settingsSpec.server.storage.groups.1.rows.0.label')} selectionMode={picking === 'restore' ? 'file-or-folder' : 'folders'}
      chosen={picking === 'folder' && backups?.folder ? [{path: backups.folder, name: backups.folder}] : []} onOpenChange={value => {if (!value) setPicking(undefined);}}
      onUse={paths => {
        const path = paths[0]?.path, purpose = picking; setPicking(undefined);
        if (!path || !group.current()) return;
        if (purpose === 'folder') act(() => source.setFolder(path)); else setRestoring({target: {path}});
      }}/>}
    <RestoreSheet group={group} t={t} target={restoring?.target} when={restoring?.when} locked={restoring?.protected} onClose={() => setRestoring(undefined)}/>
    <NativeAdvancedSection testID="server-storage-advanced" open={advancedOpen} onOpenChange={onAdvancedOpenChange}
      summary={[advancedSummary === t('next.settings.allDefaults') ? '' : advancedSummary, state.settings?.includeArtwork ? t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.label') : '', backups?.protected ? t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.label') : ''].filter(Boolean).join(' · ') || t('next.settings.allDefaults')}>
      {advancedContent}
      {state.settings && <View style={{gap: 6}}>
        <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.label')}</Text>
        <Text style={muted}>{t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.help')}</Text>
        <Switch checked={state.settings.includeArtwork} disabled={state.busy} accessibilityLabel={t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.label')}
          onCheckedChange={includeArtwork => act(() => source.saveSettings({includeArtwork}))}/>
      </View>}
      {backups && <Passphrase group={group} t={t}/>}
    </NativeAdvancedSection>
  </View>;
}

/**
 * "Protect backups with a passphrase": how the next backups are made. On, or Change, asks for the phrase twice; Off asks
 * first. The phrase lives only in these two masked fields until Save sends it, and they empty as it is sent.
 */
function Passphrase({group, t}: {group: Group; t: Translate}) {
  const theme = useTheme(), {source} = group, state = useSyncExternalStore(group.subscribe, source.getSnapshot, source.getSnapshot), on = state.backups?.protected === true;
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
  const label = t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.label');
  return <View style={{gap: 6}}>
    <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{label}</Text>
    <Text style={{color: theme.colors.textSecondary}}>{t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.help')}</Text>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
      <Switch testID="server-backup-passphrase" checked={on} disabled={disabled} accessibilityLabel={label} onCheckedChange={next => {if (next) setEditing('set'); else setStopping(true);}}/>
      {on && <Button size="compact" variant="glass" disabled={disabled} accessibilityLabel={t('next.settings.backup.changeTitle')} onPress={() => setEditing('change')}>{t('next.settings.changeFolder')}</Button>}
    </View>
    <BottomSheet open={!!editing} onOpenChange={value => {if (!value) close();}} title={t(editing === 'change' ? 'next.settings.backup.changeTitle' : 'next.settings.backup.protectTitle')} description={t('server.backups.passphraseFuture')}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" onPress={close}>{t('next.settings.cancel')}</Button>
        <Button loading={busy} disabled={!ready} style={{flex: 1}} onPress={() => {if (ready && group.current()) void send(phrase);}}>{t(editing === 'change' ? 'server.backups.savePassphrase' : 'next.settings.backup.protect')}</Button></View>}>
      <View style={{gap: 10}}>
        <TextField label={t('next.settings.backup.passphrase')} secureTextEntry value={phrase} editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={setPhrase} error={tooLong ? t('server.backups.passphraseTooLong') : undefined}/>
        <TextField label={t('next.settings.backup.repeat')} secureTextEntry value={repeat} editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={setRepeat} error={mismatch ? t('next.settings.backup.mismatch') : undefined}/>
        {refused && <Text accessibilityRole="alert" style={{color: theme.colors.record}}>{refused}</Text>}
      </View>
    </BottomSheet>
    <BottomSheet open={stopping} onOpenChange={value => {if (!value) close();}} title={t('server.backups.passphraseOffTitle')} description={t('server.backups.passphraseOffBody')}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" onPress={close}>{t('next.settings.cancel')}</Button>
        <Button variant="danger" loading={busy} disabled={busy} style={{flex: 1}} onPress={() => {if (group.current()) void send('');}}>{t('server.backups.passphraseOff')}</Button></View>}>
      {refused ? <Text accessibilityRole="alert" style={{color: theme.colors.record}}>{refused}</Text> : null}
    </BottomSheet>
  </View>;
}

/** A restore that waits for a restart: Restart now (asking first, with how many are playing, as the server says now) or Cancel. */
function PendingRestore({group, t}: {group: Group; t: Translate}) {
  const theme = useTheme(), {source} = group, state = useSyncExternalStore(group.subscribe, source.getSnapshot, source.getSnapshot), pending = state.backups?.pendingRestore;
  const [asks, setAsks] = useState<Readonly<{playing: number; canRestart: boolean}>>(), [confirm, setConfirm] = useState(false), [error, setError] = useState<string>();
  const ask = async () => {const now = await source.restartAsks(); if (group.current()) setAsks(now); return now;};
  const key = pending ? `${pending.backup}|${pending.stagedAt}` : '';
  useEffect(() => {setAsks(undefined); if (key) void ask().catch(() => undefined);
    // Asked again for each restore that waits.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  if (state.restarting) return <Text accessibilityRole="alert" style={{color: theme.colors.textSecondary}}>{t('server.backups.restarting')}</Text>;
  if (!pending) return null;
  const act = (work: () => Promise<unknown>) => {setError(undefined); void work().catch(e => {group.failure(e); if (group.current()) setError(message(e, t));});};
  return <View testID="server-backup-pending" style={{padding: 14, gap: 10, borderRadius: 14, backgroundColor: theme.colors.raised}}>
    <Text style={[shellType(15, 700, 20), {color: theme.colors.text}]}>{t('server.backups.pending', {name: pending.backup})}</Text>
    {asks && !asks.canRestart && <Text style={{color: theme.colors.textSecondary}}>{t('server.backups.restartByHand')}</Text>}
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>
      {asks?.canRestart && <Button size="compact" disabled={state.busy} onPress={() => act(async () => {if ((await ask()).canRestart && group.current()) setConfirm(true);})}>{t('server.backups.restartNow')}</Button>}
      <Button size="compact" variant="glass" disabled={state.busy} onPress={() => act(() => source.cancelRestore())}>{t('server.backups.cancelRestore')}</Button>
    </View>
    {error && <Text accessibilityRole="alert" style={{color: theme.colors.record}}>{error}</Text>}
    <BottomSheet open={confirm && !!asks} onOpenChange={value => {if (!value) setConfirm(false);}} title={t('server.backups.restartTitle')} description={t('server.backups.restartBody', {count: asks?.playing ?? 0})}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" onPress={() => setConfirm(false)}>{t('next.settings.cancel')}</Button>
        <Button disabled={state.busy} style={{flex: 1}} onPress={() => {setConfirm(false); act(async () => {if (!await source.restart() && group.current()) setAsks({playing: asks?.playing ?? 0, canRestart: false});});}}>{t('server.backups.restartNow')}</Button></View>}>
      {null}
    </BottomSheet>
  </View>;
}

/**
 * Restoring one backup, as the web's RestoreDialog: the server checks it whole and says what restoring does; the owner
 * gives their password (and two-step code) and types the word; a backup this server did not make shows its warning first.
 * A protected backup is opened with its passphrase first: a listed one says so itself, a chosen file when the check is
 * refused for it. The phrase stays in its masked field only until the request that needs it is sent.
 */
function RestoreSheet({group, t, target, when: madeAt, locked: listedLocked, onClose}: {group: Group; t: Translate; target?: RestoreTarget; when?: string; locked?: boolean; onClose: () => void}) {
  const theme = useTheme(), {source} = group;
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
  const field = {autoCapitalize: 'none' as const, autoCorrect: false, editable: !busy};
  return <BottomSheet open={!!target} onOpenChange={value => {if (!value) close();}} scrollable
    title={t(target && 'path' in target ? 'web.maintenance.restoreFileTitle' : 'web.maintenance.restoreTitle')} description={madeAt ? t('server.backups.restoreWhich', {when: madeAt}) : undefined}
    footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" onPress={close}>{t('next.settings.cancel')}</Button>
      <Button loading={busy} disabled={!ready} style={{flex: 1}} onPress={restore}>{t('web.maintenance.restoreConfirm')}</Button></View>}>
    <View style={{gap: 10, paddingBottom: 4}}>
      {!inspection && !refused && !locked && <Text style={{color: theme.colors.textSecondary}}>{t('server.backups.checking')}</Text>}
      {locked && !inspection && <>
        <Text style={{color: theme.colors.textSecondary}}>{t('next.settings.backup.restoreTitle')}. {t('server.backups.passphraseNeeded')}</Text>
        <TextField label={t('next.settings.backup.passphrase')} secureTextEntry value={phrase} onChangeText={setPhrase} onSubmitEditing={open} {...field}/>
        <View style={{flexDirection: 'row'}}><Button size="compact" loading={busy} disabled={busy || !phrase} onPress={open}>{t('server.backups.openBackup')}</Button></View>
      </>}
      {inspection?.restoring.map(line => <Text key={line} style={{color: theme.colors.textSecondary}}>{`• ${t(`server.backups.restoring.${line}` as MessageId)}`}</Text>)}
      {elsewhere && <Text accessibilityRole="alert" style={{color: theme.colors.record}}>{warning || t('server.backups.notMadeHere')}</Text>}
      {inspection && <>
        <TextField label={t('web.deleteAccount.typeLabel', {value: word})} value={typed} onChangeText={setTyped} {...field}/>
        {locked && <TextField label={t('next.settings.backup.passphrase')} secureTextEntry value={phrase} onChangeText={setPhrase} {...field}/>}
        <TextField label={t('server.backups.password')} secureTextEntry textContentType="password" value={password} onChangeText={setPassword} {...field}/>
        <TextField label={t('server.backups.code')} keyboardType="number-pad" textContentType="oneTimeCode" value={code} onChangeText={setCode} {...field}/>
      </>}
      {refused && <Text accessibilityRole="alert" style={{color: theme.colors.record}}>{refused}</Text>}
    </View>
  </BottomSheet>;
}

function backupKind(backup: BackupSummary, t: Translate): string {
  return backup.kind === 'scheduled' ? t('web.maintenance.backupKindScheduled') : backup.kind === 'pre-restore' ? t('web.maintenance.backupKindPreRestore') : t('web.maintenance.backupKindManual');
}
