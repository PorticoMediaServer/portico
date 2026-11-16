/**
 * `react-native`, as the shared TV screens (`apps/native/src-next`) see it in a TV browser.
 *
 * Everything is react-native-web except the exports below, which the shared code uses with the
 * names and behaviour of the Apple TV build (react-native-tvos), as the Fire TV app's shim does
 * (`apps/vega/src/shims/react-native.ts`):
 *
 * - `Platform`: a television (`isTV`), on the web.
 * - `View`, `Pressable`, `TVFocusGuideView`, `findNodeHandle`: the focus engine (`focus.tsx`,
 *   `components.tsx`); a host's handle is the element itself.
 * - `useTVEventHandler`, `TVEventHandler`, `TVEventControl`, `BackHandler`: the remote (`keys.ts`).
 * - `NativeModules`, `NativeEventEmitter`, `requireNativeComponent`, `UIManager`: Portico's modules
 *   and views, from the Fire TV bridge (`apps/vega/src/bridge`) on browser stand-ins (`src/sdk`).
 * - `useWindowDimensions`, `Dimensions`, `PixelRatio`: the 1920×1080 design canvas (`canvas.tsx`).
 *
 * Vite resolves `react-native` to this file for everything outside this folder (vite.config.ts).
 */
import * as Web from 'react-native-web';
import {useEffect, useRef} from 'react';
import {porticoModules, porticoViewConfigs, porticoViews} from '../../../vega/src/bridge';
import {eventsOf, type ModuleEvents} from '../../../vega/src/bridge/events';
import {addBackHandler, addTVListener, type TVRemoteEvent} from './keys';
import {CANVAS} from './canvas';

export * from 'react-native-web';
export {View, Pressable, TextInput} from './components';
export {TVFocusGuideView} from './focus';

export type FocusGuideMethods = {setDestinations(destinations: unknown[]): void};
export type HWEvent = TVRemoteEvent & {tag?: number};

export const Platform = {
  ...Web.Platform,
  OS: 'web' as const,
  isTV: true,
  isTVOS: false,
  isPad: false,
  Version: '',
  /** Model and system version, filled in by `platform/device.ts` once the set has said them. */
  constants: {Model: '', Release: '', reactNativeVersion: {major: 0, minor: 83, patch: 0}},
  select<T>(choices: {web?: T; native?: T; default?: T; [os: string]: T | undefined}): T | undefined {
    return 'web' in choices ? choices.web : 'default' in choices ? choices.default : undefined;
  },
};

/** The element itself: what `nextFocus*` and `setDestinations` take here. */
export function findNodeHandle(host: unknown): any {
  return host instanceof Element ? host : null;
}

export function useTVEventHandler(handler: (event: HWEvent) => void): void {
  const latest = useRef(handler);
  latest.current = handler;
  useEffect(() => addTVListener(event => latest.current(event)), []);
}

export class TVEventHandler {
  static addListener(listener: (event: HWEvent) => void): {remove(): void} {
    return {remove: addTVListener(listener)};
  }
}

/** On an Apple TV this decides whether Menu reaches the app. Here Back always reaches it first. */
export const TVEventControl = {
  enableTVMenuKey(): void {}, disableTVMenuKey(): void {},
  enableTVPanGesture(): void {}, disableTVPanGesture(): void {},
  enableGestureHandlersCancelTouches(): void {}, disableGestureHandlersCancelTouches(): void {},
};

export const BackHandler = {
  addEventListener(_type: 'hardwareBackPress', handler: () => boolean | null | undefined) {return addBackHandler(handler);},
  exitApp(): void {},
};

export const NativeModules: Record<string, any> = new Proxy(porticoModules as Record<string, unknown>, {
  get: (modules, name) => (typeof name === 'string' ? modules[name] : undefined),
  has: (modules, name) => typeof name === 'string' && name in modules,
});

export const TurboModuleRegistry = {get: (_name: string): undefined => undefined, getEnforcing: (name: string): never => {throw new Error(`${name} is not available`);}};

type Subscription = {remove(): void};
/** `new NativeEventEmitter(module)`: a Portico module's own events. */
export class NativeEventEmitter {
  private readonly events: ModuleEvents | undefined;
  constructor(module?: unknown) {this.events = eventsOf(module);}
  addListener(eventType: string, listener: (event: any) => void, context?: object): Subscription {
    return this.events ? this.events.addListener(eventType, context ? listener.bind(context) : listener) : {remove() {}};
  }
  removeAllListeners(eventType: string): void {this.events?.removeAll(eventType);}
  listenerCount(eventType: string): number {return this.events ? this.events.count(eventType) : 0;}
  emit(eventType: string, ...params: unknown[]): void {this.events?.emit(eventType, params[0]);}
}

const Absent = (): null => null;
export function requireNativeComponent<T>(viewName: string): React.ComponentType<T> {
  return (porticoViews[viewName] ?? Absent) as React.ComponentType<T>;
}

export const UIManager = new Proxy(Web.UIManager as unknown as Record<string | symbol, unknown>, {
  get(target, name) {
    if (name === 'hasViewManagerConfig') return (view: string) => porticoViewConfigs.has(view);
    return target[name];
  },
}) as unknown as typeof Web.UIManager & {hasViewManagerConfig(name: string): boolean};

export function registerCallableModule(_name: string, _module: unknown): void {}

type Window = {width: number; height: number; scale: number; fontScale: number};
const canvas = (): Window => ({...CANVAS});
export function useWindowDimensions(): Window {return CANVAS;}
export const Dimensions = {
  get: (_dimension: 'window' | 'screen'): Window => canvas(),
  addEventListener: (_type: 'change', _handler: unknown): Subscription => ({remove() {}}),
  set: (): void => {},
};
export const PixelRatio = {
  get: (): number => 1,
  getFontScale: (): number => 1,
  getPixelSizeForLayoutSize: (size: number): number => Math.round(size),
  roundToNearestPixel: (size: number): number => Math.round(size),
};
