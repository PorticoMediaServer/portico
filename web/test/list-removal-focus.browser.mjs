import assert from 'node:assert/strict';

/** UNRUN actual React Shelf/retained presence/Radix portal regression source.
 * No fake session/command or helper-only assertion is presented as consumer qualification. */
export async function checkListRemovalFocus(page, origin) {
  const mount = async () => {
    await page.evaluate(() => window.disposeListRemoval?.());
    await page.goto(`${origin}/dev/kit?family=states&case=shortcuts&capture=true`, {waitUntil: 'networkidle'});
    await page.evaluate(async () => {const {mountListRemovalHarness} = await import('/test/fixtures/list-removal-focus.tsx');window.disposeListRemoval = mountListRemovalHarness();});
    await page.locator('[data-primary=alpha]').waitFor();
    await page.waitForFunction(() => !!window.listRemovalHarness);
  };
  const focus = async selector => {await page.locator(selector).focus();await page.keyboard.press('Shift');};
  const remove = (...ids) => page.evaluate(values => window.listRemovalHarness.remove(...values), ids);
  const primary = id => page.waitForFunction(value => document.activeElement?.getAttribute('data-primary') === value, id);
  try {
    await mount();await focus('[data-primary=alpha]');
    await page.evaluate(() => {
      document.addEventListener('focusin', event => {
        if (event.target instanceof HTMLElement && event.target.dataset.primary === 'bravo')
          document.body.dataset.outgoingWasInert = String(!!document.querySelector('[data-layout-item=alpha]')?.hasAttribute('inert'));
      }, {once: true});
    });
    await remove('alpha');await primary('bravo');
    assert.equal(await page.evaluate(() => document.body.dataset.outgoingWasInert), 'false', 'the actual handoff precedes Shelf inert withdrawal');
    await mount();await focus('[data-primary=charlie]');await remove('bravo','charlie');await primary('alpha');
    await mount();await page.locator('[data-primary=alpha]').click();await remove('alpha');
    assert.notEqual(await page.locator('[data-primary=bravo]').evaluate(node => node === document.activeElement), true, 'pointer removal cannot steal focus');

    await mount();await focus('[data-menu-opener=alpha]');await page.keyboard.press('Enter');
    await page.locator('[data-menu-action=alpha]').waitFor();await focus('[data-menu-action=alpha]');await remove('alpha');await primary('bravo');
    await page.waitForFunction(() => !document.querySelector('[data-menu-action=alpha]'));
    assert.equal(await page.locator('[data-primary=bravo]').evaluate(node => node === document.activeElement), true, 'late portal close cannot restore the outgoing opener');

    await mount();await focus('[data-external-opener=alpha]');await page.keyboard.press('Enter');
    await page.locator('[data-external-action=alpha]').waitFor();await focus('[data-external-action=alpha]');await remove('alpha');await primary('bravo');
    assert.equal(await page.locator('[data-layout-item=alpha]').evaluate(node => node.hasAttribute('inert')), true, 'a sibling portal may unmount before the retained row becomes inert');

    await mount();await focus('[data-dialog-opener=alpha]');await page.keyboard.press('Enter');
    await page.locator('[data-dialog-cancel=alpha]').waitFor();await focus('[data-dialog-cancel=alpha]');await remove('alpha');await primary('bravo');
    assert.equal(await page.locator('[data-primary=bravo]').evaluate(node => !node.closest('[inert],[aria-hidden="true"]')), true, 'modal isolation has really been released');

    await mount();await focus('[data-dialog-opener=alpha]');await page.keyboard.press('Enter');
    await page.locator('[data-dialog-cancel=alpha]').waitFor();await page.evaluate(() => window.listRemovalHarness.replaceScope());
    await page.waitForFunction(() => !document.querySelector('[data-dialog-cancel=alpha]'));
    assert.notEqual(await page.evaluate(() => document.activeElement?.getAttribute('data-dialog-opener')), 'alpha', 'a retired original opening never adopts the replacement scope');
  } finally {await page.evaluate(() => {window.disposeListRemoval?.();delete window.disposeListRemoval;});}
}
