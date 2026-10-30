import React, {useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {SettingsBackupSource, type SettingsRestoreSelection} from '../../../../../../../packages/client-core/src/server-admin/backup-source';
import {isAuthenticationFailure, restoreRecoveryDetails, restoreRecoveryNotice, type BackupTransport} from '../../../../../../../packages/client-core/src/backups';
import {settingsRowIndex} from '../../../../../../../packages/client-core/src/presentation/settings-spec';
import {routePath} from '../../../../../../../packages/contracts/src/routes';
import {backupProgressFraction, type BackupSummary, type RestoreSource} from '../../../../../../../packages/client-core/src/administration';
import {sentence, when} from '../../../../../../../packages/client-core/src/server-admin/console-words';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import {Button, Focusable, TextField} from '../../../../kit/controls';
import {BottomSheet, Menu} from '../../../../kit/overlays';
import {FolderExplorer} from '../../owner-libraries/FolderExplorer';
import {NativeAdvancedSection} from '../AdvancedSection';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeSettingsLivePorts} from '../../ports';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from './space-source';
import {NativeBackupFolder} from './BackupFolder';
import {NativeBackupProtection} from './BackupProtection';

type Translate = typeof defaultI18n.t;
const restoreRecoveryMessage = {
  repaired: 'web.maintenance.restoreRepaired',
  recovered: 'web.maintenance.restoreRecovered',
  partial: 'web.maintenance.restorePartial',
} as const;
function createBackups(context: NativeShellBootstrapContext, operationId: () => string, reportError: NativeShellRuntime['reportError']) {
  const lifetime = new NativeScreenLifetime(context), listeners = new Set<() => void>();
  const current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
    ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  const source = new SettingsBackupSource(api, context.authority().serverId, current, () => {for (const listener of listeners) listener();}, operationId);
  return {context, source, api, current, backupSubmitted: false, removeUncertain: false,
    subscribe: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};},
    failure: (error: unknown) => {if (isAuthenticationFailure(error)) {const owned = current();source.dispose();lifetime.dispose();if (owned) reportError(error);}},
    dispose: () => {source.dispose();lifetime.dispose();listeners.clear();}};
}
type Group = ReturnType<typeof createBackups>;
type Dialog = {abort: AbortController; source: RestoreSource; listed?: BackupSummary; selection?: SettingsRestoreSelection; submitted?: boolean; browse?: BackupTransport; chosen?: string};
type LayoutProps = Readonly<{children?: React.ReactNode; advancedContent?: React.ReactNode; advancedSummary?: string; advancedOpen?: boolean; onAdvancedOpenChange?: (open: boolean) => void}>;

