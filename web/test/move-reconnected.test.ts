import test from 'node:test';
import assert from 'node:assert/strict';
import {moveReconnected, rememberMoveReconnected} from '../src/platform/move-reconnected.ts';

const memory = () => {const values = new Map<string, string>(); return {getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => {values.set(key, value);}};};

test('a browser that reconnected to a moved server stops offering the old sign-in there only', () => {
  const storage = memory();
  assert.equal(moveReconnected('srv_a', storage), false, 'never reconnected: the offer stays');
  rememberMoveReconnected('srv_a', storage);
  assert.equal(moveReconnected('srv_a', storage), true);
  assert.equal(moveReconnected('srv_b', storage), false, 'another server keeps its own offer');
  for (let i = 0; i < 40; i++) rememberMoveReconnected('srv_' + i, storage);
  assert.equal(moveReconnected('srv_39', storage), true);
  assert.equal(JSON.parse(storage.getItem('portico.move-reconnected.v1')!).length, 32, 'bounded');
});

test('damaged or unavailable storage reads as not yet reconnected and never throws', () => {
  const broken = {getItem: () => {throw new Error('denied');}, setItem: () => {throw new Error('denied');}};
  assert.equal(moveReconnected('srv_a', broken), false);
  assert.doesNotThrow(() => rememberMoveReconnected('srv_a', broken));
  const junk = memory(); junk.setItem('portico.move-reconnected.v1', '{"not":"a list"}');
  assert.equal(moveReconnected('srv_a', junk), false);
});
