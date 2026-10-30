import test from 'node:test';
import assert from 'node:assert/strict';
import {boundSubtitleControls} from '../src-next/platform/playback/subtitle-controls.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import type {SubtitleSnapshot} from '../../../packages/client-core/src/subtitles.ts';

function fixture() {
  let current = true;
  let snapshot: SubtitleSnapshot = {catalog: null, plan: null, busy: false, loading: false, suppressed: false, error: null, canRetry: false,
    candidates: [{id: 'candidate-1', language: 'en', title: 'Provider title', attribution: 'Provider attribution'}]};
  const calls: unknown[][] = [];
  let hold: Promise<void> | undefined;
  const called = async (...args: unknown[]) => { calls.push(args); await hold; };
  const service = {
    getSnapshot: () => snapshot,
    refresh: () => called('refresh'), refreshSources: () => called('refreshSources'), retry: () => called('retry'),
    search: (language = '', query = '', page = 1) => called('search', language, query, page),
    apply: (candidate: unknown, scope: string, rights: string) => called('apply', candidate, scope, rights),
    save: (input: unknown) => called('save', input),
  };
  return {controls: boundSubtitleControls(service, () => current), service, calls,
    retire: () => { current = false; }, snapshot: () => snapshot,
    error: (error: string) => { snapshot = {...snapshot, error}; }, hold: (value: Promise<void>) => { hold = value; }};
}

test('subtitle management uses the existing source service and its current offered candidate', async () => {
  const f = fixture();
  await f.controls.search('en', 'actual query', 2);
  await f.controls.apply('candidate-1', 'personal', 'Viewer supplied rights');
  const upload = {format: 'srt' as const, language: 'en', title: 'Picked subtitle', rights: 'Viewer supplied rights', scope: 'personal' as const, offsetUs: '0', content: '1\n00:00:00,000 --> 00:00:01,000\nText'};
  await f.controls.save(upload); await f.controls.refresh(); await f.controls.refreshSources(); await f.controls.retry();
  assert.deepEqual(f.calls, [['search', 'en', 'actual query', 2], ['apply', f.snapshot().candidates[0], 'personal', 'Viewer supplied rights'],
    ['save', upload], ['refresh'], ['refreshSources'], ['retry']]);
  assert.ok(Object.isFrozen(f.controls));
});

test('an unoffered provider ID cannot be passed as a fabricated candidate', async () => {
  const f = fixture();
  await assert.rejects(f.controls.apply('not-offered', 'personal', 'rights'));
  assert.deepEqual(f.calls, []);
});

test('ambiguous duplicate provider IDs cannot pick an arbitrary candidate', async () => {
  const f = fixture(), snapshot = f.snapshot(), candidate = snapshot.candidates[0];
  f.service.getSnapshot = () => ({...snapshot, candidates: [candidate, {...candidate, title: 'Different provider result'}]});
  await assert.rejects(f.controls.apply(candidate.id, 'personal', 'rights'));
  assert.deepEqual(f.calls, []);
});

test('retired sheet commands cannot call any old source service', async () => {
  const f = fixture(); f.retire();
  for (const action of [() => f.controls.refresh(), () => f.controls.refreshSources(), () => f.controls.retry(),
    () => f.controls.search(), () => f.controls.apply('candidate-1', 'personal', 'rights'),
    () => f.controls.save({format: 'srt', language: 'en', title: '', rights: '', scope: 'personal', offsetUs: '0', content: 'text'})]) {
    await assert.rejects(action());
  }
  assert.equal(f.calls.length, 0);
});

test('a late source receipt cannot be acknowledged after fence, service or owner retirement', async () => {
  const f = fixture(); let finish!: () => void;
  f.hold(new Promise<void>(resolve => { finish = resolve; }));
  const pending = f.controls.search('en');
  f.retire(); finish();
  await assert.rejects(pending);
  assert.equal(f.calls.length, 1);
});

test('a resolved service queue with a failed receipt never reports success or private error prose', async () => {
  const f = fixture(); f.error('Private provider diagnostic');
  await assert.rejects(f.controls.refresh(), {message: defaultI18n.t('player.subtitles.changeFailed')});
});
