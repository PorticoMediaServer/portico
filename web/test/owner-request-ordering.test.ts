import test from 'node:test';
import assert from 'node:assert/strict';
import {LiveSourceController} from '../src/screens/live/owner/source-controller.ts';

function deferred<T>() {let resolve!: (value: T) => void;let reject!: (error: unknown) => void;const promise = new Promise<T>((yes, no) => {resolve = yes;reject = no;});return {promise, resolve, reject};}

test('retired live-source read guards before touching a malformed successful response', async () => {
  const answer = deferred<unknown>();let reads = 0, current = true, reports = 0;
  const source = new LiveSourceController({request: <T>() => answer.promise as Promise<T>}, 'server', () => current, () => {reports++;});
  const pending = source.load();current = false;
  answer.resolve({get protocolVersion() {reads++;throw new Error('parsed');}, get serverId() {reads++;throw new Error('parsed');}});
  await pending;assert.equal(reads, 0);assert.equal(reports, 0);assert.equal(source.getSnapshot().document, undefined);source.dispose();
});
