import {readEventState} from '../platform/event-resync';
import React from 'react';
import {Platform} from 'react-native';
import {readServerNowPlaying} from '../../../../packages/client-core/src/admin/server-now-playing';
import {DashboardDataClient} from '../../../../packages/client-core/src/admin/dashboard';
import {readServerStorage} from '../../../../packages/client-core/src/server-admin/server-storage';
import type {NativeShellBootstrapContext} from './bootstrap';
import type {NativeRouteRenderer} from './route-registry';
import {NativeScreenLifetime} from '../screens/settings-live/lifetime';
import {createBoundNativeSettingsController} from '../screens/settings-live/settings-controller';
import {nativeDevicePreferences} from '../screens/settings-live/device-preference-consumer';
import {NativeDashboardController, type NativeDashboardAdmission, type NativeDashboardReaders} from '../screens/settings-live/dashboard-controller';
import {nativeServerAdmission} from '../screens/settings-live/server/tv-server-reader';
import {ServerSettingsPage} from '../pages/tv/settings/ServerSettingsPage';
import {isTVServerPage} from '../pages/shared/settings/tv-server-pages';
import {EqualizerSettingsPage} from '../pages/shared/settings/EqualizerSettingsPage';
import {NativeSettingsPage} from '../pages/shared/settings/SettingsPage';
import {ServerDashboardPage} from '../pages/touch/dashboard/ServerDashboardPage';
import {createNativeSettingsPorts, nativeSettingsRequestId} from '../screens/settings-live/settings-ports';
import {createNativeSettingsDownloads} from '../screens/settings-live/downloads-summary';

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
  // The Dashboard reads the rebuilt server: Now playing (all of it at once, so never a next page), the Dashboard read and
  // the data folder's storage.
  const sessions = {list: async (_cursor?: string, _limit?: number, signal?: AbortSignal) => {
    const page = await readServerNowPlaying(api, signal);
    return {items: page.items, totals: page.totals, scope: {serverId: lifetime.authority.serverId}, nextCursor: undefined, revision: undefined};
  }} as unknown as NativeDashboardReaders['sessions'];
  const status = new DashboardDataClient(api);
  const admission = (): NativeDashboardAdmission => nativeServerAdmission(context.account, lifetime);
  const dashboard = new NativeDashboardController(lifetime, admission, {sessions, status: () => status.status(lifetime.signal), storage: () => readServerStorage(api, lifetime.signal)});
  let disposed = false;
  const dispose = () => {if (disposed) return; disposed = true; unsubscribe(); dashboard.dispose(); try {downloads?.dispose();} catch { /* Composition retirement is final. */ } lifetime.dispose();};
  return {
    settings: {
      render: (route, runtime) => {
        lifetime.assert();
        if (route.kind !== 'shell' || route.destination.kind !== 'settings') throw new Error('native_settings_route_mismatch');
        return <NativeSettingsPage ports={ports} controller={settings} runtime={runtime} devicePreferences={devicePreferences} dashboard={dashboard} initialPage={route.destination.page}/>;
      },
      dispose,
    } satisfies NativeRouteRenderer,
    serverSettings: {
      render: (route, runtime) => {
        lifetime.assert();
        if (route.kind !== 'server-settings') throw new Error('native_server_settings_route_mismatch');
        const allowed = admission();
        const fallback = <NativeSettingsPage ports={ports} controller={settings} runtime={runtime} devicePreferences={devicePreferences} dashboard={dashboard}/>;
        if (allowed === 'denied') return fallback;
        // A named server page opens on that page of Settings, which shows it only to a viewer who may
        // see it (N5-3: Review devices opens People & devices, not the Dashboard). No page is the Dashboard.
        if (!Platform.isTV && route.page) return <NativeSettingsPage key={route.page === 'live' ? route.channelId ?? 'live' : route.page} ports={ports} controller={settings} runtime={runtime} devicePreferences={devicePreferences} dashboard={dashboard} initialPage={route.page} initialChannelId={route.page === 'live' ? route.channelId : undefined}/>;
        if (!Platform.isTV) return <ServerDashboardPage ports={ports} controller={dashboard} runtime={runtime} deniedFallback={fallback}/>;
        const requested = isTVServerPage(route.page) ? route.page : undefined;
        const page = allowed === 'owner' ? requested ?? 'general' : requested === 'people' ? 'people' : 'dashboard';
        return <ServerSettingsPage ports={ports} runtime={runtime} dashboard={dashboard} initialPage={page}/>;
      },
      dispose,
    } satisfies NativeRouteRenderer,
    dashboard: {
      render: (route, runtime) => {
        lifetime.assert();
        if (route.kind !== 'server-dashboard') throw new Error('native_server_dashboard_route_mismatch');
        const fallback = <NativeSettingsPage ports={ports} controller={settings} runtime={runtime} devicePreferences={devicePreferences} dashboard={dashboard}/>;
        // TV: the Dashboard is a page of Server settings; a viewer the server turns away gets Settings at once.
        if (Platform.isTV) return admission() !== 'denied' ? <ServerSettingsPage ports={ports} runtime={runtime} dashboard={dashboard} initialPage="dashboard"/> : fallback;
        return <ServerDashboardPage ports={ports} controller={dashboard} runtime={runtime} deniedFallback={fallback}/>;
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
      return <EqualizerSettingsPage runtime={runtime} onBack={() => {lifetime.assert(); context.navigation.back(context.authority());}}/>;
    },
    dispose: () => lifetime.dispose(),
  };
}