/** The page owns the original operation and candidate across all restore steps. */
export function BackupsTab({ports, runtime, t, ...layout}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; t: Translate} & LayoutProps) {
  const [group, setGroup] = useState<Group>(), context = ports.context, operationId = ports.requestId, reportError = runtime.reportError;
  useLayoutEffect(() => {const next = createBackups(context, operationId, reportError);setGroup(next);return next.dispose;}, [context, operationId, reportError]);
  return group?.context === context ? <Backups group={group} t={t} {...layout}/> : null;
}
function Backups({group, t, children, advancedContent, advancedSummary, advancedOpen, onAdvancedOpenChange}: {group: Group; t: Translate} & LayoutProps) {
  const theme = useTheme(), {source} = group;
  const state = useSyncExternalStore(group.subscribe, source.getSnapshot, source.getSnapshot);
  const [dialog, setDialog] = useState<Dialog>(), [remove, setRemove] = useState<Dialog>(), [path, setPath] = useState(''), [typed, setTyped] = useState(''), [passphrase, setPassphrase] = useState(''), [problem, setProblem] = useState(false), [uncertain, setUncertain] = useState(false);
  const [, redraw] = useState(0), [picking, setPicking] = useState(false), [menu, setMenu] = useState<BackupSummary>();
  const view = useMemo(() => ({source, active: true, dialog: undefined as Dialog | undefined, remove: undefined as Dialog | undefined, flight: undefined as object | undefined}), [source]);
  useLayoutEffect(() => {view.active = true;return () => {view.active = false;view.dialog?.abort.abort();view.remove?.abort.abort();view.dialog = undefined;view.remove = undefined;view.flight = undefined;};}, [view]);
  const current = () => view.active && view.source === source && group.current();
  const load = () => {if (!current()) return;void source.load().then(() => {if (current()) {group.removeUncertain = false;setProblem(false);redraw(value => value + 1);}}, error => {group.failure(error);if (current()) setProblem(true);});};
  useEffect(() => {void source.load().catch(error => {group.failure(error);if (view.active && group.current()) setProblem(true);});}, [source, group, view]);
  const listed = (value: Dialog) => !value.listed || !!source.getSnapshot().document?.backups.includes(value.listed);
  const held = dialog && view.dialog === dialog && !dialog.abort.signal.aborted && current() && listed(dialog) ? dialog : undefined;
  const heldRemove = remove && view.remove === remove && !remove.abort.signal.aborted && current() && listed(remove) ? remove : undefined;
  const retireDialog = (captured: Dialog | undefined) => {if (!captured || view.dialog !== captured) return;view.dialog = undefined;captured.abort.abort();if (captured.selection) source.cancelRestoreSelection(captured.selection);setDialog(undefined);setPassphrase('');setPicking(false);};
  const close = () => {if (held && current()) retireDialog(held);};
  const retireRemove = (captured: Dialog | undefined) => {if (!captured || view.remove !== captured) return;captured.abort.abort();view.remove = undefined;setRemove(undefined);setTyped('');};
  const closeRemove = () => {if (heldRemove && current()) retireRemove(heldRemove);};
  const open = (backup?: BackupSummary) => {
    if (!current() || view.flight || state.busy || backup && !source.getSnapshot().document?.backups.includes(backup)) return;
    retireDialog(view.dialog);const next: Dialog = {abort: new AbortController(), source: backup ? {backupId: backup.id} : {path: ''}, ...(backup ? {listed: backup} : {})};if (!backup) next.browse = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => {
      if (!current() || view.dialog !== next || next.abort.signal.aborted || path.split('?')[0] !== routePath('get_admin_filesystem') || method && method !== 'GET') return Promise.reject(screenScopeEnded());
      const request = new AbortController(), stop = () => request.abort(); next.abort.signal.addEventListener('abort', stop, {once: true}); caller?.addEventListener('abort', stop, {once: true}); if (caller?.aborted) request.abort();
      return group.api.request<T>(path, 'GET', undefined, request.signal).then(value => {if (!current() || view.dialog !== next || request.signal.aborted || next.abort.signal.aborted) throw screenScopeEnded();return value;}).finally(() => {next.abort.signal.removeEventListener('abort', stop);caller?.removeEventListener('abort', stop);});
    }};
    view.dialog = next;setDialog(next);setPath('');setPicking(!backup);setProblem(false);
  };
  const confirm = async () => {
    const captured = held;if (!captured || view.flight || !current() || view.dialog !== captured || !listed(captured)) return;
    const flight = {};view.flight = flight;setProblem(false);
    const active = () => current() && view.flight === flight && view.dialog === captured && !captured.abort.signal.aborted && listed(captured);
    try {
      if (!captured.selection) {
        const selected = captured.listed ? captured.source : {path: path.trim()};if ('path' in selected && !selected.path) return;
        const selection = await source.prepareRestore(selected, captured.abort.signal);
        if (!active()) {source.cancelRestoreSelection(selection);return;}
        captured.selection = selection;redraw(value => value + 1);
        // Keep the same parent-owned dialog and signal for encrypted input.
        if (selection.encrypted) return;
      }
      if (!active() || !captured.selection?.isCurrent()) return;
      const secret = captured.selection.encrypted ? passphrase : undefined;setPassphrase('');
      if (captured.selection.encrypted && !secret) return;
      const result = await source.restore(captured.selection, secret, captured.abort.signal);
      if (active() && result) close();
    } catch (error) {group.failure(error);if (active()) setProblem(true);} finally {if (view.flight === flight) view.flight = undefined;}
  };
  const start = async () => {
    if (!current() || view.flight || group.backupSubmitted || source.getSnapshot().busy || !state.document || state.document.running || state.document.restore) return;
    const flight = {};view.flight = flight;group.backupSubmitted = true;setProblem(false);
    try {if (await source.backupNow()) group.backupSubmitted = false;} catch (error) {group.failure(error);if (current()) {setProblem(true);setUncertain(true);}} finally {if (view.flight === flight) view.flight = undefined;}
  };
  const erase = async () => {
    const captured = heldRemove;if (!captured?.listed || captured.submitted || group.removeUncertain || typed.trim() !== 'delete' || !current() || view.remove !== captured || view.flight || !listed(captured)) return;
    const flight = {};view.flight = flight;captured.submitted = true;group.removeUncertain = true;setProblem(false);
    try {const done = await source.remove(captured.listed.id, captured.abort.signal);if (done && current() && view.remove === captured) {group.removeUncertain = false;closeRemove();}} catch (error) {group.failure(error);if (current() && view.remove === captured) setProblem(true);} finally {if (view.flight === flight) view.flight = undefined;}
  };
  const running = state.document?.running, fraction = running ? backupProgressFraction(running) : undefined;
  const result = state.document?.lastRestore;
  const progress = state.document?.restore?.jobId === state.restoreJobId ? state.document?.restore : undefined;
  const report = state.restorePhase ? (progress?.result ?? state.staged)?.validation.recovery : result?.outcome === 'restored' ? result.recovery : undefined;
  const recovery = restoreRecoveryNotice(report), details = restoreRecoveryDetails(report, t);
  return <View style={{marginTop:22,gap:12}} testID="server-storage-backups">
    <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('web.maintenance.backups')}</Text>
    {state.settings && <NativeBackupFolder source={source} settings={state.settings} api={group.api} current={group.current} failure={group.failure} t={t}/>}
    {children}
    {problem && <Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t(uncertain ? 'error.unconfirmedChange' : 'error.unavailable')}</Text>}
    {running && <View testID="server-backup-progress" style={{padding: 14, gap: 8, borderRadius: 14, backgroundColor: theme.colors.raised}}>
      <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{t('web.maintenance.backingUp')}{fraction === undefined ? '' : ` ${Math.round(fraction * 100)}%`}</Text>
      <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{sentence(running.phase)}</Text>
      <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{defaultI18n.bytes(running.bytesDone)}{running.bytesTotal > 0 ? ` / ${defaultI18n.bytes(running.bytesTotal)}` : ''}</Text>
      {fraction !== undefined && <View accessible accessibilityRole="progressbar" accessibilityLabel={t('web.maintenance.backingUp')} accessibilityValue={{min: 0, max: 100, now: Math.round(fraction * 100)}} style={{height: 5, borderRadius: 3, overflow: 'hidden', backgroundColor: theme.colors.lineSoft}}><View style={{height: 5, width: `${fraction * 100}%`, backgroundColor: theme.colors.accent}}/></View>}
      {running.kind === 'manual' && <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{t('web.maintenance.backupMayPause')}</Text>}
    </View>}
    {!running && state.document?.lastBackup?.outcome === 'failed' && <Text accessibilityRole="alert" style={{color: theme.colors.record}}>{t(state.document.lastBackup.errorCode === 'backup_busy' ? 'web.maintenance.backupBusy' : state.document.lastBackup.errorCode === 'insufficient_disk' ? 'web.maintenance.backupNoSpace' : 'web.console.jobFailed')}</Text>}
    {state.restorePhase && <Text style={{color:theme.colors.textSecondary}}>{t(state.restorePhase === 'restarting' ? 'web.maintenance.restartingTitle' : 'web.maintenance.restoreTitle')}</Text>}
    {!state.restorePhase && result && <Text accessibilityLiveRegion="polite" style={{color: result.outcome === 'rolled_back' ? theme.colors.record : theme.colors.textSecondary}}>{t(result.outcome === 'rolled_back' ? 'web.maintenance.restoreRolledBack' : 'web.maintenance.restored')}</Text>}
    {recovery && <Text accessibilityLiveRegion="polite" style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{t(report?.manifestDamage ? 'web.maintenance.restoreBackupPartial' : restoreRecoveryMessage[recovery])}</Text>}
    {details.length > 0 && <View style={{gap: 4}}>{details.map((detail, index) => <Text key={index} selectable style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{detail}</Text>)}</View>}
    <View style={{flexDirection:'row',gap:8}}><Button size="compact" disabled={state.busy || group.backupSubmitted || !state.document || !!running || !!state.document.restore} onPress={() => {void start();}}>{running || state.busy && group.backupSubmitted ? t('web.maintenance.backingUp') : t('web.maintenance.backUpNow')}</Button><Button size="compact" variant="glass" disabled={state.busy} onPress={load}>{t('action.tryAgain')}</Button></View>
    {state.document?.backups.length === 0 && <Text style={{color:theme.colors.textSecondary}}>{t('web.maintenance.noBackups')}</Text>}
    {state.document?.backups.map(backup => <Focusable key={backup.id} testID={`server-backup-${backup.id}`} accessibilityRole="button" accessibilityLabel={`${when(Date.parse(backup.createdAt))}, ${backupKind(backup, t)}`} disabled={state.busy}
      onPress={() => {if (current() && !view.flight && source.getSnapshot().document?.backups.includes(backup)) setMenu(backup);}} focusKind="row" focusRadius={12}
      style={{padding:12,gap:8,borderRadius:12,backgroundColor:theme.colors.raised}}>
      <Text style={[shellType(15,700),{color:theme.colors.text}]}>{when(Date.parse(backup.createdAt))}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12}}><Text style={{color:theme.colors.textSecondary}}>{backupKind(backup, t)}</Text><Text style={{color:theme.colors.textSecondary}}>{defaultI18n.bytes(backup.bytes)}</Text><Text style={{color:theme.colors.textTertiary}}>{t('web.maintenance.backupSchema', {version: backup.schemaVersion})}</Text><Text style={{color:theme.colors.textTertiary}}>{t('accounts.brand')} {backup.serverVersion}</Text></View>
      <Text selectable style={[shellType(12.5), {color: theme.colors.textTertiary, fontFamily: 'monospace'}]}>{backup.path}</Text>
    </Focusable>)}
    <Menu testID="server-backup-actions" open={!!menu && current() && !!state.document?.backups.includes(menu)} onOpenChange={value => {if (!value) setMenu(undefined);}} presentation="sheet" title={menu ? when(Date.parse(menu.createdAt)) : undefined} items={menu ? [
      {id: 'restore', label: t('web.maintenance.restoreBackup'), onSelect: () => open(menu)},
      {id: 'delete', label: t('action.delete'), destructive: true, disabled: state.busy || group.removeUncertain, onSelect: () => {if (!current() || view.flight || group.removeUncertain || !source.getSnapshot().document?.backups.includes(menu)) return;retireRemove(view.remove);const next: Dialog = {abort:new AbortController(),source:{backupId:menu.id},listed:menu};view.remove = next;setRemove(next);setProblem(false);}},
    ] : []}/>

    <Button variant="glass" size="compact" disabled={state.busy} onPress={() => open()}>{t('web.maintenance.restoreFromFile')}</Button>
    <BottomSheet open={!!held && !picking} onOpenChange={value => {if (!value) close();}} title={t('web.maintenance.restoreTitle')} description={t('web.maintenance.restoreBody')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={close}>{t('action.cancel')}</Button><Button disabled={state.busy || !!held?.selection?.encrypted && !passphrase || !held?.listed && !held?.selection && !path.trim()} loading={state.busy} onPress={() => {void confirm();}}>{t('web.maintenance.restoreConfirm')}</Button></View>}>
      <View style={{gap:12}}>{held?.listed && <Text style={{color:theme.colors.text}}>{when(Date.parse(held.listed.createdAt))}</Text>}{held && !held.listed && !held.selection && <><Text selectable style={{color: theme.colors.text, fontFamily: 'monospace'}}>{path}</Text><Button variant="glass" disabled={state.busy} onPress={() => {if (current() && view.dialog === held && !view.flight) setPicking(true);}}>{t(settingsRowIndex.get('storage.restoreFile')!.row.control.label!)}</Button></>} {held?.selection?.encrypted && <TextField label={t('next.settings.backup.passphrase')} value={passphrase} secureTextEntry editable={!state.busy} onChangeText={value => {if (current() && view.dialog === held && !view.flight) setPassphrase(value);}} autoCapitalize="none" autoCorrect={false}/>}</View>
    </BottomSheet>
    {picking && held?.browse && <FolderExplorer open testID="server-restore-picker" api={held.browse} title={t('web.maintenance.restoreFromFile')} selectionMode="file-or-folder" multiple={false} useLabel="Choose this path"
      chosen={path ? [{path, name: path}] : []} onOpenChange={value => {if (!value && current() && view.dialog === held) {if (held.chosen) setPicking(false); else close();}}}
      onUse={folders => {if (!current() || view.dialog !== held || view.flight || held.selection || folders.length !== 1 || !folders[0]?.path) return;held.chosen = folders[0].path;setPath(folders[0].path);setPicking(false);}}/>}
    <BottomSheet open={!!heldRemove} onOpenChange={value => {if (!value) closeRemove();}} title={t('web.maintenance.deleteBackupTitle')} description={t('web.maintenance.deleteBackupBody',{id:'delete'})} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={closeRemove}>{t('action.cancel')}</Button><Button variant="danger" disabled={state.busy || !!heldRemove?.submitted || group.removeUncertain || typed.trim() !== 'delete'} loading={state.busy} onPress={() => {void erase();}}>{t('web.maintenance.deleteBackupConfirm')}</Button></View>}><TextField label={t('confirm.typeToConfirm',{token:'delete'})} value={typed} onChangeText={value => {if (current() && view.remove === heldRemove && !view.flight) setTyped(value);}} autoCapitalize="none" autoCorrect={false}/></BottomSheet>
    <NativeAdvancedSection testID="server-storage-advanced" open={advancedOpen} onOpenChange={onAdvancedOpenChange}
      summary={[advancedSummary === t('next.settings.allDefaults') ? '' : advancedSummary, state.settings?.includeArtwork ? t('next.settingsSpec.server.storage.advanced.groups.3.rows.1.label') : '', state.settings?.passphraseProtected ? t('next.settingsSpec.server.storage.advanced.groups.3.rows.2.label') : ''].filter(Boolean).join(' · ') || (state.settings ? t('next.settings.allDefaults') : t('status.loading'))}>
      {advancedContent}
      {state.settings && <NativeBackupProtection source={source} settings={state.settings} current={group.current} failure={group.failure} t={t}/>}
    </NativeAdvancedSection>

  </View>;
}

function backupKind(backup: BackupSummary, t: Translate): string {return backup.kind === 'scheduled' ? t('web.maintenance.backupKindScheduled') : backup.kind === 'pre-restore' ? t('web.maintenance.backupKindPreRestore') : t('web.maintenance.backupKindManual');}
