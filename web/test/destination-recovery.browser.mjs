import assert from 'node:assert/strict';

/**
 * FX-156 / BLK-WEB-001 destination recovery regression source. The specimen mounts the real router,
 * ShellRoot/Shell chrome and the real App LastResortBoundary with finite fixture data, and installs
 * faults through the actual route options with exact restoration. The caller supplies the browser.
 * Authored only — not executed.
 *
 * Saved-callback cases use the development-only capture port, which records the exact committed
 * onPress closures that the presenter rendered. Re-appending a saved DOM button is not that capture
 * (React's delegated handler is updated or absent), and no React internals are read. The port only
 * records behind `import.meta.env.DEV`; a production build never executes it and no recovery decision
 * reads it. The focused ChildRouteError receipt test proves the original reset spy stays at zero.
 */
export async function checkDestinationRecovery(page, origin) {
  await page.goto(`${origin}/src/dev/entries/destination-recovery.html`, {waitUntil: 'networkidle'});
  await page.locator('[data-destination-ok]').waitFor();
  const failure = page.getByText("Something didn't load", {exact: true});
  const retry = page.getByRole('button', {name: /Try again/});
  const home = page.getByRole('button', {name: /Go to Home/});
  const shell = () => page.evaluate(() => window.destinationRecovery.shellMarker());
  const href = () => page.evaluate(() => window.destinationRecovery.href());
  try {
    assert.equal(await shell(), true, 'real shell chrome is mounted');

    // Render fault: approved child page inside the real shell, bounded retry, release recovery.
    await page.evaluate(() => {window.destinationRecovery.fault('render'); return window.destinationRecovery.navigate('/search');});
    await failure.waitFor();
    assert.equal(await shell(), true, 'shell chrome survives a destination failure');
    assert.equal(await retry.count(), 1);
    assert.equal(await home.count(), 1);
    await retry.click();
    await failure.waitFor();
    assert.equal(await page.locator('[data-destination-ok]').count(), 0);
    await page.evaluate(() => window.destinationRecovery.fault('none'));
    await retry.click();
    await page.locator('[data-destination-ok]').waitFor();

    // Layout-effect fault takes the same bounded path.
    await page.evaluate(() => {window.destinationRecovery.fault('effect'); return window.destinationRecovery.navigate('/search');});
    await failure.waitFor();
    assert.equal(await shell(), true);
    await page.evaluate(() => window.destinationRecovery.fault('none'));
    await retry.click();
    await page.locator('[data-destination-ok]').waitFor();

    // Stored child validation error: the same bounded child page, and retry after release recovers
    // through the captured child's invalidate + supplied reset (plain reset would repeat the error).
    await page.evaluate(() => {window.destinationRecovery.validate(true); return window.destinationRecovery.navigate('/search?probe=1');});
    await failure.waitFor();
    assert.equal(await shell(), true);
    await page.evaluate(() => window.destinationRecovery.validate(false));
    await retry.click();
    await page.locator('[data-destination-ok]').waitFor();
    assert.equal(await failure.count(), 0);

    // A stored child validation error recovers only through a real Retry (invalidate + reset),
    // never by changing the validator alone.
    await page.evaluate(() => {window.destinationRecovery.validate(true); return window.destinationRecovery.navigate('/search?probe=1');});
    await failure.waitFor();
    await page.evaluate(() => window.destinationRecovery.validate(false));
    await page.waitForTimeout(200);
    assert.equal(await failure.count(), 1, 'changing the validator alone does not reload the stored error');
    await retry.click();
    await page.locator('[data-destination-ok]').waitFor();

    // A successor stored error at the same location and scope: the saved A closures refuse while the
    // successor page's own captured closures act.
    await page.evaluate(() => {window.destinationRecovery.validate(true); return window.destinationRecovery.navigate('/search?probe=2');});
    await failure.waitFor();
    await page.waitForTimeout(100);
    const errorA = await page.evaluate(() => window.destinationRecovery.errorMessage());
    await page.evaluate(() => {window.savedChildA = window.destinationRecoveryActions.latest('child');});
    await page.evaluate(() => window.destinationRecovery.revalidate());
    await page.waitForFunction(message => window.destinationRecovery.errorMessage() !== message, errorA);
    await failure.waitFor();
    await page.waitForTimeout(100);
    await page.evaluate(() => {window.savedChildB = window.destinationRecoveryActions.latest('child'); window.destinationRecovery.validate(false);});
    const successorHref = await href();
    await page.evaluate(() => window.savedChildA.retry());
    await page.waitForTimeout(250);
    assert.equal(await failure.count(), 1, 'a saved Retry refuses after a successor error at the same location');
    assert.equal(await href(), successorHref, 'a saved Retry does not move the successor');
    await page.evaluate(() => window.savedChildA.home());
    await page.waitForTimeout(250);
    assert.equal(await failure.count(), 1, 'a saved Home refuses after a successor error at the same location');
    assert.equal(await href(), successorHref, 'a saved Home does not navigate for the successor');
    await page.evaluate(() => window.savedChildB.retry());
    await page.locator('[data-destination-ok]').waitFor();

    // Saved closures across a same-location scope replacement: the old receipt is dead, and the
    // successor page's own closures act.
    await page.evaluate(() => {window.destinationRecovery.validate(true); return window.destinationRecovery.navigate('/search?probe=3');});
    await failure.waitFor();
    await page.waitForTimeout(100);
    await page.evaluate(() => {window.savedScopeOld = window.destinationRecoveryActions.latest('child'); window.destinationRecovery.scope('fixture-scope-3');});
    await page.waitForTimeout(200);
    await page.evaluate(() => {window.savedScopeNew = window.destinationRecoveryActions.latest('child'); window.destinationRecovery.validate(false);});
    await page.evaluate(() => window.savedScopeOld.retry());
    await page.waitForTimeout(250);
    assert.equal(await failure.count(), 1, 'a saved Retry refuses after a same-location scope replacement');
    await page.evaluate(() => window.savedScopeOld.home());
    await page.waitForTimeout(250);
    assert.equal(await failure.count(), 1, 'a saved Home refuses after a same-location scope replacement');
    await page.evaluate(() => window.savedScopeNew.retry());
    await page.locator('[data-destination-ok]').waitFor();
    await page.evaluate(() => window.destinationRecovery.scope('fixture-scope'));

    // A deferred invalidation crossing a same-tick scope replacement settles through the router's
    // own match change. The installed CatchBoundary auto-clears a changed match, so the genuine page
    // may recover naturally and is not asserted to stay failed. The explicit old-reset refusal (the
    // original reset spy must stay at zero) is proved by the focused ChildRouteError receipt test.
    await page.evaluate(() => {window.destinationRecovery.validate(true); return window.destinationRecovery.navigate('/search?probe=4');});
    await failure.waitFor();
    await page.waitForTimeout(100);
    await page.evaluate(() => {
      window.savedPending = window.destinationRecoveryActions.latest('child');
      window.destinationRecovery.validate(false);
      window.savedPending.retry();                          // starts the invalidation and sets its token
      window.destinationRecovery.scopeNow('fixture-scope-4'); // commits before the completion runs
    });
    await page.locator('[data-destination-ok]').waitFor();
    await page.evaluate(() => window.destinationRecovery.scope('fixture-scope'));

    // A real ShellRoot failure reaches the actual App last resort with the shell withdrawn.
    await page.evaluate(() => {window.destinationRecovery.rootFault(true); return window.destinationRecovery.navigate('/search');});
    await page.getByText('Something went wrong', {exact: true}).waitFor();
    assert.equal(await shell(), false, 'the shell is withdrawn for an app-level failure');
    assert.equal(await failure.count(), 0, 'a root failure is never a destination page failure');
  } finally {
    await page.evaluate(() => {window.destinationRecovery.restore(); window.disposeDestinationRecovery?.();});
  }

  // Component-level witness case: the real outlet boundary with typed witnesses.
  await page.evaluate(() => {window.destinationRecovery.witness();});
  const witnessFailure = page.getByText("Something didn't load", {exact: true});
  try {
    await witnessFailure.waitFor();
    await page.waitForTimeout(100);

    // StrictMode's simulated unmount retires the first mount's token; the replay gets a fresh
    // admission, so the pre-replay closure refuses while the committed one acts.
    await page.evaluate(() => {
      const captured = window.destinationRecoveryWitness.actions();
      window.replayOld = captured[0];
      window.replayLive = captured[captured.length - 1];
    });
    const replayCaptures = await page.evaluate(() => window.destinationRecoveryWitness.actions().length);
    await page.evaluate(() => window.replayOld.retry());
    await page.waitForTimeout(200);
    assert.equal(await page.evaluate(() => window.destinationRecoveryWitness.actions().length), replayCaptures,
      'the pre-replay Retry cannot reset the committed page');
    assert.equal(await witnessFailure.count(), 1);
    await page.evaluate(() => window.replayOld.home());
    await page.waitForTimeout(200);
    assert.equal(await page.evaluate(() => window.destinationRecoveryWitness.homeCalls()), 0, 'the pre-replay Home cannot navigate');
    await page.evaluate(() => window.destinationRecoveryWitness.release());
    await page.evaluate(() => window.replayLive.retry());
    await page.locator('[data-witness-ok]').waitFor();

    // The replay recovery is a healthy page; the same-witness case needs an actual new fault.
    await page.evaluate(() => window.destinationRecoveryWitness.rearm());
    await witnessFailure.waitFor();
    await page.evaluate(() => window.destinationRecoveryWitness.same());
    await page.waitForTimeout(150);
    assert.equal(await witnessFailure.count(), 1, 'the same witness keeps the failed destination');
    await page.evaluate(() => window.destinationRecoveryWitness.release());
    await page.waitForTimeout(150);
    assert.equal(await witnessFailure.count(), 1, 'releasing the fault alone must not unlatch');

    await page.evaluate(() => window.destinationRecoveryWitness.scope());
    await page.locator('[data-witness-ok]').waitFor();
    assert.equal(await witnessFailure.count(), 0, 'a scope replacement recovers on its own');

    await page.evaluate(() => window.destinationRecoveryWitness.rearm());
    await witnessFailure.waitFor();
    await page.evaluate(() => {window.destinationRecoveryWitness.release(); window.destinationRecoveryWitness.missingKey();});
    await page.locator('[data-witness-ok]').waitFor();
    assert.equal(await witnessFailure.count(), 0, 'a missing entry key with a different href recovers');

    await page.evaluate(() => window.destinationRecoveryWitness.rearm());
    await witnessFailure.waitFor();
    await page.evaluate(() => {window.destinationRecoveryWitness.release(); window.destinationRecoveryWitness.sameHrefNewObject();});
    await page.locator('[data-witness-ok]').waitFor();
    assert.equal(await witnessFailure.count(), 0, 'a same-href new location object recovers');

    // A keyless destination keeps a truthful null entry in the identity; a same-href new location
    // object is still a new recovery lifetime.
    await page.evaluate(() => window.destinationRecoveryWitness.keyless());
    await witnessFailure.waitFor();
    assert.equal(await witnessFailure.count(), 1, 'a keyless destination still shows its page failure');
    await page.evaluate(() => window.destinationRecoveryWitness.keylessSameHref());
    await page.locator('[data-witness-ok]').waitFor();
    assert.equal(await witnessFailure.count(), 0, 'a keyless same-href new location object recovers');

    // A real unmount retires the mounted opening: a saved closure cannot act after unmount, and a
    // remount is a new instance with its own admission whose page acts.
    await page.evaluate(() => window.destinationRecoveryWitness.rearm());
    await witnessFailure.waitFor();
    await page.waitForTimeout(100);
    await page.evaluate(() => {window.unmountedActions = window.destinationRecoveryWitness.actions().at(-1); window.destinationRecoveryWitness.unmount();});
    await page.evaluate(() => window.destinationRecoveryWitness.remount());
    await witnessFailure.waitFor();
    const unmountCaptures = await page.evaluate(() => window.destinationRecoveryWitness.actions().length);
    await page.evaluate(() => window.unmountedActions.retry());
    await page.waitForTimeout(200);
    assert.equal(await page.evaluate(() => window.destinationRecoveryWitness.actions().length), unmountCaptures,
      'a saved Retry cannot reset after unmount');
    assert.equal(await witnessFailure.count(), 1);
    await page.evaluate(() => window.unmountedActions.home());
    await page.waitForTimeout(200);
    assert.equal(await page.evaluate(() => window.destinationRecoveryWitness.homeCalls()), 0, 'a saved Home cannot navigate after unmount');
    await page.evaluate(() => {window.remountedActions = window.destinationRecoveryWitness.actions().at(-1); window.destinationRecoveryWitness.release();});
    await page.evaluate(() => window.remountedActions.retry());
    await page.locator('[data-witness-ok]').waitFor();

    // A saved old closure after a successor fault on a new witness: the captured failure token is
    // dead, so the old Retry/Home refuse and only the successor's own closures act.
    await page.evaluate(() => window.destinationRecoveryWitness.rearm());
    await witnessFailure.waitFor();
    await page.waitForTimeout(100);
    await page.evaluate(() => {window.oldFaultActions = window.destinationRecoveryWitness.actions().at(-1);});
    await page.evaluate(() => {window.destinationRecoveryWitness.release(); window.destinationRecoveryWitness.scope();});
    await page.locator('[data-witness-ok]').waitFor();
    await page.evaluate(() => window.destinationRecoveryWitness.rearm());
    await witnessFailure.waitFor();
    await page.waitForTimeout(100);
    const successorCaptures = await page.evaluate(() => window.destinationRecoveryWitness.actions().length);
    await page.evaluate(() => window.oldFaultActions.retry());
    await page.waitForTimeout(200);
    assert.equal(await page.evaluate(() => window.destinationRecoveryWitness.actions().length), successorCaptures,
      'a saved Retry cannot reset a successor fault');
    assert.equal(await witnessFailure.count(), 1);
    await page.evaluate(() => window.oldFaultActions.home());
    await page.waitForTimeout(200);
    assert.equal(await page.evaluate(() => window.destinationRecoveryWitness.homeCalls()), 0, 'a saved Home cannot navigate for a successor fault');
    await page.evaluate(() => {window.successorActions = window.destinationRecoveryWitness.actions().at(-1); window.destinationRecoveryWitness.release();});
    await page.evaluate(() => window.successorActions.retry());
    await page.locator('[data-witness-ok]').waitFor();
  } finally {
    await page.evaluate(() => {window.destinationRecoveryWitness?.dispose();});
  }
}
