import React from 'react';
import {Platform, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {NativeScreenLifetime} from '../lifetime';
import {createNativeSettingsPorts} from '../settings-ports';
import {createNativeRecordingsController} from '../recordings-controller';
import {NativeRecordingsScreen} from '../RecordingsScreen';
import {NativeServerTroubleshooting} from './TroubleshootingPage';
import {PushedNavBar} from '../../../kit/shell';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../../shell/route-registry';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';

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
  const insets = useSafeAreaInsets();
  return <View style={{flex: 1, paddingTop: insets.top}}><NativeRecordingsScreen controller={controller} ports={ports} runtime={runtime} onGuide={onGuide}/></View>;
}

function ServerFeedbackDestination({ports, runtime, onBack}: {ports: ReturnType<typeof createNativeSettingsPorts>; runtime: NativeShellRuntime; onBack: () => void}) {
  const insets = useSafeAreaInsets();
  return <View style={{flex: 1, paddingTop: insets.top + 14}}>
    <PushedNavBar title={runtime.text.t('settings.server.troubleshooting')} backLabel={runtime.text.t('action.back')} onBack={onBack}/>
    <NativeServerTroubleshooting ports={ports} runtime={runtime} compactHeader initialTab="feedback"/>
  </View>;
}

if (!Platform.isTV) {
  nativeShellRoutes.registerRoute('recordings', createNativeRecordingsRenderer);
  nativeShellRoutes.registerRoute('server-feedback', createNativeServerFeedbackRenderer);
}
