import assert from 'node:assert/strict';
import test from 'node:test';
import {AccountExportDownload} from '../src/services/account-export-download.ts';

function held<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {resolve = yes;reject = no;});
  return {promise, resolve, reject};
}

test('completion follows the actual browser handoff, and the old rendered action cannot repeat it', async () => {
  const response = held<{state: 'ready'; data: unknown}>(), events: string[] = [];
  const download = new AccountExportDownload(async () => {events.push('request');return response.promise;},
    (_data, current) => {assert.equal(current(), true);events.push('handoff');assert.equal(download.getSnapshot().phase, 'exporting');}, () => true);
  const rendered = download.getSnapshot(), flight = download.download(rendered);
  await download.download(rendered);
  assert.equal(download.getSnapshot().phase, 'exporting');assert.deepEqual(events, ['request']);
  response.resolve({state: 'ready', data: {version: 1}});await flight;
  assert.equal(download.getSnapshot().phase, 'downloaded');assert.deepEqual(events, ['request', 'handoff']);
  await download.download(rendered);assert.deepEqual(events, ['request', 'handoff']);
});

test('pending export is not a completed browser download', async () => {
  const download = new AccountExportDownload(async () => ({state: 'pending'}), () => {assert.fail('pending handoff');}, () => true);
  await download.download(download.getSnapshot());assert.equal(download.getSnapshot().phase, 'pending');
});

test('a refused browser handoff cannot publish completion', async () => {
  const error = new Error('browser handoff refused');
  const download = new AccountExportDownload(async () => ({state: 'ready', data: {}}), () => {throw error;}, () => true);
  await download.download(download.getSnapshot());
  assert.equal(download.getSnapshot().phase, 'failed');assert.equal(download.getSnapshot().error, error);
});

test('layout disposal aborts only the original download and suppresses a late ready response', async () => {
  const response = held<{state: 'ready'; data: unknown}>(), signals: AbortSignal[] = [];
  const old = new AccountExportDownload(async captured => {signals.push(captured);return response.promise;}, () => {assert.fail('retired handoff');}, () => true);
  const flight = old.download(old.getSnapshot());old.dispose();assert.equal(signals[0]?.aborted, true);
  const successor = new AccountExportDownload(async () => ({state: 'pending'}), () => {}, () => true);
  response.resolve({state: 'ready', data: {}});await flight;
  assert.equal(successor.getSnapshot().phase, 'idle');assert.notEqual(old.getSnapshot().phase, 'downloaded');
});

test('original account retirement refuses late data and all retained actions even without unmount', async () => {
  const response = held<{state: 'ready'; data: unknown}>();let current = true, requests = 0;
  const download = new AccountExportDownload(async () => {requests++;return response.promise;}, () => {assert.fail('foreign account handoff');}, () => current);
  const rendered = download.getSnapshot(), flight = download.download(rendered);current = false;
  response.resolve({state: 'ready', data: {}});await flight;await download.download(rendered);
  assert.equal(requests, 1);assert.notEqual(download.getSnapshot().phase, 'downloaded');
});

test('reentrant subscribers cannot duplicate the request or browser handoff', async () => {
  let requests = 0, handoffs = 0;
  const download = new AccountExportDownload(async () => {requests++;return {state: 'ready', data: {}};}, () => {handoffs++;}, () => true);
  download.subscribe(() => {void download.download(download.getSnapshot());throw new Error('subscriber');});
  await download.download(download.getSnapshot());assert.equal(requests, 1);assert.equal(handoffs, 1);
});
