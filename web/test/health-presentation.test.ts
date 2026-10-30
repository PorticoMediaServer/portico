import test from 'node:test';
import assert from 'node:assert/strict';
import {METRIC_NAMES, parseTelemetryReading, type MetricName, type TelemetryPoint, type TelemetryReading} from '@core/telemetry.ts';
import {healthDetail, healthPlot, healthTiles} from '../src/screens/dashboard/health-presentation';

const point = (t: number, v: number, status?: 'available' | 'limited', detail?: string): TelemetryPoint => ({t, v, ...(status ? {status} : {}), ...(detail ? {detail} : {})});
function reading(): TelemetryReading {
  const names: MetricName[] = ['cpu', 'memory', 'diskRead', 'diskWrite', 'netIn', 'netOut', 'gpuUsage', 'gpuMemory', 'gpuEncoder', 'processCPU', 'processMemory', 'homeMbps', 'awayMbps', 'hardwareConversions', 'softwareConversions'];
  return {window: '10m', period: '10m', from: '2026-01-01T00:00:00Z', until: '2026-01-01T00:10:00Z', timeZone: 'UTC', buckets: [], observedAt: 600,
    status: Object.fromEntries(names.map(name => [name, {status: 'unavailable', value: 0}])),
    series: Object.fromEntries(names.map(name => [name, []]))} as unknown as TelemetryReading;
}

test('unknown zero or unknown middle point never becomes a measured line or statistics', () => {
  const r = reading(); r.status.cpu = {status: 'available', value: 0};
  r.series.cpu = [point(1, 1, 'available'), point(2, 0), point(3, 2, 'available')];
  const tile = healthTiles(r).find(item => item.metric === 'cpu')!;
  assert.equal(tile.value, '0%'); assert.equal(tile.history, 'gap'); assert.deepEqual(tile.points, []);
  assert.equal(healthPlot(r.series.cpu, 0, 10), undefined); assert.equal(healthDetail(r.series.cpu, 0, 10, true), undefined);
});

test('known CPU and disk history preserve available zero and real limited detail', () => {
  const r = reading(); r.status.cpu = {status: 'available', value: 0}; r.status.diskRead = {status: 'limited', value: 5, detail: 'partial volume'};
  r.series.cpu = [point(1, 0, 'available'), point(2, 3, 'available')];
  r.series.diskRead = [point(1, 0, 'available'), point(2, 5, 'limited', 'partial volume')];
  const tiles = healthTiles(r), cpu = tiles.find(item => item.metric === 'cpu')!, disk = tiles.find(item => item.metric === 'diskRead')!;
  assert.equal(cpu.history, 'samples'); assert.equal(cpu.points[0].v, 0); assert.equal(healthDetail(cpu.points, 0, 10, true)?.low, 0);
  assert.equal(disk.points[1].detail, 'partial volume'); assert.equal(disk.limited, true); assert.deepEqual(disk.details, ['partial volume']);
});

test('viewer rates use current metrics and retain paired provenance, refusing gaps', () => {
  const r = reading(); r.status.homeMbps = {status: 'available', value: 0}; r.status.awayMbps = {status: 'limited', value: 2, detail: 'limited away'};
  r.series.homeMbps = [point(1, 0, 'available'), point(2, 1, 'limited', 'limited home')];
  r.series.awayMbps = [point(1, 0, 'available'), point(2, 2, 'limited', 'limited away')];
  let viewer = healthTiles(r).find(item => item.metric === 'netOut')!;
  assert.equal(viewer.value, '2 Mbps'); assert.equal(viewer.points[0].v, 0); assert.equal(viewer.points[1].status, 'limited');
  assert.match(viewer.points[1].detail!, /limited home/); assert.match(viewer.points[1].detail!, /limited away/);
  r.series.awayMbps[1] = point(3, 2, 'available'); viewer = healthTiles(r).find(item => item.metric === 'netOut')!;
  assert.equal(viewer.value, '2 Mbps'); assert.equal(viewer.history, 'gap'); assert.equal(healthDetail(viewer.points, 0, 10, false), undefined);
  r.series.awayMbps[1] = point(2, 2); viewer = healthTiles(r).find(item => item.metric === 'netOut')!;
  assert.equal(viewer.history, 'gap'); assert.deepEqual(viewer.points, []);
});

// Parser and presenter assertions cover the returned point set, not raw-series completeness:
// keepReadable can drop malformed time/value points and omit malformed detail before this boundary.
test('parsed unknown middle and legacy warm-up points refuse history while known zero survives', () => {
  const observedAt = 1758000000000, from = new Date(observedAt - 600000).toISOString(), until = new Date(observedAt).toISOString();
  const series = Object.fromEntries(METRIC_NAMES.map(name => [name, []])) as Record<string, unknown[]>;
  const status = Object.fromEntries(METRIC_NAMES.map(name => [name, {status: 'unavailable', value: 0}]));
  status.cpu = {status: 'available', value: 0};
  series.cpu = [{t: observedAt - 3000, v: 0, status: 'available'}, {t: observedAt - 2000, v: 0, status: 'future-status'}, {t: observedAt - 1000, v: 3, status: 'available'}];
  const input = {period: '10m', window: '10m', timeZone: 'UTC', from, until, buckets: [{start: from, end: until, partial: false}], observedAt, status, series};
  const parsed = parseTelemetryReading(input);
  assert.equal(parsed.series.cpu.length, 3); assert.equal(parsed.series.cpu[0].status, 'available'); assert.equal(parsed.series.cpu[0].v, 0);
  assert.equal(parsed.series.cpu[1].status, undefined);
  assert.equal(healthTiles(parsed).find(tile => tile.metric === 'cpu')?.history, 'gap');
  assert.equal(healthDetail(parsed.series.cpu, observedAt - 600000, observedAt, true), undefined);
  series.cpu = [{t: observedAt - 3000, v: 0, status: 'available'}, {t: observedAt - 2000, v: 0, status: 'limited', detail: 'Waiting for a second reading before a rate can be reported.'}, {t: observedAt - 1000, v: 3, status: 'available'}];
  const warm = parseTelemetryReading(input);
  assert.equal(healthTiles(warm).find(tile => tile.metric === 'cpu')?.history, 'gap');
  assert.equal(healthPlot(warm.series.cpu, observedAt - 600000, observedAt), undefined);
  series.cpu = [{t: observedAt - 3000, v: 0, status: 'available'}, {t: observedAt - 1000, v: 3, status: 'available'}];
  const valid = parseTelemetryReading(input), tile = healthTiles(valid).find(item => item.metric === 'cpu')!;
  assert.equal(tile.history, 'samples'); assert.equal(healthDetail(tile.points, observedAt - 600000, observedAt, true)?.low, 0);
});
