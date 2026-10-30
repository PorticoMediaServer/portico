import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n, type I18n} from '../../../packages/i18n/src';
import type {GuideProgram} from '../../../packages/client-core/src/guide/types';
import {nativeGuideDate, nativeGuideNext, nativeProgrammeCaption} from '../src-next/screens/settings-live/guide-presentation';

const programme: GuideProgram = {id: 'programme', channelId: 'channel', title: 'Stored title (Special Edition)', start: 1, end: 2};
const clock = (at: number) => at === 1 ? '16:05' : '17:05';

function text() {
  const calls: {key: string; values: unknown}[] = [];
  const t: I18n['t'] = (key, values) => {calls.push({key, values}); return defaultI18n.t(key, values);};
  return {t, locale: 'en-GB', calls};
}

test('programme timing captions translate through the supplied source and preserve supplied clock values', () => {
  const current = text();
  assert.deepEqual(nativeProgrammeCaption(programme, true, false, clock, current), ['Ends 17:05']);
  assert.deepEqual(current.calls, [{key: 'guide.ends', values: {time: '17:05'}}]);
  current.calls.length = 0;
  assert.deepEqual(nativeProgrammeCaption(programme, false, false, clock, current), ['16:05 – 17:05']);
  assert.deepEqual(current.calls, [{key: 'guide.range', values: {start: '16:05', end: '17:05'}}]);
  current.calls.length = 0;
  assert.deepEqual(nativeProgrammeCaption(programme, true, true, clock, current), ['16:05 – 17:05']);
  assert.deepEqual(current.calls, [{key: 'guide.range', values: {start: '16:05', end: '17:05'}}]);
});

test('regional timing never replaces stored subtitle/episode facts or manufactures unavailable programme times', () => {
  const current = text(), subtitle = 'Stored subtitle: Unabridged / HG Special Edition';
  assert.deepEqual(nativeProgrammeCaption({...programme, subtitle, episode: {season: 5, number: 2}}, true, false, clock, current), ['S5 E2', subtitle]);
  assert.equal(current.calls.some(call => call.key.startsWith('guide.')), false);
  current.calls.length = 0;
  assert.deepEqual(nativeProgrammeCaption(undefined, true, false, clock, current), []);
  assert.deepEqual(nativeProgrammeCaption(programme, true, false, () => undefined, current), []);
  assert.deepEqual(current.calls, []);
});


test('next-programme text uses the same supplied Guide clock and exact stored title', () => {
  const current = text(), next = {...programme, title: 'Stored next: Special Edition'};
  assert.equal(nativeGuideNext(next, clock, current), defaultI18n.t('guide.next', {title: next.title, time: '16:05'}));
  assert.deepEqual(current.calls, [{key: 'guide.next', values: {title: next.title, time: '16:05'}}]);
  current.calls.length = 0;
  assert.equal(nativeGuideNext(undefined, clock, current), undefined);
  assert.equal(nativeGuideNext(next, () => undefined, current), undefined);
  assert.deepEqual(current.calls, []);
});


test('Guide day/date words follow format locale and genuine timezone without manufacturing invalid dates', () => {
  const at = Date.UTC(2026, 9, 24, 1, 5), options = {weekday: 'short', month: 'short', day: 'numeric'} as const;
  assert.equal(nativeGuideDate('en-US', 'UTC', options)(at), 'Sat, Oct 24');
  assert.equal(nativeGuideDate('en-GB', 'UTC', options)(at), 'Sat 24 Oct');
  assert.equal(nativeGuideDate('en-US', 'America/Halifax', {weekday: 'long'})(at), 'Friday');
  assert.equal(nativeGuideDate('en-US', 'invalid/timezone', options)(at), '');
  assert.equal(nativeGuideDate('en-US', 'UTC', options)(NaN), '');
});
