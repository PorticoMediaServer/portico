import test from 'node:test';
import assert from 'node:assert/strict';
import {SubtitleService, subtitleReason, type SubtitlePlan} from '../src/subtitles.ts';

const plan = {version: 1, sessionId: 'pbs_1', generation: 1, sourceId: 'med_1', revision: 1, catalogRevision: 1, renderer: 'external_text', mode: 'off',
  offAvailable: true, offsetUs: '0', selected: null, resources: [], discovered: []} as SubtitlePlan;

// 07 rules 65 to 68: anyone who may play a title searches OpenSubtitles and downloads one, through the rebuilt server.
test('subtitle search and download use the rebuilt routes; an unset-up search says so in plain words', async () => {
  const calls: unknown[][] = [];
  let refuse = '';
  const service = new SubtitleService({request: async <T>(...args: unknown[]) => {
    calls.push(args.slice(0, 3));
    if (refuse) throw Object.assign(new Error('x'), {status: 409, code: refuse, retryable: false});
    return (args[1] === 'POST' ? {id: 'd42', versionId: 'med_1', format: 'srt', source: 'downloaded', forced: false, sdh: false, default: false}
      : {items: [{ref: '9001', language: 'en', release: 'Alien.1979.BluRay', sdh: false, forced: false, downloads: 3}], credit: 'Subtitles provided by OpenSubtitles.com'}) as T;
  }}, 'itm_1', () => plan);
  service.start();
  assert.equal(service.getSnapshot().plan, plan, 'the plan is the session\'s own, read from nothing');
  await service.search('en');
  assert.deepEqual(calls[0], ['/v1/items/itm_1/subtitles/search?languages=en', 'GET', undefined]);
  assert.deepEqual(service.getSnapshot().candidates, [{id: '9001', language: 'en', title: 'Alien.1979.BluRay', attribution: 'Subtitles provided by OpenSubtitles.com'}]);
  assert.equal(await service.apply(service.getSnapshot().candidates[0]!), 'd42');
  assert.deepEqual(calls[1], ['/v1/items/itm_1/subtitles', 'POST', {versionId: 'med_1', ref: '9001'}]);
  refuse = 'subtitle_search_not_set_up';
  await service.search('en');
  const after = service.getSnapshot();
  assert.deepEqual(after.provider, {enabled: false, reason: 'provider_not_configured'});
  assert.match(after.error ?? '', /key and account/);
  assert.equal(after.canRetry, false);
  for (const [code, reason, words] of [['subtitle_account_not_set_up', 'provider_account_not_configured', /account details/],
    ['subtitle_key_not_set_up', 'provider_key_not_configured', /add their key/]]) {
    refuse = code;
    await service.search('en');
    assert.deepEqual(service.getSnapshot().provider, {enabled: false, reason});
    assert.match(service.getSnapshot().error ?? '', words);
    assert.match(subtitleReason(reason), words);
    assert.equal(service.getSnapshot().canRetry, false);
  }
});
