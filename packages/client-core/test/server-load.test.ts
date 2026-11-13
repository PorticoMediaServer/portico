import test from 'node:test';
import assert from 'node:assert/strict';
import {readServerLoad} from '../src/telemetry.ts';

// The server's load on the rebuilt server (console rule 12), read as the reading the Dashboard's charts draw.
test('the load is read for live or the last 24 hours; a metric the server leaves out reads as unavailable', async () => {
  const paths: string[] = [];
  const api = {request: async <T,>(path: string) => {paths.push(path); return {period: '24h', from: '2026-11-12T00:00:00Z', until: '2026-11-13T00:00:00Z', observedAt: '2026-11-13T00:00:00Z',
    buckets: [{start: '2026-11-12T23:00:00Z', end: '2026-11-13T00:00:00Z', partial: true}],
    series: {cpu: [{t: '2026-11-12T22:00:00Z', v: 12.5}, {t: '2026-11-12T23:00:00Z', v: 20}], homeMbps: [{t: '2026-11-12T23:00:00Z', v: 3.2}], conversions: [{t: '2026-11-12T23:00:00Z', v: 1}]}} as T;}};
  const reading = await readServerLoad(api, '24h');
  assert.equal(paths[0], '/v1/admin/load?range=24h');
  assert.deepEqual(reading.series.cpu.map(p => p.v), [12.5, 20]);
  assert.deepEqual([reading.status.cpu.status, reading.status.cpu.value], ['available', 20], 'a metric\'s value is its last point');
  assert.equal(reading.status.conversions.value, 1);
  assert.equal(reading.status.memory.status, 'unavailable', 'a metric this machine cannot measure is not drawn as zero');
  assert.deepEqual(reading.series.memory, []);
  assert.equal(reading.buckets[0].partial, true);
  await assert.rejects(readServerLoad({request: async <T,>() => ({series: {}}) as T}, 'live'));
});
