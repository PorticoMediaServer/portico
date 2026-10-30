import assert from 'node:assert/strict';

// Source only: caller owns the page, origin and all browser/server lifecycle.
export async function checkFixedPreviewLifecycle(page, origin) {
  await page.goto(`${origin}/dev/kit?family=cards&case=reference&capture=true`, {waitUntil: 'networkidle'});
  await page.evaluate(async () => {
    const fixture = await import('/test/fixtures/fixed-preview-lifecycle.tsx');
    window.disposeFixedPreviewLifecycle = fixture.mountFixedPreviewLifecycle();
  });
  const observe = () => page.evaluate(() => window.fixedPreviewLifecycle.observations());
  const waitReads = count => page.waitForFunction(n => window.fixedPreviewLifecycle?.observations().reads.length >= n, count);
  try {
    await waitReads(11);
    await page.waitForFunction(() => window.fixedPreviewLifecycle.observations().created.length === 11);
    const initial = await observe();
    assert.deepEqual(initial.reads.map(r => r.index).sort((a, b) => a - b), [5,6,7,8,9,10,11,12,13,14,15]);
    assert.ok(initial.reads.every(r => r.width === 96));
    await page.evaluate(() => window.fixedPreviewLifecycle.rerender());
    const host = page.locator('[data-fixed-preview-fixture]');
    const search = host.getByRole('button', {name: /search/i}).first();
    await search.click();
    const query = host.getByRole('searchbox').first();
    await query.fill('fixture');
    const unchanged = await observe();
    assert.equal(unchanged.reads.length, 11); assert.equal(unchanged.revoked.length, 0);
    await page.evaluate(() => window.fixedPreviewLifecycle.advance(11));
    await waitReads(12);
    await page.waitForFunction(() => window.fixedPreviewLifecycle.observations().created.length === 12);
    const advanced = await observe();
    assert.equal(advanced.reads.at(-1).index, 16);
    assert.equal(advanced.revoked.length, 1);
    assert.equal(advanced.reads.find(r => r.index === 5).aborted, true);
    assert.ok(advanced.reads.filter(r => r.index >= 6 && r.index <= 15).every(r => !r.aborted));

    await page.evaluate(async () => {window.fixedPreviewLifecycle.defer(17); await window.fixedPreviewLifecycle.advance(12);});
    await waitReads(13);
    const beforeReplace = await observe();
    assert.equal(beforeReplace.reads.at(-1).index, 17);
    await page.evaluate(() => window.fixedPreviewLifecycle.replace());
    await waitReads(24);
    await page.waitForFunction(oldSource => {
      const o = window.fixedPreviewLifecycle.observations();
      return o.source !== oldSource && o.displayed.length === 11 && o.created.length === 23;
    }, beforeReplace.source);
    await page.evaluate(async () => {await window.fixedPreviewLifecycle.reject();});
    const replaced = await observe();
    assert.ok(replaced.reads.filter(r => r.source === beforeReplace.reads[0].source).every(r => r.aborted));
    assert.equal(replaced.errors.length, 0, 'retired request cannot report into successor');
    assert.ok(beforeReplace.created.every(url => replaced.revoked.includes(url)));

    await page.evaluate(async () => {window.fixedPreviewLifecycle.defer(16); await window.fixedPreviewLifecycle.advance(11);});
    await waitReads(25);
    await page.evaluate(async () => {await window.fixedPreviewLifecycle.reject();});
    await page.waitForFunction(() => window.fixedPreviewLifecycle.observations().errors.length === 1);
    const failed = await observe();
    assert.deepEqual(failed.errors, [{source: replaced.source, error: {code: 'invalid_book'}}]);
    await page.evaluate(() => window.fixedPreviewLifecycle.retire());
    await page.waitForFunction(() => {
      const o = window.fixedPreviewLifecycle.observations();
      return o.listeners === 0 && o.created.every(url => o.revoked.includes(url));
    });
    const retired = await observe();
    assert.ok(retired.created.every(url => retired.revoked.filter(value => value === url).length === 1));
  } finally {
    await page.evaluate(() => window.disposeFixedPreviewLifecycle?.());
  }
}
