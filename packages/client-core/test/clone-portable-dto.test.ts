import test from 'node:test';
import assert from 'node:assert/strict';
import {clonePortableDto} from '../src/clone-portable-dto.ts';

test('portable DTO clone covers primitives, nested records and array holes without a global structuredClone', () => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'structuredClone');
  delete (globalThis as {structuredClone?: unknown}).structuredClone;
  try {
    const hole = new Array(3); hole[0] = {id: 'a'}; hole[2] = undefined;
    const source = {text: 'x', count: 0, missing: undefined, hole, nested: {ok: true}, nil: null};
    const copy = clonePortableDto(source);
    assert.deepEqual(copy, source);
    assert.notStrictEqual(copy, source);
    assert.notStrictEqual(copy.nested, source.nested);
    assert.equal(1 in copy.hole, false, 'array holes stay holes');
    assert.equal(Object.hasOwn(copy, 'missing'), true, 'an own undefined stays present');
    assert.equal(Object.getPrototypeOf(copy), Object.prototype);
  } finally {
    if (descriptor) Object.defineProperty(globalThis, 'structuredClone', descriptor);
    else delete (globalThis as {structuredClone?: unknown}).structuredClone;
  }
});

test('portable DTO clone preserves aliases/cycles, null prototype and __proto__ as data', () => {
  const shared = {value: 1}, source: Record<string, unknown> = {a: shared, b: shared};
  source.self = source;
  const nullRecord = Object.create(null); nullRecord.x = 1;
  source.nullRecord = nullRecord;
  Object.defineProperty(source, '__proto__', {value: 'literal', enumerable: true, writable: true, configurable: true});
  const copy = clonePortableDto(source);
  assert.strictEqual(copy.a, copy.b, 'aliases keep identity');
  assert.strictEqual(copy.self, copy);
  assert.equal(Object.getPrototypeOf(copy), Object.prototype);
  assert.equal(Object.getPrototypeOf(copy.nullRecord), null);
  assert.equal(Object.getPrototypeOf(copy), Object.prototype, '__proto__ is an own data key, not the prototype');
  assert.equal(Object.getOwnPropertyDescriptor(copy, '__proto__')?.value, 'literal');
});

test('portable DTO clone refuses accessors, functions, symbols, class instances and symbol keys', () => {
  const accessor = Object.defineProperty({}, 'secret', {enumerable: true, get: () => 1});
  for (const value of [
    {fn: () => 1},
    {sym: Symbol('x')},
    new Date(0),
    new Map(),
    accessor,
    {[Symbol('key')]: 1},
    Object.assign([], {[Symbol('array-key')]: 1}),
    new (class extends Array {})(),
  ]) assert.throws(() => clonePortableDto(value), TypeError);
});
