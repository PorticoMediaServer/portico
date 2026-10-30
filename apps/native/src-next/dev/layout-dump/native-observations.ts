/** Internal DEV observation transport. Final measurements keep the shared fidelity format. */
export const nativeObservationProperties = [
  'isFocused', 'effectiveOpacity', 'backgroundColor', 'effectiveBackgroundColor',
  'borderRadius', 'boxShadow', 'textColor', 'fontFamily', 'fontSize', 'fontWeight',
  'lineHeight', 'textLineRects', 'windowRect',
  'fontStyle', 'letterSpacing', 'textDecorationLine', 'textShadow', 'textTransform', 'imageHasImage', 'imageScaleType',
] as const;
export type NativeObservationProperty = typeof nativeObservationProperties[number];
export const nativeImageScaleTypes = ['fit-xy', 'fit-x', 'fit-y', 'fit-start', 'fit-center', 'fit-end', 'center',
  'center-inside', 'center-crop', 'focus-crop', 'fit-bottom-start', 'matrix'] as const;
export type NativeImageScaleType = typeof nativeImageScaleTypes[number];
export type NativeObservedRect = Readonly<{x: number; y: number; width: number; height: number}>;
export type NativeObservationValue = boolean | number | string | NativeObservedRect | readonly NativeObservedRect[];
export type NativeObservationFact = Readonly<{value: NativeObservationValue; source: string}> | Readonly<{unavailable: string}>;
export type NativeHostFacts = Readonly<Record<NativeObservationProperty, NativeObservationFact>>;
export type NativeObservationBatch = Readonly<{
  rootReactTag: number;
  fixtureReactTag: number;
  hosts: readonly Readonly<{reactTag: number; facts: NativeHostFacts}>[];
}>;
export type NativeObservationBridge = Readonly<{
  observe(rootReactTag: number, fixtureReactTag: number, hostReactTags: readonly number[]): Promise<unknown>;
}>;
export type NativeObservationRequest = Readonly<{
  bridge: NativeObservationBridge;
  rootReactTag: number;
  fixtureReactTag: number;
  hostReactTags: readonly number[];
  isCurrent: () => boolean;
  signal?: AbortSignal;
}>;
type Failure = 'invalid-request' | 'invalid-response' | 'scope-changed' | 'cancelled' | 'native-error';
type FailureBoundary = 'js-before-native' | 'js-after-native' | 'js-after-decode' | 'native-scope-rejected-while-js-current';
const nativeStages = ['admission', 'ui-current', 'root-resolve', 'root-attachment', 'root-walk',
  'pre-observation-scope', 'observation', 'post-observation-scope', 'response'] as const;
type NativeStage = typeof nativeStages[number];
type NativeObservationStage = NativeStage | `${NativeStage}-exception`;
const nativeScopeChecks = ['activity-intent-current', 'root-tag-current', 'root-attached', 'decor-attached',
  'decor-current', 'root-window-current', 'decor-window-current', 'root-in-decor', 'fixture-found',
  'fixture-tag-current', 'fixture-attached', 'fixture-window-current', 'fixture-in-root'] as const;
type NativeObservationScopeCheck = typeof nativeScopeChecks[number];
export class NativeObservationError extends Error {
  constructor(readonly code: Failure, readonly boundary?: FailureBoundary,
    readonly nativeObservationStage?: NativeObservationStage,
    readonly nativeObservationScopeCheck?: NativeObservationScopeCheck) {super(`Native observation unavailable: ${code}`);}
}

