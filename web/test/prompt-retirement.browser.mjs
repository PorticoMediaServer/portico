import assert from 'node:assert/strict';

/** UNRUN real React/DOM boundary vectors. No fake Session, seek geometry or policy-clock proof. */
export async function checkPromptRetirement(page, origin) {
  const mount = async () => {
    await page.evaluate(() => window.disposePromptRetirement?.());
    await page.goto(`${origin}/dev/kit?family=states&case=shortcuts&capture=true`, {waitUntil: 'networkidle'});
    await page.evaluate(async () => {const {mountPromptRetirementHarness} = await import('/test/fixtures/prompt-retirement.tsx');window.disposePromptRetirement = mountPromptRetirementHarness();});
    await page.locator('[data-prompt-action]').waitFor();
    await page.waitForFunction(() => !!window.promptRetirementHarness);
  };
  const keyboard = async () => {
    await page.locator('[data-prompt-seek]').focus();await page.keyboard.press('Tab');
    assert.equal(await page.locator('[data-prompt-action]').evaluate(node => node === document.activeElement), true);
  };
  const change = async method => {
    const before = await page.evaluate(() => window.promptRetirementHarness.committed());
    await page.evaluate(name => window.promptRetirementHarness[name](), method);
    await page.waitForFunction(count => window.promptRetirementHarness.committed() > count, before);
  };
  const observations = () => page.evaluate(() => window.promptRetirementHarness.observations());
  const focused = selector => page.locator(selector).evaluate(node => node === document.activeElement);
  try {
    // Placement consumes the actual host render's controls state, not a stale outer fallback.
    await mount();assert.equal(await page.locator('[data-prompt-controls]').getAttribute('data-prompt-controls'), 'true');

    // The actual focus event observes OLD non-inert DOM before React applies out/inert.
    await mount();await keyboard();await change('withdraw');
    assert.equal(await focused('[data-prompt-seek]'), true);
    assert.deepEqual((await observations()).at(-1), {target: 'seek', promptInert: false, promptPhase: 'in'});
    assert.equal(await page.locator('[data-prompt-fixture]').evaluate(node => node.inert), true);

    // A new offer does not lend the old focused button to the replacement identity.
    await mount();await keyboard();await change('replaceOffer');assert.equal(await focused('[data-prompt-seek]'), true);
    assert.equal((await observations()).at(-1).promptInert, false);

    // Pointer withdrawal must not steal focus into the seek control.
    await mount();await page.locator('[data-prompt-action]').click();await change('withdraw');
    assert.equal(await focused('[data-prompt-seek]'), false);

    // A range removed in this commit is not a survivor; the original region is the fallback.
    await mount();await keyboard();await change('removeSeekAndWithdraw');
    assert.equal(await focused('[data-prompt-region]'), true);
    assert.deepEqual((await observations()).at(-1), {target: 'region', promptInert: false, promptPhase: 'in'});

    // A modal/panel context withdrawal does not return through the old player context.
    await mount();await keyboard();await change('blockAndWithdraw');assert.equal(await focused('[data-prompt-seek]'), false);

    // An original source retirement refuses focus, even when old DOM is still connected.
    await mount();await keyboard();await change('retireAndWithdraw');assert.equal(await focused('[data-prompt-seek]'), false);

    // Actual root replacement never targets the replacement root or its new range.
    await mount();await keyboard();await change('replaceRoot');assert.equal(await focused('[data-prompt-seek]'), false);
    assert.equal(await focused('[data-prompt-region]'), false);
  } finally {await page.evaluate(() => {window.disposePromptRetirement?.();window.disposePromptRetirement = undefined;});}
}
