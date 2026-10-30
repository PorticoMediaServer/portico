import {Menu} from '../../../../kit/overlays';
import React, {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {AppState, Text, View} from 'react-native';
import {SettingsMessagesSource} from '../../../../../../../packages/client-core/src/server-admin/messages-source';
import {LOG_DETAIL_MINUTES, logLevels, logCategories, type LogDetailMinutes, type LogSettings} from '../../../../../../../packages/client-core/src/server-administration';
import type {LogsDraft} from '../../../../../../../packages/client-core/src/server-admin/server-forms';
import {defaultI18n, type MessageId} from '../../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../ports';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from '../storage/space-source';
import {useStorageForms, useStorageSet, useFormState, type StorageForms} from '../storage/forms';
import {Button, Chip, Select, Switch} from '../../../../kit/controls';
import {alpha, shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
const IDS = ['logs'] as const;

function createMessages(context: NativeShellBootstrapContext, operationId: () => string, reportError: NativeShellRuntime['reportError']) {
  const lifetime = new NativeScreenLifetime(context), listeners = new Set<() => void>();
  const current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
    ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  let failed = false, desired = false, foreground = AppState.currentState === 'active', token: object | undefined, timer: ReturnType<typeof setTimeout> | undefined;
  const notify = () => {for (const listener of listeners) listener();};
  const handleFailure = (error: unknown) => {
    if (source.getSnapshot().phase === 'idle') {const owned = current();dispose();if (owned) reportError(error);}
    else {failed = true;notify();}
  };
  const source = new SettingsMessagesSource(api, undefined, current, handleFailure, operationId);
  const stop = () => {token = undefined;clearTimeout(timer);timer = undefined;};
  const active = (captured: object) => current() && desired && foreground && token === captured;
  const poll = async (captured: object) => {
    if (!active(captured)) return;
    const state = source.getSnapshot();
    if (state.phase !== 'loading' && !state.changing) {if (!active(captured)) return;await source.load();}
    if (!active(captured)) return;
    if (source.getSnapshot().phase === 'error') {desired = false;stop();source.setLive(false);return;}
    timer = setTimeout(() => {if (active(captured)) void poll(captured);}, 3000);
  };
  const start = (immediate: boolean) => {stop();if (!current() || !desired || !foreground) return;const captured = {};token = captured;source.setLive(true);if (immediate) void poll(captured);else timer = setTimeout(() => {if (active(captured)) void poll(captured);},3000);};
  const setLive = (live: boolean) => {if (!current()) return;desired = live;stop();source.setLive(live && foreground);if (live) start(true);};
  const subscription = AppState.addEventListener('change', value => {foreground = value === 'active';stop();if (!current()) return;if (!foreground) source.pause();else if (desired) start(true);});
  const dispose = () => {desired = false;stop();subscription.remove();source.dispose();lifetime.dispose();listeners.clear();};
  let selection: object | undefined;
  return {context, source, current, dispose, setLive, failure: handleFailure,
    getFailure: () => failed, subscribeFailure: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};}, clearFailure: () => {failed = false;notify();},
    select: async (level: Parameters<SettingsMessagesSource['select']>[0], category: Parameters<SettingsMessagesSource['select']>[1]) => {if (!current()) return;const captured = {};selection = captured;stop();await source.select(level,category);if (selection === captured && current() && desired) start(false);},
  };
}
type Group = ReturnType<typeof createMessages>;
export function NativeMessages({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const context = ports.context, operationId = ports.requestId, reportError = runtime.reportError, [group, setGroup] = useState<Group>();
  const forms = useStorageForms(ports, IDS, reportError);
  useLayoutEffect(() => {const next = createMessages(context,operationId,reportError);setGroup(next);return next.dispose;},[context,operationId,reportError]);
  return forms && group?.context === context ? <Messages key={forms.mount} group={group} forms={forms} runtime={runtime}/> : null;
}
export function Messages({group, forms, runtime}: {group: Group; forms: StorageForms; runtime: NativeShellRuntime}) {
  const {source} = group, state = useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot), failure = useSyncExternalStore(group.subscribeFailure,group.getFailure,group.getFailure), theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const form = forms.forms.logs, logs = useFormState<LogsDraft, LogSettings>(form), saved = logs.status, draft = logs.draft, saveState = useStorageSet(forms);
  const [now,setNow] = useState(Date.now), [filter, setFilter] = useState<Readonly<{kind: 'level' | 'category'}>>();
  const filterRef = useRef(filter);
  const openFilter = (kind: 'level' | 'category') => {if (!group.current() || filterRef.current) return; const next = {kind}; filterRef.current = next; setFilter(next);};
  const closeFilter = () => {if (!filter || filterRef.current !== filter) return; filterRef.current = undefined; setFilter(undefined);};
  useEffect(() => {void source.load();const timer = setInterval(() => {if (group.current()) setNow(Date.now());},30000);return () => clearInterval(timer);},[source,group]);
  const until = state.settings?.debugWindowUntil, recording = !!until && Date.parse(until) > now;
  const detail = () => {if (!group.current() || source.getSnapshot().changing) return;group.clearFailure();const minutes = saved?.logDetailMinutes;void source.detail(!recording, minutes !== undefined && LOG_DETAIL_MINUTES.includes(minutes) ? minutes : undefined).catch(error => {if (group.current()) group.failure(error);});};
  return <View style={{marginTop:14,gap:12}} testID="server-troubleshooting-messages">
    <Text style={[shellType(17,800),{color:theme.colors.text}]}>{t('web.logs.messages')}</Text><Text style={[shellType(13,400,18),{color:theme.colors.textSecondary}]}>{t('web.logs.messagesLede')}</Text>
    <View style={{padding:14,gap:8,borderRadius:14,backgroundColor:theme.colors.raised}}><Text style={[shellType(15,700),{color:theme.colors.text}]}>{t('settings.server.detailWindow.forMinutes',{minutes:saved?.logDetailMinutes ?? 30})}</Text><Text style={{color:theme.colors.textSecondary}}>{recording && until ? t('settings.server.detailWindow.until',{time:defaultI18n.time(Date.parse(until))}) : t('settings.server.detailWindow.help')}</Text><Button size="compact" loading={state.changing} disabled={state.changing} onPress={detail}>{t(recording ? 'settings.server.detailWindow.stop' : 'settings.server.detailWindow.start')}</Button></View>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 8}}>
      <Chip variant="pill" trailingIcon="i-down" onPress={() => openFilter('level')} accessibilityLabel={t('web.logs.level')}>{state.level ? t(`web.logs.level.${state.level}`) : t('web.logs.allLevels')}</Chip>
      <Chip variant="pill" trailingIcon="i-down" onPress={() => openFilter('category')} accessibilityLabel={t('feedback.category')}>{state.category ? t(`server.logCategory.${state.category}` as MessageId) : t('web.logs.everything')}</Chip>
      <Switch checked={state.live} onCheckedChange={value => {if (group.current()) group.setLive(value);}}>{t('next.cards.live')}</Switch>
      {state.live && <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}><View style={{width: 8, height: 8, borderRadius: 4, backgroundColor: theme.colors.healthy}}/><Text style={[shellType(12.5, 700), {color: theme.colors.healthy}]}>{t('web.logs.following')}</Text></View>}
      <Button size="small" variant="text" disabled={state.phase === 'loading'} onPress={() => {if (group.current()) {group.clearFailure(); void source.load();}}}>{t('web.selection.refresh')}</Button>
    </View>
    <Menu open={!!filter && filterRef.current === filter && group.current()} onOpenChange={open => {if (!open) closeFilter();}} presentation="sheet" title={t(filter?.kind === 'level' ? 'web.logs.level' : 'feedback.category')}
      items={(filter?.kind === 'level' ? [{value: '', label: t('web.logs.allLevels')}, ...logLevels.map(value => ({value, label: t(`web.logs.level.${value}`)}))]
        : [{value: '', label: t('web.logs.everything')}, ...logCategories.map(value => ({value, label: t(`server.logCategory.${value}` as MessageId)}))]).map(option => ({id: option.value || 'all', label: option.label, selected: option.value === (filter?.kind === 'level' ? state.level : state.category), onSelect: () => {
          if (!filter || filterRef.current !== filter || !group.current()) return;
          closeFilter();
          if (filter.kind === 'level' && (option.value === '' || logLevels.includes(option.value as typeof logLevels[number]))) void group.select(option.value as typeof state.level, source.getSnapshot().category);
          else if (filter.kind === 'category' && (option.value === '' || logCategories.includes(option.value as typeof logCategories[number]))) void group.select(source.getSnapshot().level, option.value as typeof state.category);
        }}))}/>
    {failure && <Text accessibilityRole="alert" style={{color:theme.colors.record}}>{t('error.unavailable')}</Text>}
    {state.phase === 'loading' && !state.lines.length && <Text style={{color:theme.colors.textSecondary}}>{t('web.logs.loadingMessages')}</Text>}{state.phase === 'ready' && !state.lines.length && <Text style={{color:theme.colors.textSecondary}}>{t('web.logs.noMessages')}</Text>}
    <View style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.messageLog, borderWidth: 1, borderColor: theme.colors.lineSoft}}>
      {state.lines.map((row, index) => <View key={row.sequence} style={{gap: 3, paddingVertical: 7, paddingHorizontal: 12, backgroundColor: row.level === 'error' ? alpha(theme.colors.danger, .07) : undefined, borderTopWidth: index ? 1 : 0, borderColor: alpha(theme.colors.lineStrong, .35)}}><View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 10}}><Text style={{fontFamily: 'monospace', fontSize: 11.5, color: theme.colors.textTertiary}}>{defaultI18n.time(Date.parse(row.at))}</Text><Text style={{fontFamily: 'monospace', fontSize: 11.5, fontWeight: '700', color: row.level === 'error' ? theme.colors.danger : row.level === 'warn' ? theme.colors.amber : row.level === 'info' ? theme.colors.textSecondary : theme.colors.textTertiary}}>{t(`web.logs.level.${row.level}`)}</Text><Text style={{fontFamily: 'monospace', fontSize: 11.5, color: theme.colors.textTertiary}}>{t(`server.logCategory.${row.category}` as MessageId)}</Text></View><Text selectable style={[shellType(13.5, 500, 19), {color: row.level === 'error' ? theme.colors.text : theme.colors.textSecondary}]}>{row.message}</Text></View>)}
    </View>
    {draft && <><Select label={t('next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.0.label')} value={draft.logLevel} options={logLevels.map((value,index)=>({value,label:t(`next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.0.control.options.${index}.label` as MessageId)}))} disabled={saveState.saving} onValueChange={value=>{if(group.current() && logLevels.includes(value as typeof logLevels[number]))form.set((old:LogsDraft)=>({...old,logLevel:value as LogsDraft['logLevel']}));}}/>{saved?.logDetailMinutes !== undefined && <Select label={t('next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.1.label')} value={String(draft.logDetailMinutes ?? 30)} options={LOG_DETAIL_MINUTES.map(value=>({value:String(value),label:t('web.dvr.minutes',{count:value})}))} disabled={saveState.saving} onValueChange={value=>{const minutes=Number(value);if(group.current() && LOG_DETAIL_MINUTES.includes(minutes as LogDetailMinutes))form.set((old:LogsDraft)=>({...old,logDetailMinutes:minutes as LogDetailMinutes}));}}/>}{saveState.dirty && <View style={{flexDirection:'row',gap:8}}><Button size="compact" variant="glass" disabled={saveState.saving} onPress={()=>{if(group.current())forms.set.discard();}}>{t('action.discard')}</Button><Button size="compact" disabled={saveState.saving || !!saveState.problem} onPress={()=>{if(group.current())void forms.set.save();}}>{t('action.save')}</Button></View>}</>}
  </View>;
}
