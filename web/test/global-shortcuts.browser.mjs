import assert from 'node:assert/strict';

export async function checkGlobalShortcuts(page, origin) {
  await page.goto(`${origin}/dev/kit?family=states&case=shortcuts&capture=true`, {waitUntil: 'networkidle'});
  await page.locator('[data-gallery-ready=true]').waitFor();
  await page.keyboard.press('Escape');
  await page.evaluate(async () => {
    const {mountGlobalShortcuts} = await import('/test/fixtures/global-shortcuts.tsx');
    window.disposeGlobalShortcuts = mountGlobalShortcuts();
  });
  const button = page.locator('[data-shortcut-origin]');
  const dialog = page.getByRole('dialog');
  const open = async () => {await button.focus(); await page.keyboard.press('?'); await dialog.waitFor();};
  const close = async () => {await page.keyboard.press('Escape'); await dialog.waitFor({state:'hidden'}); assert.equal(await button.evaluate(e => e === document.activeElement), true);};
  try {
    await open();
    assert.match(await dialog.innerText(), /Back 7 seconds/);
    assert.match(await dialog.innerText(), /Forward 45 seconds/);
    assert.equal(await page.locator('[data-shortcut-section=anywhere] h3').evaluate(e => e === document.activeElement), true);
    await page.keyboard.press('Tab');
    assert.equal(await dialog.evaluate(e => e.contains(document.activeElement)), true);
    await close();
    await page.getByRole('textbox', {name:'Editable'}).focus();
    await page.keyboard.press('?');
    assert.equal(await dialog.count(), 0);
    for (const kind of ['dialog', 'menu', 'watch']) {
      for (const hidden of [false, true]) {
        await page.evaluate(({kind, hidden}) => {
          const surface = document.createElement('div');
          surface.dataset.shortcutSurface = '';
          if (kind === 'watch') surface.dataset.watchScreen = ''; else surface.setAttribute('role', kind);
          surface.textContent = 'Retained surface';
          surface.hidden = hidden;
          document.body.append(surface);
        }, {kind, hidden});
        await button.focus(); await page.keyboard.press('?');
        if (hidden) {await dialog.waitFor(); await close();}
        else assert.equal(await page.locator('[data-shortcut-section]').count(), 0);
        await page.locator('[data-shortcut-surface]').evaluate(e => e.remove());
      }
    }
    await open(); await close();
    return {actualSkips:true, anywhereFocus:true, tabContained:true, escapeReturnsFocus:true, editableIgnored:true, visibleSurfacesBlock:true, hiddenSurfacesIgnored:true};
  } finally {await page.evaluate(() => window.disposeGlobalShortcuts());}
}
