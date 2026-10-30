import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeWatchControls, type NativeWatchControlContext, type NativeWatchControlClock} from '../src-next/screens/watch/controls.ts';
import {playbackFence} from '../../../packages/player-core/src/identity.ts';

const fence = (generation = 1) => playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'version', entryId: 'entry', generation});
const expanded: NativeWatchControlContext = {expanded: true, autoHide: true, hiddenByPanel: false, paused: false, scrubbing: false, focusHeld: false};
function manualClock() {
  let now = 0, id = 0;
  const pending = new Map<number, {callback: () => void; at: number}>();
  const clock: NativeWatchControlClock = {now: () => now, set: (callback, delay) => {const key = ++id; pending.set(key, {callback, at: now + delay}); return key;}, clear: key => {pending.delete(key as number);}};
  const advance = (ms: number) => {now += ms; for (const [key, job] of [...pending]) if (job.at <= now && pending.delete(key)) job.callback();};
  return {clock, advance, pending};
}

test('TV starts hidden, first key only raises Play/Pause, then Back hides before leaving', () => {
  const time = manualClock(), controls = new NativeWatchControls(true, time.clock), current = fence();
  controls.source(current, expanded);
  assert.deepEqual([controls.getSnapshot().visible, controls.getSnapshot().focus], [false, 'surface']);
  assert.equal(controls.key(current), true);
  assert.deepEqual([controls.getSnapshot().visible, controls.getSnapshot().focus], [true, 'toggle']);
  assert.equal(controls.key(current), false);
  assert.equal(controls.back(current), 'controls');
  assert.equal(controls.back(current), 'player');
  assert.equal(controls.back(fence(2)), 'stale');
  controls.dispose(); assert.equal(time.pending.size, 0);
});

test('progress does not extend the control clock; one fold deadline lasts three seconds', () => {
  const time = manualClock(), controls = new NativeWatchControls(false, time.clock), current = fence();
  controls.source(current, expanded); time.advance(3500); controls.source(current, expanded);
  time.advance(500); assert.equal(controls.getSnapshot().visible, false);
  controls.revealAfterLayout(current, time.clock.now() + 3000);
  time.advance(2999); assert.equal(controls.getSnapshot().visible, true);
  time.advance(1); assert.equal(controls.getSnapshot().visible, false);
  controls.dispose();
});

test('a full-window panel suppresses all transport and fold reveal, retaining its own scroll', () => {
  const time = manualClock(), controls = new NativeWatchControls(false, time.clock), current = fence();
  controls.source(current, {...expanded, hiddenByPanel: true});
  controls.open('subtitles', current); controls.rememberPanel(current, 189);
  assert.equal(controls.getSnapshot().visible, false);
  assert.equal(controls.revealAfterLayout(current, 3000), false);
  assert.equal(controls.back(current), 'panel');
  assert.equal(controls.getSnapshot().visible, false);
  controls.open('audio', current); controls.closePanel(current); controls.open('subtitles', current);
  assert.equal(controls.getSnapshot().panelOffset, 189);
  controls.source(fence(2), expanded);
  assert.equal(controls.getSnapshot().panel, undefined);
  assert.equal(controls.open('audio', current), false);
  assert.equal(controls.key(current), false);
  controls.open('subtitles', fence(2)); assert.equal(controls.getSnapshot().panelOffset, 0);
  controls.dispose();
});

test('paused, tabletop, scrub and held focus keep controls visible; docked clears the clock', () => {
  for (const keep of [{paused: true}, {autoHide: false}, {scrubbing: true}, {focusHeld: true}]) {
    const time = manualClock(), controls = new NativeWatchControls(true, time.clock), current = fence();
    controls.source(current, {...expanded, ...keep}); time.advance(10000);
    assert.equal(controls.getSnapshot().visible, true);
    assert.equal(time.pending.size, 0);
    controls.back(current); controls.source(current, {...expanded, ...keep});
    assert.equal(controls.getSnapshot().visible, false);
    controls.key(current); assert.equal(controls.getSnapshot().visible, true);
    controls.source(current, {...expanded, expanded: false, ...keep});
    assert.equal(controls.getSnapshot().visible, false); assert.equal(time.pending.size, 0);
    controls.dispose();
  }
});

