import {useCallback, useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {SettingsCacheSource} from '../../../../../../packages/client-core/src/server-admin/cache-source';
import {readNoticesKeep, saveNoticesKeep, storageLineWords, type NoticesKeep} from '../../../../../../packages/client-core/src/server-admin/server-storage';
import {withGuardedRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import {settingsRowIndex} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {NativeScreenLifetime, screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {NativeStorageSpaceSource, nativeStorageOwner} from '../../../screens/settings-live/server/storage/space-source';
import {useSettingsRequestScope} from '../../../screens/settings-live/use-settings-request-scope';

const noSubscription = () => () => {};
const noSnapshot = () => undefined;

export function useStorageSpace(context: NativeShellBootstrapContext, runtime: NativeShellRuntime) {
  const [group, setGroup] = useState<{context: NativeShellBootstrapContext; source: NativeStorageSpaceSource}>();
  const {reportError} = runtime;
  useLayoutEffect(() => {
    const source = new NativeStorageSpaceSource(context, reportError);
    setGroup({context, source});
    void source.load();
    return source.dispose;
  }, [context, reportError]);
  const source = group?.context === context && nativeStorageOwner(context) ? group.source : undefined;
  const snapshot = source?.getSnapshot ?? noSnapshot;
  const state = useSyncExternalStore(source?.subscribe ?? noSubscription, snapshot, snapshot);
  const {t} = runtime.text, usage = state?.usage;
  const total = usage?.lines.reduce((sum, line) => sum + (line.bytes ?? 0), 0) ?? 0;
  return {
    state, usage, total: defaultI18n.bytes(total), retry: () => {void source?.load();},
    lines: usage?.lines.map(line => {
      const {name, help} = storageLineWords(line, t);
      return {id: line.id, name, help: help || undefined,
        size: line.bytes === undefined ? t('server.space.sizeUnknown') : defaultI18n.bytes(line.bytes),
        share: total > 0 && line.bytes !== undefined ? line.bytes / total : undefined};
    }) ?? [],
  };
}

function createCache(context: NativeShellBootstrapContext, reportError: (error: unknown) => void) {
  const lifetime = new NativeScreenLifetime(context);
  const current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
    ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  const source = new SettingsCacheSource(api, current, error => {lifetime.dispose(); reportError(error);});
  return {context, source, current, dispose: () => {source.dispose(); lifetime.dispose();}};
}

export function useEmptyCache(context: NativeShellBootstrapContext, runtime: NativeShellRuntime) {
  const [group, setGroup] = useState<ReturnType<typeof createCache>>();
  const {reportError} = runtime;
  useLayoutEffect(() => {
    const next = createCache(context, reportError);
    setGroup(next);
    return next.dispose;
  }, [context, reportError]);
  return group?.context === context ? group : undefined;
}

export function useCacheConfirmation(group: ReturnType<typeof createCache>) {
  const {source} = group, state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [confirmation, setConfirmation] = useState<object>();
  const view = useMemo(() => ({active: true, confirmation: undefined as object | undefined}), [source]);
  useLayoutEffect(() => {
    view.active = true;
    return () => {view.active = false; view.confirmation = undefined;};
  }, [view]);
  const current = () => view.active && group.current();
  const held = confirmation && view.confirmation === confirmation && current() ? confirmation : undefined;
  return {
    state, held, disabled: !group.current() || state.phase !== 'ready',
    open: () => {
      if (!current() || source.getSnapshot().phase !== 'ready') return;
      const next = {}; view.confirmation = next; setConfirmation(next);
    },
    close: () => {
      if (!held || view.confirmation !== held || source.getSnapshot().phase === 'busy') return;
      view.confirmation = undefined; setConfirmation(undefined);
    },
    submit: async () => {
      const captured = held;
      if (!captured || !current() || view.confirmation !== captured || source.getSnapshot().phase !== 'ready') return;
      await source.empty(() => current() && view.confirmation === captured);
      if (current() && view.confirmation === captured) {view.confirmation = undefined; setConfirmation(undefined);}
    },
  };
}

const notification = settingsRowIndex.get('storage.keepNotifications')!.row;
const messages = settingsRowIndex.get('storage.keepLogsLink')!.row;

export function useStorageKeeping(context: NativeShellBootstrapContext, t: NativeShellRuntime['text']['t']) {
  const {api, lifetime} = useSettingsRequestScope(context);
  const documents = useMemo(() => withGuardedRaw(api, context.api, () => {if (!lifetime.current()) throw screenScopeEnded();}), [api, context, lifetime]);
  const [notices, setNotices] = useState<NoticesKeep>(), [busy, setBusy] = useState(false), [failed, setFailed] = useState(false);
  const load = useCallback(() => {
    void readNoticesKeep(api).then(value => {if (lifetime.current()) setNotices(value);},
      () => {if (lifetime.current()) setFailed(true);});
  }, [api, lifetime]);
  useEffect(load, [load]);
  return {
    notices, busy, failed, notification, messages,
    options: (notification.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label)})),
    change: async (next: string) => {
      if (!notices || busy || !lifetime.current()) return;
      setBusy(true); setFailed(false);
      try {await saveNoticesKeep(documents, notices, Number(next));}
      catch {if (lifetime.current()) setFailed(true);}
      finally {if (lifetime.current()) {setBusy(false); load();}}
    },
  };
}
