import assert from 'node:assert/strict';

/**
 * Removal-focus regression source for the shared helper. Authored against the existing gallery
 * harness and the real browser DOM: every assertion drives actual nodes through the production
 * hook and waits for the harness's real readiness (published order after the refs commit), never an
 * assumed commit count. Authored only — not executed.
 */
export async function checkRemovalFocus(page, origin) {
  const mount = async () => {
    await page.evaluate(() => {window.disposeRemovalFocus?.();});
    await page.goto(`${origin}/dev/kit?family=states&case=shortcuts&capture=true`, {waitUntil: 'networkidle'});
    await page.evaluate(async () => {
      const {mountRemovalFocusHarness} = await import('/test/fixtures/removal-focus.tsx');
      window.disposeRemovalFocus = mountRemovalFocusHarness();
    });
    await page.locator('[data-removal-item=alpha]').waitFor();
    await page.waitForFunction(() => document.body.dataset.removalReady === 'true');
  };
  const committed = () => page.evaluate(() => Number(document.body.dataset.removalCommits ?? 0));
  const settle = async action => {
    const before = await committed();
    await action();
    await page.waitForFunction(count => Number(document.body.dataset.removalCommits ?? 0) > count, before);
    await page.waitForFunction(() => document.body.dataset.removalReady === 'true');
  };
  const run = (method, ...args) => settle(() => page.evaluate(({name, values}) => window.removalFocusHarness[name](...values), {name: method, values: args}));
  const remove = id => run('remove', id);
  const removeNow = id => run('removeNow', id);
  const removeBoth = (first, second) => run('removeBoth', first, second);
  const retainBoth = (first, second) => run('retainBoth', first, second);
  const add = id => run('add', id);
  const active = () => page.evaluate(() => {
    const element = document.activeElement;
    if (!(element instanceof HTMLElement)) return null;
    if (element.hasAttribute('data-primary')) return {kind: 'primary', id: element.textContent};
    if (element.hasAttribute('data-secondary')) return {kind: 'secondary', id: element.textContent.replace('open ', '')};
    if (element.hasAttribute('data-section-start')) return {kind: 'section', id: 'section'};
    return {kind: 'other', id: element.tagName.toLowerCase()};
  });
  // Keyboard entry: Tab from the section start until the requested item's primary owns focus.
  const keyboardTo = async id => {
    await page.locator('[data-section-start]').focus();
    for (let step = 0; step < 10; step++) {
      const now = await active();
      if (now?.kind === 'primary' && now.id === id) return;
      await page.keyboard.press('Tab');
    }
    throw new Error(`keyboard never reached ${id}`);
  };

  try {
    // next eligible
    await mount();
    await keyboardTo('bravo');
    await removeNow('bravo');
    assert.deepEqual(await active(), {kind: 'primary', id: 'charlie'});

    // previous eligible when nothing follows
    await mount();
    await keyboardTo('delta');
    await removeNow('delta');
    assert.deepEqual(await active(), {kind: 'primary', id: 'charlie'});

    // an inert neighbour is not a target
    await mount();
    await keyboardTo('bravo');
    await page.evaluate(() => document.querySelector('[data-removal-item=charlie]').setAttribute('inert', ''));
    await removeNow('bravo');
    assert.deepEqual(await active(), {kind: 'primary', id: 'delta'});

    // surviving section start when no item survives around it
    await mount();
    await keyboardTo('bravo');
    await removeNow('alpha');
    await removeNow('charlie');
    await removeNow('delta');
    await removeNow('bravo');
    assert.deepEqual(await active(), {kind: 'section', id: 'section'});

    // unrelated removal keeps the focused item
    await mount();
    await keyboardTo('bravo');
    await removeNow('delta');
    assert.deepEqual(await active(), {kind: 'primary', id: 'bravo'});

    // pointer-only focus is never moved
    await mount();
    await page.locator('[data-removal-item=bravo] [data-primary]').click();
    await removeNow('bravo');
    assert.notEqual((await active())?.id, 'charlie');

    // a detached original root yields no target and no throw
    await mount();
    await keyboardTo('bravo');
    await page.evaluate(() => window.removalFocusHarness.detachRoot());
    await removeNow('bravo');
    assert.notEqual((await active())?.id, 'charlie');

    // an added node is published once its ref has run, and its removal is detected
    await mount();
    await keyboardTo('bravo');
    await removeNow('bravo');
    assert.deepEqual(await active(), {kind: 'primary', id: 'charlie'});
    await add('bravo');
    assert.deepEqual(await active(), {kind: 'primary', id: 'charlie'});
    await keyboardTo('bravo');
    await removeNow('bravo');
    assert.deepEqual(await active(), {kind: 'primary', id: 'delta'});

    // simultaneous unrelated + focused removals: the focused row still hands focus on
    await mount();
    await keyboardTo('charlie');
    await removeBoth('alpha', 'charlie');
    assert.deepEqual(await active(), {kind: 'primary', id: 'delta'});

    // the original focused node is retained and drawn inert, not unmounted and re-keyed
    await mount();
    await page.evaluate(() => document.querySelector('[data-removal-item=bravo]').dataset.original = '1');
    await keyboardTo('bravo');
    await remove('bravo');
    assert.deepEqual(await active(), {kind: 'primary', id: 'charlie'});
    const retained = page.locator('[data-removal-item=bravo][data-retained][data-original="1"]');
    assert.equal(await retained.count(), 1);
    assert.equal(await retained.evaluate(element => element.contains(document.activeElement)), false);

    // focused and following neighbour both retained: the connected outgoing neighbour is skipped
    await mount();
    await page.evaluate(() => {for (const id of ['bravo', 'charlie']) document.querySelector(`[data-removal-item=${id}]`).dataset.original = '1';});
    await keyboardTo('bravo');
    await retainBoth('bravo', 'charlie');
    assert.deepEqual(await active(), {kind: 'primary', id: 'delta'});
    assert.equal(await page.locator('[data-removal-item=charlie][data-retained][data-original="1"]').count(), 1);

    // a replacement root under the same ref is refused, and later removals do not steal focus
    await mount();
    await keyboardTo('bravo');
    await run('replaceRoot');
    await removeNow('bravo');
    assert.notEqual((await active())?.id, 'charlie');

    // a stale section start is skipped
    await mount();
    await keyboardTo('bravo');
    await page.evaluate(() => window.removalFocusHarness.detachSection());
    await removeNow('alpha');
    await removeNow('charlie');
    await removeNow('delta');
    await removeNow('bravo');
    assert.notEqual((await active())?.kind, 'section');

    // a hidden first stop resolves to the item's next eligible stop
    await mount();
    await run('hideFirst', 'charlie');
    await keyboardTo('bravo');
    await removeNow('bravo');
    assert.deepEqual(await active(), {kind: 'secondary', id: 'charlie'});
  } finally {
    await page.evaluate(() => {window.disposeRemovalFocus?.();});
  }
}
