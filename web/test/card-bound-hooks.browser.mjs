import assert from 'node:assert/strict';

// Focused source only: the caller supplies and owns the browser/origin.
export async function checkBoundCardHooks(page, origin) {
  await page.goto(`${origin}/dev/kit?family=cards&case=reference&capture=true`, {waitUntil: 'networkidle'});
  await page.evaluate(async () => {
    const windowAdd = window.addEventListener, documentAdd = document.addEventListener, windowRemove = window.removeEventListener, documentRemove = document.removeEventListener, captured = [];
    window.addEventListener = function(type, fn, options) {if (['blur', 'scroll', 'keydown', 'keyup'].includes(type) && typeof fn === 'function') captured.push({type, fn, target: 'window', removed: false}); return windowAdd.call(this, type, fn, options);};
    document.addEventListener = function(type, fn, options) {if (type === 'visibilitychange' && typeof fn === 'function') captured.push({type, fn, target: 'document', removed: false}); return documentAdd.call(this, type, fn, options);};
    window.removeEventListener = function(type, fn, options) {for (const item of captured) if (item.target === 'window' && item.type === type && item.fn === fn) item.removed = true; return windowRemove.call(this, type, fn, options);};
    document.removeEventListener = function(type, fn, options) {for (const item of captured) if (item.target === 'document' && item.type === type && item.fn === fn) item.removed = true; return documentRemove.call(this, type, fn, options);};
    const m = await import('/test/fixtures/card-bound-hooks.tsx'); window.disposeCardBoundHooks = m.mountCardBoundHooks();
    window.cardCapturedListeners = captured; window.restoreCardListeners = () => {window.addEventListener = windowAdd; document.addEventListener = documentAdd; window.removeEventListener = windowRemove; document.removeEventListener = documentRemove;};
  });
  try {
    const mountedRenders = await page.evaluate(() => window.cardBoundHooks.renders());
    assert.ok(mountedRenders < 20, 'volatile legacy callback settles at mount');
    await page.evaluate(() => window.cardBoundHooks.publishLegacy());
    assert.ok((await page.evaluate(() => window.cardBoundHooks.renders())) - mountedRenders < 10, 'ordinary legacy publication does not remint repeatedly');
    await page.evaluate(() => {
      window.cardBoundHooks.capture();
      const timer = window.setTimeout, raf = window.requestAnimationFrame;
      const callbacks = {timer: [], raf: []};
      window.setTimeout = (fn, delay, ...args) => {if (delay === 500 && typeof fn === 'function') callbacks.timer.push(() => fn(...args)); return timer(fn, delay, ...args);};
      window.requestAnimationFrame = fn => {callbacks.raf.push(fn); return raf(fn);};
      window.cardBoundHooks.current().down(); window.cardBoundHooks.current().move();
      window.setTimeout = timer; window.requestAnimationFrame = raf; window.cardOldScheduled = callbacks;
      window.cardBoundHooks.retained(true);
      for (const fn of callbacks.timer) fn(); for (const fn of callbacks.raf) fn(performance.now());
    });
    const immediate = await page.evaluate(() => {window.restoreCardListeners(); window.cardOldListeners = [...window.cardCapturedListeners]; return window.cardBoundHooks.observations();});
    assert.equal(immediate.preview.at(-1), false, 'retained withdrawal clears owned preview synchronously');
    assert.equal(await page.evaluate(() => window.cardOldListeners.every(item => item.removed)), true, 'original listeners removed synchronously');
    await page.waitForTimeout(550); assert.equal((await page.evaluate(() => window.cardBoundHooks.observations())).toggles, 0);
    await page.evaluate(() => {window.cardBoundHooks.revive(); window.cardBoundHooks.capture(); window.cardBoundHooks.current().down(); window.cardBoundHooks.current().move();
      for (const fn of window.cardOldScheduled.timer) fn(); for (const fn of window.cardOldScheduled.raf) fn(performance.now());
      for (const {type, fn} of window.cardOldListeners) fn(type.startsWith('key') ? new KeyboardEvent(type, {key: 'Shift'}) : new Event(type));});
    const first = await page.evaluate(() => window.cardBoundHooks.observations());
    assert.equal(first.preview.at(-1), true); assert.ok(first.publications > 0);
    await page.waitForTimeout(550);
    const published = await page.evaluate(() => window.cardBoundHooks.observations());
    assert.equal(published.toggles, 1, 'volatile descriptor publication preserves original hold');
    assert.equal(published.preview.at(-1), true, 'publication does not clear live preview');
    assert.equal(await page.evaluate(() => window.cardBoundHooks.old().consume()), false, 'retired hold admission cannot consume successor suppression');
    assert.equal(await page.evaluate(() => window.cardBoundHooks.current().consume()), true, 'same receipt owns fired hold suppression');
    await page.evaluate(() => window.cardBoundHooks.retire());
    const retired = await page.evaluate(() => {const h = window.cardBoundHooks, before = h.observations(); h.old().move(); h.old().key(); h.old().down(); return {before, after: h.observations(), consumed: h.old().consume()};});
    assert.deepEqual(retired.after, retired.before); assert.equal(retired.consumed, false); assert.equal(retired.before.preview.at(-1), false);
    await page.evaluate(() => {window.cardBoundHooks.revive(); window.cardBoundHooks.current().move();});
    await page.evaluate(() => window.dispatchEvent(new KeyboardEvent('keyup', {key: 'Shift'})));
    assert.equal((await page.evaluate(() => window.cardBoundHooks.observations())).preview.at(-1), false);
    await page.evaluate(() => {window.cardBoundHooks.current().move(); window.cardBoundHooks.missing(true);});
    assert.equal((await page.evaluate(() => window.cardBoundHooks.observations())).preview.at(-1), false, 'missing bound receipt clears its original live preview');
    await page.evaluate(() => {window.cardBoundHooks.missing(false); window.cardBoundHooks.current().move(); window.cardBoundHooks.previewActive(false);});
    assert.equal((await page.evaluate(() => window.cardBoundHooks.observations())).preview.at(-1), false, 'inactive selection clears original preview');
    await page.evaluate(() => {window.cardBoundHooks.previewActive(true); window.cardBoundHooks.legacyMove();});
    const beforeLegacy = await page.evaluate(() => window.cardBoundHooks.renders());
    await page.evaluate(() => window.dispatchEvent(new KeyboardEvent('keyup', {key: 'Shift'})));
    assert.equal((await page.evaluate(() => window.cardBoundHooks.legacyEvents())).at(-1), false, 'volatile legacy callback renewal retains Shift keyup');
    assert.ok((await page.evaluate(() => window.cardBoundHooks.renders())) - beforeLegacy < 10, 'legacy preview publication settles');
    const beforeReentrant = await page.evaluate(() => {
      window.cardBoundHooks.holdActive(false); window.cardBoundHooks.capture();
      const oldReceipt = window.cardBoundHooks.receipt();
      window.cardBoundHooks.current().down(); window.cardBoundHooks.armReentrant(); window.cardBoundHooks.current().key();
      const newReceipt = window.cardBoundHooks.receipt();
      window.cardBoundHooks.current().down(); window.cardBoundHooks.current().move();
      window.cardBoundHooks.old().down(); window.cardBoundHooks.old().key();
      for (const fn of window.cardOldScheduled.timer) fn(); for (const fn of window.cardOldScheduled.raf) fn(performance.now());
      for (const {type, fn} of window.cardOldListeners) fn(type.startsWith('key') ? new KeyboardEvent(type, {key: 'Shift'}) : new Event(type));
      return {changed: !!oldReceipt && !!newReceipt && oldReceipt !== newReceipt && !oldReceipt.live, count: window.cardBoundHooks.reentrantCount(), observations: window.cardBoundHooks.observations()};
    });
    assert.equal(beforeReentrant.changed, true, 'source current synchronously publishes a fresh receipt');
    assert.equal(beforeReentrant.count, 1); assert.equal(beforeReentrant.observations.preview.at(-1), true);
    await page.waitForTimeout(550);
    assert.equal((await page.evaluate(() => window.cardBoundHooks.observations())).toggles, 2, 'old work cannot cancel reentrant successor hold');
    assert.equal(await page.evaluate(() => window.cardBoundHooks.old().consume()), false);
    assert.equal(await page.evaluate(() => window.cardBoundHooks.current().consume()), true);
    await page.evaluate(() => {window.cardBoundHooks.missing(true); window.cardBoundHooks.current().down(); window.cardBoundHooks.current().move();});
    const missing = await page.evaluate(() => window.cardBoundHooks.observations());
    await page.waitForTimeout(550); assert.deepEqual(await page.evaluate(() => window.cardBoundHooks.observations()), missing);
  } finally {await page.evaluate(() => {window.restoreCardListeners?.(); window.disposeCardBoundHooks?.();});}
}