const object = (value: unknown): value is object => typeof value === 'object' && value !== null;
const finite = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);
const tag = (value: unknown): value is number => Number.isInteger(value) && Number(value) > 0 && Number(value) <= 0x7fffffff;
const own = (value: object, key: string): unknown => {
  const property = Object.getOwnPropertyDescriptor(value, key);
  return property && 'value' in property ? property.value : undefined;
};
const reasons = new Set([
  'host-not-found', 'host-detached', 'host-outside-root', 'host-window-mismatch',
  'input-opaque', 'not-text', 'paint-unprovable', 'font-family-unavailable',
  'unsupported-background', 'unsupported-outline', 'unsupported-shadow',
  'unsupported-transform', 'unsupported-text-layout', 'root-changed',
  'not-image', 'image-state-unavailable', 'unsupported-image-scale',
]);
const unavailable = (reason: string): NativeObservationFact => Object.freeze({unavailable: reason});
const assetLike = /url\s*\(|(?:https?|file|data|content|asset|blob):|[\\/]|\.(?:png|jpe?g|webp|svg|ttf|otf|woff2?)(?:[?#]|$)/i;
const safeString = (value: unknown, length = 256): value is string => typeof value === 'string'
  && value.length > 0 && value.length <= length && !/[\x00-\x1f\x7f]/.test(value) && !assetLike.test(value);
function rgba(value: unknown): value is string {
  if (typeof value !== 'string') return false;
  const match = /^rgba\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*\)$/.exec(value);
  if (!match) return false;
  const components = match.slice(1).map(Number);
  return components.every(Number.isFinite) && components.slice(0, 3).every(n => n >= 0 && n <= 255)
    && components[3] >= 0 && components[3] <= 1;
}
function rectangles(raw: unknown): readonly NativeObservedRect[] | undefined {
  if (!Array.isArray(raw) || raw.length > 256) return undefined;
  const result: NativeObservedRect[] = [];
  for (let index = 0; index < raw.length; index++) {
    const value = own(raw, String(index));
    if (!object(value)) return undefined;
    const x = own(value, 'x'), y = own(value, 'y'), width = own(value, 'width'), height = own(value, 'height');
    if (!finite(x) || !finite(y) || !finite(width) || !finite(height) || width < 0 || height < 0) return undefined;
    result.push(Object.freeze({x, y, width, height}));
  }
  return Object.freeze(result);
}
function fact(property: NativeObservationProperty, raw: unknown): NativeObservationFact {
  if (!object(raw)) return unavailable('native-fact-missing');
  const missing = own(raw, 'unavailable');
  if (typeof missing === 'string') return unavailable(reasons.has(missing) ? missing : 'invalid-native-fact');
  const source = own(raw, 'source'), value = own(raw, 'value');
  if (typeof source !== 'string' || !/^android\.[A-Za-z0-9_.+\-]{1,150}$/.test(source)) return unavailable('invalid-native-fact');
  let accepted: NativeObservationValue | undefined;
  switch (property) {
    case 'isFocused': case 'imageHasImage': if (typeof value === 'boolean') accepted = value; break;
    case 'effectiveOpacity': if (finite(value) && value >= 0 && value <= 1) accepted = value; break;
    case 'backgroundColor': case 'effectiveBackgroundColor': case 'textColor': if (rgba(value)) accepted = value; break;
    // A raw system label is not associated typographic-name metadata. The
    // checked native implementation currently withholds every family fact.
    case 'fontFamily': return unavailable('font-family-unavailable');
    case 'textTransform': return unavailable('paint-unprovable');
    case 'fontStyle': if (value === 'normal' || value === 'italic') accepted = value; break;
    case 'letterSpacing': if (finite(value)) accepted = value; break;
    case 'textDecorationLine':
      if (value === 'none' || value === 'underline' || value === 'line-through' || value === 'underline line-through') accepted = value;
      break;
    case 'textShadow': if (value === 'none') accepted = value; break;
    case 'imageScaleType':
      if (typeof value === 'string' && nativeImageScaleTypes.some(scale => scale === value)) accepted = value;
      break;
    case 'fontSize': case 'lineHeight': if (finite(value) && value > 0) accepted = value; break;
    case 'fontWeight': if (finite(value) && Number.isInteger(value) && value >= 1 && value <= 1000) accepted = value; break;
    case 'borderRadius': if (finite(value) && value >= 0) accepted = value; break;
    case 'boxShadow':
      if (safeString(value, 512) && (value === 'none' || /^(?:[\d\s.,()%+\-]|px|rgba|rgb|inset)+$/.test(value))) accepted = value;
      break;
    case 'textLineRects': accepted = rectangles(value); break;
    case 'windowRect': accepted = rectangles([value])?.[0]; break;
  }
  return accepted === undefined ? unavailable('invalid-native-fact') : Object.freeze({value: accepted, source});
}
function decode(raw: unknown, request: Pick<NativeObservationRequest, 'rootReactTag' | 'fixtureReactTag'>, ids: readonly number[]): NativeObservationBatch {
  if (!object(raw) || own(raw, 'version') !== 1 || own(raw, 'unit') !== 'dp'
      || own(raw, 'rootReactTag') !== request.rootReactTag || own(raw, 'fixtureReactTag') !== request.fixtureReactTag) {
    throw new NativeObservationError('invalid-response');
  }
  const rows = own(raw, 'hosts');
  if (!Array.isArray(rows) || rows.length > ids.length) throw new NativeObservationError('invalid-response');
  const requested = new Set(ids), observed = new Map<number, NativeHostFacts>();
  for (let index = 0; index < rows.length; index++) {
    const row = own(rows, String(index));
    if (!object(row)) throw new NativeObservationError('invalid-response');
    const id = own(row, 'reactTag'), facts = own(row, 'facts');
    if (!tag(id) || !requested.has(id) || observed.has(id) || !object(facts)) throw new NativeObservationError('invalid-response');
    observed.set(id, Object.freeze(Object.fromEntries(nativeObservationProperties.map(property => [property, fact(property, own(facts, property))]))) as NativeHostFacts);
  }
  return Object.freeze({rootReactTag: request.rootReactTag, fixtureReactTag: request.fixtureReactTag,
    hosts: Object.freeze(ids.map(id => Object.freeze({reactTag: id, facts: observed.get(id)
      ?? Object.freeze(Object.fromEntries(nativeObservationProperties.map(property => [property, unavailable('host-not-found')]))) as NativeHostFacts}))) });
}

/** Cancellation retires publication; it does not claim to interrupt a queued native UI turn. */
export async function requestNativeObservations(request: NativeObservationRequest): Promise<NativeObservationBatch> {
  const {rootReactTag, fixtureReactTag, bridge, isCurrent, signal} = request;
  const ids = Object.freeze([...request.hostReactTags]);
  if (!tag(rootReactTag) || !tag(fixtureReactTag) || rootReactTag === fixtureReactTag || ids.length === 0 || ids.length > 2000
      || ids.some(id => !tag(id)) || new Set(ids).size !== ids.length) throw new NativeObservationError('invalid-request');
  const current = () => {try {return isCurrent();} catch {return false;}};
  const guard = (boundary: FailureBoundary) => {
    if (signal?.aborted) throw new NativeObservationError('cancelled', boundary);
    if (!current()) throw new NativeObservationError('scope-changed', boundary);
  };
  guard('js-before-native');
  let abort: (() => void) | undefined;
  try {
    const pending = Promise.resolve().then(() => {guard('js-before-native'); return bridge.observe(rootReactTag, fixtureReactTag, ids);});
    const cancelled = new Promise<never>((_resolve, reject) => {
      abort = () => reject(new NativeObservationError('cancelled'));
      signal?.addEventListener('abort', abort, {once: true});
      if (signal?.aborted) abort();
    });
    const raw = signal ? await Promise.race([pending, cancelled]) : await pending;
    guard('js-after-native');
    const result = decode(raw, {rootReactTag, fixtureReactTag}, ids);
    guard('js-after-decode');
    return result;
  } catch (error) {
    if (error instanceof NativeObservationError) throw error;
    guard('js-after-native');
    if (object(error) && own(error, 'code') === 'layout_observation_scope') {
      const userInfo = own(error, 'userInfo'), stage = object(userInfo) ? own(userInfo, 'nativeObservationStage') : undefined;
      const accepted = typeof stage === 'string' && nativeStages.some(name => stage === name || stage === `${name}-exception`)
        ? stage as NativeObservationStage : undefined;
      const check = object(userInfo) && (accepted === 'pre-observation-scope' || accepted === 'post-observation-scope')
        ? own(userInfo, 'nativeObservationScopeCheck') : undefined;
      const acceptedCheck = typeof check === 'string' && nativeScopeChecks.some(name => check === name)
        ? check as NativeObservationScopeCheck : undefined;
      throw new NativeObservationError('scope-changed', 'native-scope-rejected-while-js-current', accepted, acceptedCheck);
    }
    throw new NativeObservationError('native-error');
  } finally {
    if (abort) signal?.removeEventListener('abort', abort);
  }
}
