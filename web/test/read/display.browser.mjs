import assert from 'node:assert/strict';

// Caller owns the one browser/build/server/profile and heavy-runner lifetime.
// This tests controlled frame6b components, never production Read authority.
export async function checkReaderDisplay(page, origin, onCase = async () => {}) {
  await page.setViewportSize({width: 820, height: 1180});
  await page.goto(`${origin}/test/read/display.html`, {waitUntil: 'networkidle'});
  const panel = page.getByRole('region', {name: 'Display', exact: true});
  const trigger = page.getByRole('button', {name: 'Display: theme, font, size and layout', exact: true});
  await panel.waitFor();
  await page.evaluate(() => document.fonts.ready);
  assert.equal(await page.getByRole('radio', {name: 'Sepia', exact: true}).evaluate(e => e === document.activeElement), true, 'the parent chrome cannot steal panel focus');
  await page.keyboard.press('ArrowLeft');
  assert.equal(await page.getByRole('radio', {name: 'Black', exact: true}).getAttribute('aria-checked'), 'true', 'a choice commits before a rapid next key');
  await page.keyboard.press('Escape');
  assert.equal(await panel.count(), 0);
  assert.equal(await trigger.evaluate(e => e === document.activeElement), true, 'return to a trigger whose ref attached after initially-open Display');
  await trigger.click();
  await panel.waitFor();
  const two = page.getByRole('switch', {name: 'Two pages side by side in landscape', exact: true});
  await two.click(); assert.equal(await two.isChecked(), false);
  await page.getByRole('button', {name: 'Larger', exact: true}).click();
  assert.equal(await page.getByRole('slider', {name: 'Text size', exact: true}).inputValue(), '6');
  await page.getByRole('radiogroup', {name: 'Line spacing', exact: true}).getByRole('radio', {name: 'Loose', exact: true}).click();
  await page.getByRole('button', {name: 'Open Settings', exact: true}).click();
  const callbacks = await page.evaluate(() => window.readerDisplayProof);
  for (const [key, value] of [['twoPages', false], ['size', 6], ['spacing', 'loose'], ['settings', true]]) assert.ok(callbacks.changes.some(change => change.key === key && change.value === value), key);
  // Both DOM page clicks and forwarded iframe taps dismiss without turning.
  await page.locator('[data-reader-surface]').click({position: {x: 30, y: 300}});
  assert.equal(await panel.count(), 0);
  assert.equal((await page.evaluate(() => window.readerDisplayProof.turns)).length, 0);
  await trigger.click(); await panel.waitFor();
  await page.evaluate(() => window.readerDisplayInput({kind: 'tap', fraction: .9}));
  assert.equal(await panel.count(), 0);
  assert.equal((await page.evaluate(() => window.readerDisplayProof.turns)).length, 0);
  await trigger.click(); await panel.waitFor();
  const prevented = await page.evaluate(() => { let prevented = false; window.readerDisplayInput({kind: 'key', key: 'Escape', shift: false, alt: false, ctrl: false, meta: false, editable: false, preventDefault() { prevented = true; }}); return prevented; });
  assert.equal(prevented, true); assert.equal(await panel.count(), 0);
  await page.setViewportSize({width: 820, height: 600});
  await trigger.click(); await panel.waitFor();
  const scroll = await panel.evaluate(element => ({scroll: element.scrollHeight, client: element.clientHeight, width: element.getBoundingClientRect().width}));
  assert.equal(scroll.width, 360); assert.ok(scroll.scroll > scroll.client, 'short windows scroll the controls rather than clipping them');
  await page.getByRole('button', {name: 'Open Settings', exact: true}).scrollIntoViewIfNeeded();
  assert.equal(await page.getByRole('button', {name: 'Open Settings', exact: true}).isVisible(), true);
  await onCase({id: 'reader-display-short-window', locator: panel, data: scroll});
  await page.keyboard.press('Escape');
  await page.evaluate(() => { document.documentElement.dataset.reduceMotion = 'true'; });
  await trigger.click(); await panel.waitFor();
  const motion = await panel.evaluate(element => element.getAnimations().map(animation => ({duration: animation.effect.getTiming().duration, transforms: animation.effect.getKeyframes().map(frame => frame.transform)})));
  assert.ok(motion.length > 0, 'observe the actual reduced-motion entry animation');
  for (const animation of motion) { assert.equal(animation.duration, 160); assert.ok(animation.transforms.every(value => value === 'none')); }
  await page.evaluate(() => window.disposeReaderDisplay());
  const final = await page.evaluate(() => window.readerDisplayProof);
  assert.deepEqual(final.cleanup, {listeners: 0, DOM: 0, fontFaces: 0});
  await checkReaderFirstRunHint(page, origin, onCase);
  return {callbacks, scroll, motion, final, scope: 'controlled-components-only; no canonical Reading15/public Read/full frame acceptance'};
}


/** Actual focus/inert/keyboard behavior of the first-run modal over the reader. */
export async function checkReaderFirstRunHint(page, origin, onCase = async () => {}) {
  await page.setViewportSize({width: 390, height: 844});
  await page.goto(`${origin}/test/read/display.html?presentation=phone&hint=1`, {waitUntil: 'networkidle'});
  const hint = page.locator('[data-reader-hint]'), done = hint.getByRole('button');
  await hint.waitFor();
  assert.equal(await hint.getAttribute('aria-modal'), 'true');
  assert.equal(await done.evaluate(element => element === document.activeElement), true);
  for (const key of ['Tab', 'Shift+Tab', 'ArrowRight', 'ArrowLeft']) {
    await page.keyboard.press(key);
    assert.equal(await done.evaluate(element => element === document.activeElement), true);
  }
  assert.equal(await page.locator('[data-reader-surface]').evaluate(element => element.inert), true);
  await page.evaluate(() => {
    window.readerDisplayInput({kind: 'tap', fraction: .9});
    window.readerDisplayInput({kind: 'swipe', direction: 'left'});
    window.readerDisplayInput({kind: 'key', key: 'ArrowRight', shift: false, alt: false, ctrl: false, meta: false, editable: false, preventDefault() {}});
    document.querySelector('[data-reader-root]').dispatchEvent(new KeyboardEvent('keydown', {key: 'ArrowRight', bubbles: true}));
  });
  assert.deepEqual(await page.evaluate(() => window.readerDisplayProof.turns), []);
  await onCase({id: 'reader-first-hint-modal', locator: hint});
  await done.click();
  await hint.waitFor({state: 'detached'});
  await page.waitForFunction(() => document.activeElement?.hasAttribute('data-reader-root'));
  assert.equal(await page.locator('[data-reader-surface]').evaluate(element => element.inert), false);
  await page.keyboard.press('ArrowRight');
  assert.deepEqual(await page.evaluate(() => window.readerDisplayProof.turns), ['next']);
  await page.evaluate(() => window.disposeReaderDisplay());
}
