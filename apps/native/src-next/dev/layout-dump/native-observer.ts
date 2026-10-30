import {NativeObservationError, requestNativeObservations, type NativeHostFacts, type NativeObservationBridge,
  nativeImageScaleTypes, type NativeImageScaleType, type NativeObservationProperty, type NativeObservedRect} from './native-observations.ts';

export type NativeObservedHost = Readonly<{
  style: Readonly<Record<string, string>>;
  focused?: boolean;
  effectiveOpacity?: number;
  effectiveBackgroundColor?: string;
  textRects?: readonly NativeObservedRect[];
  textRectsSource?: string;
  windowRect?: NativeObservedRect;
  windowRectSource?: string;
  /** Native renderer mechanisms only; neither implies canonical loaded/fit. */
  imageHasImage?: boolean;
  imageScaleType?: NativeImageScaleType;
  unavailable: Readonly<Record<string, string>>;
}>;
export type NativeTreeObservationRequest = Readonly<{
  root: object; fixture: object; hosts: readonly object[];
  isCurrent: () => boolean; signal?: AbortSignal;
}>;
export type NativeTreeObserver = (request: NativeTreeObservationRequest) => Promise<ReadonlyMap<object, NativeObservedHost>>;

function normalized(facts: NativeHostFacts): NativeObservedHost {
  const style: Record<string, string> = {}, missing: Record<string, string> = {};
  const value = (property: NativeObservationProperty) => {
    const fact = facts[property];
    if ('value' in fact) return fact.value;
    missing[property] = `Native ${property} unavailable: ${fact.unavailable}`;
    return undefined;
  };
  for (const [native, canonical] of [['backgroundColor', 'backgroundColor'], ['textColor', 'color'], ['boxShadow', 'boxShadow'],
    ['fontStyle', 'fontStyle'], ['textDecorationLine', 'textDecorationLine'], ['textShadow', 'textShadow']] as const) {
    const observed = value(native); if (typeof observed === 'string') style[canonical] = observed;
  }
  for (const property of ['fontSize', 'lineHeight', 'borderRadius', 'letterSpacing'] as const) {
    const observed = value(property); if (typeof observed === 'number') style[property] = `${observed}px`;
  }
  const weight = value('fontWeight'); if (typeof weight === 'number') style.fontWeight = String(weight);
  // Never promote a requested CSS chain or unassociated native system label.
  value('fontFamily');
  value('textTransform');
  if (missing.fontFamily) {missing.resolvedFontFamily = missing.fontFamily; delete missing.fontFamily;}
  const focus = value('isFocused'), opacity = value('effectiveOpacity'), background = value('effectiveBackgroundColor');
  const lines = value('textLineRects'), window = value('windowRect');
  const imagePresent = value('imageHasImage'), imageScale = value('imageScaleType');
  return Object.freeze({style: Object.freeze(style),
    ...(typeof focus === 'boolean' ? {focused: focus} : {}),
    ...(typeof opacity === 'number' ? {effectiveOpacity: opacity} : {}),
    ...(typeof background === 'string' ? {effectiveBackgroundColor: background} : {}),
    ...(Array.isArray(lines) ? {textRects: lines as readonly NativeObservedRect[],
      textRectsSource: 'source' in facts.textLineRects ? facts.textLineRects.source : undefined} : {}),
    ...(window && typeof window === 'object' && !Array.isArray(window) ? {windowRect: window as NativeObservedRect,
      windowRectSource: 'source' in facts.windowRect ? facts.windowRect.source : undefined} : {}),
    ...(typeof imagePresent === 'boolean' ? {imageHasImage: imagePresent} : {}),
    ...(typeof imageScale === 'string' && nativeImageScaleTypes.some(scale => scale === imageScale)
      ? {imageScaleType: imageScale as NativeImageScaleType} : {}),
    unavailable: Object.freeze(missing)});
}
const absent = (reason: string): NativeObservedHost => Object.freeze({style: Object.freeze({}),
  unavailable: Object.freeze({nativeObservation: reason})});

/** One typed adapter for the native observation seam, not another tree walker. */
export function createNativeTreeObserver(options: Readonly<{
  bridge: NativeObservationBridge; tagForHost: (host: object) => number | null | undefined;
}>): NativeTreeObserver {
  const readTag = (host: object) => {
    try {
      const tag = options.tagForHost(host);
      return typeof tag === 'number' && Number.isInteger(tag) && tag > 0 && tag <= 0x7fffffff ? tag : undefined;
    } catch {return undefined;}
  };
  return async request => {
    const root = readTag(request.root), fixture = readTag(request.fixture);
    const hosts = [...request.hosts], tags = new Map<object, number>();
    const allAbsent = (reason: string) => new Map(hosts.map(host => [host, absent(reason)]));
    if (!root || !fixture || root === fixture) return allAbsent('Native root/fixture tag unavailable or ambiguous');
    for (const host of hosts) {const tag = readTag(host); if (tag) tags.set(host, tag);}
    if (new Set(tags.values()).size !== tags.size) return allAbsent('Native host tags are ambiguous');
    const current = () => request.isCurrent() && readTag(request.root) === root && readTag(request.fixture) === fixture;
    try {
      const batch = await requestNativeObservations({bridge: options.bridge, rootReactTag: root, fixtureReactTag: fixture,
        hostReactTags: [...tags.values()], isCurrent: current, signal: request.signal});
      const byTag = new Map(batch.hosts.map(row => [row.reactTag, normalized(row.facts)]));
      return new Map(hosts.map(host => [host, byTag.get(tags.get(host)!) ?? absent('Native host tag unavailable')]));
    } catch (error) {
      if (error instanceof NativeObservationError && ['cancelled', 'scope-changed'].includes(error.code)) throw error;
      return allAbsent('Native observation request unavailable');
    }
  };
}

/** App invokes this constructor only inside its existing DEV/public install seam. */
export function createAndroidNativeTreeObserver(): NativeTreeObserver | undefined {
  if (typeof __DEV__ === 'undefined' || !__DEV__) return undefined;
  const {NativeModules, Platform, findNodeHandle} = require('react-native') as typeof import('react-native');
  if (Platform.OS !== 'android') return undefined;
  const bridge = NativeModules.PorticoLayoutObservation as NativeObservationBridge | undefined;
  if (!bridge || typeof bridge.observe !== 'function') return undefined;
  return createNativeTreeObserver({bridge, tagForHost: host => findNodeHandle(host as Parameters<typeof findNodeHandle>[0])});
}
