import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeTitleStarts} from '../src-next/screens/watch/title-starts.ts';
import type {NativePlaybackStart} from '../src-next/platform/playback/types.ts';
const input = {entry: {id: 'episode', kind: 'episode' as const, title: 'Actual episode'}, target: {itemId: 'episode', startSeconds: 17}};
function fixture() {
  let revision = 1, owner = true, member = true, release!: () => void, captured: NativePlaybackStart | undefined;
  const pending = new Promise<void>(resolve => {release = resolve;});
  const starts = new NativeTitleStarts({current: () => owner, revision: () => revision, start: async value => {captured = value; await pending;}});
  return {starts, captured: () => captured, release, member: () => member, remove: () => {member = false; starts.invalidate();}, navigate: () => {revision++; starts.invalidate();}, retire: () => {owner = false; starts.invalidate();}};
}
for (const retirement of ['remove', 'navigate', 'retire', 'dispose'] as const) test(`title caller carries a real abort/current lifetime through pending start: ${retirement}`, async () => {
  const d = fixture(), pending = d.starts.start(input, d.member);
  try {
    const capture = d.captured(); assert.equal(capture?.target, input.target); assert.equal(capture?.lifetime?.isCurrent(), true);
    if (retirement === 'dispose') d.starts.dispose(); else d[retirement]();
    assert.equal(capture?.lifetime?.signal.aborted, true); assert.equal(capture?.lifetime?.isCurrent(), false);
  } finally {d.release(); await pending; d.starts.dispose();}
});
test('accepted output replacement alone is not a title/nav/member retirement; settled start does not own player lifetime', async () => {
  const d = fixture(), pending = d.starts.start(input, d.member);
  d.starts.invalidate();
  assert.equal(d.captured()?.lifetime?.isCurrent(), true); assert.equal(d.captured()?.lifetime?.signal.aborted, false);
  d.release(); await pending; d.starts.dispose();
  assert.equal(d.captured()?.lifetime?.signal.aborted, false);
});
