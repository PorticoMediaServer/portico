import type {NativeObservedHost, NativeTreeObserver} from '../../src-next/dev/layout-dump/native-observer.ts';
import type {NativeMeasurementRegistry} from '../../src-next/dev/layout-dump/registry.ts';

type Rect = Readonly<{x: number; y: number; width: number; height: number}>;
export type TestNativeHost = {nodeType: number; nodeName: string; isConnected: boolean; childNodes: object[];
  offsetWidth: number; offsetHeight: number; getBoundingClientRect: () => Rect};
export const windowAPI = 'android.View.transformMatrixToGlobal+getLocationInWindow.boundsDp';
const models = new WeakMap<object, NativeObservedHost | (() => NativeObservedHost)>();
const empty = (): NativeObservedHost => ({style: {}, unavailable: {}});
function model(host: object): NativeObservedHost {
  const value = models.get(host);
  return typeof value === 'function' ? value() : value ?? empty();
}

/** Independent synthetic physical observations; never derive them with the producer's DOM reader. */
export function nativeHost(nodeName: string, box: Rect, childNodes: object[] = []): TestNativeHost {
  const host = {nodeType: 1, nodeName, isConnected: true, childNodes,
    offsetWidth: box.width, offsetHeight: box.height, getBoundingClientRect: () => ({...box})};
  models.set(host, {...empty(), windowRect: {...box}, windowRectSource: windowAPI});
  return host;
}
export function setNativeObservation(host: object, value: Partial<NativeObservedHost> | (() => NativeObservedHost)) {
  models.set(host, typeof value === 'function' ? value : {...model(host), ...value});
}
export const observeNativeWindow: NativeTreeObserver = async request => {
  if (!request.isCurrent()) throw new Error('Synthetic native scope retired.');
  return new Map(request.hosts.map(host => [host, model(host)]));
};
export function addNativeFixture(root: TestNativeHost,
  register: (host: object, facts: Parameters<NativeMeasurementRegistry['register']>[1]) => ReturnType<NativeMeasurementRegistry['register']> | undefined) {
  const fixture = nativeHost('RN:View', model(root).windowRect ?? {x: 0, y: 0, width: 1, height: 1});
  root.childNodes.push(fixture);
  const lease = register(fixture, {kind: 'view', semanticId: 'accounts-welcome'});
  return () => {
    lease?.unregister();
    const index = root.childNodes.indexOf(fixture);
    if (index >= 0) root.childNodes.splice(index, 1);
  };
}
