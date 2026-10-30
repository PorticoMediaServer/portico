import {readEventState} from '../../platform/event-resync';
import React from 'react';
import {Platform} from 'react-native';
import {AdminSessionsClient} from '../../../../../packages/client-core/src/playback-v1/admin-sessions';
import {localV1Http} from '../../../../../packages/client-core/src/playback-v1/local-http';
import {ConsoleClient} from '../../../../../packages/client-core/src/console';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeRouteRenderer} from '../../shell/route-registry';
import {NativeScreenLifetime} from './lifetime';
import {createBoundNativeSettingsController} from './settings-controller';
import {nativeDevicePreferences} from './device-preference-consumer';
import {NativeDashboardController, type NativeDashboardAdmission} from './dashboard-controller';
import {nativeServerAdmission} from './server/tv-server-reader';
import {NativeTVServerScreen} from './server/TVServerScreen';
import {isTVServerPage} from './server/TVServerPageMenu';
import {NativeEqualizerSettingsScreen} from './EqualizerSettingsScreen';
import {NativeSettingsScreen} from './SettingsScreen';
import {NativeDashboardScreen} from './DashboardScreen';
import {createNativeSettingsPorts, nativeSettingsRequestId} from './settings-ports';
import {createNativeSettingsDownloads} from './downloads-summary';

/**
 * The one composition Accounts hands the shell: the Settings destination and the read-only
 * owner Dashboard route. It registers nothing itself; the shell's settings-register module owns
 * the single registration and the TV/phone entry imports.
 */
function createComposition(context: NativeShellBootstrapContext) {
  const lifetime = new NativeScreenLifetime(context);
  const devicePreferences = nativeDevicePreferences(context);
  const settings = createBoundNativeSettingsController(lifetime, context);
  // Borrow the genuine viewer-scoped downloads store for a read-only size row.
  // This never creates a second store or starts the online service by itself;
  // the real Downloads route upgrades the same lease if it is opened.
  let downloads: ReturnType<typeof createNativeSettingsDownloads> | undefined;
  try {if (!Platform.isTV) downloads = createNativeSettingsDownloads(context);} catch { /* No session/authority: the row stays out. */ }
  const ports = createNativeSettingsPorts(context, lifetime, route => context.navigation.open(route, context.authority()), downloads);
  const unsubscribe = context.subscribePreferences(() => {if (lifetime.current()) settings.accept(context.preferences());});
  const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => (!method || method === 'GET') ? readEventState(context.events, next, () => context.api.request<T>(path, method, body, next)) : context.api.request<T>(path, method, body, next), signal)};
  const raw = localV1Http(context.api);
  const sessions = new AdminSessionsClient({send: request => lifetime.request(signal => request.method === 'GET' ? readEventState(context.events, signal, () => raw.send({...request, signal})) : raw.send({...request, signal}), request.signal)}, nativeSettingsRequestId);
  const admission = (): NativeDashboardAdmission => nativeServerAdmission(context.account, lifetime);
  const dashboard = new NativeDashboardController(lifetime, admission, {sessions, console: new ConsoleClient(api, {serverId: lifetime.authority.serverId, viewerId: lifetime.authority.profileId})});
  let disposed = false;
  const dispose = () => {if (disposed) return; disposed = true; unsubscribe(); dashboard.dispose(); try {downloads?.dispose();} catch { /* Composition retirement is final. */ } lifetime.dispose();};
  return {
    settings: {
      render: (route, runtime) => {
        lifetime.assert();
        if (route.kind !== 'shell' || route.destination.kind !== 'settings') throw new Error('native_settings_route_mismatch');
        return <NativeSettingsScreen ports={ports} controller={settings} runtime={runtime} devicePreferences={devicePreferences} dashboard={dashboard}/>;
      },
      dispose,
    } satisfies NativeRouteRenderer,
    serverSettings: {
      render: (route, runtime) => {
        lifetime.assert();
        if (route.kind !== 'server-settings') throw new Error('native_server_settings_route_mismatch');
        const allowed = admission();
        const fallback = <NativeSettingsScreen ports={ports} controller={settings} runtime={runtime} devicePreferences={devicePreferences} dashboard={dashboard}/>;
        if (allowed === 'denied') return fallback;
        if (!Platform.isTV && allowed === 'owner' && route.page === 'live') return <NativeSettingsScreen key={route.channelId ?? 'live'} ports={ports} controller={settings} runtime={runtime} devicePreferences={devicePreferences} dashboard={dashboard} initialPage="live" initialChannelId={route.channelId}/>;
        if (!Platform.isTV) return <NativeDashboardScreen ports={ports} controller={dashboard} runtime={runtime} deniedFallback={fallback}/>;
        const requested = isTVServerPage(route.page) ? route.page : undefined;
        const page = allowed === 'owner' ? requested ?? 'general' : requested === 'people' ? 'people' : 'dashboard';
        return <NativeTVServerScreen ports={ports} runtime={runtime} dashboard={dashboard} initialPage={page}/>;
      },
      dispose,
    } satisfies NativeRouteRenderer,
    dashboard: {
      render: (route, runtime) => {
        lifetime.assert();
        if (route.kind !== 'server-dashboard') throw new Error('native_server_dashboard_route_mismatch');
        if (Platform.isTV && admission() !== 'denied') return <NativeTVServerScreen ports={ports} runtime={runtime} dashboard={dashboard} initialPage="dashboard"/>;
        return <NativeDashboardScreen ports={ports} controller={dashboard} runtime={runtime}
          deniedFallback={<NativeSettingsScreen ports={ports} controller={settings} runtime={runtime} devicePreferences={devicePreferences} dashboard={dashboard}/>}/>;
      },
      dispose,
    } satisfies NativeRouteRenderer,
  } as const;
}

export function createNativeSettingsRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  return createComposition(context).settings;
}

export function createNativeServerDashboardRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  return createComposition(context).dashboard;
}

export function createNativeTVServerSettingsRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  return createComposition(context).serverSettings;
}

/** Additive device editor route; playback and Settings share the native singleton. */
export function createNativeEqualizerSettingsRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = new NativeScreenLifetime(context);
  return {
    render: (route, runtime) => {
      lifetime.assert();
      if (route.kind !== 'settings-equalizer') throw new Error('native_equalizer_settings_route_mismatch');
      return <NativeEqualizerSettingsScreen runtime={runtime} onBack={() => {lifetime.assert(); context.navigation.back(context.authority());}}/>;
    },
    dispose: () => lifetime.dispose(),
  };
}
