import test from 'node:test';
import assert from 'node:assert/strict';
import {activityTitle, bindAppleActivity, type NativeAppleActivityOwner} from '../src-next/platform/apple-activity.ts';

const episode = {id: 'ep1', kind: 'episode', title: 'Failure\'s Contagious', show: {id: 's1', name: 'Slow Horses'}} as never;
const movie = {id: 'm1', kind: 'movie', title: 'Heat'} as never;
const row = (id: string, entries: unknown[], children?: unknown[]) => ({id, entries, ...(children ? {children} : {})});

function rig(over: Partial<NativeAppleActivityOwner> = {}) {
  const calls: unknown[][] = [], home = new Set<() => void>(), playback = new Set<() => void>();
  let document: any = {rows: [row('new_on_server', [movie]), row('continue', [episode, movie])]}, playing: any;
  const owner: NativeAppleActivityOwner = {current: () => true, home: () => document, subscribeHome: l => {home.add(l); return () => home.delete(l);},
    playing: () => playing, subscribePlayback: l => {playback.add(l); return () => playback.delete(l);}, ...over};
  const dispose = bindAppleActivity(owner, {setCurrent: t => calls.push(['setCurrent', t]), indexTitles: l => calls.push(['index', l]), clear: () => calls.push(['clear'])}, error => {throw error;});
  return {calls, dispose, setHome: (next: unknown) => {document = next; home.forEach(l => l());}, play: (next: unknown) => {playing = next; playback.forEach(l => l());}, home, playback};
}

test('N2-2: Home\'s Continue Watching becomes Siri\'s title list and Spotlight\'s results, named by the show for an episode', () => {
  const {calls} = rig();
  assert.deepEqual(calls[0], ['index', {continueWatching: [{itemId: 'ep1', title: 'Slow Horses', subtitle: 'Failure\'s Contagious'}, {itemId: 'm1', title: 'Heat'}], saved: []}]);
  assert.equal(calls.length, 1, 'nothing playing: no activity to withdraw');
});

test('the index is rewritten only when Continue Watching changes, including inside a row family', () => {
  const {calls, setHome} = rig();
  calls.length = 0;
  setHome({rows: [row('new_on_server', []), row('continue', [episode, movie])]});
  assert.deepEqual(calls, [], 'same titles: no rewrite');
  setHome({rows: [row('for_you', [], [row('continue', [movie])])]});
  assert.deepEqual(calls, [['index', {continueWatching: [{itemId: 'm1', title: 'Heat'}], saved: []}]]);
});

test('the playing title is the Handoff activity, and stopping withdraws it', () => {
  const {calls, play} = rig();
  calls.length = 0;
  play(movie); play(movie); play(undefined);
  assert.deepEqual(calls, [['setCurrent', {itemId: 'm1', title: 'Heat'}], ['setCurrent', null]]);
});

test('sign-out or a profile switch clears everything and stops listening', () => {
  const {calls, dispose, home, playback} = rig();
  dispose(); dispose();
  assert.deepEqual(calls.at(-1), ['clear']); assert.equal(calls.filter(c => c[0] === 'clear').length, 1);
  assert.equal(home.size, 0); assert.equal(playback.size, 0);
});

test('a retired viewer publishes nothing', () => {
  const {calls} = rig({current: () => false});
  assert.deepEqual(calls, []);
});

test('an id the native side would refuse is left out', () => {
  assert.equal(activityTitle({id: 'bad id', kind: 'movie', title: 'X'} as never), undefined);
  assert.equal(activityTitle({id: 'ok', kind: 'movie', title: ''} as never), undefined);
});
