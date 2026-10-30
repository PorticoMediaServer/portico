import assert from 'node:assert/strict';

/** Run with an isolated page; the caller owns server and browser cleanup. */
export async function checkSeriesRules(page, origin) {
  const errors = [];
  page.on('pageerror', error => errors.push(String(error)));
  await page.goto(`${origin}/dev/kit?family=live-rules&case=editor&capture=true`);
  await page.locator('[data-gallery-ready=true]').waitFor();
  const editor = page.getByRole('region', {name: 'Edit rule for The Clockmakers'});
  await editor.waitFor();
  const enabled = editor.getByRole('switch');
  assert.equal(await enabled.isChecked(), true);
  await enabled.click();
  assert.equal(await enabled.isChecked(), false);
  assert.equal(await editor.locator('[role=img][aria-label=Scheduled]').count(), 0);
  await enabled.click();
  await editor.getByRole('radio', {name: 'New episodes', exact: true}).click();
  assert.equal(await editor.locator('[role=img][aria-label=Scheduled]').count(), 1);
  await editor.getByRole('radio', {name: 'All episodes', exact: true}).click();
  assert.equal(await editor.locator('[role=img][aria-label=Scheduled]').count(), 3);
  await editor.getByRole('radio', {name: 'This channel', exact: true}).click();
  assert.equal(await editor.getByRole('radio', {name: 'This channel', exact: true}).getAttribute('aria-checked'), 'true');
  for (const [name, option] of [['Keep', 'Keep the latest 3'], ['Start early', 'None'], ['End late', '2 minutes']]) {
    await editor.getByRole('combobox', {name, exact: true}).click();
    await page.getByRole('option', {name: option, exact: true}).click();
    assert.ok((await editor.getByRole('combobox', {name, exact: true}).textContent()).includes(option));
  }
  await editor.getByRole('button', {name: 'Cancel', exact: true}).click();
  await editor.waitFor({state: 'hidden'});
  assert.ok((await page.evaluate(() => document.activeElement?.textContent)).includes('The Clockmakers'));
  await page.getByRole('button', {name: 'Actions for The Clockmakers'}).click();
  await editor.waitFor();
  assert.ok((await editor.getByRole('combobox', {name: 'Keep', exact: true}).textContent()).includes('Keep every episode'));
  await page.getByRole('button', {name: 'Actions for World Tonight'}).click();
  const world = page.getByRole('region', {name: 'Edit rule for World Tonight'});
  await world.waitFor();
  assert.equal(await world.getByRole('radio', {name: 'This channel', exact: true}).getAttribute('aria-checked'), 'true');
  await world.getByRole('button', {name: 'Cancel', exact: true}).click();
  await world.waitFor({state: 'hidden'});
  assert.ok((await page.evaluate(() => document.activeElement?.textContent)).includes('World Tonight'));
  await page.getByRole('button', {name: 'Actions for Wild Plains'}).click();
  const wild = page.getByRole('region', {name: 'Edit rule for Wild Plains'});
  assert.equal(await wild.getByRole('switch').isChecked(), false);
  await wild.getByRole('switch').click();
  await wild.getByRole('button', {name: 'Save', exact: true}).click();
  await wild.waitFor({state: 'hidden'});
  await page.getByRole('button', {name: 'Actions for Wild Plains'}).click();
  await wild.waitFor();
  assert.equal(await wild.getByRole('switch').isChecked(), true);
  await page.keyboard.press('Escape');
  await wild.waitFor({state: 'hidden'});
  await page.getByRole('button', {name: 'Actions for The Clockmakers'}).click();
  await editor.waitFor();
  await page.keyboard.press('Tab');
  assert.equal(await enabled.evaluate(element => document.activeElement === element), true);
  assert.ok(await enabled.evaluate(element => getComputedStyle(element).outlineStyle !== 'none'));
  await page.keyboard.press('Tab');
  const all = editor.getByRole('radio', {name: 'All episodes', exact: true});
  assert.equal(await all.evaluate(element => document.activeElement === element), true);
  await page.keyboard.press('ArrowLeft');
  assert.equal(await editor.getByRole('radio', {name: 'New episodes', exact: true}).getAttribute('aria-checked'), 'true');
  assert.deepEqual(errors, []);
  return {checks: ['toggle', 'episode and channel choices', 'all selects', 'matching airing count', 'card selection', 'Cancel and focus return', 'Save', 'Escape', 'keyboard order and visible focus', 'arrow selection'], errors};
}
