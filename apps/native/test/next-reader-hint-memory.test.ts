import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

test('the reader hint is remembered in this device\'s settings, and a retired or unreadable owner just shows it again', async () => {
  const record: Record<string, unknown> = {}, writes: unknown[][] = [];
  let live = true;
  const settings = {read: (id: string) => {if (!live) throw new Error('retired'); return record[id] === true;},
    write: async (id: string, value: unknown) => {if (!live) throw new Error('retired'); writes.push([id, value]); record[id] = value;}};
  const {nativeReaderHintMemory} = await screenModule(new URL('../src-next/platform/reader-hint-memory.ts', import.meta.url), {
    './device-settings': {nativeDeviceSettings: () => settings}, react: {default: {}}});
  const memory = nativeReaderHintMemory({});
  assert.equal(memory.seen(), false, 'a new device shows the hint');
  memory.markSeen(); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(writes, [['readerHintSeen', true]]);
  assert.equal(memory.seen(), true, 'and then never again');
  memory.markSeen(); await new Promise(resolve => setImmediate(resolve));
  assert.equal(writes.length, 1, 'a repeated Got it writes nothing more');
  live = false;
  assert.equal(memory.seen(), false);
  assert.doesNotThrow(() => memory.markSeen());
});
