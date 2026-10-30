import assert from 'node:assert/strict';

// Caller owns the local Vite server and browser lifetime. The fixture mounts
// real components; engine iframe producers and native guest assets need a
// separate full-reader/device run after the reviewer rebuilds them.
export async function checkReaderAccessibility(page, origin) {
  assert.ok(['localhost', '127.0.0.1', '[::1]'].includes(new URL(origin).hostname));
  const errors = [];
  const onError = error => errors.push(String(error));
  page.on('pageerror', onError);
  try {
    await page.setViewportSize({width: 1280, height: 800});
    await page.goto(`${origin}/test/read/accessibility.html`, {waitUntil: 'networkidle'});
    const root = page.locator('[data-reader-root]'), back = page.getByRole('button', {name: 'Back', exact: true});
    const waitChrome = state => page.waitForFunction(state => document.querySelector('[data-reader-root]')?.getAttribute('data-chrome') === state, state);
    const hide = async () => {await root.focus(); await page.keyboard.press('PageDown'); await waitChrome('hidden');};
    for (const key of ['Tab', 'Shift+Tab', 'Escape']) {
      await hide(); await page.keyboard.press(key); await waitChrome('shown');
      assert.equal(await back.evaluate(element => document.activeElement === element), true, `${key} enters a real control after inert is removed`);
      assert.equal(await back.evaluate(element => !!element.closest('[inert],[aria-hidden="true"]')), false);
      await page.evaluate(() => window.readerAccessibility.read());
      assert.equal(await root.getAttribute('data-chrome'), 'shown', 'reading activity cannot hide focused controls');
    }
    await page.keyboard.press('Tab');
    assert.equal(await page.getByRole('button', {name: 'Contents', exact: true}).evaluate(element => document.activeElement === element), true, 'ordinary Tab advances after reveal');
    for (const key of ['Tab', 'Escape']) {
      await hide();
      const prevented = await page.evaluate(key => {
        let prevented = false;
        window.readerAccessibility.input({kind: 'key', key, shift: key === 'Tab', alt: false, ctrl: false, meta: false, editable: false, preventDefault() {prevented = true;}});
        return prevented;
      }, key);
      assert.equal(prevented, true); await waitChrome('shown');
      assert.equal(await back.evaluate(element => document.activeElement === element), true, 'the iframe/native surface-input path also restores controls');
    }
    await hide();
    const show = page.getByRole('button', {name: 'Show controls', exact: true});
    assert.equal(await show.evaluate(element => !!element.closest('[inert],[aria-hidden="true"]')), false);
    await show.focus(); await page.keyboard.press('Enter'); await waitChrome('shown');
    assert.equal(await back.evaluate(element => document.activeElement === element), true);

    await page.setViewportSize({width: 390, height: 844});
    for (const name of ['Contents', 'Search', 'Display']) {
      const trigger = page.getByRole('button', {name, exact: true});
      await trigger.click();
      const dialog = page.getByRole('dialog', {name, exact: true}); await dialog.waitFor();
      const close = dialog.getByRole('button', {name: 'Close', exact: true});
      assert.equal(await close.evaluate(element => getComputedStyle(element).clipPath), 'inset(50%)', 'the approved sheet gains no visible close button');
      await close.focus(); await page.keyboard.press('Enter');
      await dialog.waitFor({state: 'detached'});
      assert.equal(await trigger.evaluate(element => document.activeElement === element), true, `${name} returns to its exact opener`);
    }

    await page.goto(`${origin}/test/read/accessibility.html?surface=book`, {waitUntil: 'networkidle'});
    const contents = page.getByRole('tab', {name: 'Contents', exact: true}), details = page.getByRole('tab', {name: 'Details', exact: true});
    for (const width of [390, 820]) {
      await page.setViewportSize({width, height: 1180});
      await contents.click(); assert.equal(await contents.getAttribute('aria-selected'), 'true');
      assert.equal(await page.getByText('EPUB', {exact: true}).count(), 0);
      await contents.focus(); await page.keyboard.press('ArrowRight');
      await page.waitForFunction(() => document.querySelector('[role="tab"][aria-selected="true"]')?.textContent === 'Details');
      assert.equal(await page.getByText('EPUB', {exact: true}).isVisible(), true);
      assert.equal(await page.getByRole('button', {name: 'Chapter one', exact: true}).count(), 0);
      const panel = page.getByRole('tabpanel', {name: 'Details', exact: true});
      assert.equal(await panel.getAttribute('id'), await details.getAttribute('aria-controls'));
      assert.equal(await panel.getAttribute('aria-labelledby'), await details.getAttribute('id'));
    }
    await page.setViewportSize({width: 1440, height: 1000});
    assert.equal(await page.getByRole('tab').count(), 0);
    assert.equal(await page.getByText('EPUB', {exact: true}).isVisible(), true);
    assert.equal(await page.getByRole('button', {name: 'Chapter one', exact: true}).isVisible(), true);
    for (const [title, desktopSize, phoneSize] of [['', '92px', '46px'], ['long', '72px', '34px'], ['short', '104px', '46px']]) {
      await page.goto(`${origin}/test/read/accessibility.html?surface=book&title=${title}`, {waitUntil: 'networkidle'});
      assert.equal(await page.getByRole('heading', {level: 1}).evaluate(element => getComputedStyle(element).fontSize), desktopSize);
      await page.setViewportSize({width: 390, height: 844});
      assert.equal(await page.getByRole('heading', {level: 1}).evaluate(element => getComputedStyle(element).fontSize), phoneSize);
      await page.setViewportSize({width: 1440, height: 1000});
    }
    assert.deepEqual(errors, []);
    return {scope: 'ReaderChrome recovery, phone sheet close/return, book tabs and title treatments; controlled components only'};
  } finally {page.off('pageerror', onError);}
}
