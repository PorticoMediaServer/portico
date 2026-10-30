import React from 'react';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../shell/route-registry';
import {nativeRouteCrypto} from '../../platform/server-connections';
import {NativeScreenLifetime} from './lifetime';
import {NativeLiveController} from './live-controller';
import {nativeGuideMemory} from './guide-memory';
import {nativeScreenServices} from '../native-screen-services';
import {createNativeRecordingsController} from './recordings-controller';
import {NativeLiveScreen} from './LiveScreen';
import {NativeLiveOwnerController} from './live-owner-controller';
import {nativeSettingsRequestId} from './settings-ports';
import {publishNativeLiveWatchPort} from './watch-port';

export function createNativeLiveRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = new NativeScreenLifetime(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => context.api.request<T>(path, method, body, next), signal)};
  const dvrRequestId = async () => {lifetime.assert(); return [...nativeRouteCrypto.random(24)].map(byte => byte.toString(16).padStart(2, '0')).join('');};
  const controller = new NativeLiveController(lifetime, api, Intl.DateTimeFormat().resolvedOptions().timeZone, {port: nativeGuideMemory(context.session()?.viewer, lifetime), reportError: error => nativeScreenServices(context).reportError(error)});
  const recordings = createNativeRecordingsController(lifetime, api, dvrRequestId);
  const operationId = () => [...nativeRouteCrypto.random(24)].map(byte => byte.toString(16).padStart(2, '0')).join('');
  const owner = new NativeLiveOwnerController(lifetime, api, operationId);
  const releaseWatch = publishNativeLiveWatchPort(context, lifetime, {recordings, live: controller, owner, requestId: dvrRequestId});
  return {
    render: (route, runtime) => {
      lifetime.assert();
      if (route.kind !== 'shell' || route.destination.kind !== 'live') throw new Error('native_live_route_mismatch');
      return <NativeLiveScreen ports={{context, dvrRequestId, requestId: nativeSettingsRequestId}} controller={controller} recordings={recordings} owner={owner} runtime={runtime}/>;
    },
    dispose: () => {releaseWatch(); owner.dispose(); controller.dispose(); recordings.dispose(); lifetime.dispose();},
  };
}

nativeShellRoutes.registerDestination('live', createNativeLiveRenderer);