test('an actual scrub remains pinned through producer progress and releases only its own fence', () => {
  const time = manualClock(), controls = new NativeWatchControls(false, time.clock), current = fence();
  controls.source(current, expanded); controls.scrub(current, true);
  time.advance(5000); controls.source(current, expanded); time.advance(5000);
  assert.equal(controls.getSnapshot().visible, true);
  controls.scrub(fence(2), false); time.advance(5000);
  assert.equal(controls.getSnapshot().visible, true);
  controls.scrub(current, false); time.advance(4000);
  assert.equal(controls.getSnapshot().visible, false);
  controls.dispose();
});
test('Back from Playback info restores the actual Options opener and its retained scroll before leaving', () => {
  const controls = new NativeWatchControls(true), current = fence();
  controls.source(current, expanded); controls.open('options', current); controls.rememberPanel(current, 470);
  controls.open('playback-info', current); controls.rememberPanel(current, 93);
  assert.equal(controls.back(current), 'panel');
  assert.equal(controls.getSnapshot().panel, 'options');
  assert.equal(controls.getSnapshot().panelFocus, 'playback-info');
  assert.equal(controls.getSnapshot().panelOffset, 470);
  assert.equal(controls.back(current), 'panel'); assert.equal(controls.getSnapshot().panel, undefined);
  controls.open('playback-info', current); assert.equal(controls.getSnapshot().panelOffset, 93);
  controls.dispose();
});


test('a focused prompt consumes Back before the player and its stale cleanup cannot clear a successor', () => {
  const controls = new NativeWatchControls(true), current = fence();
  controls.source(current, expanded);
  const old = controls.registerPromptDismiss(current, () => assert.fail('obsolete prompt'));
  let shown = true;
  const release = controls.registerPromptDismiss(current, () => {const active = shown; shown = false; return active;});
  old();
  assert.equal(controls.back(current), 'controls');
  assert.equal(controls.getSnapshot().visible, false);
  assert.equal(controls.back(current), 'player');
  shown = true; controls.source(fence(2), expanded);
  assert.equal(controls.back(fence(2)), 'player');
  release(); controls.dispose();
});


test('TV direct panel Back retains its real opener until the exact exit, with no fabricated Options parent', () => {
  const controls = new NativeWatchControls(true), current = fence();
  controls.source(current, expanded);
  const opener = {regionId: 'watch-transport', childId: 'watch-option-audio'};
  controls.open('audio', current, opener);
  assert.equal(controls.back(current), 'panel');
  const closing = controls.getSnapshot();
  assert.equal(closing.panel, undefined); assert.equal(closing.panelReturning, true);
  assert.equal(closing.visible, true); assert.deepEqual(closing.returnTarget, opener);
  assert.equal(controls.key(current), false); assert.equal(controls.back(current), 'panel');
  controls.panelExited(current, closing.panelExitRevision! + 1);
  assert.equal(controls.getSnapshot().panelReturning, true);
  controls.open('quality', current, {regionId: 'watch-transport', childId: 'watch-option-quality'});
  controls.back(current); const successor = controls.getSnapshot();
  controls.panelExited(current, closing.panelExitRevision!);
  assert.equal(controls.getSnapshot().panelReturning, true, 'late old exit cannot release the successor');
  controls.panelExited(current, successor.panelExitRevision!);
  assert.equal(controls.getSnapshot().panelReturning, false);
  controls.dispose();
});

test('TV nested panel Back restores the actual parent row and its scroll before the root opener', () => {
  const controls = new NativeWatchControls(true), current = fence();
  controls.source(current, expanded);
  controls.open('options', current, {regionId: 'watch-transport', childId: 'watch-options'}); controls.rememberPanel(current, 470);
  controls.open('subtitles', current, {regionId: 'watch-panel', childId: 'watch-action-subtitles'}); controls.rememberPanel(current, 130);
  controls.open('style', current, {regionId: 'watch-panel', childId: 'watch-action-style'});
  controls.back(current);
  assert.equal(controls.getSnapshot().panel, 'subtitles'); assert.equal(controls.getSnapshot().panelOffset, 130);
  assert.deepEqual(controls.getSnapshot().returnTarget, {regionId: 'watch-panel', childId: 'watch-action-style'});
  controls.back(current);
  assert.equal(controls.getSnapshot().panel, 'options'); assert.equal(controls.getSnapshot().panelOffset, 470);
  assert.deepEqual(controls.getSnapshot().returnTarget, {regionId: 'watch-panel', childId: 'watch-action-subtitles'});
  controls.back(current);
  assert.deepEqual(controls.getSnapshot().returnTarget, {regionId: 'watch-transport', childId: 'watch-options'});
  controls.source(fence(2), expanded);
  assert.equal(controls.getSnapshot().returnTarget, undefined); assert.equal(controls.getSnapshot().panelReturning, false);
  controls.dispose();
});
