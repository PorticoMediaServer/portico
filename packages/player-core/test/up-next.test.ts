import test from 'node:test';
import assert from 'node:assert/strict';
import {upNextCountdown, upNextOffer} from '../src/up-next.ts';

const credits = {id: 'c1', kind: 'credits', startSeconds: 1290, endSeconds: 1380, automaticSafe: true};
const opening = {id: 'o1', kind: 'credits', startSeconds: 30, endSeconds: 90, automaticSafe: true};
const intro = {id: 'i1', kind: 'intro', startSeconds: 60, endSeconds: 120, automaticSafe: true};
const base = {markers: [intro, credits], positionSeconds: 1300, durationSeconds: 1380, kind: 'episode', mode: 'ask' as const, nextAvailable: true, passoutDue: false};

test('the next title is offered while the closing credits run, and only then', () => {
  const cases: {name: string; input: Partial<typeof base> & {markers?: typeof base.markers | null}; want: string | null}[] = [
    {name: 'inside the credits', input: {}, want: 'marker:c1'},
    {name: 'a moment before the credits', input: {positionSeconds: 1289}, want: null},
    {name: 'the last half second belongs to the ending', input: {positionSeconds: 1379.8}, want: null},
    {name: 'nothing queued after this title', input: {nextAvailable: false}, want: null},
    {name: 'the viewer turned credits prompts off', input: {mode: 'off'}, want: null},
    {name: 'credits skipped unattended are left to the skip itself', input: {mode: 'auto'}, want: null},
    {name: 'unattended skipping asked for, but this marker is not safe to skip unasked', input: {mode: 'auto', markers: [{...credits, automaticSafe: false}]}, want: 'marker:c1'},
    {name: 'the server wants to ask whether anyone is watching', input: {passoutDue: true}, want: null},
    {name: 'opening credits are not closing credits', input: {markers: [opening], positionSeconds: 40}, want: null},
    {name: 'with a closing marker, the last seconds after it (a scene after the credits) are left alone', input: {markers: [{...credits, endSeconds: 1340}], positionSeconds: 1360}, want: null},
    {name: 'an outro marker counts', input: {markers: [{...credits, id: 'x', kind: 'outro'}]}, want: 'marker:x'},
    {name: 'an episode with no marker: its last thirty seconds', input: {markers: [intro], positionSeconds: 1352}, want: 'tail'},
    {name: 'an episode with no marker, earlier', input: {markers: null, positionSeconds: 1340}, want: null},
    {name: 'a film with no marker is never interrupted', input: {markers: [], positionSeconds: 1370, kind: 'movie'}, want: null},
    {name: 'a short clip with no marker', input: {markers: [], durationSeconds: 300, positionSeconds: 290}, want: null},
    {name: 'a title whose length is not known', input: {durationSeconds: 0}, want: null},
    {name: 'a marker with no length is ignored', input: {markers: [{...credits, endSeconds: 1290}], positionSeconds: 1290}, want: null},
  ];
  for (const c of cases) assert.equal(upNextOffer({...base, ...c.input})?.id ?? null, c.want, c.name);
  assert.equal(upNextOffer(base)?.marker?.id, 'c1');
});

test('the countdown starts the next title early only when the viewer allows it, for credits the server is sure of, with autoplay on', () => {
  const sure = {id: 'marker:c1', marker: credits}, unsure = {id: 'marker:c2', marker: {...credits, id: 'c2', automaticSafe: false}}, tail = {id: 'tail'};
  const at = {positionSeconds: 1300, durationSeconds: 1380, rate: 1, autoplay: true, countdownSeconds: 10, elapsedSeconds: 3.2, startDuringCredits: true};
  assert.deepEqual(upNextCountdown({...at, offer: sure}), {automatic: true, seconds: 7});
  assert.deepEqual(upNextCountdown({...at, offer: sure, elapsedSeconds: 12}), {automatic: true, seconds: 0});
  // "Start the next episode during the credits" is off by default (Justin, 15 Oct): the card still
  // says when the next title starts, but nothing starts before this title ends.
  const {startDuringCredits: _on, ...byDefault} = at;
  assert.deepEqual(upNextCountdown({...byDefault, offer: sure, elapsedSeconds: 12}), {automatic: false, seconds: 90});
  assert.deepEqual(upNextCountdown({...at, startDuringCredits: false, offer: sure, elapsedSeconds: 12}), {automatic: false, seconds: 90});
  // Otherwise the count is what is left of this title, at its speed, plus the end card's countdown.
  assert.deepEqual(upNextCountdown({...at, offer: unsure}), {automatic: false, seconds: 90});
  assert.deepEqual(upNextCountdown({...at, offer: tail, rate: 2}), {automatic: false, seconds: 50});
  assert.equal(upNextCountdown({...at, offer: sure, autoplay: false}), null);
});
