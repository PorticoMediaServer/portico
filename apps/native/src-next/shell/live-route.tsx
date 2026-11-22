import React from 'react';
import {Platform} from 'react-native';
import type {NativeShellBootstrapContext} from './bootstrap';
import {nativeShellRoutes, type NativeRouteRenderer} from './route-registry';
import {nativeRouteCrypto} from '../platform/server-connections';
import {NativeScreenLifetime} from '../screens/settings-live/lifetime';
import {NativeLiveController} from '../screens/settings-live/live-controller';
import {nativeGuideMemory} from '../screens/settings-live/guide-memory';
import {nativeScreenServices} from '../screens/native-screen-services';
import {createNativeRecordingsController} from '../screens/settings-live/recordings-controller';
import {GuidePage as TouchGuidePage} from '../pages/touch/live/GuidePage';
import {GuidePage as TVGuidePage} from '../pages/tv/live/GuidePage';
import {NativeLiveOwnerController} from '../screens/settings-live/live-owner-controller';
import {withGuardedRaw} from '../../../../packages/client-core/src/home-selection/list-destinations';
import {nativeSettingsRequestId} from '../screens/settings-live/settings-ports';
import {publishNativeLiveWatchPort} from '../screens/settings-live/watch-port';

export function createNativeLiveRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = new NativeScreenLifetime(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => context.api.request<T>(path, method, body, next), signal)};
  const dvrRequestId = async () => {lifetime.assert(); return [...nativeRouteCrypto.random(24)].map(byte => byte.toString(16).padStart(2, '0')).join('');};
  const controller = new NativeLiveController(lifetime, api, Intl.DateTimeFormat().resolvedOptions().timeZone, {port: nativeGuideMemory(context.session()?.viewer, lifetime), reportError: error => nativeScreenServices(context).reportError(error)});
  const recordings = createNativeRecordingsController(lifetime, api, dvrRequestId);
  const operationId = () => [...nativeRouteCrypto.random(24)].map(byte => byte.toString(16).padStart(2, '0')).join('');
  const owner = new NativeLiveOwnerController(lifetime, withGuardedRaw(api, context.api, () => lifetime.assert()), operationId);
  const releaseWatch = publishNativeLiveWatchPort(context, lifetime, {recordings, live: controller, owner, requestId: dvrRequestId});
  return {
    render: (route, runtime) => {
      lifetime.assert();
      if (route.kind !== 'shell' || route.destination.kind !== 'live') throw new Error('native_live_route_mismatch');
      const GuidePage = Platform.isTV ? TVGuidePage : TouchGuidePage;
      return <GuidePage ports={{context, dvrRequestId, requestId: nativeSettingsRequestId}} controller={controller} recordings={recordings} owner={owner} runtime={runtime}/>;
    },
    dispose: () => {releaseWatch(); owner.dispose(); controller.dispose(); recordings.dispose(); lifetime.dispose();},
  };
}

nativeShellRoutes.registerDestination('live', createNativeLiveRenderer);
