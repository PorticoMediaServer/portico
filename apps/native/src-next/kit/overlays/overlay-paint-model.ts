import React from 'react';

/** A controller-free kit surface, frozen for exit paint. */
export type OverlayPaintNode = React.ReactElement<Record<string, unknown>>;
export type OverlayPaintTypes = readonly unknown[];

const noop = () => {};
const ROOT_REF_KEYS = new Set(['anchorRef', 'returnFocusRef']);
const MAX_VISITS = 4096;
const MAX_DEPTH = 64;
const OMIT = Symbol('overlay-paint-omit');
const REJECT = Symbol('overlay-paint-reject');

const isPlainObject = (value: unknown): value is Record<string, unknown> => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
};

type Context = {
  allowed: Set<unknown>;
  omitted: Set<unknown>;
  /** One budget for the whole capture, charged per value and per own property. */
  budget: {left: number};
  /** Objects on the current path only, so shared readonly aliases stay legal. */
  path: Set<object>;
  memo: WeakMap<object, unknown>;
};

const spend = (context: Context) => --context.budget.left >= 0;

/** A paint accumulator that can never touch a prototype. */
const accumulator = (): Record<string, unknown> => Object.create(null) as Record<string, unknown>;
const define = (target: Record<string, unknown>, key: string, value: unknown) => {
  if (key === '__proto__') throw REJECT;
  Object.defineProperty(target, key, {value, enumerable: true, configurable: true, writable: true});
};

/** Own descriptor without ever invoking an accessor. */
function ownDescriptor(value: object, key: string): PropertyDescriptor | undefined {
  return Object.getOwnPropertyDescriptor(value, key);
}

/** Preserve an array only when normal array methods cannot hide executable data. */
function plainArray(value: unknown[], context: Context): boolean {
  if (Object.getPrototypeOf(value) !== Array.prototype || Object.getOwnPropertySymbols(value).length) return false;
  for (const key of Object.getOwnPropertyNames(value)) {
    if (!spend(context)) return false;
    if (key === 'length') continue;
    const index = Number(key), descriptor = ownDescriptor(value, key);
    if (!Number.isInteger(index) || index < 0 || index >= value.length || String(index) !== key
      || !descriptor || descriptor.get || descriptor.set) return false;
  }
  return true;
}

