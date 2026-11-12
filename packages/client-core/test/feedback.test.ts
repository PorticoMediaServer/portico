import {test} from 'node:test';
import assert from 'node:assert/strict';
import {FEEDBACK_MAX_MESSAGE, FeedbackService, feedbackKinds} from '../src/feedback.ts';
import {setInstallationIdentity} from '../src/installation.ts';

/** A stand-in for POST /v1/reports: records what was sent, answers as told. */
function reports(answers: (number | 'offline')[]) {
  const sent: {path: string; method: string; headers?: Record<string, string>; body?: any}[] = [];
  const api = {requestRaw: async (path: string, method: string, options?: {headers?: Record<string, string>; body?: unknown}) => {
    sent.push({path, method, headers: options?.headers as Record<string, string>, body: options?.body});
    const answer = answers.shift() ?? 200;
    if (answer === 'offline') throw new TypeError('Failed to fetch');
    return answer === 200 ? {status: 200, body: {id: 'rpt_1', kind: 'problem', status: 'open'}}
      : {status: answer, body: {error: {code: answer === 429 ? 'too_many_reports' : 'invalid', message: 'refused'}}};
  }};
  return {api, sent};
}
const draft = {kind: 'playback', category: 'buffering', message: '  It keeps pausing every minute.  ', item: {id: 'itm_1', title: 'Heat'}, playbackSessionId: 'pbs_1'};

test('a report goes to POST /v1/reports with what it is about, the title and its playback, and a failed send loses nothing', async () => {
  const {api, sent} = reports(['offline', 200]);
  let n = 0;
  const service = new FeedbackService(api, () => 'key-' + ++n, () => true);
  assert.equal(await service.submit(draft), false);
  assert.equal(service.getSnapshot().phase, 'ready', 'back to the form');
  assert.match(service.getSnapshot().error ?? '', /reach/);
  assert.equal(await service.submit(draft), true);
  assert.equal(service.getSnapshot().phase, 'sent');
  assert.equal(sent[1]!.path, '/v1/reports'); assert.equal(sent[1]!.method, 'POST');
  assert.equal(sent[0]!.headers!['Idempotency-Key'], sent[1]!.headers!['Idempotency-Key'], 'a retry is the same report, never a second one');
  assert.deepEqual(sent[1]!.body, sent[0]!.body, 'the retry repeats the captured facts');
  assert.deepEqual(sent[1]!.body, {kind: 'problem', message: 'It keeps pausing every minute.',
    context: {about: 'playback/buffering', item: {id: 'itm_1', title: 'Heat'}, playbackSessionId: 'pbs_1', online: true}});
});

test('a report names the app and platform, never the device\'s own name, and nothing secret', async () => {
  const {api, sent} = reports([200, 200]);
  setInstallationIdentity({installationId: 'i'.repeat(32), name: 'Kitchen', platform: 'android', app: 'Portico', appVersion: '1.4.2'});
  try {
    const service = new FeedbackService(api);
    assert.equal(await service.submit({...draft, kind: 'apps', category: 'layout'}), true);
    const context = sent[0]!.body.context;
    assert.deepEqual(context, {about: 'apps/layout', item: {id: 'itm_1', title: 'Heat'}, platform: 'android', app: 'Portico', appVersion: '1.4.2'},
      'a category not about playback does not carry the playback');
    const wire = JSON.stringify(sent[0]);
    assert.equal(wire.includes('Kitchen'), false, 'no device name leaves');
    assert.doesNotMatch(wire, /token|password|Authorization|https?:\/\/|\/home\/|[A-Z]:\\/i);
  } finally {setInstallationIdentity(undefined);}
});

test('a refused report says so in plain words and keeps the draft; a new key is used only after one is sent', async () => {
  const {api, sent} = reports([429, 200, 200]);
  let n = 0;
  const service = new FeedbackService(api, () => 'key-' + ++n);
  assert.equal(await service.submit(draft), false);
  assert.ok(service.getSnapshot().error, 'the refusal is shown');
  assert.doesNotMatch(service.getSnapshot().error!, /too_many_reports/);
  assert.equal(await service.submit(draft), true);
  const again = new FeedbackService(api, () => 'key-' + ++n);
  assert.equal(await again.submit(draft), true);
  assert.deepEqual(sent.map(s => s.headers!['Idempotency-Key']), ['key-1', 'key-1', 'key-2']);
});

test('a draft needs a kind, a category and words, at most 5,000 characters counted as people count them', () => {
  const service = new FeedbackService(reports([]).api);
  const base = {kind: 'playback', category: 'buffering', message: 'x'};
  assert.equal(service.problem(base), '');
  assert.match(service.problem({...base, kind: 'nope'}), /what this is about/);
  assert.match(service.problem({...base, category: 'nope'}), /category/);
  assert.notEqual(service.problem({...base, message: '   '}), '');
  assert.equal(service.problem({...base, message: '😀'.repeat(FEEDBACK_MAX_MESSAGE)}), '', '5,000 astral characters are 10,000 UTF-16 units and still fit');
  assert.notEqual(service.problem({...base, message: '日'.repeat(FEEDBACK_MAX_MESSAGE + 1)}), '');
});

test('the dialog offers the same kinds and categories as before, each labelled', () => {
  const kinds = feedbackKinds();
  assert.deepEqual(kinds.map(k => k.id), ['playback', 'library', 'apps', 'account', 'other']);
  assert.equal(kinds.flatMap(k => k.categories).length, 24);
  for (const k of kinds) for (const label of [k.label, ...k.categories.map(c => c.label)]) assert.doesNotMatch(label, /^feedback\./, 'every label is in the catalogue');
});

test('a dialog that ended sends nothing, and a late answer does not come back to it', async () => {
  const {api, sent} = reports([200]);
  const life = new AbortController();
  let live = true;
  const service = new FeedbackService(api, undefined, undefined, {signal: life.signal, isCurrent: () => live});
  live = false;
  assert.equal(await service.submit(draft), false);
  assert.equal(sent.length, 0);
});
