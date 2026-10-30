import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

test('History reports a failed time-zone lookup and still captures a UTC report', async t => {
  const calls: string[] = [], errors: unknown[] = [], error = new Error('timezone-fixture');
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/HistoryPage.tsx', import.meta.url), new Proxy({
    react: {default: {}},
    '../../../../../../packages/client-core/src/report-context': {captureReportContext: (timeZone: string) => {calls.push(timeZone); return {timeZone};}},
  }, {get: (target: Record<string, unknown>, key: string) => target[key] ?? {}}));
  t.mock.method(Intl, 'DateTimeFormat', () => {throw error;});
  assert.deepEqual(module.historyReportContext((value: unknown) => errors.push(value)), {timeZone: 'UTC'});
  assert.deepEqual(errors, [error]); assert.deepEqual(calls, ['UTC']);
});
