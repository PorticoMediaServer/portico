import assert from 'node:assert/strict';

/** Actual React/DOM regression for CardFrame's artwork geometry layers: one observer and
 * one layout read per card feed both the auxiliary buttons and the hover shadow, with
 * real product CSS, captions, font/track resizes, retained paint, replacement and unmount.
 * The caller owns the browser and origin; run only this case. */
export async function checkCardGeometry(page, origin) {
  await page.setViewportSize({width: 1024, height: 900});
  await page.goto(`${origin}/test/card-geometry.html`, {waitUntil: 'networkidle'});
  await page.evaluate(async () => {
    const fixture = await import('/test/fixtures/card-geometry.tsx');
    window.disposeCardGeometry = fixture.mountCardGeometry();
  });
  const cards = () => page.evaluate(() => window.cardGeometry.cards());
  const records = () => page.evaluate(() => window.cardGeometry.records());
  const settle = () => page.evaluate(() => window.cardGeometry.settle());
  const expectGeometry = (label, views) => {
    assert.equal(views.length, 2, `${label}: both cards are mounted`);
    for (const [index, view] of views.entries()) {
      assert.equal(view.liveObservers, 1, `${label}: card ${index} runs exactly one live observer`);
      assert.equal(view.observed, true, `${label}: card ${index} observes its primary and every child`);
      assert.equal(view.shadowExact, true, `${label}: card ${index} shadow copies the artwork layout box`);
      assert.equal(view.shadowAligned, true, `${label}: card ${index} shadow paints on the artwork box`);
      assert.equal(view.oneReadPerCallback, true, `${label}: card ${index} takes one computed-style read per callback`);
      if (view.hasAux) {
        assert.equal(view.auxExact, true, `${label}: card ${index} auxiliary copies the artwork layout box`);
        assert.equal(view.auxAligned, true, `${label}: card ${index} auxiliary paints on the artwork box`);
      } else assert.equal(view.aux, null, `${label}: card ${index} has no auxiliary layer to align`);
    }
  };
  try {
    await page.waitForFunction(() => window.cardGeometry?.ready() === true, undefined, {polling: 50});

    // Mount: one observer per card, shared by both geometry layers.
    const mounted = await cards();
    expectGeometry('mount', mounted);
    assert.equal(mounted[0].hasAux, true, 'the interactive card keeps its auxiliary buttons');
    assert.equal(mounted[1].hasAux, false, 'the plain card has only the shadow layer');
    assert.equal(mounted[0].deviceWidth, 190); assert.equal(mounted[1].deviceWidth, 190);
    const created = await records();
    assert.equal(created.length, 2, 'two cards construct two observers, not three');
    assert.equal(await page.evaluate(() => window.cardGeometry.recordIds(0).length), 1, 'the interactive card owns one observer');
    assert.equal(await page.evaluate(() => window.cardGeometry.recordIds(1).length), 1, 'the plain card owns one observer');

    // Caption/font layout: the art really moves inside the primary, and both layers follow exactly.
    const beforeFont = await cards();
    await page.evaluate(() => window.cardGeometry.probe(40)); await settle();
    const font = await cards();
    expectGeometry('font probe', font);
    assert.ok(font[0].deviceTop > beforeFont[0].deviceTop, 'the font probe moves the artwork down inside the primary');
    assert.ok(Number.parseInt(font[0].art.top, 10) > 0, 'the copied offset is the genuine non-zero layout offset');
    assert.equal(font[0].aux?.top, font[0].art.top); assert.equal(font[0].shadow.top, font[0].art.top);

    await page.evaluate(() => window.cardGeometry.caption(26)); await settle();
    const caption = await cards();
    expectGeometry('caption font', caption);
    assert.ok(caption[0].primaryHeight > font[0].primaryHeight, 'the caption font change resizes the primary target');
    assert.equal(caption[0].art.top, font[0].art.top, 'the caption does not move the artwork itself');
    assert.equal(caption[1].primaryHeight > font[1].primaryHeight, true);

    // Width resize: new artwork box, exact copy, and the one read still serves both layers.
    await page.evaluate(() => window.cardGeometry.width(280)); await settle();
    const resized = await cards();
    expectGeometry('width resize', resized);
    assert.equal(resized[0].art.width, '280px'); assert.equal(resized[0].art.height, '420px');
    assert.equal(resized[0].deviceWidth, 280);
    const afterResize = (await records()).filter(record => record.disconnects === 0);
    assert.ok(afterResize.every(record => record.calls > 0), 'each live observer delivered the resize callback');

    // Hover lift: layout geometry stays authoritative, and both layers ride the same transform.
    await page.evaluate(() => window.cardGeometry.scale('1.045')); await settle();
    const lifted = await cards();
    expectGeometry('hover lift', lifted);
    assert.equal(lifted[0].art.width, '280px', 'the copied values stay layout values, never transformed rects');
    assert.equal(lifted[0].aux?.width, '280px');
    await page.evaluate(() => window.cardGeometry.scale('1')); await settle();
    expectGeometry('hover rest', await cards());

    // Auxiliary presence: removing and restoring it re-observes once and never drops the shadow.
    const beforeToggle = await page.evaluate(() => window.cardGeometry.recordIds(0));
    await page.evaluate(() => window.cardGeometry.aux(false)); await settle();
    const plain = await cards();
    assert.equal(plain[0].hasAux, false);
    expectGeometry('aux removed', plain);
    const afterToggle = await page.evaluate(() => window.cardGeometry.recordIds(0));
    assert.equal(afterToggle.length, 1); assert.notEqual(afterToggle[0], beforeToggle[0], 'the replaced layout effect retires its observer and observes once more');
    assert.equal((await records()).find(record => record.id === beforeToggle[0]).disconnects, 1, 'the retired observer is disconnected');
    await page.evaluate(() => window.cardGeometry.aux(true)); await settle();
    expectGeometry('aux restored', await cards());

    // A retained-paint lock re-renders the card but must not create or re-arm an observer.
    await page.evaluate(() => window.cardGeometry.retained(true)); await settle();
    const held = (await records()).map(record => ({id: record.id, targets: record.targets, disconnects: record.disconnects}));
    await page.evaluate(() => window.cardGeometry.rerender()); await settle();
    const afterHeldRerender = (await records()).map(record => ({id: record.id, targets: record.targets, disconnects: record.disconnects}));
    assert.deepEqual(afterHeldRerender, held, 'a retained-paint commit neither constructs nor re-observes');
    expectGeometry('retained paint', await cards());
    await page.evaluate(() => window.cardGeometry.retained(false));

    // Focus, selection and actions stay wired to their own targets.
    await page.evaluate(() => window.cardGeometry.focus());
    assert.equal(await page.evaluate(() => window.cardGeometry.activeIsPrimary()), true, 'the primary button still takes focus');
    await page.evaluate(() => window.cardGeometry.clickPrimary());
    let calls = await page.evaluate(() => window.cardGeometry.calls());
    assert.equal(calls.navigate, 1, 'the primary activation navigates once');
    const more = await page.evaluate(() => window.cardGeometry.clickMore());
    assert.deepEqual(more, {inside: false, expanded: null}, 'the More control stays outside the primary button');
    calls = await page.evaluate(() => window.cardGeometry.calls());
    assert.equal(calls.more, 1); assert.equal(calls.lastMore, true, 'More receives its own anchor');
    const selected = await page.evaluate(() => window.cardGeometry.clickSelection());
    assert.deepEqual(selected, {inside: false, selected: 'false'}, 'the selection control stays outside the primary button');
    calls = await page.evaluate(() => window.cardGeometry.calls());
    assert.equal(calls.toggle, 1, 'the selection control toggles once');
    assert.equal(calls.navigate, 1, 'selecting does not navigate');
    assert.deepEqual(calls.lastToggle, {range: false, source: 'keyboard'});

    // A real replacement: the retired callback must never rewrite the new card.
    const staleId = (await page.evaluate(() => window.cardGeometry.recordIds(1)))[0];
    await page.evaluate(() => window.cardGeometry.replace(300)); await settle();
    assert.equal(await page.evaluate(() => window.cardGeometry.secondConnected()), true);
    const replaced = await cards();
    expectGeometry('replacement', replaced);
    assert.equal(replaced[1].art.width, '300px', 'the replacement has its own geometry');
    assert.equal((await records()).find(record => record.id === staleId).disconnects, 1, 'the replaced card disconnects its original observer');
    const fresh = await page.evaluate(() => window.cardGeometry.recordIds(1));
    assert.equal(fresh.length, 1); assert.notEqual(fresh[0], staleId);
    const beforeLate = await page.evaluate(() => window.cardGeometry.secondStyles());
    await page.evaluate(id => window.cardGeometry.invokeRecord(id), staleId);
    await settle();
    const afterLate = await page.evaluate(() => window.cardGeometry.secondStyles());
    assert.deepEqual(afterLate, beforeLate, 'a late callback cannot mutate the replacement');
    assert.equal(afterLate.art.width, '300px');

    // Unmount: every observer this page constructed is disconnected.
    assert.equal((await records()).filter(record => record.disconnects === 0).length, 2);
    await page.evaluate(() => window.disposeCardGeometry());
    const closed = await page.evaluate(() => window.cardGeometryClosed);
    assert.ok(closed.length >= 4, 'every observer the fixture constructed is accounted for');
    assert.ok(closed.every(record => record.disconnects >= 1), 'unmount disconnects every observer');
    assert.equal(await page.evaluate(() => typeof window.cardGeometry), 'undefined');
  } finally {
    await page.evaluate(() => window.disposeCardGeometry?.());
  }
}
