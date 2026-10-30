import test from 'node:test';
import assert from 'node:assert/strict';
import {advancePendingChoice, preplaySubtitles, type PendingChoiceSession} from '../src/pending-track-choice.ts';
import {enUS} from '../../i18n/src/index.ts';

const t = (id: keyof typeof enUS, values?: Record<string, unknown>) => String(enUS[id]).replace(/\{(\w+)\}/g, (_, k) => String(values?.[k] ?? ''));
const resources = [
  {id: 'en-full', language: 'en', enabled: true},
  {id: 'en-sdh', language: 'en', enabled: true},
  {id: 'unknown', language: 'und', enabled: true},
];
const session = (selectedId?: string): PendingChoiceSession => ({sessionId: 's1', audio: {availability: 'unavailable'}, tracks: {tracks: [], selected: null}, subtitles: {mode: selectedId ? 'track' : 'off', selectedId, busy: false, resources}});

test('a pre-play track named by its id is chosen exactly, including one with no language', () => {
  const chosen: (string | null)[] = [];
  const actions = {selectRendition() {}, selectTrack() {}, chooseSubtitle: (id: string | null) => { chosen.push(id); }};
  assert.deepEqual(advancePendingChoice({choice: {itemId: 'i', subtitle: {resourceId: 'unknown'}}}, session(), actions), {});
  assert.deepEqual(advancePendingChoice({choice: {itemId: 'i', subtitle: {resourceId: 'en-sdh'}}}, session('en-full'), actions), {});
  assert.deepEqual(chosen, ['unknown', 'en-sdh']);
});

test('a track the session no longer offers is not replaced by another in the same language', () => {
  const chosen: (string | null)[] = [];
  const out = advancePendingChoice({choice: {itemId: 'i', subtitle: {resourceId: 'gone', language: 'en'}}}, session(), {selectRendition() {}, selectTrack() {}, chooseSubtitle: id => { chosen.push(id); }});
  assert.deepEqual(chosen, []);
  assert.deepEqual(out, {});
});

test('the pre-play list keeps every track, unknown and same-language ones apart', () => {
  const list = preplaySubtitles([
    {id: 'a', language: 'en', forced: false, sdh: false, format: 'srt'},
    {id: 'b', language: 'en', forced: false, sdh: true, format: 'srt'},
    {id: 'c', forced: false, sdh: false, title: 'Commentary'},
    {id: 'd', forced: false, sdh: false},
    {id: 'e', language: 'en', forced: true, sdh: false, format: 'ass'},
  ], t as never, 'en');
  assert.deepEqual(list.map(x => x.id), ['a', 'b', 'c', 'd', 'e']);
  assert.equal(new Set(list.map(x => x.label)).size, 5);
  assert.match(list[1]!.label, /SDH/);
  assert.equal(list[2]!.label, 'Commentary');
});
