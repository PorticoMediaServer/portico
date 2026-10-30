import type {NativeBinding} from './NativePlaybackCoordinator';

const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const fields = ['lease', 'intentId', 'sessionId', 'generation', 'itemId'] as const;
export function sameNativePresentation(a: NativeBinding, b: NativeBinding): boolean {
  return fields.every(field => a[field] === b[field]);
}

/** Only accepted current-engine facts may call this parser. Unknown on older
 * binaries is distinct from the native controller's explicit unready null.
 * This is an external-presentation acknowledgement, never a timeline fact. */
export function nativeExternalPresentationBinding(fact: unknown, current: NativeBinding | undefined): NativeBinding | null | undefined {
  if (!object(fact) || !Object.prototype.hasOwnProperty.call(fact, 'externalPresentationBinding')) return undefined;
  const value = fact.externalPresentationBinding, factBinding = fact.binding;
  if (!current || !object(factBinding) || !object(value) || Object.keys(value).length !== fields.length) return null;
  if (fields.some(field => value[field] !== current[field] || factBinding[field] !== current[field])) return null;
  if (typeof value.lease !== 'string' || !value.lease || value.lease.length > 256
    || typeof value.sessionId !== 'string' || !value.sessionId || value.sessionId.length > 256
    || typeof value.itemId !== 'string' || !value.itemId || value.itemId.length > 256
    || !Number.isSafeInteger(value.intentId) || Number(value.intentId) < 0
    || !Number.isSafeInteger(value.generation) || Number(value.generation) < 0) return null;
  return Object.freeze({lease: current.lease, intentId: current.intentId, sessionId: current.sessionId, generation: current.generation, itemId: current.itemId});
}
