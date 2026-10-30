/** Portable clone for the typed DTOs shared by web and native.
 *
 * The pinned React Native/Hermes startup has no global `structuredClone`, so
 * canonical services must not depend on one. This clones the admitted DTO
 * domain only — primitives, arrays and own enumerable string-keyed plain or
 * null-prototype records — with an identity map for aliases and cycles.
 * Accessors, symbol keys, functions and non-DTO class/native objects are
 * refused explicitly rather than silently dropped or coerced.
 */

function refuse(value: unknown, reason: string): never {
  throw new TypeError(`Cannot clone DTO value: ${reason}`);
}

function descriptorOf(source: object, key: string): PropertyDescriptor {
  const descriptor = Object.getOwnPropertyDescriptor(source, key);
  if (!descriptor) refuse(source, 'own property disappeared');
  if (!('value' in descriptor)) refuse(source, `accessor property "${key}" is not DTO data`);
  return descriptor;
}

export function clonePortableDto<T>(input: T): T {
  const seen = new Map<object, unknown>();

  const clone = (value: unknown): unknown => {
    if (value === null) return null;
    switch (typeof value) {
      case 'undefined': case 'string': case 'number': case 'boolean': case 'bigint':
        return value;
      case 'function': case 'symbol':
        refuse(value, `${typeof value} values are not DTO data`);
    }
    const object = value as object;
    const existing = seen.get(object);
    if (existing !== undefined) return existing;

    const prototype = Object.getPrototypeOf(object);
    for (const symbol of Object.getOwnPropertySymbols(object)) {
      const descriptor = Object.getOwnPropertyDescriptor(object, symbol);
      if (descriptor?.enumerable) refuse(object, 'symbol-keyed properties are not DTO data');
    }

    if (Array.isArray(object)) {
      if (prototype !== Array.prototype) refuse(object, 'Array subclasses are not DTO data');
      const copy: unknown[] = new Array(object.length);
      seen.set(object, copy);
      for (const key of Object.keys(object)) {
        if (!/^(?:0|[1-9][0-9]*)$/.test(key)) refuse(object, `array property "${key}" is not DTO data`);
        Object.defineProperty(copy, key, {value: clone(descriptorOf(object, key).value), enumerable: true, writable: true, configurable: true});
      }
      return copy;
    }

    if (prototype !== Object.prototype && prototype !== null) refuse(object, 'class/native objects are not DTO data');
    const copy = Object.create(prototype);
    seen.set(object, copy);
    for (const key of Object.keys(object)) {
      const descriptor = descriptorOf(object, key);
      if (!descriptor.enumerable) continue;
      Object.defineProperty(copy, key, {value: clone(descriptor.value), enumerable: true, writable: true, configurable: true});
    }
    return copy;
  };

  return clone(input) as T;
}
