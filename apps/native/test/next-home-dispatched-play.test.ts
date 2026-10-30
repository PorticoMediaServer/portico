import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeDispatchedPlayLifetime, nativeHomeEntryLifetime, reportDispatchedPlayFailure} from '../src-next/screens/home/entry-lifetime.ts';

const emitter = () => {const listeners = new Set<() => void>(); return {subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}, emit: () => listeners.forEach(listener => listener())};};

// E-tv-36: Home Play logged dispatch and then nothing. The start's lifetime followed the Home
// document and the navigation snapshot, so a Home re-read during the start aborted it before the
// options request, and the abort was swallowed. A dispatched Play now ends only with authority.
test('E-tv-36: a dispatched Home Play survives a Home re-read and ends only with the authority', () => {
  let document = 1, authorityCurrent = true;
  const home = emitter(), authority = emitter();
  const old = nativeHomeEntryLifetime(() => document === 1, [home.subscribe, authority.subscribe], new AbortController().signal);
  const play = nativeDispatchedPlayLifetime(() => authorityCurrent, authority.subscribe, new AbortController().signal);
  document = 2; home.emit();
  assert.equal(old.lifetime.isCurrent(), false, 'the card-creation lifetime still tracks the document');
  assert.equal(play.lifetime.isCurrent(), true, 'a Home re-read does not retire a dispatched Play');
  assert.equal(play.lifetime.signal.aborted, false);
  authorityCurrent = false; authority.emit();
  assert.equal(play.lifetime.signal.aborted, true, 'an authority change still retires it');
  old.dispose(); play.dispose();
});

test('E-tv-36: a dispatched Play failure is always said, except a retirement or a gone owner', () => {
  const said: unknown[] = [], report = (error: unknown) => said.push(error);
  const failure = Object.assign(new Error('playback_unavailable'), {code: 'unavailable'});
  reportDispatchedPlayFailure(failure, () => true, report);
  reportDispatchedPlayFailure(Object.assign(new Error('Playback start retired'), {name: 'AbortError'}), () => true, report);
  reportDispatchedPlayFailure(failure, () => false, report);
  assert.deepEqual(said, [failure]);
});
