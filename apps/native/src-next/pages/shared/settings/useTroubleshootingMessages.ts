import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {KEEP_MESSAGE_DAYS, SettingsMessagesSource, type KeepMessageDays} from '../../../../../../packages/client-core/src/server-admin/messages-source';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeScreenLifetime, screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {nativeStorageOwner} from '../../../screens/settings-live/server/storage/space-source';
import {AppState} from 'react-native';


/** The owner and administrators read Messages (console rule 1, frame 33); only the owner keeps it and records more detail. */
/** Troubleshooting's server calls on the phone: on this screen's lifetime, and only while `current` holds. */
export function troubleshootingApi(context: NativeShellBootstrapContext, lifetime: NativeScreenLifetime, current: () => boolean) {
  return {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
    ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
}
export function messagesReader(context: NativeShellBootstrapContext): boolean {
  const authority = context.authority(), account = context.account;
  return nativeStorageOwner(context) || context.current() && account.serverId === authority.serverId && account.account.id === authority.accountId && !account.account.disabled && account.canManage;
}
function createMessages(context: NativeShellBootstrapContext, reportError: NativeShellRuntime['reportError']) {
  const lifetime = new NativeScreenLifetime(context), listeners = new Set<() => void>(), owner = nativeStorageOwner(context);
  const current = () => lifetime.current() && messagesReader(context);
  const api = troubleshootingApi(context, lifetime, current);
  let failed = false, desired = false, foreground = AppState.currentState === 'active', token: object | undefined, timer: ReturnType<typeof setTimeout> | undefined;
  const notify = () => {for (const listener of listeners) listener();};
  const handleFailure = (error: unknown) => {
    if (source.getSnapshot().phase === 'idle') {const owned = current();dispose();if (owned) reportError(error);}
    else {failed = true;notify();}
  };
  // How long messages are kept is the owner's alone: an administrator's source never reads it.
  const source = new SettingsMessagesSource(api, undefined, current, handleFailure, {keeping: owner});
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
  return {context, source, current, owner, dispose, setLive, failure: handleFailure,
    getFailure: () => failed, subscribeFailure: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};}, clearFailure: () => {failed = false;notify();},
    select: async (level: Parameters<SettingsMessagesSource['select']>[0], kind: Parameters<SettingsMessagesSource['select']>[1]) => {if (!current()) return;const captured = {};selection = captured;stop();await source.select(level, kind);if (selection === captured && current() && desired) start(false);},
  };
}
export type Group = ReturnType<typeof createMessages>;
/** `pageScrollY` is how far the page is scrolled and `scrollToY` scrolls it: away from the log's top, new lines wait. */
export function useMessages(ports: NativeSettingsLivePorts, runtime: NativeShellRuntime) {
  const context = ports.context, reportError = runtime.reportError, [group, setGroup] = useState<Group>();
  useLayoutEffect(() => {const next = createMessages(context,reportError);setGroup(next);return next.dispose;},[context,reportError]);
  return group?.context === context ? group : undefined;
}
export function useMessageControls(group: Group, runtime: NativeShellRuntime) {
  const {source} = group, state = useSyncExternalStore(source.subscribe,source.getSnapshot,source.getSnapshot), failure = useSyncExternalStore(group.subscribeFailure,group.getFailure,group.getFailure), t = runtime.text.t as typeof defaultI18n.t;
  const [now,setNow] = useState(serverNow), [filter, setFilter] = useState<Readonly<{kind: 'level' | 'category'}>>();
  const filterRef = useRef(filter);
  const openFilter = (kind: 'level' | 'category') => {if (!group.current() || filterRef.current) return; const next = {kind}; filterRef.current = next; setFilter(next);};
  const closeFilter = () => {if (!filter || filterRef.current !== filter) return; filterRef.current = undefined; setFilter(undefined);};
  useEffect(() => {void source.load();const timer = setInterval(() => {if (group.current()) setNow(serverNow());},30000);return () => clearInterval(timer);},[source,group]);
  const until = state.detailUntil, recording = !!until && Date.parse(until) > now;
  const detail = () => {if (!group.current() || source.getSnapshot().changing) return;group.clearFailure();void source.detail(!recording).catch(error => {if (group.current()) group.failure(error);});};
  const keep = (days: KeepMessageDays) => {if (!group.current()) return;group.clearFailure();void source.keep(days).catch(error => {if (group.current()) group.failure(error);});};
  return {state, failure, source, t, filter, filterRef, openFilter, closeFilter, until, recording, detail, keep};
}
