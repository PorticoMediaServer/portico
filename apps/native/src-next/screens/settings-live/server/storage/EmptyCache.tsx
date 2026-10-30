import React, {useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {SettingsCacheSource} from '../../../../../../../packages/client-core/src/server-admin/cache-source';
import {defaultI18n} from '../../../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../../shell/runtime';
import {Button} from '../../../../kit/controls/Button';
import {BottomSheet} from '../../../../kit/overlays/BottomSheet';
import {shellType} from '../../../../kit/shell/visual';
import {useTheme} from '../../../../kit/theme';
import {NativeScreenLifetime, screenScopeEnded} from '../../lifetime';
import {nativeStorageOwner} from './space-source';

function createCache(context: NativeShellBootstrapContext, reportError: (error: unknown) => void) {
  const lifetime = new NativeScreenLifetime(context);
  const current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
    ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  const source = new SettingsCacheSource(api, current, error => {lifetime.dispose();reportError(error);});
  return {context, source, current, dispose: () => {source.dispose();lifetime.dispose();}};
}
/** Keep this mounted for the Storage page, even while an older tab is hidden:
 * changing the visible section must not discard a submitted restart lock. */
export function NativeEmptyCache({context, runtime, visible = true}: Readonly<{context: NativeShellBootstrapContext; runtime: NativeShellRuntime; visible?: boolean}>) {
  const [group, setGroup] = useState<ReturnType<typeof createCache>>();
  const reportError = runtime.reportError;
  useLayoutEffect(() => {const next = createCache(context, reportError);setGroup(next);return next.dispose;}, [context, reportError]);
  return group?.context === context ? <CacheButton group={group} runtime={runtime} visible={visible}/> : null;
}
function CacheButton({group, runtime, visible}: Readonly<{group: ReturnType<typeof createCache>; runtime: NativeShellRuntime; visible: boolean}>) {
  const {source} = group, theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [confirmation, setConfirmation] = useState<object>();
  const view = useMemo(() => ({source, active: true, visible: true, confirmation: undefined as object | undefined}), [source]);
  view.visible = visible;
  useLayoutEffect(() => {view.active = true;return () => {view.active = false;view.confirmation = undefined;};}, [view]);
  const current = () => view.active && view.visible && view.source === source && group.current();
  const held = confirmation && view.confirmation === confirmation && current() ? confirmation : undefined;
  const open = () => {if (!current() || source.getSnapshot().phase !== 'ready') return;const next = {};view.confirmation = next;setConfirmation(next);};
  const close = () => {if (!held || view.confirmation !== held || source.getSnapshot().phase === 'busy') return;view.confirmation = undefined;setConfirmation(undefined);};
  const submit = async () => {
    const captured = held;if (!captured || !current() || view.confirmation !== captured || source.getSnapshot().phase !== 'ready') return;
    await source.empty(() => current() && view.confirmation === captured);
    if (current() && view.confirmation === captured) {view.confirmation = undefined;setConfirmation(undefined);}
  };
  return <View style={{display: visible ? 'flex' : 'none', marginTop: 16, gap: 8}} testID="server-storage-empty-cache">
    <Button size="compact" variant="glass" disabled={!group.current() || state.phase !== 'ready'} onPress={open}>{t('next.settingsSpec.server.storage.groups.0.rows.3.control.label')}</Button>
    {state.phase === 'scheduled' && <Text accessibilityRole="text" style={[shellType(13,400),{color:theme.colors.textSecondary}]}>{t('web.maintenance.restartingTitle')}</Text>}
    {state.phase === 'uncertain' && <Text accessibilityRole="alert" style={[shellType(13,500),{color:theme.colors.record}]}>{t('error.unconfirmedChange')}</Text>}
    <BottomSheet open={!!held} onOpenChange={value => {if (!value) close();}} title={t('next.settingsSpec.server.storage.groups.0.rows.3.control.label')} dismissOnBack={state.phase !== 'busy'} dismissOnGesture={state.phase !== 'busy'} dismissOnOutsidePress={state.phase !== 'busy'} footer={<View style={{flexDirection:'row',gap:8}}><Button variant="glass" disabled={state.phase === 'busy'} onPress={close}>{t('action.cancel')}</Button><Button variant="primary" loading={state.phase === 'busy'} disabled={state.phase !== 'ready'} onPress={() => {void submit();}}>{t('next.settingsSpec.server.storage.groups.0.rows.3.control.label')}</Button></View>}>
      <View style={{gap:12}}><Text style={[shellType(14,400,20),{color:theme.colors.textSecondary}]}>{t('next.settingsSpec.server.storage.groups.0.rows.3.control.confirm')}</Text><Text style={[shellType(14,400,20),{color:theme.colors.textSecondary}]}>{t('next.settingsSpec.server.general.groups.3.rows.1.control.confirm')}</Text></View>
    </BottomSheet>
  </View>;
}
