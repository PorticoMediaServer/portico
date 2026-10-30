import test from 'node:test';
import assert from 'node:assert/strict';
import {easings, reducedMotion} from '../../packages/design/src/index.ts';
import {
  SOURCE_ART_MORPH_MS, SOURCE_ART_PREFIX, releaseSourceArt, sourceArtEligible, sourceArtMorph, sourceArtOwnsName,
  sourceArtReverseEligible, viewportIntersects, type SourceArtClaim,
} from '../src/kit/cards/source-art-transition.ts';

const good = {attached: true, width: 190, height: 285, top: 40, left: 40, intersectsViewport: true, visible: true, named: false, reduce: false};

test('motion3.3: 420ms out, and the reduce policy keeps 160ms opacity only', () => {
  assert.equal(SOURCE_ART_MORPH_MS, 420);
  assert.deepEqual(sourceArtMorph(false), {duration: 420, easing: easings.out});
  assert.deepEqual(sourceArtMorph(true), {duration: reducedMotion.durationMs, easing: reducedMotion.easing});
  assert.equal(reducedMotion.durationMs, 160);
});

test('eligibility needs finite on-screen geometry, real visibility and no name or reduce', () => {
  assert.equal(sourceArtEligible(good), true);
  // A positive rect that sits off the viewport is not visible, whatever its size.
  assert.equal(sourceArtEligible({...good, intersectsViewport: false}), false);
  assert.equal(sourceArtEligible({...good, visible: false}), false);
  // Non-finite or empty geometry is refused, including positions.
  for (const facts of [{width: 0}, {height: -1}, {width: Number.NaN}, {height: Number.POSITIVE_INFINITY}, {top: Number.NaN}, {left: Number.NEGATIVE_INFINITY}]) {
    assert.equal(sourceArtEligible({...good, ...facts}), false, JSON.stringify(facts));
  }
  assert.equal(sourceArtEligible({...good, attached: false}), false);
  assert.equal(sourceArtEligible({...good, named: true}), false, 'a computed name of its own refuses the claim');
  assert.equal(sourceArtEligible({...good, reduce: true}), false);
});

test('a reverse only targets a source still attached, laid out and on screen', () => {
  const {named: _named, reduce: _reduce, ...reverse} = good;
  assert.equal(sourceArtReverseEligible(reverse), true);
  assert.equal(sourceArtReverseEligible({...reverse, attached: false}), false);
  assert.equal(sourceArtReverseEligible({...reverse, intersectsViewport: false}), false);
  assert.equal(sourceArtReverseEligible({...reverse, visible: false}), false);
  assert.equal(sourceArtReverseEligible({...reverse, width: 0}), false);
  assert.equal(sourceArtReverseEligible({...reverse, top: Number.NaN}), false);
});

test('each claim owns only its own minted name, so a late release cannot clear a newer claim', () => {
  assert.equal(SOURCE_ART_PREFIX.endsWith('-'), true, 'names carry a per-claim suffix');
  const oldClaim = `${SOURCE_ART_PREFIX}1`, newClaim = `${SOURCE_ART_PREFIX}2`;
  // The old claim's release, or an old queued callback, no longer matches the element.
  assert.equal(sourceArtOwnsName(oldClaim, newClaim), false);
  assert.equal(sourceArtOwnsName(oldClaim, oldClaim), true);
  assert.equal(sourceArtOwnsName(newClaim, newClaim), true);
  for (const other of ['', 'none', undefined, null, 'nav', 'playback']) {
    assert.equal(sourceArtOwnsName(oldClaim, other), false, String(other));
  }
});

test('release clears its own inline name on a detached or reused node and never a newer claim', () => {
  const fakeArt = (inline: string, connected: boolean) => ({isConnected: connected, style: {viewTransitionName: inline}});
  const claim = (name: string, art: {isConnected: boolean; style: {viewTransitionName: string}}) => ({name, art} as unknown as SourceArtClaim);
  // A detached node still carries the name this helper put on it; release must take it off.
  const detached = fakeArt('card-source-art-1', false);
  releaseSourceArt(claim('card-source-art-1', detached));
  assert.equal(detached.style.viewTransitionName, '');
  // A reused node with a newer claim keeps the newer name when the old claim is released.
  const reused = fakeArt('card-source-art-2', true);
  releaseSourceArt(claim('card-source-art-1', reused));
  assert.equal(reused.style.viewTransitionName, 'card-source-art-2');
  // Its own claim still clears it.
  releaseSourceArt(claim('card-source-art-2', reused));
  assert.equal(reused.style.viewTransitionName, '');
});

test('an unknown, non-finite or non-positive viewport is never evidence of visibility', () => {
  const bounds = {right: 10, bottom: 10, left: 0, top: 0};
  assert.equal(viewportIntersects(bounds, {width: 100, height: 100}), true);
  const refused: readonly {width: unknown; height: unknown}[] = [
    {width: 0, height: 100}, {width: 100, height: 0}, {width: -1, height: 100},
    {width: Number.NaN, height: 100}, {width: 100, height: Number.POSITIVE_INFINITY},
    {width: undefined, height: 100}, {width: '100', height: 100},
  ];
  for (const view of refused) assert.equal(viewportIntersects(bounds, view), false, JSON.stringify(view));
  // A rect wholly outside a known viewport is refused too.
  assert.equal(viewportIntersects({right: -1, bottom: 10, left: -50, top: 0}, {width: 100, height: 100}), false);
});
