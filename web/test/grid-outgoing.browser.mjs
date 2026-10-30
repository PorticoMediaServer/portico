import assert from 'node:assert/strict';

/** UNRUN actual React/DOM/collection cases. No elapsed-time, I/O or playback qualification. */
export async function checkGridOutgoing(page, origin) {
  const mount = async () => {
    await page.evaluate(() => window.disposeGridOutgoing?.());
    await page.goto(`${origin}/dev/kit?family=states&case=shortcuts&capture=true`, {waitUntil: 'networkidle'});
    await page.evaluate(async () => {const {mountGridOutgoingHarness} = await import('/test/fixtures/grid-outgoing.tsx');window.disposeGridOutgoing = mountGridOutgoingHarness();});
    await page.locator('[data-original-card="a-0"]').waitFor();
    await page.waitForFunction(() => document.querySelector('[data-grid-ready]')?.getAttribute('data-grid-ready') === 'true');
  };
  const replace = async ids => {
    const generation = await page.evaluate(() => window.gridOutgoingHarness.generation());
    await page.evaluate(values => window.gridOutgoingHarness.replace(values), ids);
    await page.waitForFunction(previous => Number(document.querySelector('[data-grid-generation]')?.getAttribute('data-grid-generation')) > previous
      && document.querySelector('[data-grid-ready]')?.getAttribute('data-grid-ready') === 'true', generation);
  };
  const ids = (prefix, count) => Array.from({length: count}, (_, i) => `${prefix}-${i}`);
  try {
    // Visible mounted outgoing slots are bounded across overlapping accepted models, live slots are not.
    await mount();await replace(ids('b', 100));
    await page.waitForFunction(() => document.querySelectorAll('[data-grid-leaving]').length > 0);
    assert.ok(await page.locator('[data-grid-leaving]').count() <= 40);
    assert.ok(await page.locator('[data-original-card^="b-"]').count() > 40);
    await replace(ids('c', 100));assert.ok(await page.locator('[data-grid-leaving]').count() <= 40);

    // Original outgoing components stay mounted; their original hold and action admission retires.
    await mount();await page.locator('[data-original-card="a-0"] button').first().dispatchEvent('pointerdown', {isPrimary: true, pointerId: 7, pointerType: 'touch', button: 0, clientX: 20, clientY: 40});
    await replace(ids('b', 100));
    assert.equal(await page.evaluate(() => window.gridOutgoingHarness.mounted('a-0')), 1);
    assert.equal(await page.evaluate(() => window.gridOutgoingHarness.retired('a-0')), 0);
    await page.locator('[data-original-card="a-0"] button').first().evaluate(node => node.click());
    assert.equal(await page.evaluate(() => window.gridOutgoingHarness.navigations()), 0);
    await page.waitForTimeout(600);assert.equal(await page.evaluate(() => window.gridOutgoingHarness.toggles()), 0);

    // The original sparse root survives an empty accepted answer while visible outgoing paint retires.
    await mount();await page.evaluate(() => {window.__originalSparseRoot = document.querySelector('[aria-label="Sparse fixture"]');});
    await replace([]);assert.equal(await page.evaluate(() => window.__originalSparseRoot === document.querySelector('[aria-label="Sparse fixture"]')), true);
    assert.equal(await page.locator('[data-grid-empty]').count(), 0);assert.ok(await page.locator('[data-grid-leaving]').count() > 0);
    await page.waitForFunction(() => document.querySelectorAll('[data-grid-leaving]').length === 0);
    await page.locator('[data-grid-empty]').waitFor();

    // A real bound hovered receipt is cleared before the old physical slot is inert.
    await mount();await page.evaluate(() => window.gridOutgoingHarness.selecting(true));
    await page.waitForFunction(() => document.querySelector('[data-original-card="a-0"] button')?.hasAttribute('aria-pressed') === true);
    await page.locator('[data-original-card="a-0"] article').dispatchEvent('pointermove', {pointerType: 'mouse', shiftKey: true});
    assert.ok(await page.evaluate(() => window.gridOutgoingHarness.previews()) > 0);
    await replace(ids('b', 100));
    assert.equal((await page.evaluate(() => window.gridOutgoingHarness.clearBeforeInert())).at(-1), false);

    // Source replacement retires old paint immediately instead of retaining it under a new collection.
    await mount();await replace(ids('b', 100));await page.evaluate(() => window.gridOutgoingHarness.replaceSource());
    await page.waitForFunction(() => document.querySelector('[data-grid-source]')?.getAttribute('data-grid-source') === '1'
      && document.querySelector('[data-grid-ready]')?.getAttribute('data-grid-ready') === 'true');
    await page.locator('[data-original-card="a-0"]').waitFor();assert.equal(await page.locator('[data-grid-leaving]').count(), 0);

    // Pure viewport scrolling is paging, not an outgoing cohort.
    await mount();await page.locator('[aria-label="Sparse fixture"]').evaluate(node => {node.scrollTop = 250;node.dispatchEvent(new Event('scroll'));});
    await page.waitForTimeout(50);assert.equal(await page.locator('[data-grid-leaving]').count(), 0);

    // Existing before-mutation focus remains before the retained row receives inert.
    await mount();await page.locator('[data-original-card="a-0"] button').first().focus();await page.keyboard.press('Shift');
    await replace(Array.from({length: 99}, (_, i) => `a-${i + 1}`));
    assert.equal(await page.locator('[data-original-card="a-1"]').evaluate(node => node.contains(document.activeElement)), true);
    assert.equal((await page.evaluate(() => window.gridOutgoingHarness.focusBeforeInert())).at(-1), false);

    // An unrelated state publication must not renew a live slot's already admitted layout move.
    await mount();await replace(Array.from({length: 96}, (_, i) => `a-${i + 4}`));
    await page.evaluate(() => {
      const slot = document.querySelector('[data-item-id="a-4"]');
      window.__originalGridMove = slot.getAnimations().find(job => job.effect?.getTiming().duration === 320);
      window.__originalGridMove?.pause();
    });
    assert.equal(await page.evaluate(() => !!window.__originalGridMove), true);
    await page.evaluate(() => window.gridOutgoingHarness.publish());
    await page.waitForFunction(() => document.querySelector('[data-original-card="a-4"]')?.getAttribute('data-publication') === '1');
    assert.equal(await page.locator('[data-item-id="a-4"]').evaluate(node => node.getAnimations().includes(window.__originalGridMove)), true);

    // Actual incoming→exit→returning paint stays with the same node and takes
    // its current opacity; a queued old completion cannot write through it.
    await mount();await replace(ids('b', 100));
    await page.evaluate(() => {
      const node = document.querySelector('[data-item-id="b-0"]');
      const job = node.getAnimations().find(animation => animation.effect?.getKeyframes().some(frame => frame.opacity !== undefined));
      if (!job) throw new Error('Actual incoming opacity job missing');
      job.pause();job.currentTime = 60;
      window.__originalGridPaint = {node, job, finish: job.onfinish, opacity: Number(getComputedStyle(node).opacity), inline: node.style.opacity};
    });
    // Let the prior A cohort retire while the real B arrival remains paused;
    // otherwise its shared40 budget correctly refuses another outgoing cohort.
    await page.waitForFunction(() => document.querySelectorAll('[data-grid-leaving]').length === 0);
    await replace(ids('c', 100));
    const exit = await page.evaluate(() => {
      const old = window.__originalGridPaint;
      const job = old.node.getAnimations().find(animation => animation.effect?.getKeyframes().at(-1)?.opacity === '0' || animation.effect?.getKeyframes().at(-1)?.opacity === 0);
      if (!job) throw new Error('Actual outgoing opacity job missing');
      job.pause();job.currentTime = 40;
      const first = Number(job.effect.getKeyframes()[0].opacity);
      old.finish?.call(old.job, new Event('finish'));
      const result = {same: document.querySelector('[data-item-id="b-0"]') === old.node, first, original: old.opacity, inlineUnchanged: old.inline === old.node.style.opacity};
      old.opacity = Number(getComputedStyle(old.node).opacity);return result;
    });
    assert.equal(exit.same, true);assert.ok(Math.abs(exit.first - exit.original) < .02);assert.equal(exit.inlineUnchanged, true);
    await replace(ids('b', 100));
    const returned = await page.evaluate(() => {
      const old = window.__originalGridPaint, node = document.querySelector('[data-item-id="b-0"]');
      const job = node.getAnimations().find(animation => {
        const last = animation.effect?.getKeyframes().at(-1)?.opacity;return last === '1' || last === 1;
      });
      if (!job) throw new Error('Actual returning opacity job missing');
      return {same: node === old.node, first: Number(job.effect.getKeyframes()[0].opacity), original: old.opacity};
    });
    assert.equal(returned.same, true);assert.ok(Math.abs(returned.first - returned.original) < .02);
  } finally {await page.evaluate(() => {window.disposeGridOutgoing?.();window.disposeGridOutgoing = undefined;delete window.__originalSparseRoot;delete window.__originalGridMove;delete window.__originalGridPaint;});}
}