function styleIsPaintable(value: unknown, context: Context, depth: number): boolean {
  if (!spend(context) || depth > MAX_DEPTH) return false;
  if (value === undefined || value === null || typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') return true;
  if (typeof value !== 'object') return false;
  if (context.path.has(value)) return false;
  context.path.add(value);
  try {
    if (Array.isArray(value)) {
      if (!plainArray(value, context)) return false;
      const length = value.length;
      for (let index = 0; index < length; index++) {
        if (!spend(context)) return false;
        const descriptor = ownDescriptor(value, String(index));
        if (!descriptor) continue;
        if (descriptor.get || descriptor.set) return false;
        if (!styleIsPaintable(descriptor.value, context, depth + 1)) return false;
      }
      return true;
    }
    if (!isPlainObject(value)) return false;
    if (Object.getOwnPropertySymbols(value).length) return false;
    for (const key of Object.getOwnPropertyNames(value)) {
      if (!spend(context)) return false;
      const descriptor = ownDescriptor(value, key);
      if (!descriptor) return false;
      if (descriptor.get || descriptor.set) return false;
      if (key === '__proto__') return false;
      if (!styleIsPaintable(descriptor.value, context, depth + 1)) return false;
    }
    return true;
  } finally {context.path.delete(value);}
}

/** Reads a marker without ever executing an accessor: React.isValidElement would
 * invoke a getter installed for $$typeof on ordinary prop data. */
function elementProbe(value: unknown): 'element' | 'data' | 'reject' {
  if (!value || typeof value !== 'object') return 'data';
  const descriptor = Object.getOwnPropertyDescriptor(value, '$$typeof');
  if (!descriptor) return 'data';
  if (descriptor.get || descriptor.set) return 'reject';
  if (descriptor.value !== Symbol.for('react.element') && descriptor.value !== Symbol.for('react.transitional.element')) return 'data';
  for (const key of ['type', 'props', 'key']) {
    const field = Object.getOwnPropertyDescriptor(value, key);
    if (!field || field.get || field.set) return 'reject';
  }
  return 'element';
}

export function freezeOverlayPaint(node: OverlayPaintNode, allowedTypes: OverlayPaintTypes, omitTypes: OverlayPaintTypes = []): OverlayPaintNode | undefined {
  const context: Context = {allowed: new Set(allowedTypes), omitted: new Set(omitTypes), budget: {left: MAX_VISITS}, path: new Set<object>(), memo: new WeakMap<object, unknown>()};

  function freezeElement(element: OverlayPaintNode, root: boolean, depth: number): unknown {
    if (!spend(context) || depth > MAX_DEPTH) return REJECT;
    if (context.omitted.has(element.type)) return OMIT;
    if (!context.allowed.has(element.type)) return REJECT;
    if (context.path.has(element as unknown as object)) return REJECT;
    context.path.add(element as unknown as object);
    try {
      const props = accumulator();
      let children: unknown;
      let hasChildren = false;
      for (const key of Object.getOwnPropertyNames(element.props)) {
        if (!spend(context)) throw REJECT;
        // Reserved keys are skipped before descriptor checks: a development React
        // element exposes a `key` warning accessor that must never be read.
        if (key === 'key' || key === 'ref') continue;
        if (key === '__proto__') throw REJECT;
        const descriptor = ownDescriptor(element.props, key);
        if (!descriptor) throw REJECT;
        if (descriptor.get || descriptor.set) throw REJECT;
        if (!descriptor.enumerable) continue;
        const prop = descriptor.value;
        if (key === 'children') {children = prop; hasChildren = true; continue;}
        if (key === 'initialFocusRef') continue;
        if (ROOT_REF_KEYS.has(key)) {
          if (root) {
            const current = prop && typeof prop === 'object' ? ownDescriptor(prop, 'current') : undefined;
            const usable = !!current && !current.get && !current.set && typeof current.value !== 'function';
            if (usable) define(props, key, prop);
          }
          continue;
        }
        if (key === 'style') {
          if (!styleIsPaintable(prop, context, depth + 1)) throw REJECT;
          define(props, key, prop);
          continue;
        }
        if (typeof prop === 'function') {
          if (key.startsWith('on')) define(props, key, noop);
          continue;
        }
        const next = freezeValue(prop, false, depth + 1);
        if (next === REJECT) throw REJECT;
        if (next === OMIT) continue;
        define(props, key, next);
      }
      if (hasChildren) {
        const next = freezeValue(children, false, depth + 1);
        if (next === REJECT) throw REJECT;
        if (next !== OMIT) define(props, 'children', next);
      }
      const keyed = element.key !== null && element.key !== undefined;
      const built = React.createElement(element.type as never, keyed ? {children: undefined, ...props} : props);
      // React's development build turns a key passed through createElement's
      // config into a warning accessor on the new props object. The key belongs
      // to the element, so transfer it there and leave the frozen props free of
      // any accessor, exactly as the source element's props were.
      return keyed ? React.cloneElement(built, {key: element.key}) : built;
    } finally {context.path.delete(element as unknown as object);}
  }

  function freezeValue(value: unknown, root: boolean, depth: number): unknown {
    if (!spend(context) || depth > MAX_DEPTH) return REJECT;
    if (value === undefined || value === null || typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') return value;
    if (typeof value === 'function') return REJECT;
    // React elements are plain objects with a $$typeof symbol: recognise them first.
    const probe = elementProbe(value);
    if (probe === 'reject') return REJECT;
    if (probe === 'element') return freezeElement(value as OverlayPaintNode, root, depth + 1);
    if (Array.isArray(value) || isPlainObject(value)) {
      if (context.path.has(value)) return REJECT;
      if (context.memo.has(value)) return context.memo.get(value);
      context.path.add(value);
      try {
        if (Array.isArray(value)) {
          if (!plainArray(value, context)) throw REJECT;
          const length = value.length;
          let changed = false;
          const frozen: unknown[] = [];
          for (let index = 0; index < length; index++) {
            if (!spend(context)) throw REJECT;
            const descriptor = ownDescriptor(value, String(index));
            if (!descriptor) {frozen.push(undefined); changed = true; continue;}
            if (descriptor.get || descriptor.set) throw REJECT;
            const entry = descriptor.value;
            const next = freezeValue(entry, false, depth + 1);
            if (next === REJECT) throw REJECT;
            if (next === OMIT) {changed = true; continue;}
            if (next !== entry) changed = true;
            frozen.push(next);
          }
          const result = changed ? frozen : value;
          context.memo.set(value, result);
          return result;
        }
        if (Object.getOwnPropertySymbols(value).length) throw REJECT;
        let changed = false;
        const frozen = accumulator();
        for (const key of Object.getOwnPropertyNames(value)) {
          if (!spend(context) || key === '__proto__') throw REJECT;
          const descriptor = ownDescriptor(value, key);
          if (!descriptor) throw REJECT;
          if (descriptor.get || descriptor.set) throw REJECT;
          if (!descriptor.enumerable) {
            const passive = freezeValue(descriptor.value, false, depth + 1);
            if (passive === REJECT || passive === OMIT || passive !== descriptor.value) throw REJECT;
            continue;
          }
          const entry = descriptor.value;
          if (typeof entry === 'function') {
            if (key.startsWith('on')) {define(frozen, key, noop); changed = true;}
            else changed = true;
            continue;
          }
          const next = freezeValue(entry, false, depth + 1);
          if (next === REJECT) throw REJECT;
          if (next === OMIT) {changed = true; continue;}
          define(frozen, key, next);
          if (next !== entry) changed = true;
        }
        const result = changed ? frozen : value;
        context.memo.set(value, result);
        return result;
      } finally {context.path.delete(value);}
    }
    return REJECT;
  }

  try {
    const frozen = freezeValue(node, true, 0);
    return frozen === OMIT || frozen === REJECT ? undefined : frozen as OverlayPaintNode;
  } catch (error) {
    if (error === REJECT) return undefined;
    throw error;
  }
}
