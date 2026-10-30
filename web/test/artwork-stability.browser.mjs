import assert from 'node:assert/strict';

/** Actual React/DOM regression: real runtime, <Artwork>/<Shelf>, IntersectionObserver,
 * HTMLImageElement.decode() and the real 180 ms admission CSS over a synthetic
 * service boundary. The caller owns the browser and origin. */
export async function checkArtworkStability(page, origin) {
  await page.setViewportSize({width: 1024, height: 900});
  await page.goto(`${origin}/test/artwork-stability.html`, {waitUntil: 'networkidle'});
  await page.evaluate(async () => {
    const fixture = await import('/test/fixtures/artwork-stability.tsx');
    window.disposeArtworkStability = fixture.mountArtworkStability();
  });
  const snapshot = () => page.evaluate(() => window.artworkStability.snapshot());
  const shelf = () => page.evaluate(() => window.artworkStability.shelf());
  const waitDecodes = (count, admitted = true) => page.waitForFunction(([expected, needsAdmitted]) => {
    const value = window.artworkStability.snapshot();
    return value.decodes >= expected && (!needsAdmitted || (value.images === 1 && value.admitted));
  }, [count, admitted]);

  // The first real load owns its node, its one decode and the exact 180 ms fade.
  await page.waitForFunction(() => window.artworkStability?.snapshot().status === 'ready');
  await waitDecodes(1);
  await page.waitForFunction(() => window.artworkStability.snapshot().transitions >= 1);
  const first = await snapshot();
  assert.equal(first.decodes, 1, 'one live image element owns one native decode');
  assert.equal(first.images, 1); assert.equal(first.transitionDuration, '0.18s', 'legitimate arrival keeps the 180 ms fade');
  assert.ok(first.transitions >= 1, 'the arrival fade actually runs once');

  // Ordinary parent rerenders mint a fresh runtime.source wrapper every time.
  await page.evaluate(() => window.artworkStability.capture());
  for (let index = 0; index < 12; index++) await page.evaluate(() => window.artworkStability.rerender());
  await page.waitForTimeout(80);
  const stable = await snapshot();
  assert.equal(await page.evaluate(() => window.artworkStability.sameImage()), true, 'the live img node is the same');
  assert.equal(await page.evaluate(() => window.artworkStability.capturedConnected()), true);
  assert.equal(stable.tick - first.tick, 12, 'the parent really rendered twelve times');
  assert.equal(stable.decodes, first.decodes, 'no extra decode');
  assert.equal(stable.acquisitions, first.acquisitions, 'no extra retain lease');
  assert.equal(stable.releases, first.releases, 'no lease retirement');
  assert.equal(stable.transitions, first.transitions, 'no replayed arrival fade');
  assert.equal(stable.paintRemovals, first.paintRemovals, 'admission is never removed from the live image');
  assert.equal(stable.admitted, true); assert.equal(stable.status, 'ready');

  // A genuine key change retires the original image and its callbacks, and takes one new fade.
  await page.evaluate(() => window.artworkStability.capture());
  const mark = await page.evaluate(() => window.artworkStability.mark());
  await page.evaluate(() => window.artworkStability.key('beta'));
  assert.equal(await page.evaluate(() => window.artworkStability.sameImage()), false, 'a new key gets its own node');
  await waitDecodes(first.decodes + 1);
  assert.equal(await page.evaluate(() => window.artworkStability.capturedConnected()), false, 'the retired node leaves the document');
  await page.waitForFunction(expected => window.artworkStability.snapshot().transitions >= expected, first.transitions + 1);
  const changed = await snapshot();
  assert.equal(changed.decodes, first.decodes + 1);
  assert.ok(changed.transitions > first.transitions, 'genuine arrival fades again');
  assert.equal(changed.transitionDuration, '0.18s');
  const ready = (await page.evaluate(index => window.artworkStability.statusesSince(index), mark)).filter(item => item.status === 'ready');
  assert.ok(ready.length >= 1); assert.ok(ready.at(-1).key.includes('beta'), 'ready belongs to the new original request');

  // Same authority, same key, ordinary rerenders stay stable again.
  await page.evaluate(() => window.artworkStability.capture());
  for (let index = 0; index < 4; index++) await page.evaluate(() => window.artworkStability.rerender());
  await page.waitForTimeout(60);
  assert.equal(await page.evaluate(() => window.artworkStability.sameImage()), true);
  assert.equal((await snapshot()).decodes, changed.decodes);

  // Negative: a custom source owns only a fresh retain closure, so a changed
  // retain callback still retires the original and never adopts it.
  await page.evaluate(() => window.artworkStability.customRetain(true));
  await waitDecodes(changed.decodes + 1);
  const custom = await snapshot();
  await page.evaluate(() => window.artworkStability.capture());
  await page.evaluate(() => window.artworkStability.rerender());
  await waitDecodes(custom.decodes + 1);
  assert.equal(await page.evaluate(() => window.artworkStability.sameImage()), false, 'a fresh custom retain closure is not the same lifetime');
  assert.equal((await snapshot()).decodes, custom.decodes + 1);

  // Returning to the factory wrapper restores one stable lease.
  await page.evaluate(() => window.artworkStability.customRetain(false));
  await page.waitForTimeout(80);
  const restored = await snapshot();
  await page.evaluate(() => window.artworkStability.capture());
  for (let index = 0; index < 3; index++) await page.evaluate(() => window.artworkStability.rerender());
  await page.waitForTimeout(60);
  assert.equal(await page.evaluate(() => window.artworkStability.sameImage()), true);
  assert.equal((await snapshot()).decodes, restored.decodes);

  // A request lifetime change (order) is a different original request.
  await page.evaluate(() => window.artworkStability.order(1));
  await waitDecodes(restored.decodes + 1);
  assert.equal((await snapshot()).status, 'ready');
  await page.evaluate(() => window.artworkStability.capture());
  await page.evaluate(() => window.artworkStability.rerender());
  await page.waitForTimeout(60);
  assert.equal(await page.evaluate(() => window.artworkStability.sameImage()), true);

  // Visibility retirement and return are real; the cached fast path stays 0 ms.
  const beforeHide = await snapshot();
  await page.evaluate(() => window.artworkStability.visible(false));
  const hidden = await snapshot();
  assert.equal(hidden.images, 0); assert.equal(hidden.status, 'empty');
  assert.ok(hidden.releases > beforeHide.releases, 'hiding retires the original lease');
  await page.evaluate(() => window.artworkStability.visible(true));
  await waitDecodes(hidden.decodes + 1);
  const shown = await snapshot();
  assert.equal(shown.transitionDuration, '0s', 'a genuinely cached return keeps the fast path');
  assert.equal(shown.status, 'ready'); assert.equal(shown.admitted, true);
  await page.evaluate(() => window.artworkStability.capture());
  await page.evaluate(() => window.artworkStability.rerender());
  await page.waitForTimeout(60);
  assert.equal(await page.evaluate(() => window.artworkStability.sameImage()), true);

  // A changed runtime authority is a different scheduler and retires the old lease.
  await page.evaluate(() => window.artworkStability.capture());
  const beforeAuthority = await snapshot();
  await page.evaluate(() => window.artworkStability.authority());
  await waitDecodes(beforeAuthority.decodes + 1);
  assert.equal(await page.evaluate(() => window.artworkStability.sameImage()), false, 'a new authority never adopts the old lease');
  await page.evaluate(() => window.artworkStability.capture());
  await page.evaluate(() => window.artworkStability.rerender());
  await page.waitForTimeout(60);
  assert.equal(await page.evaluate(() => window.artworkStability.sameImage()), true);

  // Shelf: a scroll inside the same edge range does not rebuild every child,
  // while the visible-range callback and both edge buttons still update.
  await page.waitForFunction(() => window.artworkStability.shelf().maxLeft > 200);
  const start = await shelf();
  assert.ok(start.rangeCalls > 0); assert.equal(start.leftDisabled, true); assert.equal(start.rightDisabled, false);
  await page.evaluate(() => window.artworkStability.scroll(60));
  await page.waitForTimeout(120);
  const leavingEdge = await shelf();
  assert.equal(leavingEdge.leftDisabled, false, 'the left arrow appears after scrolling right');
  assert.equal(leavingEdge.rightDisabled, false);
  assert.ok(leavingEdge.childRenders > start.childRenders, 'the changed edge commits once');
  const settled = leavingEdge, settledRenders = leavingEdge.childRenders;
  await page.evaluate(() => window.artworkStability.scroll(1200));
  await page.evaluate(() => window.artworkStability.scroll(1400));
  await page.waitForTimeout(120);
  const inside = await shelf();
  assert.equal(inside.childRenders, settledRenders, 'same edge range rebuilds no child');
  assert.ok(inside.rangeCalls >= settled.rangeCalls + 2, 'visible range still publishes for each scroll');
  assert.ok(inside.lastRange[0] > settled.lastRange[0] || inside.lastRange[1] > settled.lastRange[1], 'the visible range moved with the scroll');
  await page.evaluate(() => window.artworkStability.scroll('end'));
  await page.waitForTimeout(120);
  const end = await shelf();
  assert.equal(end.rightDisabled, true, 'the right arrow retires at the real end');
  assert.equal(end.leftDisabled, false);
  assert.ok(end.childRenders > inside.childRenders, 'the end edge commits once');
  assert.ok(end.lastRange[1] > inside.lastRange[1], 'the end range includes the last mounted item');
  await page.evaluate(() => window.artworkStability.scroll(0));
  await page.waitForTimeout(120);
  assert.equal((await shelf()).leftDisabled, true);

  await page.evaluate(() => window.disposeArtworkStability?.());
}
