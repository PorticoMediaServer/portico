/** Send a report (Settings › About, the Watch panel and a failed start all use it): what reaches the rebuilt server. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeModules} from 'react-native';
import {setInstallationIdentity} from '../../../packages/client-core/src/installation.ts';
import {fatalRecord} from '../src-next/platform/error-journal.ts';

const records = new Map<string, string>();
NativeModules.PorticoRuntime.loadQueueRecord = async (key: string) => records.get(key) ?? null;
NativeModules.PorticoRuntime.saveQueueRecord = async (key: string, value: string | null) => {if (value == null) records.delete(key); else records.set(key, value);};
const {sendNativeDeviceReport} = await import('../src-next/screens/settings-live/device-client-report.ts');

setInstallationIdentity({installationId: 'native-report-test-0000000000000000000000', name: 'Sam’s Living Room TV', platform: 'tvos', app: 'portico-tv', appVersion: '1.4.0'});

function stand() {
  const sent: {path: string; method?: string; body?: Record<string, unknown>}[] = [];
  const context = {api: {request: async (path: string, method?: string, body?: Record<string, unknown>) => {sent.push({path, method, body}); return {id: 'rep_1'};}}};
  const lifetime = {assert() {}, request: <T,>(work: (signal: AbortSignal) => Promise<T>) => work(new AbortController().signal), authority: {serverId: 'srv_x'}};
  return {sent, deps: {context, lifetime} as never};
}
const consent = {signal: new AbortController().signal, current: () => true};

test('a report is a problem report on POST /v1/reports with the app log, and never names the device', async () => {
  records.clear();
  const {sent, deps} = stand();
  await sendNativeDeviceReport(deps, consent);
  assert.equal(sent.length, 1);
  assert.equal(sent[0]!.method, 'POST');
  assert.equal(sent[0]!.path, '/v1/reports');
  const body = sent[0]!.body!;
  assert.equal(body.kind, 'problem');
  assert.ok(typeof body.message === 'string' && body.message.length > 0, 'the server asks a problem report to say what went wrong');
  assert.deepEqual(body.context, {app: 'portico-tv', platform: 'tvos', appVersion: '1.4.0', deviceClass: 'mobile'});
  assert.match(String(body.log), /^Portico app report\nSent: .+\nApp: 1\.4\.0 \(build dev\) on tvos \(mobile\)\n/);
  assert.ok(!JSON.stringify(body).includes('Living Room'), 'the device’s own name is not sent');
});

test('after a crash it is a crash report, and the kept crash is forgotten once sent', async () => {
  records.clear();
  records.set('report.fatal:v1', JSON.stringify(fatalRecord(new Error('boom'), '1.4.0', Date.now())));
  const {sent, deps} = stand();
  await sendNativeDeviceReport(deps, consent);
  assert.equal(sent[0]!.path, '/v1/reports');
  assert.equal(sent[0]!.body!.kind, 'crash');
  assert.equal(sent[0]!.body!.message, undefined);
  assert.match(String(sent[0]!.body!.log), /Closed unexpectedly: /);
  assert.equal(JSON.parse(records.get('report.fatal:v1') ?? 'null'), null);
});
