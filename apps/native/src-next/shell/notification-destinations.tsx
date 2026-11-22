import React from 'react';
import {Platform, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {NativeScreenLifetime} from '../screens/settings-live/lifetime';
import {createNativeSettingsPorts} from '../screens/settings-live/settings-ports';
import {createNativeRecordingsController} from '../screens/settings-live/recordings-controller';
import {RecordingsPage as TouchRecordingsPage} from '../pages/touch/live/RecordingsPage';
import {RecordingsPage as TVRecordingsPage} from '../pages/tv/live/RecordingsPage';
import {ServerTroubleshootingPage} from '../pages/touch/settings/ServerTroubleshootingPage';
import {nativeShellRoutes, type NativeRouteRenderer} from './route-registry';
import type {NativeShellBootstrapContext} from './bootstrap';
import type {NativeShellRuntime} from './runtime';

/** The existing recordings list and the existing Troubleshooting Feedback tab, mounted
 * as real destinations for notification Open actions. They read through the same owner
 * ports and controllers as their Settings pages; no notice action is synthesized. */
export function createNativeRecordingsRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = new NativeScreenLifetime(context);
  const ports = createNativeSettingsPorts(context, lifetime, route => context.navigation.open(route, context.authority()));
  const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => lifetime.request(next => context.api.request<T>(path, method, body, next), signal)};
  const controller = createNativeRecordingsController(lifetime, api, ports.dvrRequestId);
  return {
    render: (route, runtime) => {
      lifetime.assert();
      if (route.kind !== 'recordings') throw new Error('native_recordings_route_mismatch');
      return <RecordingsDestination controller={controller} ports={ports} runtime={runtime} onGuide={() => {if (lifetime.current()) context.navigation.back(context.authority());}}/>;
    },
    dispose: () => {controller.dispose(); lifetime.dispose();},
  };
}

export function createNativeServerFeedbackRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = new NativeScreenLifetime(context);
  const ports = createNativeSettingsPorts(context, lifetime, route => context.navigation.open(route, context.authority()));
  return {
    render: (route, runtime) => {
      lifetime.assert();
      if (route.kind !== 'server-feedback') throw new Error('native_server_feedback_route_mismatch');
      return <ServerFeedbackDestination ports={ports} runtime={runtime} onBack={() => {if (lifetime.current()) context.navigation.back(context.authority());}}/>;
    },
    dispose: () => lifetime.dispose(),
  };
}

function RecordingsDestination({controller, ports, runtime, onGuide}: {controller: ReturnType<typeof createNativeRecordingsController>; ports: ReturnType<typeof createNativeSettingsPorts>; runtime: NativeShellRuntime; onGuide: () => void}) {
  const RecordingsPage = Platform.isTV ? TVRecordingsPage : TouchRecordingsPage;
  return <RecordingsPage controller={controller} ports={ports} runtime={runtime} onGuide={onGuide}/>;
}

function ServerFeedbackDestination({ports, runtime, onBack}: {ports: ReturnType<typeof createNativeSettingsPorts>; runtime: NativeShellRuntime; onBack: () => void}) {
  const insets = useSafeAreaInsets();
  return <View style={{flex: 1, paddingTop: insets.top + 14}}>
    <ServerTroubleshootingPage ports={ports} runtime={runtime} initialTab="feedback" back={{label: runtime.text.t('action.back'), onPress: onBack}}/>
  </View>;
}

if (!Platform.isTV) {
  nativeShellRoutes.registerRoute('recordings', createNativeRecordingsRenderer);
  nativeShellRoutes.registerRoute('server-feedback', createNativeServerFeedbackRenderer);
}
