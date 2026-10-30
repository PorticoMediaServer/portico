import assert from 'node:assert/strict';
import test from 'node:test';
import type {PlaybackFence} from '../../packages/player-core/src/index.ts';
import {WatchDockPanelOpening, type WatchDockPanel} from '../src/screens/watch/dock-panel-opening.ts';

// Authored source vectors for the production opening owner. UNRUN.
const fence: PlaybackFence = Object.freeze<PlaybackFence>({kind: 'queue', playback: {run: 1, scope: {
  serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile',
  controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane',
}}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1});
function fixture() {
  const authority = {}, binding = {};
  let active = true, expanded = false, selected = fence, principal = authority, remote: object | null = null;
  let localSender = true;
  let presentationFence = fence;
  let expands = 0, renewals = 0;
  const shown: WatchDockPanel[] = [], errors: unknown[] = [];
  const owner = new WatchDockPanelOpening({binding, authority, fence, panels: ['options', 'subtitles-audio'],
    snapshot: () => ({authority: principal, disposed: false, fence: selected, presentation: {kind: 'video', fence: presentationFence},
      dock: {local: {kind: 'video', expanded, fence: selected}, remote}}), isCurrent: () => active && localSender,
    expand: () => {expands++; expanded = true;}, show: panel => {shown.push(panel);},
    renew: () => {renewals++;}, reportError: error => {errors.push(error);}});
  return {owner, shown, errors, get expands() {return expands;}, get renewals() {return renewals;},
    collapse: () => {expanded = false;}, replace: () => {selected = {...fence, generation: 2};},
    replaceAuthority: () => {principal = {};}, retireView: () => {active = false;},
    replacePresentation: () => {presentationFence = {...fence, generation: 2};},
    senderTakesOver: () => {localSender = false;}, senderReturns: () => {localSender = true;},
    cast: () => {remote = {};}};
}

test('one original offer claims once; the same owner publishes only after expanded commit', () => {
  const f = fixture(), offer = f.owner.offer()!;
  assert.equal(offer.request('options'), true);
  assert.equal(offer.request('subtitles-audio'), false);
  assert.equal(f.expands, 1); assert.deepEqual(f.shown, []);
  offer.retire(); // Collapsed-view cleanup must not cancel its admitted expansion.
  f.owner.commitExpanded(); f.owner.commitExpanded();
  assert.deepEqual(f.shown, ['options']);
});

test('collapse and reopen mint a new offer; retained A cannot act through B', () => {
  const f = fixture(), a = f.owner.offer()!;
  a.request('options'); f.owner.commitExpanded(); f.collapse();
  const b = f.owner.offer()!;
  assert.equal(a.request('options'), false);
  assert.equal(b.request('subtitles-audio'), true);
  f.owner.commitExpanded(); assert.deepEqual(f.shown, ['options', 'subtitles-audio']);
});

test('same-tick expand then collapse cancels before an expanded commit and permits reopening', () => {
  const f = fixture(), a = f.owner.offer()!;
  a.request('options'); f.collapse(); f.owner.observe(); f.owner.commitExpanded();
  assert.deepEqual(f.shown, []); assert.equal(f.renewals, 1);
  const b = f.owner.offer()!;
  assert.equal(a.request('options'), false); assert.equal(b.request('subtitles-audio'), true);
  f.owner.commitExpanded(); assert.deepEqual(f.shown, ['subtitles-audio']);
});

test('a captured sender takeover retires A even if local availability returns before React commits', () => {
  const f = fixture(), a = f.owner.offer()!;
  f.senderTakesOver(); f.owner.observe(); f.senderReturns(); f.owner.observe();
  assert.equal(a.request('options'), false); assert.equal(f.renewals, 1);
  assert.equal(f.owner.offer()!.request('subtitles-audio'), true);
  f.owner.commitExpanded(); assert.deepEqual(f.shown, ['subtitles-audio']);
});

test('sender takeover after expansion cannot revive the old pending panel when it returns', () => {
  const f = fixture(); f.owner.offer()!.request('options');
  f.senderTakesOver(); f.owner.observe(); f.senderReturns(); f.owner.observe();
  f.owner.commitExpanded(); assert.deepEqual(f.shown, []);
});

test('a replaced generation, authority, remote dock or retired view cannot lend its successor a panel', () => {
  for (const retire of ['replace', 'replaceAuthority', 'replacePresentation', 'cast', 'retireView'] as const) {
    const f = fixture(), offer = f.owner.offer()!;
    f[retire](); assert.equal(offer.request('options'), false); assert.equal(f.expands, 0);
  }
});

test('retirement after expand but before layout drops the original pending panel', () => {
  for (const retire of ['replace', 'replaceAuthority', 'replacePresentation', 'cast', 'retireView'] as const) {
    const f = fixture(); f.owner.offer()!.request('options'); f[retire](); f.owner.commitExpanded();
    assert.deepEqual(f.shown, []);
  }
});

test('Strict replay/unmount disposes the owner and its pending request without reviving old offers', () => {
  const a = fixture(), old = a.owner.offer()!;
  old.request('options'); a.owner.dispose(); a.owner.commitExpanded();
  a.owner.observe(); // A queued old subscription cannot renew the retired screen.
  const b = fixture(); assert.equal(old.request('options'), false);
  assert.deepEqual(a.shown, []); assert.equal(a.renewals, 0);
  assert.equal(b.owner.offer()!.request('subtitles-audio'), true);
});

test('unoffered panels cannot consume an otherwise usable opening', () => {
  const f = fixture(), owner = new WatchDockPanelOpening({...f.owner.owner, panels: ['options']});
  const offer = owner.offer()!;
  assert.equal(offer.request('subtitles-audio'), false);
  assert.equal(offer.request('options'), true);
});

test('a synchronous expansion failure spends only its offer, reports once, and permits a fresh offer', () => {
  const f = fixture(), failure = new Error('unavailable');
  const owner = new WatchDockPanelOpening({...f.owner.owner, expand: () => {throw failure;}});
  const a = owner.offer()!;
  assert.equal(a.request('options'), true); assert.equal(a.request('options'), false);
  assert.deepEqual(f.errors, [failure]); assert.equal(f.renewals, 1); assert.deepEqual(f.shown, []);
  assert.equal(owner.offer()!.current(), true); assert.equal(a.current(), false);
});
