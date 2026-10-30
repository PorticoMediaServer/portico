import {useSyncExternalStore} from 'react';
import {NativeModules, NativeEventEmitter, Platform} from 'react-native';
import {createPlatformPostureStore, type PlatformPostureTransport} from './platformPostureStore';
export type {PlatformPostureSnapshot, PlatformFold, PlatformPostureBounds} from './platformPostureStore';

const bridge = Platform.OS === 'android' ? NativeModules.PorticoPosture : undefined;
const transport:PlatformPostureTransport|null = typeof bridge?.getSnapshot === 'function' ? {
  getSnapshot: () => bridge.getSnapshot(),
  subscribe: listener => {
    const subscription = new NativeEventEmitter(bridge).addListener('PorticoPostureChanged',listener);
    return () => subscription.remove();
  },
} : null;
export const platformPosture = createPlatformPostureStore(transport);
export function usePlatformPosture() { return useSyncExternalStore(platformPosture.subscribe,platformPosture.getSnapshot); }
