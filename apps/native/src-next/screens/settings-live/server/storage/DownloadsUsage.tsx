import React, {useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {DownloadsUsageSource, type DownloadsUsageRow} from '../../../../../../../packages/client-core/src/server-admin/downloads-usage-source';
import {DownloadRemovalSource} from '../../../../../../../packages/client-core/src/server-admin/download-removal-source';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';
import {Button} from '../../../../kit/controls';
import {BottomSheet} from '../../../../kit/overlays';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import {downloadPerson} from '../settings-values';
import {nativeStorageOwner} from './space-source';

function createDownloads(context: NativeShellBootstrapContext, operationId: () => string, reportError: NativeShellRuntime['reportError']) {
  const lifetime = new NativeScreenLifetime(context);
  const current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
    ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  const fail = (error: unknown) => {const owned = current();dispose();if (owned) reportError(error);};
  const usage = new DownloadsUsageSource(api, current, fail), removal = new DownloadRemovalSource(api, usage, current, fail, operationId);
  const dispose = () => {usage.dispose();removal.dispose();lifetime.dispose();};
  return {context, usage, removal, current, dispose};
}
type Group = ReturnType<typeof createDownloads>;
type Confirmation = Readonly<{row: DownloadsUsageRow}>;
export function NativeDownloadsUsage({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const context = ports.context, operationId = ports.requestId, reportError = runtime.reportError, [group, setGroup] = useState<Group>();
  useLayoutEffect(() => {const next = createDownloads(context, operationId, reportError);setGroup(next);return next.dispose;}, [context, operationId, reportError]);
  return group?.context === context ? <Downloads group={group} runtime={runtime}/> : null;
}
function Downloads({group, runtime}: {group: Group; runtime: NativeShellRuntime}) {
  const {usage, removal} = group, theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const read = useSyncExternalStore(usage.subscribe, usage.getSnapshot, usage.getSnapshot), action = useSyncExternalStore(removal.subscribe, removal.getSnapshot, removal.getSnapshot);
  useEffect(() => {void usage.load();}, [usage]);
  const [confirmation, setConfirmation] = useState<Confirmation>();
  const view = useMemo(() => ({usage, removal, active: true, confirmation: undefined as Confirmation | undefined}), [usage, removal]);
  useLayoutEffect(() => {view.active = true;return () => {view.active = false;view.confirmation = undefined;};}, [view]);
  const current = () => view.active && view.usage === usage && view.removal === removal && group.current();
  const admitted = (row: DownloadsUsageRow) => {const state = removal.getSnapshot(), page = usage.getSnapshot();return state.pending === row || !state.pending && (state.continuation === row || page.phase === 'ready' && !!page.page?.rows.includes(row));};
  const held = confirmation && view.confirmation === confirmation && current() && admitted(confirmation.row) ? confirmation : undefined;
  const open = (row: DownloadsUsageRow) => {if (!current() || removal.getSnapshot().busy || !admitted(row)) return;const next = {row};view.confirmation = next;setConfirmation(next);};
  const close = () => {if (!held || !current() || view.confirmation !== held) return;view.confirmation = undefined;setConfirmation(undefined);};
  const submit = async () => {
    const captured = held;if (!captured || !current() || view.confirmation !== captured || removal.getSnapshot().busy || !admitted(captured.row)) return;
    const confirmed = () => current() && view.confirmation === captured && admitted(captured.row);
    const pending = removal.getSnapshot().pending;
    const done = await (pending === captured.row ? removal.retry(confirmed) : removal.remove(captured.row, confirmed));
    if (done && current() && view.confirmation === captured) {view.confirmation = undefined;setConfirmation(undefined);}
  };
  const busy = read.phase === 'loading' || action.busy, remaining = action.pending ?? action.continuation;
  const rows = read.page?.rows ?? [], extra = remaining && !rows.includes(remaining) ? remaining : undefined;
  const rowView = (row: DownloadsUsageRow) => <View key={row.profileKey} style={{padding:12,gap:8,backgroundColor:theme.colors.raised,borderRadius:12}}><Text style={[shellType(15,600),{color:theme.colors.text}]}>{downloadPerson(row).name}</Text>{downloadPerson(row).profile && <Text style={[shellType(14,400),{color:theme.colors.textSecondary}]}>{downloadPerson(row).profile}</Text>}<Text style={[shellType(14,400),{color:theme.colors.textSecondary}]}>{defaultI18n.bytes(row.storedBytes)}</Text>{(row.count > 0 || remaining === row) && <Button size="compact" variant="danger" disabled={busy || !admitted(row)} onPress={() => open(row)}>{t('next.settingsSpec.server.storage.advanced.groups.2.rows.1.control.itemAction.label')}</Button>}</View>;
  return <View style={{marginTop:22,gap:12}} testID="server-storage-downloads">
    <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('next.settingsSpec.server.storage.advanced.groups.2.rows.1.label')}</Text>
    <Text style={[shellType(13,400,18),{color:theme.colors.textSecondary}]}>{t('next.settingsSpec.server.storage.advanced.groups.2.rows.1.help')}</Text>
    {(read.phase === 'idle' || read.phase === 'loading') && <Text style={[shellType(14,400),{color:theme.colors.textSecondary}]}>{t('next.settings.loading')}</Text>}
    {read.phase === 'error' && <Button size="compact" variant="glass" onPress={() => {if (current()) void usage.load('retry');}}>{t('action.tryAgain')}</Button>}
    {rows.map(rowView)}{extra && <View style={{padding:12,gap:8,backgroundColor:theme.colors.raised,borderRadius:12}}><Text style={[shellType(15,600),{color:theme.colors.text}]}>{downloadPerson(extra).name}</Text>{downloadPerson(extra).profile && <Text style={[shellType(14,400),{color:theme.colors.textSecondary}]}>{downloadPerson(extra).profile}</Text>}<Button size="compact" variant={action.pending ? "glass" : "danger"} disabled={busy || !admitted(extra)} onPress={() => open(extra)}>{t(action.pending ? 'action.tryAgain' : 'next.settingsSpec.server.storage.advanced.groups.2.rows.1.control.itemAction.label')}</Button></View>}
    {action.error && <Text accessibilityRole="alert" style={[shellType(14,400),{color:theme.colors.record}]}>{t(action.pending ? 'error.unconfirmedChange' : 'error.unavailable')}</Text>}
    {action.done && <Text style={[shellType(14,400),{color:theme.colors.textSecondary}]}>{t('server.downloadUsage.removed')}</Text>}
    {read.phase === 'ready' && !rows.length && <Text style={[shellType(14,400),{color:theme.colors.textSecondary}]}>{t('web.downloads.emptyTitle')}</Text>}
    <View style={{flexDirection:'row',gap:8}}>{read.previous && <Button size="compact" disabled={busy} onPress={() => {if (current() && usage.getSnapshot().page === read.page) void usage.load('previous');}}>{t('next.reader.previous')}</Button>}{read.page?.next && <Button size="compact" disabled={busy} onPress={() => {if (current() && usage.getSnapshot().page === read.page) void usage.load('next');}}>{t('next.reader.next')}</Button>}</View>
    <BottomSheet open={!!held} onOpenChange={value => {if (!value) close();}} title={t('next.settingsSpec.server.storage.advanced.groups.2.rows.1.control.itemAction.label')} description={t('next.settingsSpec.server.storage.advanced.groups.2.rows.1.control.itemAction.confirm')} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" onPress={close}>{t('action.cancel')}</Button><Button variant="danger" disabled={action.busy || read.phase === 'loading'} loading={action.busy} onPress={() => {void submit();}}>{t(action.pending ? 'action.tryAgain' : 'next.settingsSpec.server.storage.advanced.groups.2.rows.1.control.itemAction.label')}</Button></View>}><View style={{gap:4}}><Text style={[shellType(14,400),{color:theme.colors.text}]}>{held ? downloadPerson(held.row).name : '—'}</Text>{held && downloadPerson(held.row).profile && <Text style={[shellType(14,400),{color:theme.colors.textSecondary}]}>{downloadPerson(held.row).profile}</Text>}</View></BottomSheet>
  </View>;
}
